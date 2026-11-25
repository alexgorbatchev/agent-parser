use crate::common::s;
use regex::Regex;
use serde_json::Value;
use std::sync::LazyLock;

pub(crate) fn is_context(input: &Value) -> bool {
    if let Some(text) = input.as_str() {
        return contextual_text(text);
    }
    input
        .as_array()
        .into_iter()
        .flatten()
        .any(|part| s(part, "type") == "input_text" && contextual_text(s(part, "text")))
}

fn contextual_text(text: &str) -> bool {
    let text = text.trim();
    let lower = text.to_ascii_lowercase();
    let tags = [
        "environment_context",
        "agent_message_board_notification",
        "skill",
        "user_shell_command",
        "turn_aborted",
        "subagent_notification",
        "recommended_plugins",
    ];
    if tags.iter().any(|tag| marked(&lower, tag))
        || (lower.starts_with("# agents.md instructions") && lower.ends_with("</instructions>"))
        || marked(text, "goal_context")
    {
        return true;
    }
    if [
        "Warning: The maximum number of unified exec processes you can keep open is",
        "Warning: Your account was flagged for potentially high-risk cyber activity",
    ]
    .iter()
    .any(|prefix| text.starts_with(prefix))
        || (text.starts_with("Warning: apply_patch was requested via ")
            && text.ends_with("Use the apply_patch tool instead of exec_command."))
    {
        return true;
    }
    if let Some((key, body)) = text
        .strip_prefix("<external_")
        .and_then(|rest| rest.split_once('>'))
        && body.ends_with(&format!("</external_{key}>"))
    {
        return true;
    }
    static CONTEXT: LazyLock<Regex> = LazyLock::new(|| {
        Regex::new(
            r#"(?s)^<codex_internal_context source="[a-z][a-z0-9_]*">.*</codex_internal_context>$"#,
        )
        .expect("valid context regex")
    });
    static HOOK: LazyLock<Regex> = LazyLock::new(|| {
        Regex::new(r#"(?s)^<hook_prompt\s+[^<>]*\bhook_run_id\s*=\s*(?:"[^"]*\S[^"]*"|'[^']*\S[^']*')[^<>]*>.*</hook_prompt>$"#).expect("valid hook regex")
    });
    CONTEXT.is_match(text) || HOOK.is_match(text)
}

fn marked(text: &str, tag: &str) -> bool {
    text.starts_with(&format!("<{tag}>")) && text.ends_with(&format!("</{tag}>"))
}
