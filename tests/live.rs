use agent_parser::LiveParser;
use serde_json::{Value, json};

fn feed(parser: &mut LiveParser, record: Value, expected: &[&str]) {
    let events = parser.parse_line(record.to_string().as_bytes()).unwrap();
    assert_eq!(
        events
            .iter()
            .map(|e| e.event_type.as_str())
            .collect::<Vec<_>>(),
        expected,
        "{record}"
    );
}

#[test]
fn codex_stream_preserves_final_message_tool_outcome_and_usage() {
    let mut parser = LiveParser::new("codex").unwrap();
    assert!(parser.result().is_none());
    feed(
        &mut parser,
        json!({"type":"thread.started","thread_id":"t"}),
        &["run_started"],
    );
    feed(&mut parser, json!({"type":"turn.started"}), &["turn_start"]);
    for (status, kind) in [
        ("started", "tool_call"),
        ("updated", "tool_update"),
        ("completed", "tool_result"),
    ] {
        let line = json!({"type":format!("item.{status}"),"item":{"id":"c","type":"command_execution","exit_code":1,"aggregated_output":"failed"}});
        let events = parser.parse_line(line.to_string().as_bytes()).unwrap();
        assert_eq!(events[0].event_type, kind);
        assert_eq!(events[0].data.tool_call_id.as_deref(), Some("c"));
        if kind == "tool_result" {
            assert_eq!(events[0].data.is_error, Some(true));
            assert_eq!(events[0].data.tool_output.as_deref(), Some("failed"));
        }
    }
    feed(
        &mut parser,
        json!({"type":"item.updated","item":{"type":"reasoning","delta":"thought"}}),
        &["thinking_delta"],
    );
    feed(
        &mut parser,
        json!({"type":"item.started","item":{"type":"agent_message","text":"draft"}}),
        &[],
    );
    feed(
        &mut parser,
        json!({"type":"item.completed","item":{"type":"agent_message","text":"answer"}}),
        &["agent_message"],
    );
    feed(
        &mut parser,
        json!({"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":3,"output_tokens":2}}),
        &["run_result"],
    );
    let result = parser.result().unwrap();
    assert_eq!(result.session_id, "t");
    assert_eq!(result.text, "answer");
    assert!(result.has_text);
    let usage = result.usage.as_ref().unwrap();
    assert_eq!((usage.input, usage.cache_read, usage.total), (10, 3, 12));
    feed(
        &mut parser,
        json!({"type":"error","message":"retry"}),
        &["error"],
    );
    feed(
        &mut parser,
        json!({"type":"turn.failed","error":{"message":"failed"}}),
        &["run_result"],
    );
    assert_eq!(parser.result().unwrap().error_message, "failed");
    assert!(parser.result().unwrap().is_error);
    assert!(parser.parse_line(br#"{"type":"item.started"}"#).is_err());
    assert!(parser.parse_line(b"{}").is_err());
}

#[test]
fn claude_stream_keeps_explicit_null_and_latest_result() {
    let mut parser = LiveParser::new("claude").unwrap();
    feed(
        &mut parser,
        json!({"type":"system","subtype":"init","session_id":"s","model":"m"}),
        &["run_started"],
    );
    feed(
        &mut parser,
        json!({"type":"stream_event","event":{"type":"message_start","message":{"model":"new"}}}),
        &[],
    );
    for (native, key, expected) in [
        ("text_delta", "text", "agent_message_delta"),
        ("thinking_delta", "thinking", "thinking_delta"),
        ("input_json_delta", "partial_json", "tool_input_delta"),
    ] {
        feed(
            &mut parser,
            json!({"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":native,(key):"part"}}}),
            &[expected],
        );
    }
    feed(
        &mut parser,
        json!({"type":"assistant","message":{"content":[{"type":"text","text":"draft"},{"type":"thinking","thinking":"think"},{"type":"tool_use","id":"id","name":"Read","input":{"file_path":"x"}}]}}),
        &["agent_message", "thinking", "tool_call"],
    );
    feed(
        &mut parser,
        json!({"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"id","content":"ok"},{"type":"text","text":"user"}]}}),
        &["tool_result", "user_message"],
    );
    feed(
        &mut parser,
        json!({"type":"result","subtype":"error_max_turns","errors":["too many turns"]}),
        &["run_result"],
    );
    assert!(parser.result().unwrap().is_error);
    feed(
        &mut parser,
        json!({"type":"result","subtype":"success","result":"","structured_output":null,"usage":{"input_tokens":3,"output_tokens":2,"cache_read_input_tokens":4,"cache_creation_input_tokens":5}}),
        &["run_result"],
    );
    let result = parser.result().unwrap();
    assert!(!result.is_error);
    assert!(result.has_text);
    assert_eq!(result.text, "");
    assert_eq!(result.structured_output, Some(Value::Null));
    assert_eq!(result.session_id, "s");
    assert_eq!(result.model, "new");
    assert_eq!(result.usage.as_ref().unwrap().total, 14);
}

#[test]
fn antigravity_stream_emits_one_tool_call_and_normalizes_usage() {
    let mut parser = LiveParser::new("antigravity").unwrap();
    feed(
        &mut parser,
        json!({"event":"init","conversation_id":"c","init":{"cwd":"/project","model":"m"}}),
        &["run_started"],
    );
    feed(
        &mut parser,
        json!({"event":"step_update","step_update":{"step_type":"user_input"}}),
        &["turn_start"],
    );
    feed(
        &mut parser,
        json!({"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"t","thinking_delta":"r"}}),
        &["thinking_delta", "agent_message_delta"],
    );
    let running = json!({"event":"step_update","step_update":{"step_type":"tool","state":"RUNNING","step_index":2,"tool_name":"exec"}});
    feed(&mut parser, running.clone(), &["tool_call"]);
    feed(&mut parser, running, &["tool_update"]);
    let done = br#"{"event":"step_update","step_update":{"step_type":"tool","state":"DONE","step_index":2,"tool_info":{"name":"exec","error":{"message":"failed"}}}}"#;
    let events = parser.parse_line(done).unwrap();
    assert_eq!(events[0].data.tool_call_id.as_deref(), Some("step-2"));
    assert_eq!(events[0].data.tool_output.as_deref(), Some("failed"));
    feed(
        &mut parser,
        json!({"event":"step_update","step_update":{"step_type":"tool","state":"DONE","step_index":3,"subagent_info":{}}}),
        &["tool_call", "tool_result"],
    );
    feed(
        &mut parser,
        json!({"event":"result","result":{"status":"SUCCESS","response":"","usage":{"input_tokens":3,"output_tokens":5,"thinking_tokens":2,"cache_read_tokens":1}}}),
        &["run_result"],
    );
    let result = parser.result().unwrap();
    assert_eq!(result.session_id, "c");
    assert_eq!(result.model, "m");
    assert!(result.has_text);
    let usage = result.usage.as_ref().unwrap();
    assert_eq!((usage.output, usage.reasoning, usage.total), (5, 2, 9));
    feed(
        &mut parser,
        json!({"event":"result","result":{"status":"ERROR","response":"","error":"unavailable"}}),
        &["run_result"],
    );
    let result = parser.result().unwrap();
    assert!(result.is_error);
    assert!(!result.has_text);
    assert_eq!(result.error_message, "unavailable");
    assert!(parser.parse_line(br#"{"event":"result"}"#).is_err());
    assert!(parser.parse_line(b"{}").is_err());
    assert!(parser.parse_line(b" \n").unwrap().is_empty());
    assert!(LiveParser::new("pi").is_err());
}
