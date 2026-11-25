use crate::codex::{parse_input, reasoning, user_data};
use crate::codex_tool::{call, command_input, json_text, output_failed, status_failed};
use crate::common::*;
use crate::{Error, EventData, ParsedEvent, normalize_file_modification};
use serde_json::{Value, json};
pub(crate) fn parse_response_item(
    p: &Value,
    ts: i64,
    recorded_outcome: Option<bool>,
) -> Result<Vec<ParsedEvent>, Error> {
    let mut events = vec![];
    match s(p, "type") {
        "function_call" | "custom_tool_call" => {
            let name = s(p, "name");
            let (preferred, legacy) = if s(p, "type") == "custom_tool_call" {
                (&p["input"], &p["arguments"])
            } else {
                (&p["arguments"], &p["input"])
            };
            let input = if preferred.is_null() {
                legacy
            } else {
                preferred
            };
            let mut params = parse_input(input);
            if let Some(m) = &mut params {
                let keys = match name {
                    "exec_command" => vec![("cmd", "command")],
                    "spawn_agent" => {
                        vec![("message", "prompt"), ("agent_type", "description")]
                    }
                    _ => vec![],
                };
                for (from, to) in keys {
                    if m.get(to).is_none_or(Value::is_null)
                        && let Some(v) = m.get(from).cloned()
                    {
                        m.insert(to.into(), v);
                    }
                }
            }
            let is_question = matches!(
                name,
                "AskUserQuestion"
                    | "ask_user"
                    | "ask_question"
                    | "request_user_input"
                    | "request_user_input_async"
            );
            events.push(event(
                if is_question {
                    "question_prompt"
                } else {
                    "tool_call"
                },
                ts,
                "",
                "",
                EventData {
                    role: Some("assistant".into()),
                    tool_name: Some(name.into()),
                    tool_call_id: opt(s(p, "call_id")),
                    question: is_question.then(|| {
                        question(&Value::Object(params.clone().unwrap_or_default()), true)
                    }),
                    file_modification: normalize_file_modification(name, params.as_ref()),
                    tool_input: params,
                    ..Default::default()
                },
            ))
        }
        "function_call_output" | "custom_tool_call_output" => {
            let (output, head) = match &p["output"] {
                Value::String(s) => (s.clone(), s.as_str()),
                Value::Array(parts) => {
                    let head = parts
                        .first()
                        .filter(|part| s(part, "type") == "input_text")
                        .map_or("", |part| s(part, "text"));
                    (p["output"].to_string(), head)
                }
                Value::Null => (String::new(), ""),
                _ => {
                    return Err(Error::Invalid(
                        "tool output must be text or content array".into(),
                    ));
                }
            };
            events.push(event(
                "tool_result",
                ts,
                "",
                "",
                EventData {
                    role: Some("toolResult".into()),
                    tool_call_id: opt(s(p, "call_id")),
                    tool_output: Some(output),
                    is_error: output_failed(head).or(recorded_outcome),
                    images: crate::images::codex(&p["output"], "input_image"),
                    ..Default::default()
                },
            ))
        }
        "message" => {
            let text = content(&p["content"]);
            if s(p, "role") == "user" {
                if let Some(data) =
                    user_data(&text, crate::images::codex(&p["content"], "input_image"))
                {
                    events.push(event("turn_start", ts, "", "", data))
                }
            } else if s(p, "role") == "assistant" && !text.is_empty() {
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
        "reasoning" => {
            if let Some(data) = reasoning(p) {
                events.push(event("thinking", ts, "", "", data))
            }
        }
        "tool_search_call" => events.push(call(
            ts,
            "tool_search".into(),
            opt(s(p, "call_id")),
            p["arguments"].as_object().cloned(),
        )),
        "tool_search_output" => events.push(event(
            "tool_result",
            ts,
            "",
            "",
            EventData {
                role: Some("toolResult".into()),
                tool_call_id: opt(s(p, "call_id")),
                tool_output: json_text(&p["tools"]),
                ..Default::default()
            },
        )),
        "web_search_call" | "image_generation_call" => {
            let image = s(p, "type") == "image_generation_call";
            let id = opt(s(p, "id"));
            let (name, input) = if image {
                let input = opt(s(p, "revised_prompt")).map(|prompt| {
                    serde_json::Map::from_iter([("revised_prompt".into(), json!(prompt))])
                });
                ("image_generation", input)
            } else {
                ("web_search", p["action"].as_object().cloned())
            };
            events.push(call(ts, name.into(), id.clone(), input));
            events.push(event(
                "tool_result",
                ts,
                "",
                "",
                EventData {
                    role: Some("toolResult".into()),
                    tool_call_id: id,
                    is_error: status_failed(s(p, "status")),
                    images: if image {
                        crate::images::codex_generated(s(p, "result"))
                    } else {
                        vec![]
                    },
                    ..Default::default()
                },
            ));
        }
        "local_shell_call" => {
            let action = &p["action"];
            let input = action["command"]
                .as_array()
                .filter(|argv| !argv.is_empty())
                .map(|_| {
                    let mut input =
                        command_input(&action["command"], s(action, "working_directory"));
                    if let Some(timeout) = action["timeout_ms"].as_i64() {
                        input.insert("timeout_ms".into(), json!(timeout));
                    }
                    input
                });
            events.push(call(ts, "local_shell".into(), opt(s(p, "call_id")), input));
        }
        "agent_message" => {
            let text = content(&p["content"]);
            if !text.is_empty() {
                events.push(event(
                    "turn_start",
                    ts,
                    "",
                    "",
                    EventData {
                        role: Some("agent".into()),
                        last_prompt: Some(text.clone()),
                        content: Some(text),
                        ..Default::default()
                    },
                ));
            }
        }
        _ => {}
    };
    Ok(events)
}
