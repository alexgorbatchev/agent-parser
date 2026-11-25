use crate::common::*;
use crate::*;
use serde_json::Value;
use std::collections::HashMap;
/// Reconstruct a stable, chronological timeline from one session snapshot.
pub fn reconstruct_session_events(
    session: &OpencodeSession,
    messages: &[OpencodeMessage],
    parts: &[OpencodePart],
) -> Vec<ParsedEvent> {
    let mut events = vec![event(
        "run_started",
        session.time_created,
        &session.directory,
        "",
        EventData {
            title: opt(&session.title),
            model: opt(&session.model),
            ..Default::default()
        },
    )];
    let mut parts_by_message: HashMap<&str, Vec<&OpencodePart>> = HashMap::new();
    for p in parts {
        parts_by_message.entry(&p.message_id).or_default().push(p)
    }
    for msg in messages {
        let cwd = msg
            .data
            .path
            .as_ref()
            .map(|p| p.cwd.as_str())
            .filter(|s| !s.is_empty())
            .unwrap_or(&session.directory);
        let msg_parts = parts_by_message
            .get(msg.id.as_str())
            .cloned()
            .unwrap_or_default();
        let model = if msg.data.model_id.is_empty() {
            &session.model
        } else {
            &msg.data.model_id
        };
        if msg.data.role == "user" {
            let text = msg_parts
                .iter()
                .filter(|p| p.data.part_type == "text" && !p.data.text.is_empty())
                .map(|p| p.data.text.as_str())
                .collect::<Vec<_>>()
                .join("\n");
            if !text.is_empty() {
                events.push(event(
                    "user_prompt",
                    msg.time_created,
                    cwd,
                    "",
                    EventData {
                        role: Some("user".into()),
                        content: Some(text.clone()),
                        last_prompt: Some(text),
                        ..Default::default()
                    },
                ))
            }
            continue;
        }
        if msg.data.role != "assistant" {
            continue;
        }
        let mut retry_errors = vec![];
        let mut tool_errors = vec![];
        let mut rate_info: Option<ApiErrorInfo> = None;
        for part in &msg_parts {
            let p = &part.data;
            let ts = part.time_created;
            match p.part_type.as_str() {
                "reasoning" | "text" => {
                    if !p.text.is_empty() {
                        events.push(event(
                            if p.part_type == "reasoning" {
                                "thinking"
                            } else {
                                "text"
                            },
                            ts,
                            cwd,
                            "",
                            EventData {
                                role: Some("assistant".into()),
                                content: Some(p.text.clone()),
                                model: Some(model.clone()),
                                ..Default::default()
                            },
                        ))
                    }
                }
                "tool" => {
                    if p.tool.is_empty() && p.call_id.is_empty() {
                        continue;
                    }
                    let state = p.state.as_ref();
                    let start = state
                        .and_then(|s| s.time.as_ref())
                        .map(|t| t.start)
                        .filter(|t| *t > 0)
                        .unwrap_or(ts);
                    let input = state.and_then(|s| s.input.clone());
                    events.push(event(
                        "tool_use",
                        start,
                        cwd,
                        "",
                        EventData {
                            role: Some("assistant".into()),
                            tool_name: Some(p.tool.clone()),
                            tool_call_id: Some(p.call_id.clone()),
                            file_modification: normalize_file_modification(&p.tool, input.as_ref()),
                            skills: if p.tool == "skill" {
                                crate::skill::named_tool(input.as_ref(), "name")
                            } else {
                                vec![]
                            },
                            tool_input: input,
                            content: state.and_then(|s| opt(&s.title)),
                            ..Default::default()
                        },
                    ));
                    if let Some(state) = state {
                        if matches!(state.status.as_str(), "completed" | "error")
                            || !state.output.is_empty()
                            || !state.error.is_empty()
                        {
                            let end = state
                                .time
                                .as_ref()
                                .and_then(|t| t.end)
                                .filter(|t| *t > 0)
                                .unwrap_or(ts);
                            let output = if state.output.is_empty() {
                                &state.error
                            } else {
                                &state.output
                            };
                            events.push(event(
                                "tool_result",
                                end,
                                cwd,
                                "",
                                EventData {
                                    role: Some("toolResult".into()),
                                    tool_call_id: Some(p.call_id.clone()),
                                    tool_output: Some(output.clone()),
                                    is_error: Some(state.status == "error"),
                                    ..Default::default()
                                },
                            ))
                        }
                        if !state.error.trim().is_empty() {
                            tool_errors.push(state.error.trim().to_owned())
                        } else if let Some(error) = &p.error {
                            let text = extract_error(error);
                            if !text.is_empty() {
                                tool_errors.push(text)
                            }
                        }
                    } else if let Some(error) = &p.error {
                        let text = extract_error(error);
                        if !text.is_empty() {
                            tool_errors.push(text)
                        }
                    }
                }
                "retry" => {
                    if let Some(error) = &p.error {
                        let info = parse_api_error(error, ts);
                        if !info.message.is_empty() {
                            retry_errors.push(info.message.clone())
                        }
                        if info.is_rate_limit {
                            events.push(event(
                                "error",
                                ts,
                                cwd,
                                "",
                                EventData {
                                    role: Some("assistant".into()),
                                    model: Some(model.clone()),
                                    content: Some(if info.message.is_empty() {
                                        "Rate limit exceeded".into()
                                    } else {
                                        info.message.clone()
                                    }),
                                    is_error: Some(true),
                                    is_rate_limit: Some(true),
                                    retry_after_seconds: info.retry_after_seconds,
                                    rate_limit_reset_at: info.reset_at,
                                    ..Default::default()
                                },
                            ));
                            rate_info = Some(info)
                        }
                    }
                }
                _ => {}
            }
        }
        let end = msg
            .data
            .time
            .as_ref()
            .and_then(|t| t.completed)
            .filter(|t| *t > 0)
            .unwrap_or_else(|| {
                msg_parts
                    .iter()
                    .map(|p| p.time_created)
                    .fold(msg.time_created, i64::max)
            });
        let usage = msg.data.tokens.as_ref().map(|u| TokenUsage {
            input: u.input,
            output: u.output,
            cache_read: u.cache.read,
            cache_write: u.cache.write,
            reasoning: u.reasoning,
            total: if u.total != 0 {
                u.total
            } else {
                u.input + u.output + u.cache.read + u.cache.write
            },
        });
        let stop = msg.data.finish.as_ref().filter(|s| !s.is_empty()).cloned();
        if usage.is_some() || stop.is_some() || msg.data.cost.is_some() {
            events.push(event(
                "turn_end",
                end,
                cwd,
                "",
                EventData {
                    role: Some("assistant".into()),
                    model: Some(model.clone()),
                    usage,
                    stop_reason: stop,
                    raw_harness_cost: msg.data.cost,
                    ..Default::default()
                },
            ))
        }
        let is_rl = if let Some(error) = &msg.data.error {
            let info = parse_api_error(error, msg.time_created);
            let rl = info.is_rate_limit;
            rate_info = Some(info);
            Some(rl)
        } else {
            rate_info.as_ref().map(|_| true)
        };
        let terminal = !msg.data.error.as_ref().is_some_and(is_aborted)
            && (msg.data.finish.as_deref() == Some("error")
                || msg
                    .data
                    .error
                    .as_ref()
                    .is_some_and(|e| !extract_error(e).is_empty()));
        if terminal {
            let mut text = msg
                .data
                .error
                .as_ref()
                .map(extract_error)
                .unwrap_or_default();
            if text.is_empty() {
                text = retry_errors.join("\n")
            }
            if text.is_empty() {
                text = tool_errors.join("\n")
            }
            if text.is_empty() {
                text = "error".into()
            }
            let rate = rate_info.filter(|i| i.is_rate_limit);
            events.push(event(
                "error",
                end,
                cwd,
                "",
                EventData {
                    role: Some("assistant".into()),
                    model: Some(model.clone()),
                    content: Some(text),
                    is_error: Some(true),
                    is_rate_limit: is_rl,
                    retry_after_seconds: rate.as_ref().and_then(|i| i.retry_after_seconds),
                    rate_limit_reset_at: rate.and_then(|i| i.reset_at),
                    ..Default::default()
                },
            ))
        }
    }
    events.sort_by_key(|e| e.timestamp);
    events
}
#[derive(Default)]
struct ApiErrorInfo {
    is_rate_limit: bool,
    retry_after_seconds: Option<i64>,
    reset_at: Option<i64>,
    message: String,
}
fn parse_api_error(v: &Value, now: i64) -> ApiErrorInfo {
    let mut info = ApiErrorInfo {
        message: extract_error(v),
        ..Default::default()
    };
    if !v.is_object() {
        return info;
    }
    let data = if v["data"].is_object() { &v["data"] } else { v };
    let status = [data, v]
        .iter()
        .find_map(|m| integer(&m["statusCode"]).or_else(|| integer(&m["status"])));
    info.is_rate_limit = status == Some(429);
    if !s(data, "message").trim().is_empty() {
        info.message = s(data, "message").trim().into()
    }
    for candidate in [data, v] {
        for key in ["responseHeaders", "headers"] {
            if let Some(headers) = candidate[key].as_object() {
                for (key, value) in headers {
                    if !matches!(key.to_lowercase().as_str(), "retry-after" | "retry_after") {
                        continue;
                    }
                    let text = value
                        .as_str()
                        .map(str::to_owned)
                        .unwrap_or_else(|| value.to_string());
                    let reset = if let Ok(seconds) = text.trim().parse::<f64>()
                        && seconds > 0.0
                        && seconds.is_finite()
                    {
                        now.checked_add((seconds * 1000.0) as i64)
                            .map(|at| (at, seconds as i64))
                    } else {
                        chrono::DateTime::parse_from_rfc2822(text.trim())
                            .or_else(|_| chrono::DateTime::parse_from_rfc3339(text.trim()))
                            .ok()
                            .map(|t| {
                                let at = t.timestamp_millis();
                                (at, ((at - now) / 1000).max(0))
                            })
                    };
                    if let Some((at, seconds)) = reset {
                        info.retry_after_seconds = Some(seconds);
                        info.reset_at = Some(at);
                        info.is_rate_limit = true;
                        return info;
                    }
                }
            }
        }
    }
    info
}
fn empty_error(v: &Value) -> bool {
    match v {
        Value::Null => true,
        Value::String(s) => s.trim().is_empty(),
        Value::Object(m) => m.values().all(empty_error),
        Value::Array(a) => a.iter().all(empty_error),
        _ => false,
    }
}
fn is_aborted(v: &Value) -> bool {
    match v {
        Value::String(s) => s.contains("MessageAbortedError"),
        Value::Object(m) => {
            s(v, "name") == "MessageAbortedError"
                || s(v, "message").contains("MessageAbortedError")
                || ["error", "data"]
                    .iter()
                    .any(|k| m.get(*k).is_some_and(is_aborted))
        }
        _ => false,
    }
}
fn extract_error(v: &Value) -> String {
    if empty_error(v) {
        return String::new();
    }
    match v {
        Value::String(s) => s.trim().into(),
        Value::Array(a) => a
            .iter()
            .map(extract_error)
            .filter(|s| !s.is_empty())
            .collect::<Vec<_>>()
            .join("\n"),
        Value::Object(_) => {
            let data = &v["data"];
            if data.is_object() {
                for key in ["message", "exception", "error"] {
                    if !s(data, key).trim().is_empty() {
                        return s(data, key).trim().into();
                    }
                }
            } else if let Some(text) = data.as_str().filter(|s| !s.trim().is_empty()) {
                return text.trim().into();
            }
            if !s(v, "message").trim().is_empty() {
                return s(v, "message").trim().into();
            }
            for key in ["error", "exception"] {
                let text = extract_error(&v[key]);
                if !text.is_empty() {
                    return text;
                }
            }
            for key in ["text", "details", "name"] {
                if !s(v, key).trim().is_empty() {
                    return s(v, key).trim().into();
                }
            }
            v.to_string()
        }
        _ => v.to_string(),
    }
}
