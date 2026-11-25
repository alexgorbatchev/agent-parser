use crate::common::*;
use crate::{Error, EventData, ParsedEvent, TokenUsage, normalize_file_modification};
pub fn extract_pi_title_from_prompt(prompt: &str) -> Option<String> {
    title(prompt)
}
pub fn parse_pi_line(line: &[u8], fallback_cwd: &str) -> Result<Vec<ParsedEvent>, Error> {
    if line.is_empty() {
        return Ok(vec![]);
    }
    let raw = record(line)?;
    crate::validation::pi(&raw)?;
    let ts = timestamp(&raw["timestamp"]);
    let cwd = if s(&raw, "cwd").is_empty() {
        fallback_cwd
    } else {
        s(&raw, "cwd")
    };
    let mut events = vec![];
    let mut emit = |kind: &str, data: EventData| events.push(event(kind, ts, cwd, "", data));
    match s(&raw, "type") {
        "session" => emit(
            "run_started",
            EventData {
                title: Some(format!("Pi Session {}", s(&raw, "id"))),
                model: opt(first(&raw, &["modelId", "model", "model_id"])),
                ..Default::default()
            },
        ),
        "label" | "session_info" => {
            if let Some(title) = opt(first(&raw, &["label", "title", "name"])) {
                emit(
                    "title_change",
                    EventData {
                        title: Some(title),
                        ..Default::default()
                    },
                )
            }
        }
        "model_change" => {
            if let Some(model) = opt(first(&raw, &["modelId", "model", "model_id"])) {
                emit(
                    "model_change",
                    EventData {
                        model: Some(model),
                        ..Default::default()
                    },
                )
            }
        }
        "message" => {
            let msg = &raw["message"];
            match s(msg, "role") {
                "user" => {
                    let texts = blocks(&msg["content"], false)
                        .iter()
                        .map(|b| s(b, "text").to_owned())
                        .filter(|s| !s.is_empty())
                        .collect::<Vec<_>>()
                        .join("\n");
                    let prompt = if texts.is_empty() {
                        content(&msg["content"])
                    } else {
                        texts
                    };
                    emit(
                        "turn_start",
                        EventData {
                            role: Some("user".into()),
                            content: Some(prompt.clone()),
                            last_prompt: Some(prompt.clone()),
                            title: title(&prompt),
                            skills: crate::skill::pi_prompt(&prompt),
                            ..Default::default()
                        },
                    )
                }
                "toolResult" | "tool_result" => emit(
                    "tool_result",
                    EventData {
                        role: Some("toolResult".into()),
                        tool_name: opt(first(msg, &["toolName", "tool_name"])),
                        tool_call_id: opt(first(msg, &["toolCallId", "tool_call_id"])),
                        tool_output: Some(content(&msg["content"])),
                        is_error: Some(
                            msg["isError"]
                                .as_bool()
                                .or_else(|| msg["is_error"].as_bool())
                                .unwrap_or(false),
                        ),
                        ..Default::default()
                    },
                ),
                "assistant" => {
                    let model = opt(s(msg, "model"));
                    let mut texts = vec![];
                    for b in blocks(&msg["content"], false) {
                        match s(&b, "type") {
                            "thinking" => {
                                let thought = first(&b, &["thinking", "text"]);
                                if !thought.is_empty() {
                                    emit(
                                        "thinking",
                                        EventData {
                                            role: Some("assistant".into()),
                                            content: Some(thought.into()),
                                            model: model.clone(),
                                            ..Default::default()
                                        },
                                    )
                                }
                            }
                            "text" => {
                                if !s(&b, "text").is_empty() {
                                    texts.push(s(&b, "text").to_owned())
                                }
                            }
                            "toolCall" | "tool_call" | "tool_use" => {
                                let name = first(&b, &["toolName", "tool_name", "name"]);
                                let params = input(&b, &["input", "parameters", "arguments"]);
                                let is_question =
                                    matches!(name, "AskUserQuestion" | "ask_user" | "ask_question");
                                let q = is_question.then(|| {
                                    question(
                                        &serde_json::Value::Object(
                                            params.clone().unwrap_or_default(),
                                        ),
                                        false,
                                    )
                                });
                                emit(
                                    if is_question {
                                        "question_prompt"
                                    } else {
                                        "tool_call"
                                    },
                                    EventData {
                                        role: Some("assistant".into()),
                                        model: model.clone(),
                                        tool_name: Some(name.into()),
                                        tool_call_id: opt(first(
                                            &b,
                                            &["toolCallId", "tool_call_id", "id"],
                                        )),
                                        file_modification: normalize_file_modification(
                                            name,
                                            params.as_ref(),
                                        ),
                                        skills: if name == "read" {
                                            crate::skill::read_tool(params.as_ref())
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
                    let u = &msg["usage"];
                    let usage = u.as_object().map(|_| {
                        let mut usage = TokenUsage {
                            input: n(u, "input"),
                            output: n(u, "output"),
                            cache_read: n(u, "cacheRead"),
                            cache_write: n(u, "cacheWrite"),
                            reasoning: n(u, "reasoning"),
                            total: n(u, "total"),
                        };
                        if usage.total == 0 {
                            usage.total = n(u, "totalTokens")
                        }
                        if usage.total == 0 {
                            usage.total = usage.input
                                + usage.output
                                + usage.cache_read
                                + usage.cache_write
                                + usage.reasoning
                        }
                        usage
                    });
                    emit(
                        "turn_end",
                        EventData {
                            role: Some("assistant".into()),
                            content: (!texts.is_empty()).then(|| texts.join("\n")),
                            model: model.clone(),
                            stop_reason: opt(s(msg, "stopReason")),
                            usage,
                            raw_harness_cost: u["cost"]
                                .as_f64()
                                .or_else(|| u["cost"]["total"].as_f64()),
                            ..Default::default()
                        },
                    );
                    if s(msg, "stopReason") == "error" {
                        let text = if !s(msg, "errorMessage").is_empty() {
                            s(msg, "errorMessage").to_owned()
                        } else if !texts.is_empty() {
                            texts.join("\n")
                        } else {
                            "error".into()
                        };
                        emit(
                            "error",
                            EventData {
                                role: Some("assistant".into()),
                                content: Some(text),
                                model,
                                is_error: Some(true),
                                ..Default::default()
                            },
                        )
                    }
                }
                _ => {}
            }
        }
        _ => {}
    }
    Ok(events)
}
