use crate::common::{integer, s};
use serde_json::Value;

pub(crate) fn error_rate_limit(v: &Value) -> Option<bool> {
    fn class(kind: &str) -> Option<bool> {
        let mut snake = String::new();
        for (index, ch) in kind.chars().enumerate() {
            if ch.is_ascii_uppercase() && index > 0 {
                snake.push('_');
            }
            snake.push(ch.to_ascii_lowercase());
        }
        match snake.as_str() {
            "rate_limit_exceeded" => Some(true),
            "context_window_exceeded"
            | "session_budget_exceeded"
            | "usage_limit_exceeded"
            | "flex_unavailable"
            | "server_overloaded"
            | "model_not_found"
            | "policy_capacity_unavailable"
            | "cyber_policy"
            | "bio_policy"
            | "misalignment_policy_violation"
            | "too_many_denials"
            | "http_connection_failed"
            | "response_stream_connection_failed"
            | "internal_server_error"
            | "unauthorized"
            | "bad_request"
            | "invalid_prompt"
            | "sandbox_error"
            | "response_stream_disconnected"
            | "response_too_many_failed_attempts"
            | "active_turn_not_steerable"
            | "thread_rollback_failed"
            | "other" => Some(false),
            _ => None,
        }
    }
    if let Some(text) = v.as_str() {
        return class(text);
    }
    let m = v.as_object()?;
    let status = m.iter().find_map(|(key, value)| {
        if key == "http_status_code" {
            integer(value)
        } else {
            integer(&value["http_status_code"])
        }
    });
    if let Some(status) = status {
        return Some(status == 429);
    }
    m.keys()
        .find_map(|key| class(if key == "type" { s(v, key) } else { key }))
}
pub(crate) fn rate_reset(rates: &Value, now: i64, error: bool) -> Option<(i64, i64)> {
    let windows = [&rates["primary"], &rates["secondary"]];
    let exhausted = |window: &Value| {
        window["used_percent"]
            .as_f64()
            .is_some_and(|percent| percent >= 100.0)
    };
    let marker_only =
        !rates["rate_limit_reached_type"].is_null() && !windows.iter().any(|w| exhausted(w));
    let native = windows
        .into_iter()
        .filter(|window| marker_only || exhausted(window))
        .filter_map(|window| {
            window["resets_at"]
                .as_i64()
                .filter(|seconds| *seconds > 0)?
                .checked_mul(1_000)
        })
        .max()
        .filter(|at| *at > now)
        .map(|at| (at, seconds_until(at, now)));
    native.or_else(|| {
        ["requests", "tokens"]
            .iter()
            .filter_map(|kind| {
                let reset = s(rates, &format!("reset_{kind}"));
                let remaining = &rates[format!("remaining_{kind}")];
                if reset.is_empty()
                    || !(integer(remaining).is_some_and(|n| n <= 0)
                        || (error && remaining.is_null()))
                {
                    return None;
                }
                parse_reset(reset, now)
            })
            .filter(|(at, _)| *at > now)
            .max_by_key(|(at, _)| *at)
    })
}
pub(crate) fn quota_available(rates: &Value) -> bool {
    if !rates["rate_limit_reached_type"].is_null() {
        return false;
    }
    let windows = [&rates["primary"], &rates["secondary"]];
    if windows.iter().any(|window| window.is_object()) {
        return windows
            .iter()
            .all(|window| window["used_percent"].as_f64().unwrap_or_default() < 100.0);
    }
    let remaining = ["remaining_tokens", "remaining_requests"]
        .into_iter()
        .filter_map(|key| integer(&rates[key]))
        .collect::<Vec<_>>();
    !remaining.is_empty() && remaining.into_iter().all(|available| available > 0)
}
pub(crate) fn seconds_until(at: i64, now: i64) -> i64 {
    at.saturating_sub(now).saturating_add(999) / 1_000
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
