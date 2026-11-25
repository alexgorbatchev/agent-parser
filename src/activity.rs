use crate::{Error, common::s};
use serde_json::Value;

/// Returns the envelope time of a conversational Claude Code record in Unix milliseconds.
pub fn claude_code_activity_timestamp(line: &[u8]) -> Result<Option<i64>, Error> {
    let raw = envelope(line)?;
    match s(&raw, "type") {
        "user" | "assistant" | "attachment" | "system" | "progress" => {
            activity_time(&raw["timestamp"])
        }
        _ => Ok(None),
    }
}

/// Returns the envelope time of a Pi message, compaction, or branch summary.
pub fn pi_activity_timestamp(line: &[u8]) -> Result<Option<i64>, Error> {
    let raw = envelope(line)?;
    match s(&raw, "type") {
        "message" | "custom_message" | "compaction" | "branch_summary" => {
            activity_time(&raw["timestamp"])
        }
        _ => Ok(None),
    }
}

/// Returns the envelope time of a known Codex activity record, ignoring nested task times.
pub fn codex_activity_timestamp(line: &[u8]) -> Result<Option<i64>, Error> {
    let raw = envelope(line)?;
    let active = match s(&raw, "type") {
        "turn_context" => true,
        "event_msg" => matches!(
            payload_kind(&raw)?,
            "user_message"
                | "agent_message"
                | "reasoning"
                | "token_count"
                | "task_started"
                | "task_complete"
                | "error"
                | "item_completed"
        ),
        "response_item" => matches!(
            payload_kind(&raw)?,
            "message"
                | "function_call"
                | "function_call_output"
                | "custom_tool_call"
                | "custom_tool_call_output"
                | "reasoning"
                | "tool_search_call"
                | "tool_search_output"
        ),
        _ => false,
    };
    if active {
        activity_time(&raw["timestamp"])
    } else {
        Ok(None)
    }
}

/// Returns the creation time of a conversational Antigravity record.
pub fn agy_activity_timestamp(line: &[u8]) -> Result<Option<i64>, Error> {
    let raw = envelope(line)?;
    match s(&raw, "type") {
        "USER_INPUT" | "PLANNER_RESPONSE" | "GENERIC" => activity_time(&raw["created_at"]),
        _ => Ok(None),
    }
}

fn envelope(line: &[u8]) -> Result<Value, Error> {
    if line.iter().all(u8::is_ascii_whitespace) {
        return Ok(Value::Null);
    }
    let value = serde_json::from_slice(line)?;
    kind(&value)?;
    Ok(value)
}

fn kind(raw: &Value) -> Result<&str, Error> {
    if !raw.is_object() && !raw.is_null() {
        return Err(Error::Invalid("activity record must be an object".into()));
    }
    match &raw["type"] {
        Value::Null => Ok(""),
        Value::String(kind) => Ok(kind),
        _ => Err(Error::Invalid(
            "activity record type must be a string".into(),
        )),
    }
}

fn payload_kind(value: &Value) -> Result<&str, Error> {
    kind(
        value
            .get("payload")
            .ok_or_else(|| Error::Invalid("Codex activity record is missing its payload".into()))?,
    )
}

pub(crate) fn activity_time(raw: &Value) -> Result<Option<i64>, Error> {
    match raw {
        Value::Null => Ok(None),
        Value::String(text) if text.is_empty() => Ok(None),
        Value::String(text) => chrono::DateTime::parse_from_rfc3339(text)
            .map(|stamp| Some(stamp.timestamp_millis()))
            .map_err(|err| Error::Invalid(format!("invalid activity timestamp: {err}"))),
        _ => Err(Error::Invalid(
            "activity timestamp must be an RFC3339 string".into(),
        )),
    }
}
