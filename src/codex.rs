use crate::codex_response::parse_response_item;
use crate::common::*;
use crate::{Error, EventData, ParsedEvent, TokenUsage};
use serde_json::{Map, Value, json};
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
                let mut role = if s(spawn, "agent_role").is_empty() {
                    s(p, "agent_role")
                } else {
                    s(spawn, "agent_role")
                };
                if role.is_empty() {
                    role = p["source"]["subagent"].as_str().unwrap_or("")
                }
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
                let model = opt(first(p, &["model", "model_provider"]));
                if let Some(m) = &model {
                    self.current_model = m.clone()
                }
                events.push(event(
                    "run_started",
                    ts,
                    s(p, "cwd"),
                    s(&p["git"], "branch"),
                    EventData {
                        title: Some(title),
                        model,
                        ..Default::default()
                    },
                ))
            }
            "turn_context" => {
                if let Some(model) = opt(s(p, "model")) {
                    self.current_model = model.clone();
                    events.push(event(
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
            }
            "event_msg" => {
                events = self.parse_event_message(p, ts, stateful)?;
                if s(p, "type") == "task_started" && stateful {
                    let previous = self.release_turn_start();
                    self.turn_open = true;
                    self.pending_turn_start = events.pop();
                    return Ok(previous);
                }
            }
            "response_item" => {
                let prompt = s(p, "type") == "message" && s(p, "role") == "user";
                if !prompt || !(self.paginated || crate::codex_context::is_context(&p["content"])) {
                    events = parse_response_item(p, ts)?;
                }
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
            event.event_type == "turn_start" && event.data.role.as_deref() == Some("user")
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
    ) -> Result<Vec<ParsedEvent>, Error> {
        let mut events = vec![];
        match s(p, "type") {
            "item_completed" => {
                let item = &p["item"];
                if matches!(s(item, "type"), "FileChange" | "file_change") {
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
                    if let Some(prompt) = user_data(&text) {
                        events.insert(0, event("turn_start", ts, "", "", prompt));
                    }
                }
                if s(item, "type") == "AgentMessage" {
                    let text = item["content"]
                        .as_array()
                        .map(|a| {
                            a.iter()
                                .filter(|b| s(b, "type") == "Text")
                                .map(|b| s(b, "text"))
                                .collect::<String>()
                        })
                        .unwrap_or_default();
                    let questions = item["questions"]
                        .as_array()
                        .map(|a| {
                            a.iter()
                                .map(|q| question(q, true))
                                .filter(|s| !s.is_empty())
                                .collect::<Vec<_>>()
                                .join("\n")
                        })
                        .unwrap_or_default();
                    if !text.trim().is_empty() || !questions.is_empty() {
                        events.push(event(
                            if questions.is_empty() {
                                "agent_message"
                            } else {
                                "question_prompt"
                            },
                            ts,
                            "",
                            "",
                            EventData {
                                role: Some("assistant".into()),
                                content: Some(text),
                                question: opt(&questions),
                                tool_call_id: (!questions.is_empty())
                                    .then(|| opt(s(item, "id")))
                                    .flatten(),
                                ..Default::default()
                            },
                        ))
                    }
                }
            }
            "patch_apply_end" => return crate::codex_file_change::events(p, ts),
            "user_message" if !stateful => {
                if let Some(data) = user_data(s(p, "message")) {
                    events.push(event("turn_start", ts, "", "", data))
                }
            }
            "agent_message" if !stateful => {
                if !s(p, "message").is_empty() {
                    events.push(event(
                        "agent_message",
                        ts,
                        "",
                        "",
                        EventData {
                            role: Some("assistant".into()),
                            content: Some(s(p, "message").into()),
                            ..Default::default()
                        },
                    ))
                }
            }
            "reasoning" => {
                if let Some(data) = reasoning(p) {
                    events.push(event("thinking", ts, "", "", data))
                }
            }
            "token_count" => {
                let info = &p["info"];
                if info.is_object() {
                    let last = &info["last_token_usage"];
                    let chosen = if n(last, "total_tokens") == 0
                        && n(last, "prompt_tokens") == 0
                        && n(last, "completion_tokens") == 0
                    {
                        &info["total_token_usage"]
                    } else {
                        last
                    };
                    let total = if n(chosen, "total_tokens") == 0 {
                        n(chosen, "prompt_tokens") + n(chosen, "completion_tokens")
                    } else {
                        n(chosen, "total_tokens")
                    };
                    let usage = TokenUsage {
                        input: (n(chosen, "prompt_tokens") - n(chosen, "cached_prompt_tokens"))
                            .max(0),
                        output: n(chosen, "completion_tokens"),
                        cache_read: n(chosen, "cached_prompt_tokens"),
                        reasoning: n(chosen, "reasoning_tokens"),
                        total,
                        ..Default::default()
                    };
                    let cw = if n(info, "model_context_window") == 0 {
                        self.context_window
                    } else {
                        n(info, "model_context_window")
                    };
                    if cw > 0 {
                        self.context_window = cw
                    }
                    let rates = &p["rate_limits"];
                    let reset = rate_reset(rates, ts, false).filter(|(at, _)| *at > ts);
                    if let Some((at, _)) = reset {
                        self.rate_limit_reset_at = Some(at)
                    } else if rates.is_object()
                        && (rates["remaining_tokens"].is_null() || n(rates, "remaining_tokens") > 0)
                        && (rates["remaining_requests"].is_null()
                            || n(rates, "remaining_requests") > 0)
                        && (!rates["remaining_tokens"].is_null()
                            || !rates["remaining_requests"].is_null())
                    {
                        self.rate_limit_reset_at = None
                    }
                    self.expire(ts);
                    events.push(event(
                        "turn_end",
                        ts,
                        "",
                        "",
                        EventData {
                            role: Some("assistant".into()),
                            model: opt(&self.current_model),
                            model_context_window: (cw > 0).then_some(cw),
                            usage: Some(usage),
                            rate_limit_reset_at: reset.map(|(at, _)| at),
                            retry_after_seconds: reset.map(|(_, sec)| sec),
                            ..Default::default()
                        },
                    ))
                }
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
                }
                events.push(event(
                    if complete { "turn_end" } else { "turn_start" },
                    ts,
                    "",
                    "",
                    EventData {
                        role: Some("assistant".into()),
                        content: complete.then(|| opt(s(p, "last_agent_message"))).flatten(),
                        stop_reason: opt(s(p, "reason")),
                        ..Default::default()
                    },
                ))
            }
            "error" => {
                let mut is_rl = error_rate_limit(&p["codex_error_info"]);
                let line_reset = rate_reset(&p["rate_limits"], ts, true).filter(|(at, _)| *at > ts);
                if let Some((at, _)) = line_reset {
                    self.rate_limit_reset_at = Some(at)
                }
                self.expire(ts);
                let reset = match is_rl {
                    Some(false) => None,
                    Some(true) => line_reset.or_else(|| {
                        self.rate_limit_reset_at
                            .map(|at| (at, at.saturating_sub(ts).saturating_add(999) / 1000))
                    }),
                    None => {
                        if line_reset.is_some() {
                            is_rl = Some(true)
                        }
                        line_reset
                    }
                };
                events.push(event(
                    "error",
                    ts,
                    "",
                    "",
                    EventData {
                        role: Some("assistant".into()),
                        content: Some(s(p, "message").into()),
                        is_error: Some(true),
                        is_rate_limit: is_rl,
                        rate_limit_reset_at: reset.map(|(at, _)| at),
                        retry_after_seconds: reset.map(|(_, sec)| sec),
                        ..Default::default()
                    },
                ))
            }
            _ => {}
        };
        Ok(events)
    }
    fn expire(&mut self, ts: i64) {
        if self.rate_limit_reset_at.is_some_and(|at| at <= ts) {
            self.rate_limit_reset_at = None
        }
    }
}
#[derive(serde::Deserialize)]
struct Compacted {
    message: Option<String>,
}
pub(crate) fn user_data(prompt: &str) -> Option<EventData> {
    let prompt = clean_codex_prompt(prompt);
    if prompt.is_empty() {
        return None;
    }
    Some(EventData {
        role: Some("user".into()),
        content: Some(prompt.clone()),
        last_prompt: Some(prompt.clone()),
        title: title(&prompt),
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
fn error_rate_limit(v: &Value) -> Option<bool> {
    fn class(s: &str) -> Option<bool> {
        match s {
            "RateLimitExceeded" | "rate_limit_exceeded" => Some(true),
            "UsageLimitExceeded"
            | "usage_limit_exceeded"
            | "ServerOverloaded"
            | "server_overloaded" => Some(false),
            _ => None,
        }
    }
    if let Some(text) = v.as_str() {
        return class(text);
    }
    let m = v.as_object()?;
    let status = integer(&v["http_status_code"])
        .or_else(|| integer(&v["HttpConnectionFailed"]["http_status_code"]))
        .or_else(|| integer(&v["http_connection_failed"]["http_status_code"]));
    if let Some(status) = status {
        return Some(status == 429);
    }
    class(s(v, "type")).or_else(|| m.keys().find_map(|s| class(s)))
}
fn rate_reset(rates: &Value, now: i64, error: bool) -> Option<(i64, i64)> {
    ["requests", "tokens"]
        .iter()
        .filter_map(|kind| {
            let reset = s(rates, &format!("reset_{kind}"));
            let remaining = &rates[format!("remaining_{kind}")];
            if reset.is_empty()
                || !(integer(remaining).is_some_and(|n| n <= 0) || (error && remaining.is_null()))
            {
                return None;
            }
            parse_reset(reset, now)
        })
        .max_by_key(|(at, _)| *at)
}
fn parse_reset(text: &str, now: i64) -> Option<(i64, i64)> {
    let text = text.trim();
    if let Some(ns) = crate::parse_go_duration(text) {
        return now
            .checked_add(ns / 1_000_000)
            .map(|at| (at, ns / 1_000_000_000));
    }
    if let Ok(seconds) = text.parse::<f64>()
        && seconds > 0.0
        && seconds.is_finite()
    {
        return now
            .checked_add((seconds * 1000.0) as i64)
            .map(|at| (at, seconds as i64));
    }
    chrono::DateTime::parse_from_rfc3339(text).ok().map(|t| {
        let at = t.timestamp_millis();
        (at, (at.saturating_sub(now) / 1000).max(0))
    })
}
