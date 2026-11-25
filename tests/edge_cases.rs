use agent_parser::*;
use serde_json::{Value, json};
fn bytes(value: Value) -> Vec<u8> {
    serde_json::to_vec(&value).unwrap()
}
fn codex(parser: &mut CodexParser, ts: i64, payload: Value) -> Vec<ParsedEvent> {
    parser
        .parse_line(&bytes(
            json!({"type":"event_msg","timestamp":ts,"payload":payload}),
        ))
        .unwrap()
}
#[test]
fn malformed_empty_and_unknown_records() {
    for input in [b"bad".as_slice(), b"[]", b"null"] {
        assert!(ClaudeCodeParser::new().parse_line(input).is_err());
        assert!(parse_pi_line(input, "").is_err());
        assert!(parse_codex_line(input).is_err())
    }
    assert!(ClaudeCodeParser::new().parse_line(b"").unwrap().is_empty());
    assert!(parse_pi_line(b"", "").unwrap().is_empty());
    assert!(parse_codex_line(b" \n\t").unwrap().is_empty());
    assert!(parse_codex_line(br#"{"type":"event_msg","payload":[]} "#).is_err());
    let events = parse_pi_line(
        br#"{"type":"message","message":{"role":"user","content":{"unknown":"value"}}}"#,
        "",
    )
    .unwrap();
    assert_eq!(
        events[0].data.content.as_deref(),
        Some(r#"{"unknown":"value"}"#)
    );
    assert!(events[0].timestamp > 0);
    for raw in [
        json!({"type":"label"}),
        json!({"type":"model_change"}),
        json!({"type":"unknown"}),
    ] {
        assert!(parse_pi_line(&bytes(raw), "").unwrap().is_empty())
    }
}
#[test]
fn titles_clean_synthetic_wrappers_and_preserve_unicode() {
    let prompt = "<environment_context>x</environment_context> <session_context>y</session_context> Hello\nWorld";
    assert_eq!(clean_codex_prompt(prompt), "Hello\nWorld");
    assert_eq!(
        extract_codex_title_from_prompt(prompt).as_deref(),
        Some("Hello")
    );
    assert!(extract_codex_title_from_prompt("<environment_context>unterminated").is_none());
    assert!(extract_pi_title_from_prompt("  ").is_none());
    assert_eq!(
        extract_codex_title_from_prompt(&"é".repeat(101)).unwrap(),
        "é".repeat(100)
    );
    assert!(extract_claude_code_title_from_prompt("<system-reminder>unterminated").is_none());
}
#[test]
fn rollout_paths_support_partitioned_flat_timezone_and_legacy_uuid() {
    for (path, year, id) in [
        (
            "/root/sessions/2026/09/13/rollout-2026-09-13T12-34-56.1+08-00-session_1.jsonl",
            "2026",
            "session_1",
        ),
        ("C:\\root\\rollout-2026-09-13T12-34-56Z-id.jsonl", "", "id"),
        (
            "sessions/2026/09/13/rollout-legacy-date-12345678-abcd-abcd-abcd-123456789abc.jsonl",
            "2026",
            "12345678-abcd-abcd-abcd-123456789abc",
        ),
        (
            "rollout-legacy-date-12345678-abcd-abcd-abcd-123456789abc.jsonl",
            "",
            "12345678-abcd-abcd-abcd-123456789abc",
        ),
    ] {
        let info = parse_codex_rollout_path(path).unwrap();
        assert_eq!(info.file_path, path);
        assert_eq!(info.year, year);
        assert_eq!(info.session_id, id);
        assert!(!info.timestamp_string.is_empty())
    }
    assert!(parse_codex_rollout_path("").is_err());
    assert!(parse_codex_rollout_path("file.jsonl").is_err());
}
#[test]
fn claude_fifo_retains_recent_requests_and_inherits_timestamps() {
    let mut parser = ClaudeCodeParser::new();
    for i in 0..201 {
        let events=parser.parse_line(&bytes(json!({"type":"assistant","timestamp":1000+i,"requestId":format!("r{i}"),"message":{"stop_reason":"end_turn","usage":{"input_tokens":1}}}))).unwrap();
        assert!(events.last().unwrap().data.usage.is_some())
    }
    let repeated=parser.parse_line(br#"{"type":"assistant","requestId":"r200","message":{"stop_reason":"end_turn","usage":{"input_tokens":1}}}"#).unwrap();
    assert!(repeated.is_empty());
    let old=parser.parse_line(br#"{"type":"assistant","requestId":"r0","message":{"stop_reason":"end_turn","usage":{"input_tokens":1}}}"#).unwrap();
    assert_eq!(old[0].timestamp, 1200);
    assert!(old[0].data.usage.is_some());
    let summary = parser
        .parse_line(br#"{"type":"system","subtype":"away_summary","content":"recap"}"#)
        .unwrap();
    assert_eq!(summary[0].data.content.as_deref(), Some("recap"));
    assert_eq!(summary[0].timestamp, 1200);
}
#[test]
fn codex_completed_agent_items_and_async_questions() {
    let mut parser = CodexParser::new();
    parser
        .parse_line(br#"{"type":"session_meta","payload":{"history_mode":"paginated"}}"#)
        .unwrap();
    let events = codex(
        &mut parser,
        1000,
        json!({"type":"item_completed","item":{"type":"AgentMessage","id":"id","content":[{"type":"Text","text":"Choose"}],"questions":[{"question":"Which?"},{"questions":[{"prompt":"Why?"}]}]}}),
    );
    assert!(events.is_empty());
    for item in [
        json!({"type":"AgentMessage","content":[]}),
        json!({"type":"Other"}),
    ] {
        assert!(
            codex(
                &mut parser,
                1000,
                json!({"type":"item_completed","item":item})
            )
            .is_empty()
        )
    }
    let events = codex(
        &mut parser,
        1000,
        json!({"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"Done"}]}}),
    );
    assert_eq!(events[0].event_type, "agent_message");
}
#[test]
fn codex_lifecycle_timestamp_units_and_invalid_values() {
    for (value, expected) in [
        (json!(2), 2000),
        (json!("1970-01-01T00:00:03Z"), 3000),
        (Value::Null, 1000),
        (json!(""), 1000),
    ] {
        let events = parse_codex_line(&bytes(json!({
            "type":"event_msg","timestamp":1000,
            "payload":{"type":"task_started","started_at":value}
        })))
        .unwrap();
        assert_eq!(events[0].timestamp, expected)
    }
    for bad in [json!(1.5), json!({}), json!("bad"), json!(i64::MAX)] {
        assert!(parse_codex_line(&bytes(json!({"type":"event_msg","timestamp":1000,"payload":{"type":"task_complete","completed_at":bad}}))).is_err())
    }
}
#[test]
fn codex_rate_limits_expire_clear_and_respect_explicit_other_errors() {
    let mut parser = CodexParser::new();
    for (reset, at, seconds) in [
        ("500ms", 1500, 0),
        ("45.5", 46500, 45),
        ("1970-01-01T00:00:04Z", 4000, 3),
        ("1s", 2000, 1),
    ] {
        let events = codex(
            &mut parser,
            1000,
            json!({"type":"error","rate_limits":{"reset_tokens":reset}}),
        );
        assert_eq!(events[0].data.rate_limit_reset_at, Some(at));
        assert_eq!(events[0].data.retry_after_seconds, Some(seconds))
    }
    let events = codex(
        &mut parser,
        1100,
        json!({"type":"error","codex_error_info":{"http_connection_failed":{"http_status_code":500}}}),
    );
    assert_eq!(events[0].data.is_rate_limit, Some(false));
    assert!(events[0].data.rate_limit_reset_at.is_none());
    let events = codex(
        &mut parser,
        1200,
        json!({"type":"error","message":"unclassified"}),
    );
    assert!(events[0].data.is_rate_limit.is_none());
    let events = codex(
        &mut parser,
        2100,
        json!({"type":"error","codex_error_info":{"type":"RateLimitExceeded"}}),
    );
    assert!(events[0].data.rate_limit_reset_at.is_none());
    for bad in ["bad", "-1s", "0", "-2", "1970-01-01T00:00:00Z"] {
        let events = codex(
            &mut parser,
            1000,
            json!({"type":"error","rate_limits":{"reset_tokens":bad}}),
        );
        assert!(events[0].data.rate_limit_reset_at.is_none())
    }
    let info = json!({"last_token_usage":{"prompt_tokens":1}});
    codex(
        &mut parser,
        1000,
        json!({"type":"token_count","info":info,"rate_limits":{"remaining_requests":0,"reset_requests":"5s","remaining_tokens":0,"reset_tokens":"10s"}}),
    );
    codex(
        &mut parser,
        1100,
        json!({"type":"token_count","info":info,"rate_limits":{"remaining_requests":1}}),
    );
    let events = codex(
        &mut parser,
        1200,
        json!({"type":"error","codex_error_info":"RateLimitExceeded"}),
    );
    assert!(events[0].data.rate_limit_reset_at.is_none());
}
#[test]
fn codex_tool_inputs_outputs_and_search_preserve_payload() {
    for input in [
        json!("{bad"),
        json!("[]"),
        json!("null"),
        json!({"questions":[{"title":"Choose"}]}),
    ] {
        let events=parse_codex_line(&bytes(json!({"type":"response_item","timestamp":1000,"payload":{"type":"custom_tool_call","name":"request_user_input_async","input":input}}))).unwrap();
        assert_eq!(events[0].event_type, "question_prompt")
    }
    let events=parse_codex_line(&bytes(json!({"type":"response_item","timestamp":1000,"payload":{"type":"function_call_output","output":[{"type":"image","data":"opaque"}]}}))).unwrap();
    assert_eq!(
        events[0].data.tool_output.as_deref(),
        Some(r#"[{"data":"opaque","type":"image"}]"#)
    );
    assert!(
        parse_codex_line(&bytes(
            json!({"type":"response_item","payload":{"type":"function_call_output","output":{}}})
        ))
        .is_err()
    );
    for (kind, event) in [
        ("tool_search_call", "tool_call"),
        ("tool_search_output", "tool_result"),
    ] {
        let events=parse_codex_line(&bytes(json!({"type":"response_item","timestamp":1000,"payload":{"type":kind,"call_id":"id","query":"test","output":"found"}}))).unwrap();
        assert_eq!(events[0].event_type, event);
        assert_eq!(events[0].data.tool_call_id.as_deref(), Some("id"))
    }
}
#[test]
fn unified_patch_rejects_cross_file_hunk_misattribution() {
    let input = json!({"patch":"diff --git a/a b/a\n+++ b/a\n@@ -1 +1 @@\n-old\n+new\ndiff --git a/b b/b\n+++ b/b\n@@ -1 +1 @@\n-x\n+y\n"});
    assert!(normalize_file_modification("patch", input.as_object()).is_none());
    let input = json!({"patch":"+++ b/a\n@@ -1 +1 @@\n context\n"});
    let modification = normalize_file_modification("patch", input.as_object()).unwrap();
    assert_eq!(modification.hunks[0].lines.len(), 1);
    assert_eq!(modification.hunks[0].lines[0].line_type, "ctx");
    let input =
        json!({"path":"a","edits":[{"oldText":"x","newText":"y"},{"oldText":"a","newText":"b"}]});
    assert_eq!(
        normalize_file_modification("edit", input.as_object())
            .unwrap()
            .hunks[1]
            .header,
        "Hunk 2 of 2"
    );
}
