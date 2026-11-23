use agent_parser::{AgyParser, parse_agy_line};
use serde_json::json;

fn parse(parser: &mut AgyParser, value: serde_json::Value) -> Vec<agent_parser::ParsedEvent> {
    parser
        .parse_line(&serde_json::to_vec(&value).unwrap())
        .unwrap()
}

#[test]
fn native_transcript_preserves_turn_tools_model_and_usage() {
    let mut parser = AgyParser::new();
    let start = parse(
        &mut parser,
        json!({"type":"USER_INPUT","content":"Fix the bug","created_at":"2026-11-22T00:00:00Z"}),
    );
    assert_eq!(start[0].event_type, "turn_start");
    assert_eq!(start[0].data.last_prompt.as_deref(), Some("Fix the bug"));
    assert_eq!(start[0].timestamp, 1795305600000);
    let response = parse(
        &mut parser,
        json!({"type":"PLANNER_RESPONSE","step_index":2,"created_at":1000,"model":"gemini","thinking":"Investigating","content":"Reading","input_tokens":100,"output_tokens":20,"cache_read_tokens":30,"thinking_tokens":5,"tool_calls":[{"id":"read-1","name":"read_file","args":{"file":"a.rs"}},{"name":"write_to_file","args":{"TargetFile":"a.rs","CodeContent":"fixed","options":"{\"safe\":true}","list":"[1]","plain":"42","bad":"{x"}}]}),
    );
    assert_eq!(
        response
            .iter()
            .map(|e| e.event_type.as_str())
            .collect::<Vec<_>>(),
        [
            "thinking",
            "agent_message",
            "tool_call",
            "tool_call",
            "usage"
        ]
    );
    assert_eq!(
        response[3].data.tool_call_id.as_deref(),
        Some("step-2-tool-1")
    );
    assert_eq!(
        response[3].data.tool_input.as_ref().unwrap()["options"],
        json!({"safe":true})
    );
    assert_eq!(response[3].data.tool_input.as_ref().unwrap()["plain"], "42");
    assert_eq!(
        response[3]
            .data
            .file_modification
            .as_ref()
            .unwrap()
            .content
            .as_deref(),
        Some("fixed")
    );
    let usage = response[4].data.usage.as_ref().unwrap();
    assert_eq!(
        (
            usage.input,
            usage.output,
            usage.cache_read,
            usage.reasoning,
            usage.total
        ),
        (100, 20, 30, 5, 150)
    );
    let result = parse(
        &mut parser,
        json!({"type":"GENERIC","content":"file contents"}),
    );
    assert_eq!(result[0].data.tool_call_id.as_deref(), Some("read-1"));
    assert_eq!(result[0].data.tool_name.as_deref(), Some("read_file"));
    let failed = parse(
        &mut parser,
        json!({"type":"GENERIC","status":"ERROR","error":"denied"}),
    );
    assert_eq!(
        failed[0].data.tool_call_id.as_deref(),
        Some("step-2-tool-1")
    );
    assert_eq!(failed[0].data.tool_output.as_deref(), Some("denied"));
    assert_eq!(failed[0].data.is_error, Some(true));
    let finish = parse(
        &mut parser,
        json!({"type":"PLANNER_RESPONSE","content":"Done"}),
    );
    assert_eq!(
        finish
            .iter()
            .map(|e| e.event_type.as_str())
            .collect::<Vec<_>>(),
        ["agent_message", "turn_end"]
    );
    assert_eq!(finish[0].data.model.as_deref(), Some("gemini"));
    assert_eq!(
        parse(
            &mut parser,
            json!({"type":"PLANNER_RESPONSE","content":"Same"})
        )
        .len(),
        1
    );
}

#[test]
fn reset_new_turn_unknown_and_error_records() {
    let mut parser = AgyParser::new();
    parse(&mut parser, json!({"type":"USER_INPUT"}));
    let error = parse(
        &mut parser,
        json!({"type":"PLANNER_RESPONSE","model":"old","status":"ERROR","error":"failed"}),
    );
    assert_eq!(
        error
            .iter()
            .map(|e| e.event_type.as_str())
            .collect::<Vec<_>>(),
        ["error", "turn_end"]
    );
    parse(
        &mut parser,
        json!({"type":"PLANNER_RESPONSE","tool_calls":[{"name":"read"}]}),
    );
    parse(&mut parser, json!({"type":"USER_INPUT"}));
    assert_eq!(
        parse(
            &mut parser,
            json!({"type":"GENERIC","step_index":9,"content":"orphan"})
        )[0]
        .data
        .tool_call_id
        .as_deref(),
        Some("step-9")
    );
    parser.reset();
    let after = parse(
        &mut parser,
        json!({"type":"PLANNER_RESPONSE","content":"No turn","thinking_tokens":5}),
    );
    assert_eq!(after.len(), 1);
    assert!(after[0].data.model.is_none());
    assert_eq!(
        parse(
            &mut parser,
            json!({"type":"SYSTEM_MESSAGE","error":"shutdown"})
        )[0]
        .data
        .content
        .as_deref(),
        Some("shutdown")
    );
    for line in [
        br#"{"type":"UNKNOWN"}"#.as_slice(),
        br#"{"type":"SYSTEM_MESSAGE"}"#,
        b" \t\n",
    ] {
        assert!(parse_agy_line(line).unwrap().is_empty());
    }
    for line in [
        b"{".as_slice(),
        b"null",
        b"[]",
        br#"{"type":2}"#,
        br#"{"type":"PLANNER_RESPONSE","input_tokens":"x"}"#,
        br#"{"type":"PLANNER_RESPONSE","tool_calls":[{"args":[]}]}"#,
    ] {
        assert!(
            parse_agy_line(line).is_err(),
            "{}",
            String::from_utf8_lossy(line)
        );
    }
}

#[test]
fn token_overflow_and_invalid_records_leave_pending_state_intact() {
    let mut parser = AgyParser::new();
    parse(&mut parser, json!({"type":"USER_INPUT"}));
    parse(
        &mut parser,
        json!({"type":"PLANNER_RESPONSE","model":"retained","tool_calls":[{"id":"kept","name":"read"}]}),
    );
    let invalid = json!({"type":"PLANNER_RESPONSE","model":"discarded","input_tokens":i64::MAX,"output_tokens":1,"tool_calls":[{"name":"bad"}]});
    assert!(matches!(
        parser.parse_line(&serde_json::to_vec(&invalid).unwrap()),
        Err(agent_parser::Error::Invalid(_))
    ));
    assert!(
        parser
            .parse_line(br#"{"type":"PLANNER_RESPONSE","tool_calls":[null]}"#)
            .is_err()
    );
    let result = parse(&mut parser, json!({"type":"GENERIC","content":"ok"}));
    assert_eq!(result[0].data.tool_call_id.as_deref(), Some("kept"));
    let finish = parse(
        &mut parser,
        json!({"type":"PLANNER_RESPONSE","status":"DONE","input_tokens":i64::MAX,"output_tokens":1,"cache_read_tokens":-1}),
    );
    assert_eq!(finish[0].data.usage.as_ref().unwrap().total, i64::MAX);
    assert_eq!(finish[1].event_type, "turn_end");
    assert_eq!(finish[1].data.model.as_deref(), Some("retained"));
}

#[test]
fn native_file_tools_preserve_writes_and_replacement_chunks() {
    use agent_parser::normalize_file_modification;
    for (path_key, content_key) in [
        ("Path", "Content"),
        ("TargetFile", "CodeContent"),
        ("target_file", "code_content"),
        ("AbsolutePath", "content"),
        ("absolute_path", "content"),
    ] {
        let args = json!({path_key:"a.rs",content_key:""});
        let modification = normalize_file_modification("write_to_file", args.as_object()).unwrap();
        assert_eq!(modification.file_path, "a.rs");
        assert_eq!(modification.content.as_deref(), Some(""));
    }
    for (chunk_key, old_key, new_key) in [
        ("ReplacementChunks", "TargetContent", "ReplacementContent"),
        (
            "replacement_chunks",
            "target_content",
            "replacement_content",
        ),
        ("edits", "Target", "Replacement"),
    ] {
        let args = json!({"TargetFile":"a.rs",chunk_key:[{old_key:"old",new_key:"new"},{old_key:"two",new_key:"three"}]});
        let modification =
            normalize_file_modification("multi_replace_file_content", args.as_object()).unwrap();
        assert_eq!(modification.action, "EDIT");
        assert_eq!(modification.hunks.len(), 2);
        assert_eq!(modification.hunks[0].lines[0].content, "old");
        assert_eq!(modification.hunks[0].lines[1].content, "new");
    }
    let args = json!({"TargetFile":"a.rs","TargetContent":"old","ReplacementContent":"new","content":"unrelated"});
    let modification =
        normalize_file_modification("replace_file_content", args.as_object()).unwrap();
    assert_eq!(modification.action, "EDIT");
    assert_eq!(modification.hunks[0].lines[1].content, "new");
}
