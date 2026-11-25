use crate::common::*;
use crate::{Error, EventData, ParsedEvent, TokenUsage, normalize_file_modification};
use std::collections::{HashMap, HashSet, VecDeque};
pub fn clean_claude_code_prompt(prompt: &str) -> String {
    clean_prompt(
        prompt,
        &[
            "local-command-caveat",
            "command-name",
            "command-message",
            "command-args",
            "local-command-stdout",
            "task-notification",
            "system-reminder",
            "fork-boilerplate",
            "environment_context",
            "session_context",
        ],
    )
}
pub fn extract_claude_code_title_from_prompt(prompt: &str) -> Option<String> {
    title(&clean_claude_code_prompt(prompt))
}
#[derive(Default)]
pub struct ClaudeCodeParser {
    started: bool,
    seen_sessions: HashSet<String>,
    explicit_titles: HashSet<String>,
    titles: HashMap<String, String>,
    models: HashMap<String, String>,
    last_timestamp: i64,
    requests: HashSet<String>,
    request_order: VecDeque<String>,
    pending_commands: HashMap<String, String>,
}
impl ClaudeCodeParser {
    pub fn new() -> Self {
        Self::default()
    }
    pub fn reset(&mut self) {
        *self = Self::default()
    }
    fn remember(&mut self, key: String) {
        if self.requests.contains(&key) {
            return;
        }
        if self.request_order.len() >= 200 {
            for _ in 0..100 {
                if let Some(old) = self.request_order.pop_front() {
                    self.requests.remove(&old);
                }
            }
        }
        self.requests.insert(key.clone());
        self.request_order.push_back(key);
    }
    pub fn parse_line(&mut self, line: &[u8]) -> Result<Vec<ParsedEvent>, Error> {
        if line.is_empty() {
            return Ok(vec![]);
        }
        let raw = record(line)?;
        crate::validation::claude(&raw)?;
        let ts = if raw["timestamp"].is_null() && self.last_timestamp > 0 {
            self.last_timestamp
        } else {
            let ts = timestamp(&raw["timestamp"]);
            if !raw["timestamp"].is_null() && ts > 0 {
                self.last_timestamp = ts
            }
            ts
        };
        let cwd = s(&raw, "cwd");
        let branch = s(&raw, "gitBranch");
        let session = s(&raw, "sessionId");
        let key = if session.is_empty() {
            "__default__"
        } else {
            session
        };
        let sub = raw["isSidechain"].as_bool().unwrap_or(false) || !s(&raw, "agentId").is_empty();
        let mut events = vec![];
        let mut emit =
            |kind: &str, data: EventData| events.push(event(kind, ts, cwd, branch, data));
        let msg = &raw["message"];
        match s(&raw, "type") {
            "system" => {
                if s(&raw, "subtype") == "away_summary" && !s(&raw, "content").trim().is_empty() {
                    emit(
                        "summary",
                        EventData {
                            content: Some(s(&raw, "content").into()),
                            ..Default::default()
                        },
                    )
                }
            }
            "ai-title" | "title" | "custom-title" | "agent-name" => {
                let keys = match s(&raw, "type") {
                    "custom-title" => vec!["customTitle", "title"],
                    "agent-name" => vec!["agentName", "name"],
                    _ => vec!["aiTitle", "ai_title", "title"],
                };
                let mut text = first(&raw, &keys).to_owned();
                if text.is_empty() && s(&raw, "type") != "agent-name" {
                    text = content(&raw["content"])
                }
                if !text.is_empty() {
                    if !sub && s(&raw, "type") != "agent-name" {
                        self.explicit_titles.insert(key.into());
                        self.titles.insert(key.into(), text.clone());
                    }
                    emit(
                        "title_change",
                        EventData {
                            title: Some(text),
                            ..Default::default()
                        },
                    )
                }
            }
            "last-prompt" => {
                let prompt = if s(&raw, "lastPrompt").is_empty() {
                    content(&raw["content"])
                } else {
                    s(&raw, "lastPrompt").into()
                };
                if !sub
                    && !self.explicit_titles.contains(key)
                    && !self.titles.contains_key(key)
                    && let Some(title) = extract_claude_code_title_from_prompt(&prompt)
                {
                    self.titles.insert(key.into(), title);
                }
            }
            "user" => self.parse_user(&raw, key, sub, &mut emit),
            "queue-operation"
                if s(&raw, "operation") == "remove"
                    && s(&raw, "reason") == "absorbed_mid_turn"
                    && !s(&raw, "commandUuid").is_empty() =>
            {
                emit(
                    "message_absorbed",
                    EventData {
                        role: Some("user".into()),
                        content: raw["content"].as_str().map(str::to_owned),
                        message_uuid: opt(s(&raw, "commandUuid")),
                        ..Default::default()
                    },
                );
            }
            "assistant" => self.parse_assistant(&raw, key, &mut emit),
            _ => {}
        }
        let start = if !session.is_empty() {
            self.seen_sessions.insert(session.into())
        } else {
            !self.started && !events.is_empty()
        };
        if start {
            self.started = true;
            let mut title = opt(first(
                &raw,
                &[
                    "aiTitle",
                    "ai_title",
                    "customTitle",
                    "title",
                    "agentName",
                    "name",
                ],
            ));
            if title.is_none() {
                title = if !sub && !s(&raw, "lastPrompt").is_empty() {
                    extract_claude_code_title_from_prompt(s(&raw, "lastPrompt"))
                } else if sub && !s(&raw, "agentId").is_empty() {
                    Some(format!("Claude Code Subagent: {}", s(&raw, "agentId")))
                } else if !sub && !session.is_empty() {
                    Some(format!("Claude Code Session {session}"))
                } else {
                    None
                }
            }
            events.insert(
                0,
                event(
                    "run_started",
                    ts,
                    cwd,
                    branch,
                    EventData {
                        title,
                        model: opt(s(msg, "model")),
                        ..Default::default()
                    },
                ),
            )
        }
        Ok(events)
    }
    fn parse_user(
        &mut self,
        raw: &serde_json::Value,
        key: &str,
        sub: bool,
        emit: &mut impl FnMut(&str, EventData),
    ) {
        let msg = &raw["message"];
        if msg.is_object() {
            let mut texts = vec![];
            let mut has_result = false;
            for b in blocks(&msg["content"], true) {
                match s(&b, "type") {
                    "text" => {
                        if !s(&b, "text").is_empty() {
                            texts.push(s(&b, "text").to_owned())
                        }
                    }
                    "tool_result" => {
                        has_result = true;
                        let output = content(&b["content"]);
                        emit(
                            "tool_result",
                            EventData {
                                role: Some("user".into()),
                                tool_call_id: opt(first(&b, &["tool_use_id", "id"])),
                                tool_output: Some(if output.is_empty() {
                                    s(&b, "text").into()
                                } else {
                                    output
                                }),
                                is_error: Some(b["is_error"].as_bool().unwrap_or(false)),
                                ..Default::default()
                            },
                        )
                    }
                    _ => {}
                }
            }
            if !texts.is_empty() && !has_result {
                let prompt = texts.join("\n");
                let skills =
                    crate::skill::claude_prompt(raw, key, &prompt, &mut self.pending_commands);
                if raw["isMeta"].as_bool() == Some(true) {
                    if !skills.is_empty() {
                        emit(
                            "skill_invoked",
                            EventData {
                                role: Some("user".into()),
                                skills,
                                ..Default::default()
                            },
                        );
                    }
                    return;
                }
                let title = if !sub
                    && !self.explicit_titles.contains(key)
                    && !self.titles.contains_key(key)
                {
                    extract_claude_code_title_from_prompt(&prompt)
                } else {
                    None
                };
                if let Some(t) = &title {
                    self.titles.insert(key.into(), t.clone());
                }
                emit(
                    "turn_start",
                    EventData {
                        role: Some("user".into()),
                        content: Some(prompt.clone()),
                        last_prompt: Some(prompt),
                        message_uuid: opt(s(raw, "uuid")),
                        merged_message_uuids: raw["mergedSendUuids"]
                            .as_array()
                            .into_iter()
                            .flatten()
                            .filter_map(|uuid| uuid.as_str().map(str::to_owned))
                            .collect(),
                        title,
                        ..Default::default()
                    },
                )
            }
        }
    }
    fn parse_assistant(
        &mut self,
        raw: &serde_json::Value,
        key: &str,
        emit: &mut impl FnMut(&str, EventData),
    ) {
        let msg = &raw["message"];
        let model = if !s(msg, "model").is_empty() {
            self.models.insert(key.into(), s(msg, "model").into());
            Some(s(msg, "model").to_owned())
        } else {
            self.models.get(key).cloned()
        };
        if msg.is_object() {
            let mut texts = vec![];
            let mut has_tool = false;
            for b in blocks(&msg["content"], true) {
                match s(&b, "type") {
                    "text" => {
                        if !s(&b, "text").is_empty() {
                            texts.push(s(&b, "text").to_owned())
                        }
                    }
                    "thinking" => {
                        if !s(&b, "thinking").is_empty() {
                            emit(
                                "thinking",
                                EventData {
                                    role: Some("assistant".into()),
                                    content: Some(s(&b, "thinking").into()),
                                    model: model.clone(),
                                    ..Default::default()
                                },
                            )
                        }
                    }
                    "tool_use" => {
                        has_tool = true;
                        let name = s(&b, "name");
                        let params = input(&b, &["input"]);
                        let q = if name == "AskUserQuestion" {
                            Some(question(&b["input"], true))
                        } else {
                            None
                        };
                        emit(
                            if q.is_some() {
                                "question_prompt"
                            } else {
                                "tool_call"
                            },
                            EventData {
                                role: Some("assistant".into()),
                                model: model.clone(),
                                tool_name: Some(name.into()),
                                tool_call_id: opt(s(&b, "id")),
                                file_modification: normalize_file_modification(
                                    name,
                                    params.as_ref(),
                                ),
                                skills: if name == "Skill" {
                                    crate::skill::named_tool(params.as_ref(), "skill")
                                } else {
                                    vec![]
                                },
                                tool_input: params,
                                question: q,
                                ..Default::default()
                            },
                        )
                    }
                    _ => {}
                }
            }
            if !texts.is_empty() || has_tool || !matches!(s(msg, "stop_reason"), "" | "tool_use") {
                let request_keys = [("req", s(raw, "requestId")), ("msg", s(msg, "id"))]
                    .into_iter()
                    .filter(|(_, v)| !v.is_empty())
                    .map(|(kind, v)| format!("{key}:{kind}:{v}"))
                    .collect::<Vec<_>>();
                let duplicate = request_keys.iter().any(|k| self.requests.contains(k));
                for k in request_keys {
                    self.remember(k)
                }
                if !duplicate || !texts.is_empty() {
                    let u = &msg["usage"];
                    let usage = (!duplicate && u.is_object()).then(|| {
                        let reasoning = if n(u, "thinking_tokens") != 0 {
                            n(u, "thinking_tokens")
                        } else {
                            n(u, "reasoning_tokens")
                        };
                        let (input, output, cache_read, cache_write) = (
                            n(u, "input_tokens"),
                            n(u, "output_tokens"),
                            n(u, "cache_read_input_tokens"),
                            n(u, "cache_creation_input_tokens"),
                        );
                        TokenUsage {
                            input,
                            output,
                            cache_read,
                            cache_write,
                            reasoning,
                            total: input + output + cache_read + cache_write,
                        }
                    });
                    emit(
                        "turn_end",
                        EventData {
                            role: Some("assistant".into()),
                            model: model.clone(),
                            usage,
                            content: (!texts.is_empty()).then(|| texts.join("\n")),
                            stop_reason: opt(s(msg, "stop_reason")),
                            ..Default::default()
                        },
                    )
                }
            }
        }
        let status = [
            raw.get("apiErrorStatus"),
            raw.get("api_error_status"),
            raw.get("status")
                .filter(|v| integer(v).is_some_and(|n| n >= 400)),
            msg.get("apiErrorStatus"),
            msg.get("api_error_status"),
            msg.get("status")
                .filter(|v| integer(v).is_some_and(|n| n >= 400)),
        ]
        .into_iter()
        .flatten()
        .find_map(integer);
        let has_error = !s(raw, "error").is_empty()
            || !s(msg, "error").is_empty()
            || raw["isApiErrorMessage"].as_bool().unwrap_or(false)
            || status.is_some_and(|n| n > 0);
        if has_error {
            let mut text = first(raw, &["error"]).to_owned();
            if text.is_empty() {
                text = s(msg, "error").into()
            }
            if text.is_empty() {
                text = content(&msg["content"])
            }
            if text.is_empty() {
                text = content(&raw["content"])
            }
            if text.is_empty() {
                text = if let Some(status) = status.filter(|n| *n > 0) {
                    format!("API error: status {status}")
                } else {
                    "API error".into()
                }
            }
            emit(
                "error",
                EventData {
                    role: Some("assistant".into()),
                    content: Some(text),
                    model,
                    is_error: Some(true),
                    is_rate_limit: status.map(|n| n == 429),
                    ..Default::default()
                },
            )
        }
    }
}
