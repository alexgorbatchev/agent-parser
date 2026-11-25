use crate::{EventData, ParsedEvent};
use serde_json::{Map, Value};

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("invalid JSON: {0}")]
    Json(#[from] serde_json::Error),
    #[error("invalid transcript: {0}")]
    Invalid(String),
    #[error("database error: {0}")]
    Sqlite(#[from] rusqlite::Error),
    #[error("I/O error: {0}")]
    Io(#[from] std::io::Error),
    #[error("invalid Antigravity trajectory metadata: {0}")]
    Protobuf(#[from] prost::DecodeError),
}
pub(crate) fn record(line: &[u8]) -> Result<Value, Error> {
    let v: Value = serde_json::from_slice(line)?;
    if !v.is_object() {
        return Err(Error::Invalid("record must be an object".into()));
    }
    Ok(v)
}
pub(crate) fn s<'a>(v: &'a Value, key: &str) -> &'a str {
    v.get(key).and_then(Value::as_str).unwrap_or("")
}
pub(crate) fn first<'a>(v: &'a Value, keys: &[&str]) -> &'a str {
    keys.iter()
        .map(|k| s(v, k))
        .find(|s| !s.is_empty())
        .unwrap_or("")
}
pub(crate) fn opt(s: &str) -> Option<String> {
    (!s.is_empty()).then(|| s.to_owned())
}
pub(crate) fn n(v: &Value, key: &str) -> i64 {
    integer(&v[key]).unwrap_or(0)
}
pub(crate) fn integer(v: &Value) -> Option<i64> {
    v.as_i64().or_else(|| v.as_f64().map(|n| n as i64))
}
pub(crate) fn timestamp(v: &Value) -> i64 {
    integer(v)
        .or_else(|| {
            v.as_str()
                .and_then(|s| chrono::DateTime::parse_from_rfc3339(s).ok())
                .map(|t| t.timestamp_millis())
        })
        .unwrap_or_else(|| chrono::Utc::now().timestamp_millis())
}
pub(crate) fn content(v: &Value) -> String {
    match v {
        Value::Null => String::new(),
        Value::String(s) => s.clone(),
        Value::Array(a) => a.iter().filter_map(|b| b["text"].as_str()).collect(),
        v => v.to_string(),
    }
}
pub(crate) fn blocks(v: &Value, infer_text: bool) -> Vec<Value> {
    let mut blocks = match v {
        Value::String(text) => vec![serde_json::json!({"type":"text","text":text})],
        Value::Array(a) => a
            .iter()
            .filter(|v| v.is_object() && crate::validation::block(v, infer_text))
            .cloned()
            .collect(),
        Value::Object(_) if crate::validation::block(v, infer_text) => vec![v.clone()],
        _ => vec![],
    };
    if infer_text {
        for b in &mut blocks {
            if s(b, "type").is_empty() && !s(b, "text").is_empty() {
                b["type"] = "text".into()
            }
        }
    }
    blocks
}
pub(crate) fn event(kind: &str, ts: i64, cwd: &str, branch: &str, data: EventData) -> ParsedEvent {
    ParsedEvent {
        event_type: kind.into(),
        timestamp: ts,
        cwd: cwd.into(),
        git_branch: branch.into(),
        data,
    }
}
pub(crate) fn title(prompt: &str) -> Option<String> {
    let first = prompt.trim().lines().next()?.trim();
    (!first.is_empty()).then(|| first.chars().take(100).collect())
}
pub(crate) fn clean_prompt(prompt: &str, tags: &[&str]) -> String {
    let mut text = prompt.trim();
    loop {
        let original = text;
        for tag in tags {
            let open = format!("<{tag}>");
            let close = format!("</{tag}>");
            if text.starts_with(&open) {
                if let Some(i) = text.find(&close) {
                    text = text[i + close.len()..].trim()
                } else {
                    return String::new();
                }
            }
        }
        if text == original {
            return text.to_owned();
        }
    }
}
pub(crate) fn input(v: &Value, keys: &[&str]) -> Option<Map<String, Value>> {
    keys.iter().find_map(|k| v[*k].as_object().cloned())
}
pub(crate) fn question(v: &Value, nested: bool) -> String {
    for key in ["question", "prompt", "title"] {
        if !s(v, key).trim().is_empty() {
            return s(v, key).into();
        }
    }
    if nested && let Some(q) = v["questions"].as_array() {
        return q
            .iter()
            .map(|v| question(v, true))
            .filter(|s| !s.is_empty())
            .collect::<Vec<_>>()
            .join("\n");
    }
    String::new()
}
