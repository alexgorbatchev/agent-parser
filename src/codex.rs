use crate::codex_limits::{error_rate_limit, quota_available, rate_reset, seconds_until};
use crate::codex_response::parse_response_item;
use crate::common::*;
use crate::{Error, EventData, Image, ParsedEvent, TokenUsage};
use serde_json::{Map, Value, json};
use std::collections::HashMap;
pub fn clean_codex_prompt(prompt: &str) -> String {
    clean_prompt(prompt, &["environment_context", "session_context"])
}
pub fn extract_codex_title_from_prompt(prompt: &str) -> Option<String> {
    title(&clean_codex_prompt(prompt))
}
#[derive(Default)]
pub struct CodexParser {
    current_model: String,
    context_window: i64,
    rate_limit_reset_at: Option<i64>,
    session_meta_seen: bool,
    history_start_ordinal: Option<u64>,
    paginated: bool,
    turn_open: bool,
    pending_turn_start: Option<ParsedEvent>,
    held_turn_events: Vec<ParsedEvent>,
    adjacent_user_images: Option<Vec<Image>>,
    recorded_calls: HashMap<String, Option<bool>>,
    last_total_usage: Option<TokenUsage>,
}
pub fn parse_codex_line(line: &[u8]) -> Result<Vec<ParsedEvent>, Error> {
    CodexParser::default().parse_line_inner(line, false)
}
impl CodexParser {
    pub fn new() -> Self {
        Self::default()
    }
    pub fn reset(&mut self) {
        *self = Self::default()
    }
    pub fn parse_line(&mut self, line: &[u8]) -> Result<Vec<ParsedEvent>, Error> {
        self.parse_line_inner(line, true)
    }
    fn parse_line_inner(&mut self, line: &[u8], stateful: bool) -> Result<Vec<ParsedEvent>, Error> {
        if line.iter().all(u8::is_ascii_whitespace) {
            return Ok(vec![]);
        }
        let raw = record(line)?;
        let adjacent_images = self.adjacent_user_images.take();
        if matches!((self.history_start_ordinal, raw["ordinal"].as_u64()), (Some(start), Some(at)) if at < start)
        {
            return Ok(vec![]);
        }
        crate::validation::codex(&raw)?;
        let mut ts = timestamp(&raw["timestamp"]);
        let p = &raw["payload"];
        let mut events = vec![];
        let kind = s(&raw, "type");
        if matches!(
            kind,
            "session_meta" | "turn_context" | "event_msg" | "response_item"
        ) && !p.is_object()
        {
            return Err(Error::Invalid(format!("{kind} payload must be an object")));
        }
        match kind {
            "session_meta" => {
                if !self.session_meta_seen {
                    self.session_meta_seen = true;
                    self.history_start_ordinal = p["subagent_history_start_ordinal"].as_u64();
                    self.paginated = stateful && s(p, "history_mode") == "paginated";
                }
                if !s(p, "timestamp").is_empty() {
                    ts = timestamp(&p["timestamp"])
                }
                let spawn = &p["source"]["subagent"]["thread_spawn"];
                let parent = first(spawn, &["parent_thread_id"]);
                let parent = if parent.is_empty() {
                    s(p, "parent_thread_id")
                } else {
                    parent
                };
                let nick = if s(spawn, "agent_nickname").is_empty() {
                    s(p, "agent_nickname")
                } else {
                    s(spawn, "agent_nickname")
                };
                let subagent = &p["source"]["subagent"];
                let role = [
                    first(spawn, &["agent_role", "agent_type"]),
                    first(p, &["agent_role", "agent_type"]),
                    subagent.as_str().unwrap_or(""),
                    s(subagent, "other"),
                ]
                .into_iter()
                .find(|role| !role.is_empty())
                .unwrap_or("");
                let id = first(p, &["id", "session_id"]);
                let title = if !nick.is_empty() && !role.is_empty() {
                    format!("Codex Subagent: {nick} ({role})")
                } else if !nick.is_empty() {
                    format!("Codex Subagent: {nick}")
                } else if !role.is_empty() {
                    format!("Codex Subagent: ({role})")
                } else if !parent.is_empty() {
                    format!("Codex Subagent {id}")
                } else if !id.is_empty() {
                    format!("Codex Session {id}")
                } else {
                    "Codex Session".into()
                };
                events.push(event(
                    "run_started",
                    ts,
                    s(p, "cwd"),
                    s(&p["git"], "branch"),
                    EventData {
                        title: Some(title),
                        ..Default::default()
                    },
                ))
            }
            "turn_context" => events.extend(self.model_change(p, ts)),
            "event_msg" => {
                events = self.parse_event_message(p, ts, stateful, adjacent_images)?;
                if s(p, "type") == "task_started" && stateful {
                    let previous = self.release_turn_start();
                    self.turn_open = true;
                    self.pending_turn_start = events.pop();
                    return Ok(previous);
                }
            }
            "response_item" => {
                let kind = s(p, "type");
                if kind == "message" && s(p, "role") == "user" {
                    if crate::codex_context::is_context(&p["content"]) {
                        return Ok(events);
                    }
                    if self.paginated {
                        self.adjacent_user_images =
                            Some(crate::images::codex(&p["content"], "input_image"));
                        return Ok(events);
                    }
                } else if self.paginated
                    && (kind == "web_search_call"
                        || (kind == "message" && s(p, "role") == "assistant"))
                {
                    return Ok(events);
                }
                let call_id = s(p, "call_id");
                let mut outcome = None;
                if self.paginated && !call_id.is_empty() {
                    match kind {
                        "function_call" | "custom_tool_call" | "local_shell_call" => {
                            self.recorded_calls.insert(call_id.into(), None);
                        }
                        "function_call_output" | "custom_tool_call_output" => {
                            outcome = self.recorded_calls.get_mut(call_id).and_then(Option::take);
                        }
                        _ => {}
                    }
                }
                events = parse_response_item(p, ts, outcome)?;
            }
            "compacted" => {
                let compacted: Compacted = serde_json::from_value(p.clone())?;
                events.push(event(
                    "compaction",
                    ts,
                    "",
                    "",
                    EventData {
                        content: compacted.message.filter(|message| !message.is_empty()),
                        ..Default::default()
                    },
                ));
            }
            _ => {}
        }
        if let Some(index) = events.iter().position(|event| {
            event.event_type == "turn_start"
                && matches!(event.data.role.as_deref(), Some("user" | "agent"))
        }) && self.turn_open
        {
            if let Some(start) = &mut self.pending_turn_start {
                start.data = std::mem::take(&mut events.remove(index).data);
            } else {
                let prompt = &mut events[index];
                prompt.event_type = "message_absorbed".into();
                prompt.data.last_prompt = None;
                prompt.data.title = None;
            }
            let mut turn = self.release_turn_start();
            turn.append(&mut events);
            return Ok(turn);
        }
        if events.is_empty() || self.pending_turn_start.is_none() {
            return Ok(events);
        }
        if events
            .iter()
            .all(|event| matches!(event.event_type.as_str(), "compaction" | "model_change"))
        {
            self.held_turn_events.append(&mut events);
            return Ok(events);
        }
        let mut released = self.release_turn_start();
        released.append(&mut events);
        Ok(released)
    }
    fn release_turn_start(&mut self) -> Vec<ParsedEvent> {
        let mut events: Vec<_> = self.pending_turn_start.take().into_iter().collect();
        events.append(&mut self.held_turn_events);
        events
    }
    fn parse_event_message(
        &mut self,
        p: &Value,
        ts: i64,
        stateful: bool,
        adjacent_images: Option<Vec<Image>>,
    ) -> Result<Vec<ParsedEvent>, Error> {
        let mut events = vec![];
        match s(p, "type") {
            "item_completed" => {
                let item = &p["item"];
                let kind = s(item, "type");
                let file = matches!(kind, "FileChange" | "file_change");
                if self.paginated && (file || crate::codex_tool::is_tool_item(kind)) {
                    if let Some(outcome) = self.recorded_calls.get_mut(s(item, "id")) {
                        *outcome = crate::codex_tool::item_failed(item);
                    } else if !file {
                        events =
                            crate::codex_tool::item_events(item, p["started_at_ms"].as_i64(), ts);
                    }
                }
                if file {
                    return crate::codex_file_change::events(item, ts);
                }
                if let Some(skills) = crate::skill::codex_item(item)? {
                    events.push(event("skill_invoked", ts, "", "", skills));
                }
                if self.paginated && matches!(s(item, "type"), "UserMessage" | "user_message") {
                    let text: String = item["content"]
                        .as_array()
                        .into_iter()
                        .flatten()
                        .filter(|part| s(part, "type") == "text")
                        .map(|part| s(part, "text"))
                        .collect();
                    let images = adjacent_images
                        .unwrap_or_else(|| crate::images::codex(&item["content"], "image"));
                    if let Some(prompt) = user_data(&text, images) {
                        events.insert(0, event("turn_start", ts, "", "", prompt));
                    }
                }
                if kind == "AgentMessage"
                    && self.paginated
                    && item["questions"].as_array().is_none_or(Vec::is_empty)
                {
                    let text = item["content"]
                        .as_array()
                        .map(|a| {
                            a.iter()
                                .filter(|b| s(b, "type") == "Text")
                                .map(|b| s(b, "text"))
                                .collect::<String>()
                        })
                        .unwrap_or_default();
                    if !text.is_empty() {
                        events.push(event(
                            "agent_message",
                            ts,
                            "",
                            "",
                            EventData {
                                role: Some("assistant".into()),
                                content: Some(text),
                                ..Default::default()
                            },
                        ))
                    }
                }
            }
            "patch_apply_end" => return crate::codex_file_change::events(p, ts),
            "reasoning" => {
                if let Some(data) = reasoning(p) {
                    events.push(event("thinking", ts, "", "", data))
                }
            }
            "token_count" => events.extend(self.usage(p, ts, stateful)),
            "thread_settings_applied" => {
                events.extend(self.model_change(&p["thread_settings"], ts))
            }
            "task_started" | "task_complete" | "turn_aborted" => {
                let complete = s(p, "type") != "task_started";
                let field = if complete {
                    "completed_at"
                } else {
                    "started_at"
                };
                let ts = task_timestamp(&p[field], ts)?;
                if complete {
                    self.turn_open = false;
                    self.recorded_calls.clear();
                    if p["error"].is_object() {
                        events.push(self.error(&p["error"], ts));
                    }
                }
                events.push(event(
                    if complete { "turn_end" } else { "turn_start" },
                    ts,
                    "",
                    "",
                    EventData {
                        role: Some("assistant".into()),
                        content: if s(p, "type") == "task_complete" {
                            opt(s(p, "last_agent_message"))
                        } else {
                            None
                        },
                        stop_reason: if s(p, "type") == "turn_aborted" {
                            opt(s(p, "reason"))
                        } else {
                            None
                        },
                        ..Default::default()
                    },
                ))
            }
            "error" => events.push(self.error(p, ts)),
            _ => {}
        };
        Ok(events)
    }
    fn model_change(&mut self, p: &Value, ts: i64) -> Option<ParsedEvent> {
        let model = opt(s(p, "model"))?;
        self.current_model = model.clone();
        Some(event(
            "model_change",
            ts,
            s(p, "cwd"),
            "",
            EventData {
                model: Some(model),
                ..Default::default()
            },
        ))
    }
    fn usage(&mut self, p: &Value, ts: i64, stateful: bool) -> Option<ParsedEvent> {
        let limits = &p["rate_limits"];
        let reset = rate_reset(limits, ts, false);
        if let Some((at, _)) = reset {
            self.rate_limit_reset_at = Some(at);
        } else if quota_available(limits) || self.rate_limit_reset_at.is_some_and(|at| at <= ts) {
            self.rate_limit_reset_at = None;
        }
        let info = p.get("info").filter(|info| info.is_object())?;
        if stateful && info["total_token_usage"].is_object() {
            let total = token_usage(&info["total_token_usage"]);
            if self.last_total_usage.replace(total.clone()).as_ref() == Some(&total) {
                return None;
            }
        }
        let mut usage = token_usage(&info["last_token_usage"]);
        if usage.input == 0 && usage.output == 0 {
            return None;
        }
        if usage.total == 0 {
            usage.total = usage.input.saturating_add(usage.output);
        }
        usage.input = usage
            .input
            .saturating_sub(usage.cache_read)
            .saturating_sub(usage.cache_write)
            .max(0);
        let reported_window = n(info, "model_context_window");
        let window = if reported_window == 0 {
            self.context_window
        } else {
            reported_window
        };
        if window > 0 {
            self.context_window = window;
        }
        Some(event(
            "usage",
            ts,
            "",
            "",
            EventData {
                role: Some("assistant".into()),
                model: opt(&self.current_model),
                model_context_window: (window > 0).then_some(window),
                usage: Some(usage),
                rate_limit_reset_at: reset.map(|(at, _)| at),
                retry_after_seconds: reset.map(|(_, sec)| sec),
                ..Default::default()
            },
        ))
    }
    fn error(&mut self, p: &Value, ts: i64) -> ParsedEvent {
        let classification = error_rate_limit(&p["codex_error_info"]);
        let line_reset = rate_reset(&p["rate_limits"], ts, true);
        if let Some((at, _)) = line_reset {
            self.rate_limit_reset_at = Some(at);
        } else if self.rate_limit_reset_at.is_some_and(|at| at <= ts) {
            self.rate_limit_reset_at = None;
        }
        let reset = match classification {
            Some(false) => None,
            Some(true) => line_reset.or_else(|| {
                self.rate_limit_reset_at
                    .map(|at| (at, seconds_until(at, ts)))
            }),
            None => line_reset,
        };
        event(
            "error",
            ts,
            "",
            "",
            EventData {
                role: Some("assistant".into()),
                content: Some(s(p, "message").into()),
                is_error: Some(true),
                is_rate_limit: classification.or_else(|| line_reset.map(|_| true)),
                rate_limit_reset_at: reset.map(|(at, _)| at),
                retry_after_seconds: reset.map(|(_, sec)| sec),
                ..Default::default()
            },
        )
    }
}
fn token_usage(value: &Value) -> TokenUsage {
    let field = |native, legacy| integer(&value[native]).unwrap_or_else(|| n(value, legacy));
    TokenUsage {
        input: field("input_tokens", "prompt_tokens"),
        output: field("output_tokens", "completion_tokens"),
        cache_read: field("cached_input_tokens", "cached_prompt_tokens"),
        cache_write: n(value, "cache_write_input_tokens"),
        reasoning: field("reasoning_output_tokens", "reasoning_tokens"),
        total: n(value, "total_tokens"),
    }
}
#[derive(serde::Deserialize)]
struct Compacted {
    message: Option<String>,
}
pub(crate) fn user_data(prompt: &str, images: Vec<Image>) -> Option<EventData> {
    let prompt = clean_codex_prompt(prompt);
    if prompt.is_empty() && images.is_empty() {
        return None;
    }
    Some(EventData {
        role: Some("user".into()),
        content: Some(prompt.clone()),
        last_prompt: Some(prompt.clone()),
        title: title(&prompt),
        images,
        ..Default::default()
    })
}
pub(crate) fn reasoning(p: &Value) -> Option<EventData> {
    let text = p["summary"]
        .as_array()?
        .iter()
        .map(|b| s(b, "text"))
        .filter(|s| !s.is_empty())
        .collect::<Vec<_>>()
        .join("\n");
    opt(&text).map(|content| EventData {
        role: Some("assistant".into()),
        content: Some(content),
        ..Default::default()
    })
}
fn task_timestamp(value: &Value, fallback: i64) -> Result<i64, Error> {
    if value.is_null() || value.as_str() == Some("") {
        return Ok(fallback);
    }
    if let Some(text) = value.as_str() {
        return chrono::DateTime::parse_from_rfc3339(text)
            .map(|t| t.timestamp_millis())
            .map_err(|e| Error::Invalid(e.to_string()));
    }
    value
        .as_i64()
        .and_then(|s| s.checked_mul(1000))
        .ok_or_else(|| {
            Error::Invalid("task timestamp must be RFC3339 or integer Unix seconds".into())
        })
}
pub(crate) fn parse_input(v: &Value) -> Option<Map<String, Value>> {
    if let Some(map) = v.as_object() {
        return Some(map.clone());
    }
    let text = v.as_str()?;
    match serde_json::from_str::<Value>(text) {
        Ok(Value::Object(map)) => Some(map),
        Ok(Value::Null) => None,
        _ => json!({"raw":text}).as_object().cloned(),
    }
}
