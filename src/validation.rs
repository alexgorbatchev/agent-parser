use crate::Error;
use serde_json::Value;
#[derive(Clone, Copy)]
enum Kind {
    Text,
    Integer,
    Boolean,
    Object,
    Array,
}
fn fields(v: &Value, kind: Kind, keys: &[&str]) -> Result<(), Error> {
    for key in keys {
        let Some(value) = v.get(*key).filter(|v| !v.is_null()) else {
            continue;
        };
        let valid = match kind {
            Kind::Text => value.is_string(),
            Kind::Integer => value.as_i64().is_some(),
            Kind::Boolean => value.is_boolean(),
            Kind::Object => value.is_object(),
            Kind::Array => value.is_array(),
        };
        if !valid {
            return Err(Error::Invalid(format!("field {key:?} has an invalid type")));
        }
    }
    Ok(())
}
pub(crate) fn agy(v: &Value) -> Result<(), Error> {
    fields(
        v,
        Kind::Text,
        &[
            "type", "source", "status", "content", "thinking", "error", "model",
        ],
    )?;
    fields(
        v,
        Kind::Integer,
        &[
            "step_index",
            "input_tokens",
            "output_tokens",
            "cache_read_tokens",
            "thinking_tokens",
        ],
    )?;
    fields(v, Kind::Array, &["tool_calls", "truncated_fields"])?;
    if let Some(calls) = v["tool_calls"].as_array() {
        for call in calls {
            if !call.is_object() {
                return Err(Error::Invalid("tool call must be an object".into()));
            }
            fields(call, Kind::Text, &["id", "name"])?;
            fields(call, Kind::Object, &["args"])?;
        }
    }
    Ok(())
}
pub(crate) fn claude(v: &Value) -> Result<(), Error> {
    fields(
        v,
        Kind::Text,
        &[
            "type",
            "subtype",
            "uuid",
            "parentUuid",
            "sessionId",
            "agentId",
            "cwd",
            "gitBranch",
            "title",
            "customTitle",
            "aiTitle",
            "ai_title",
            "agentName",
            "name",
            "lastPrompt",
            "question",
            "error",
            "requestId",
        ],
    )?;
    fields(v, Kind::Boolean, &["isSidechain", "isApiErrorMessage"])?;
    fields(
        v,
        Kind::Integer,
        &[
            "apiErrorStatus",
            "api_error_status",
            "status",
            "apiBlockIndex",
        ],
    )?;
    fields(v, Kind::Object, &["message"])?;
    let msg = &v["message"];
    fields(
        msg,
        Kind::Text,
        &["id", "role", "model", "stop_reason", "error"],
    )?;
    fields(
        msg,
        Kind::Integer,
        &["apiErrorStatus", "api_error_status", "status"],
    )?;
    fields(msg, Kind::Object, &["usage"])?;
    fields(&msg["usage"], Kind::Object, &["output_tokens_details"])?;
    fields(
        &msg["usage"]["output_tokens_details"],
        Kind::Integer,
        &["thinking_tokens"],
    )?;
    fields(
        &msg["usage"],
        Kind::Integer,
        &[
            "input_tokens",
            "output_tokens",
            "cache_read_input_tokens",
            "cache_creation_input_tokens",
            "thinking_tokens",
            "reasoning_tokens",
        ],
    )
}
pub(crate) fn pi(v: &Value) -> Result<(), Error> {
    fields(
        v,
        Kind::Text,
        &[
            "type",
            "id",
            "parentId",
            "cwd",
            "model",
            "modelId",
            "model_id",
            "provider",
            "title",
            "name",
            "label",
            "targetId",
            "thinkingLevel",
        ],
    )?;
    fields(v, Kind::Object, &["message"])?;
    let msg = &v["message"];
    fields(
        msg,
        Kind::Text,
        &[
            "role",
            "model",
            "provider",
            "stopReason",
            "errorMessage",
            "toolName",
            "tool_name",
            "toolCallId",
            "tool_call_id",
        ],
    )?;
    fields(msg, Kind::Boolean, &["isError", "is_error"])?;
    fields(msg, Kind::Object, &["usage"])?;
    fields(
        &msg["usage"],
        Kind::Integer,
        &[
            "input",
            "output",
            "cacheRead",
            "cacheWrite",
            "reasoning",
            "total",
            "totalTokens",
        ],
    )
}
pub(crate) fn block(v: &Value, claude: bool) -> bool {
    let text = if claude {
        &["type", "text", "thinking", "name", "id", "tool_use_id"][..]
    } else {
        &[
            "type",
            "text",
            "thinking",
            "toolName",
            "tool_name",
            "toolCallId",
            "tool_call_id",
            "name",
            "id",
        ][..]
    };
    fields(v, Kind::Text, text)
        .and_then(|_| {
            fields(
                v,
                Kind::Boolean,
                if claude {
                    &["is_error"]
                } else {
                    &["isError", "is_error"]
                },
            )
        })
        .and_then(|_| {
            fields(
                v,
                Kind::Object,
                if claude {
                    &["input"]
                } else {
                    &["input", "parameters", "arguments"]
                },
            )
        })
        .is_ok()
}
pub(crate) fn codex(v: &Value) -> Result<(), Error> {
    fields(v, Kind::Text, &["type"])?;
    let p = &v["payload"];
    match v["type"].as_str().unwrap_or("") {
        "session_meta" => {
            fields(
                p,
                Kind::Text,
                &[
                    "id",
                    "session_id",
                    "timestamp",
                    "cwd",
                    "originator",
                    "cli_version",
                    "model",
                    "model_provider",
                    "parent_thread_id",
                    "agent_nickname",
                    "agent_role",
                    "agent_type",
                    "history_mode",
                    "multi_agent_version",
                ],
            )?;
            fields(p, Kind::Object, &["base_instructions", "git"])?;
            fields(&p["base_instructions"], Kind::Text, &["text"])?;
            fields(
                &p["git"],
                Kind::Text,
                &["commit_hash", "branch", "repository_url"],
            )
        }
        "turn_context" => {
            fields(
                p,
                Kind::Text,
                &[
                    "turn_id",
                    "cwd",
                    "current_date",
                    "timezone",
                    "model",
                    "personality",
                    "approval_policy",
                    "summary",
                ],
            )?;
            fields(p, Kind::Boolean, &["realtime_active"])?;
            fields(p, Kind::Object, &["sandbox_policy", "collaboration_mode"])
        }
        "event_msg" => {
            fields(
                p,
                Kind::Text,
                &[
                    "type",
                    "message",
                    "turn_id",
                    "last_agent_message",
                    "encrypted_content",
                    "reason",
                ],
            )?;
            fields(
                p,
                Kind::Integer,
                &["duration_ms", "started_at_ms", "model_context_window"],
            )?;
            fields(
                p,
                Kind::Object,
                &["info", "rate_limits", "error", "thread_settings"],
            )?;
            fields(&p["thread_settings"], Kind::Text, &["model", "cwd"])?;
            fields(&p["error"], Kind::Text, &["message"])?;
            fields(
                &p["info"],
                Kind::Object,
                &["last_token_usage", "total_token_usage"],
            )?;
            fields(&p["info"], Kind::Integer, &["model_context_window"])?;
            for key in ["last_token_usage", "total_token_usage"] {
                fields(
                    &p["info"][key],
                    Kind::Integer,
                    &[
                        "total_tokens",
                        "prompt_tokens",
                        "completion_tokens",
                        "cached_prompt_tokens",
                        "reasoning_tokens",
                        "input_tokens",
                        "output_tokens",
                        "cached_input_tokens",
                        "cache_write_input_tokens",
                        "reasoning_output_tokens",
                    ],
                )?
            }
            fields(
                &p["rate_limits"],
                Kind::Integer,
                &[
                    "limit_requests",
                    "remaining_requests",
                    "limit_tokens",
                    "remaining_tokens",
                ],
            )?;
            fields(
                &p["rate_limits"],
                Kind::Text,
                &["reset_requests", "reset_tokens", "rate_limit_reached_type"],
            )?;
            fields(&p["rate_limits"], Kind::Object, &["primary", "secondary"])?;
            for key in ["primary", "secondary"] {
                fields(
                    &p["rate_limits"][key],
                    Kind::Integer,
                    &["resets_at", "window_minutes"],
                )?;
            }
            summary(p)?;
            if p["type"].as_str() == Some("item_completed") && !p["item"].is_null() {
                fields(&p["item"], Kind::Text, &["type"])?;
                if p["item"]["type"].as_str() == Some("AgentMessage") {
                    fields(&p["item"], Kind::Text, &["id"])?;
                    fields(&p["item"], Kind::Array, &["content", "questions"])?;
                    if let Some(content) = p["item"]["content"].as_array() {
                        for block in content {
                            fields(block, Kind::Text, &["type", "text"])?
                        }
                    }
                }
            }
            Ok(())
        }
        "response_item" => {
            fields(
                p,
                Kind::Text,
                &[
                    "type",
                    "role",
                    "name",
                    "call_id",
                    "query",
                    "encrypted_content",
                ],
            )?;
            let arguments = if p["type"].as_str() == Some("tool_search_call") {
                Kind::Object
            } else {
                Kind::Text
            };
            fields(p, arguments, &["arguments"])?;
            summary(p)
        }
        _ => Ok(()),
    }
}
fn summary(v: &Value) -> Result<(), Error> {
    fields(v, Kind::Array, &["summary"])?;
    if let Some(summary) = v["summary"].as_array() {
        for item in summary {
            if !item.is_object() && !item.is_null() {
                return Err(Error::Invalid("summary item must be an object".into()));
            }
            fields(item, Kind::Text, &["type", "text"])?
        }
    }
    Ok(())
}
