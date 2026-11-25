use agent_parser::*;
use serde_json::{Value, json};
fn bytes(v: Value) -> Vec<u8> {
    serde_json::to_vec(&v).unwrap()
}
#[test]
fn claude_deduplicates_usage_preserves_text_and_resets() {
    let mut parser = ClaudeCodeParser::new();
    let raw = json!({"type":"assistant","sessionId":"s","timestamp":1000,"requestId":"r","message":{"id":"m","model":"model","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"plan"},{"type":"text","text":"done"}],"usage":{"input_tokens":5,"output_tokens":2}}});
    let first = parser.parse_line(&bytes(raw.clone())).unwrap();
    assert_eq!(
        first
            .iter()
            .map(|e| e.event_type.as_str())
            .collect::<Vec<_>>(),
        ["run_started", "thinking", "turn_end"]
    );
    assert_eq!(first[2].data.usage.as_ref().unwrap().total, 7);
    let repeated = parser.parse_line(&bytes(raw.clone())).unwrap();
    assert_eq!(repeated[1].data.content.as_deref(), Some("done"));
    assert!(repeated[1].data.usage.is_none());
    parser.reset();
    assert!(
        parser.parse_line(&bytes(raw)).unwrap()[2]
            .data
            .usage
            .is_some()
    );
}
#[test]
fn pi_usage_cost_questions_and_unicode_titles() {
    let raw = json!({"type":"message","timestamp":1000,"message":{"role":"assistant","model":"model","stopReason":"error","usage":{"input":2,"output":3,"cacheRead":4,"cacheWrite":5,"reasoning":1,"cost":{"total":0.25}},"content":[{"type":"toolCall","name":"ask_user","id":"q","arguments":{"question":"Which?"}},{"type":"text","text":"failed"}]}});
    let events = parse_pi_line(&bytes(raw), "/fallback").unwrap();
    assert_eq!(events.len(), 3);
    assert_eq!(events[0].data.question.as_deref(), Some("Which?"));
    assert_eq!(events[1].data.usage.as_ref().unwrap().total, 14);
    assert_eq!(events[1].data.raw_harness_cost, Some(0.25));
    assert_eq!(events[2].cwd, "/fallback");
    assert_eq!(
        extract_pi_title_from_prompt(&"é".repeat(101)).unwrap(),
        "é".repeat(100)
    );
}
#[test]
fn codex_carries_model_cache_and_structured_rate_limit() {
    let mut parser = CodexParser::new();
    parser
        .parse_line(&bytes(
            json!({"type":"turn_context","timestamp":1000,"payload":{"model":"m"}}),
        ))
        .unwrap();
    let events=parser.parse_line(&bytes(json!({"type":"event_msg","timestamp":1000,"payload":{"type":"token_count","info":{"last_token_usage":{"prompt_tokens":50,"completion_tokens":5,"cached_prompt_tokens":20},"model_context_window":1000},"rate_limits":{"remaining_requests":0,"reset_requests":"1m30s"}}}))).unwrap();
    assert_eq!(events[0].data.model.as_deref(), Some("m"));
    assert_eq!(events[0].data.usage.as_ref().unwrap().input, 30);
    assert_eq!(events[0].data.rate_limit_reset_at, Some(91000));
    let err=parser.parse_line(&bytes(json!({"type":"event_msg","timestamp":2000,"payload":{"type":"error","message":"wait","codex_error_info":"RateLimitExceeded"}}))).unwrap();
    assert_eq!(err[0].data.retry_after_seconds, Some(89));
    parser.reset();
    let err=parser.parse_line(&bytes(json!({"type":"event_msg","timestamp":2000,"payload":{"type":"error","message":"wait"}}))).unwrap();
    assert!(err[0].data.rate_limit_reset_at.is_none());
}
#[test]
fn file_edits_preserve_hunk_numbers_and_write_content() {
    let input = json!({"file_path":"file","old_string":"one\ntwo\n","new_string":"new\n"});
    let modification = normalize_file_modification("Edit", input.as_object()).unwrap();
    assert_eq!(modification.hunks[0].lines.len(), 3);
    assert_eq!(modification.hunks[0].lines[1].old_line_number, Some(2));
    let input = json!({"path":"file","content":""});
    assert_eq!(
        normalize_file_modification("write", input.as_object())
            .unwrap()
            .content
            .as_deref(),
        Some("")
    );
    let input = json!({"patch":"diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -5,2 +7,2 @@\n context\n-old\n+new\n"});
    let modification = normalize_file_modification("patch", input.as_object()).unwrap();
    assert_eq!(modification.file_path, "f");
    assert_eq!(modification.hunks[0].lines[1].old_line_number, Some(6));
}
