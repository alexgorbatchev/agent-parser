use crate::common::{content, event, input, opt, record, s};
use crate::{Error, EventData, ParsedEvent, TokenUsage};
use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;
use std::collections::HashSet;

/// The latest terminal record emitted by a live harness process.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct LiveResult {
    pub text: String,
    pub has_text: bool,
    #[serde(
        default,
        deserialize_with = "present_value",
        skip_serializing_if = "Option::is_none"
    )]
    pub structured_output: Option<Value>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub model: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub session_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub usage: Option<TokenUsage>,
    pub is_error: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error_message: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub native_subtype: String,
}

fn present_value<'de, D: Deserializer<'de>>(decoder: D) -> Result<Option<Value>, D::Error> {
    Value::deserialize(decoder).map(Some)
}

enum LiveHarness {
    Claude,
    Codex,
    Agy,
}

/// Parses one live Claude, Codex, or Antigravity process stream.
pub struct LiveParser {
    harness: LiveHarness,
    result: LiveResult,
    ended: bool,
    agy_tool_steps: HashSet<i64>,
}

impl LiveParser {
    /// Select the native stream protocol by executable name.
    pub fn new(harness: &str) -> Result<Self, Error> {
        let harness = match harness {
            "claude" => LiveHarness::Claude,
            "codex" => LiveHarness::Codex,
            "agy" | "antigravity" => LiveHarness::Agy,
            _ => {
                return Err(Error::Invalid(format!(
                    "unsupported live harness {harness:?}"
                )));
            }
        };
        Ok(Self {
            harness,
            result: LiveResult::default(),
            ended: false,
            agy_tool_steps: HashSet::new(),
        })
    }

    /// Read the latest terminal result. Drain the stream before choosing the final result.
    pub fn result(&self) -> Option<&LiveResult> {
        self.ended.then_some(&self.result)
    }

    /// Parse one process output line. Blank lines and unknown record types emit no events.
    pub fn parse_line(&mut self, line: &[u8]) -> Result<Vec<ParsedEvent>, Error> {
        if line.iter().all(u8::is_ascii_whitespace) {
            return Ok(vec![]);
        }
        let raw = record(line)?;
        let key = if matches!(self.harness, LiveHarness::Agy) {
            "event"
        } else {
            "type"
        };
        let kind = s(&raw, key);
        if kind.is_empty() {
            return Err(Error::Invalid(format!(
                "live harness record is missing its {key}"
            )));
        }
        match self.harness {
            LiveHarness::Claude => self.claude(kind, &raw),
            LiveHarness::Codex => self.codex(kind, &raw),
            LiveHarness::Agy => self.agy(kind, &raw),
        }
    }

    fn finish(&mut self) -> Vec<ParsedEvent> {
        self.ended = true;
        vec![live_event(
            "run_result",
            EventData {
                result: Some(self.result.clone()),
                usage: self.result.usage.clone(),
                is_error: Some(self.result.is_error),
                ..Default::default()
            },
        )]
    }

    fn codex(&mut self, kind: &str, raw: &Value) -> Result<Vec<ParsedEvent>, Error> {
        let event = match kind {
            "thread.started" => {
                self.result.session_id = s(raw, "thread_id").into();
                live_event("run_started", EventData::default())
            }
            "turn.started" => {
                self.ended = false;
                live_event("turn_start", EventData::default())
            }
            "turn.completed" => {
                set_usage(&mut self.result, raw)?;
                return Ok(self.finish());
            }
            "turn.failed" => {
                self.result.is_error = true;
                self.result.error_message = native_error(&raw["error"]).into();
                return Ok(self.finish());
            }
            "error" => live_event(
                "error",
                EventData {
                    content: Some(native_error(raw.get("message").unwrap_or(&raw["error"])).into()),
                    is_error: Some(true),
                    ..Default::default()
                },
            ),
            "item.started" | "item.updated" | "item.completed" => {
                return self.codex_item(kind, required_payload(raw, "item")?);
            }
            _ => return Ok(vec![]),
        };
        Ok(vec![event])
    }

    fn codex_item(&mut self, kind: &str, item: &Value) -> Result<Vec<ParsedEvent>, Error> {
        let completed = kind == "item.completed";
        let item_kind = s(item, "type");
        let event = if matches!(item_kind, "agent_message" | "reasoning") {
            let (message, delta) = if item_kind == "reasoning" {
                ("thinking", "thinking_delta")
            } else {
                ("agent_message", "agent_message_delta")
            };
            if let Some(text) = item["delta"].as_str() {
                live_event(
                    delta,
                    EventData {
                        delta: Some(text.into()),
                        ..Default::default()
                    },
                )
            } else if completed {
                let text = s(item, "text");
                if item_kind == "agent_message" {
                    self.result.text = text.into();
                    self.result.has_text = true;
                }
                live_event(
                    message,
                    EventData {
                        content: Some(text.into()),
                        ..Default::default()
                    },
                )
            } else {
                return Ok(vec![]);
            }
        } else {
            let exit_code = item["exit_code"].as_i64();
            let event_kind = match kind {
                "item.started" => "tool_call",
                "item.completed" => "tool_result",
                _ => "tool_update",
            };
            live_event(
                event_kind,
                EventData {
                    tool_call_id: Some(s(item, "id").into()),
                    tool_name: Some(item_kind.into()),
                    native: Some(item.clone()),
                    exit_code,
                    tool_output: item
                        .get("aggregated_output")
                        .filter(|_| completed)
                        .map(|value| native_error(value).into()),
                    is_error: exit_code.filter(|_| completed).map(|code| code != 0),
                    ..Default::default()
                },
            )
        };
        Ok(vec![event])
    }

    fn claude(&mut self, kind: &str, raw: &Value) -> Result<Vec<ParsedEvent>, Error> {
        update_text(&mut self.result.session_id, s(raw, "session_id"));
        update_text(&mut self.result.model, s(raw, "model"));
        match kind {
            "system" if s(raw, "subtype") == "init" => Ok(vec![live_event(
                "run_started",
                EventData {
                    model: opt(s(raw, "model")),
                    ..Default::default()
                },
            )]),
            "stream_event" => Ok(self.claude_delta(required_payload(raw, "event")?)),
            "assistant" | "user" => self.claude_message(kind, &raw["message"]),
            "result" => {
                set_usage(&mut self.result, raw)?;
                let subtype = s(raw, "subtype");
                self.result.is_error =
                    raw["is_error"].as_bool().unwrap_or(false) || subtype.starts_with("error");
                self.result.native_subtype = subtype.into();
                self.result.has_text = raw["result"].is_string();
                self.result.text = s(raw, "result").into();
                self.result.structured_output = raw.get("structured_output").cloned();
                self.result.error_message = raw["errors"]
                    .as_array()
                    .into_iter()
                    .flatten()
                    .filter_map(Value::as_str)
                    .collect::<Vec<_>>()
                    .join("\n");
                Ok(self.finish())
            }
            _ => Ok(vec![]),
        }
    }

    fn claude_delta(&mut self, raw: &Value) -> Vec<ParsedEvent> {
        update_text(&mut self.result.model, s(&raw["message"], "model"));
        if s(raw, "type") != "content_block_delta" {
            return vec![];
        }
        let delta = &raw["delta"];
        let (kind, field) = match s(delta, "type") {
            "text_delta" => ("agent_message_delta", "text"),
            "thinking_delta" => ("thinking_delta", "thinking"),
            "input_json_delta" => ("tool_input_delta", "partial_json"),
            _ => return vec![],
        };
        vec![live_event(
            kind,
            EventData {
                delta: Some(s(delta, field).into()),
                native: Some(raw.clone()),
                ..Default::default()
            },
        )]
    }

    fn claude_message(&mut self, kind: &str, raw: &Value) -> Result<Vec<ParsedEvent>, Error> {
        if raw.is_null() {
            return Ok(vec![]);
        }
        if !raw.is_object() {
            return Err(Error::Invalid(
                "live Claude message must be an object".into(),
            ));
        }
        update_text(&mut self.result.model, s(raw, "model"));
        let text_kind = if kind == "assistant" {
            "agent_message"
        } else {
            "user_message"
        };
        let message = &raw["content"];
        if let Some(text) = message.as_str() {
            return Ok(vec![live_event(
                text_kind,
                EventData {
                    content: Some(text.into()),
                    ..Default::default()
                },
            )]);
        }
        if message.is_null() {
            return Ok(vec![]);
        }
        let blocks = message.as_array().ok_or_else(|| {
            Error::Invalid("live Claude message content must be a string or array".into())
        })?;
        Ok(blocks
            .iter()
            .filter_map(|block| {
                let mut data = EventData::default();
                let kind = match s(block, "type") {
                    "text" => {
                        data.content = Some(s(block, "text").into());
                        text_kind
                    }
                    "thinking" => {
                        data.content = Some(s(block, "thinking").into());
                        "thinking"
                    }
                    "tool_use" => {
                        data.tool_call_id = Some(s(block, "id").into());
                        data.tool_name = Some(s(block, "name").into());
                        data.tool_input = input(block, &["input"]);
                        "tool_call"
                    }
                    "tool_result" => {
                        data.tool_call_id = Some(s(block, "tool_use_id").into());
                        data.tool_output = Some(content(&block["content"]));
                        data.is_error = block["is_error"].as_bool();
                        data.native = Some(raw.clone());
                        "tool_result"
                    }
                    _ => return None,
                };
                Some(live_event(kind, data))
            })
            .collect())
    }
}

pub(super) fn live_event(kind: &str, data: EventData) -> ParsedEvent {
    event(kind, chrono::Utc::now().timestamp_millis(), "", "", data)
}

pub(super) fn update_text(target: &mut String, source: &str) {
    if !source.is_empty() {
        *target = source.into();
    }
}

pub(super) fn required_payload<'a>(raw: &'a Value, key: &str) -> Result<&'a Value, Error> {
    let value = &raw[key];
    if !value.is_object() {
        return Err(Error::Invalid(format!(
            "live record requires a {key} object"
        )));
    }
    Ok(value)
}

fn native_error(value: &Value) -> &str {
    value.as_str().unwrap_or_else(|| s(value, "message"))
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct LiveUsage {
    input_tokens: i64,
    output_tokens: i64,
    cache_read_input_tokens: i64,
    cached_input_tokens: i64,
    cache_creation_input_tokens: i64,
}

fn set_usage(result: &mut LiveResult, raw: &Value) -> Result<(), Error> {
    if !raw["usage"].is_null() {
        let native: LiveUsage = serde_json::from_value(raw["usage"].clone())?;
        let cache_read = native
            .cache_read_input_tokens
            .checked_add(native.cached_input_tokens)
            .ok_or_else(|| Error::Invalid("live cache-read token count overflow".into()))?;
        result.usage = Some(TokenUsage {
            input: native.input_tokens,
            output: native.output_tokens,
            cache_read,
            cache_write: native.cache_creation_input_tokens,
            total: sum_usage([
                native.input_tokens,
                native.output_tokens,
                native.cache_read_input_tokens,
                native.cache_creation_input_tokens,
            ])?,
            ..Default::default()
        });
    }
    Ok(())
}

pub(super) fn sum_usage(counts: [i64; 4]) -> Result<i64, Error> {
    i64::try_from(counts.into_iter().map(i128::from).sum::<i128>())
        .map_err(|_| Error::Invalid("live token count overflow".into()))
}

#[path = "live_agy.rs"]
mod agy;
