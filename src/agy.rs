use crate::common::{event, n, opt, record, s, timestamp};
use crate::{Error, EventData, ParsedEvent, TokenUsage, normalize_file_modification};
use serde_json::{Map, Value};
use std::collections::VecDeque;

/// Stateful parser for native Antigravity transcript JSONL records.
///
/// Correlates `GENERIC` results with planner tool calls in FIFO order. Create one
/// parser per transcript; call [`Self::reset`] before replaying a replaced file.
#[derive(Default)]
pub struct AgyParser {
    pending: VecDeque<(String, String)>,
    in_turn: bool,
    model: String,
}

impl AgyParser {
    pub fn new() -> Self {
        Self::default()
    }

    /// Clear all turn, tool correlation, and model state.
    pub fn reset(&mut self) {
        *self = Self::default();
    }

    /// Parse one record. Blank lines and unknown record types emit no events.
    /// Malformed JSON or invalid known fields return an error without changing state.
    pub fn parse_line(&mut self, line: &[u8]) -> Result<Vec<ParsedEvent>, Error> {
        if line.iter().all(u8::is_ascii_whitespace) {
            return Ok(vec![]);
        }
        let raw = record(line)?;
        crate::validation::agy(&raw)?;
        let ts = timestamp(&raw["created_at"]);
        let events = match s(&raw, "type") {
            "USER_INPUT" => {
                self.pending.clear();
                self.in_turn = true;
                vec![event(
                    "turn_start",
                    ts,
                    "",
                    "",
                    EventData {
                        role: Some("user".into()),
                        content: Some(s(&raw, "content").into()),
                        last_prompt: Some(s(&raw, "content").into()),
                        ..Default::default()
                    },
                )]
            }
            "PLANNER_RESPONSE" => self.planner(&raw, ts)?,
            "GENERIC" => {
                let (id, name) = self
                    .pending
                    .pop_front()
                    .unwrap_or_else(|| (format!("step-{}", n(&raw, "step_index")), String::new()));
                let output = if s(&raw, "content").is_empty() {
                    s(&raw, "error")
                } else {
                    s(&raw, "content")
                };
                vec![event(
                    "tool_result",
                    ts,
                    "",
                    "",
                    EventData {
                        tool_call_id: Some(id),
                        tool_name: opt(&name),
                        tool_output: Some(output.into()),
                        is_error: Some(
                            s(&raw, "status") == "ERROR" || !s(&raw, "error").is_empty(),
                        ),
                        ..Default::default()
                    },
                )]
            }
            "SYSTEM_MESSAGE" if !s(&raw, "error").is_empty() => vec![event(
                "error",
                ts,
                "",
                "",
                EventData {
                    content: opt(s(&raw, "error")),
                    is_error: Some(true),
                    ..Default::default()
                },
            )],
            _ => vec![],
        };
        Ok(events)
    }

    fn planner(&mut self, raw: &Value, ts: i64) -> Result<Vec<ParsedEvent>, Error> {
        let (input, output, cache_read, reasoning) = (
            n(raw, "input_tokens"),
            n(raw, "output_tokens"),
            n(raw, "cache_read_tokens"),
            n(raw, "thinking_tokens"),
        );
        let total = i64::try_from(i128::from(input) + i128::from(output) + i128::from(cache_read))
            .map_err(|_| Error::Invalid("Antigravity token total overflow".into()))?;
        if !s(raw, "model").is_empty() {
            self.model = s(raw, "model").into();
        }
        let model = opt(&self.model);
        let mut events = vec![];
        for (kind, field) in [("thinking", "thinking"), ("agent_message", "content")] {
            if !s(raw, field).is_empty() {
                events.push(event(
                    kind,
                    ts,
                    "",
                    "",
                    EventData {
                        role: Some("assistant".into()),
                        content: opt(s(raw, field)),
                        model: model.clone(),
                        ..Default::default()
                    },
                ));
            }
        }
        let calls = raw["tool_calls"]
            .as_array()
            .map(Vec::as_slice)
            .unwrap_or_default();
        for (index, call) in calls.iter().enumerate() {
            let id = opt(s(call, "id"))
                .unwrap_or_else(|| format!("step-{}-tool-{index}", n(raw, "step_index")));
            let name = s(call, "name");
            let args = call["args"].as_object().map(decode_args);
            let modification = normalize_file_modification(name, args.as_ref());
            self.pending.push_back((id.clone(), name.into()));
            events.push(event(
                "tool_call",
                ts,
                "",
                "",
                EventData {
                    role: Some("assistant".into()),
                    model: model.clone(),
                    tool_call_id: Some(id),
                    tool_name: Some(name.into()),
                    skills: crate::skill::agy_tool(name, args.as_ref()),
                    tool_input: args,
                    file_modification: modification,
                    ..Default::default()
                },
            ));
        }
        if input > 0 || output > 0 || cache_read > 0 {
            events.push(event(
                "usage",
                ts,
                "",
                "",
                EventData {
                    model: model.clone(),
                    usage: Some(TokenUsage {
                        input,
                        output,
                        cache_read,
                        reasoning,
                        total,
                        ..Default::default()
                    }),
                    ..Default::default()
                },
            ));
        }
        if !s(raw, "error").is_empty() {
            events.push(event(
                "error",
                ts,
                "",
                "",
                EventData {
                    content: opt(s(raw, "error")),
                    model: model.clone(),
                    is_error: Some(true),
                    ..Default::default()
                },
            ));
        }
        if calls.is_empty()
            && self.in_turn
            && (!s(raw, "content").is_empty() || matches!(s(raw, "status"), "DONE" | "ERROR"))
        {
            self.in_turn = false;
            events.push(event(
                "turn_end",
                ts,
                "",
                "",
                EventData {
                    model,
                    ..Default::default()
                },
            ));
        }
        Ok(events)
    }
}

/// Parse an isolated native Antigravity record without cross-line state.
pub fn parse_agy_line(line: &[u8]) -> Result<Vec<ParsedEvent>, Error> {
    AgyParser::new().parse_line(line)
}

fn decode_args(args: &Map<String, Value>) -> Map<String, Value> {
    args.iter()
        .map(|(key, value)| {
            let decoded = value
                .as_str()
                .filter(|v| matches!(v.trim().as_bytes().first(), Some(b'{' | b'[' | b'"')))
                .and_then(|v| serde_json::from_str(v).ok())
                .unwrap_or_else(|| value.clone());
            (key.clone(), decoded)
        })
        .collect()
}
