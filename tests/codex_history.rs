use agent_parser::{
    ClaudeCodeParser, CodexParser, ParsedEvent, TokenUsage, parse_codex_line, parse_pi_line,
};
use serde_json::{Value, json};

fn feed(parser: &mut CodexParser, kind: &str, ts: i64, payload: Value) -> Vec<ParsedEvent> {
    parser
        .parse_line(
            &serde_json::to_vec(&json!({"type":kind,"timestamp":ts,"payload":payload})).unwrap(),
        )
        .unwrap()
}

fn standalone(payload: Value) -> Vec<ParsedEvent> {
    parse_codex_line(
        &serde_json::to_vec(&json!({"type":"response_item","timestamp":1000,"payload":payload}))
            .unwrap(),
    )
    .unwrap()
}

#[test]
fn usage_is_per_response_and_quota_does_not_close_a_turn() {
    let mut parser = CodexParser::new();
    let send = |parser: &mut CodexParser, ts, payload| feed(parser, "event_msg", ts, payload);
    assert!(send(&mut parser, 1100, json!({"type":"task_started"})).is_empty());
    assert!(send(&mut parser, 1200, json!({"type":"thread_settings_applied","thread_settings":{"model":"native-model","cwd":"/work"}})).is_empty());
    let start = feed(
        &mut parser,
        "response_item",
        1250,
        json!({"type":"message","role":"user","content":"Read the notes"}),
    );
    assert_eq!(
        start
            .iter()
            .map(|e| e.event_type.as_str())
            .collect::<Vec<_>>(),
        ["turn_start", "model_change"]
    );
    assert_eq!(start[1].cwd, "/work");
    let usage = json!({"type":"token_count","info":{
        "last_token_usage":{"input_tokens":500,"cached_input_tokens":200,"cache_write_input_tokens":30,"output_tokens":40,"reasoning_output_tokens":17},
        "total_token_usage":{"input_tokens":500,"output_tokens":40},"model_context_window":32400},
        "rate_limits":{"primary":{"used_percent":100,"resets_at":12},"secondary":{"used_percent":55,"resets_at":45},"rate_limit_reached_type":"limit"}});
    let events = send(&mut parser, 2100, usage.clone());
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].event_type, "usage");
    assert_eq!(events[0].data.model.as_deref(), Some("native-model"));
    assert_eq!(events[0].data.model_context_window, Some(32400));
    assert_eq!(
        events[0].data.usage,
        Some(TokenUsage {
            input: 270,
            output: 40,
            cache_read: 200,
            cache_write: 30,
            reasoning: 17,
            total: 540
        })
    );
    assert_eq!(events[0].data.rate_limit_reset_at, Some(12000));
    assert_eq!(events[0].data.retry_after_seconds, Some(10));
    assert!(send(&mut parser, 2300, usage).is_empty());
    assert!(send(&mut parser, 2500, json!({"type":"token_count","info":{"last_token_usage":{"total_tokens":123},"total_token_usage":{"total_tokens":123}}})).is_empty());
    let ended = send(
        &mut parser,
        2600,
        json!({"type":"turn_aborted","reason":"failed","error":{"message":"retry later","codex_error_info":{"response_stream_disconnected":{"http_status_code":429}}}}),
    );
    assert_eq!(
        ended
            .iter()
            .map(|e| e.event_type.as_str())
            .collect::<Vec<_>>(),
        ["error", "turn_end"]
    );
    assert_eq!(ended[0].data.retry_after_seconds, Some(10));
    assert_eq!(ended[1].data.stop_reason.as_deref(), Some("failed"));
    assert!(send(&mut parser, 2700, json!({"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":24,"resets_at":45}}})).is_empty());
    let error = send(
        &mut parser,
        2800,
        json!({"type":"error","codex_error_info":"RateLimitExceeded"}),
    );
    assert_eq!(error[0].data.rate_limit_reset_at, None);
    let marker = send(
        &mut parser,
        2900,
        json!({"type":"error","rate_limits":{"rate_limit_reached_type":"limit","primary":{"resets_at":15},"secondary":{"resets_at":25}}}),
    );
    assert_eq!(marker[0].data.rate_limit_reset_at, Some(25000));
    let nonlimit = send(
        &mut parser,
        3000,
        json!({"type":"error","codex_error_info":"InternalServerError"}),
    );
    assert_eq!(nonlimit[0].data.is_rate_limit, Some(false));
}

#[test]
fn paginated_history_keeps_the_item_and_legacy_images_once() {
    let mut parser = CodexParser::new();
    let meta = feed(
        &mut parser,
        "session_meta",
        1000,
        json!({"id":"child","model_provider":"openai","history_mode":"paginated","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent","agent_nickname":"leaf","agent_type":"reviewer"}}}}),
    );
    assert_eq!(
        meta[0].data.title.as_deref(),
        Some("Codex Subagent: leaf (reviewer)")
    );
    assert_eq!(meta[0].data.model, None);
    feed(
        &mut parser,
        "event_msg",
        1100,
        json!({"type":"task_started"}),
    );
    assert!(feed(&mut parser, "response_item", 1200, json!({"type":"message","role":"user","content":[{"type":"input_text","text":"Inspect the image"},{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo="}]})).is_empty());
    let start = feed(
        &mut parser,
        "event_msg",
        1300,
        json!({"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"Inspect the image"},{"type":"image","image_url":"https://example.test/duplicate"}]}}),
    );
    assert_eq!(start[0].timestamp, 1100);
    assert_eq!(start[0].data.content.as_deref(), Some("Inspect the image"));
    assert_eq!(start[0].data.images[0].media_type, "image/png");
    let item = feed(
        &mut parser,
        "event_msg",
        1400,
        json!({"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"image","file_id":"uploaded"}]}}),
    );
    assert_eq!(item[0].event_type, "message_absorbed");
    assert_eq!(item[0].data.images[0].file_id, "uploaded");
    assert!(
        feed(
            &mut parser,
            "response_item",
            1500,
            json!({"type":"message","role":"assistant","content":"Done"})
        )
        .is_empty()
    );
    let reply = feed(
        &mut parser,
        "event_msg",
        1600,
        json!({"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"Done"}]}}),
    );
    assert_eq!(reply[0].data.content.as_deref(), Some("Done"));
}

#[test]
fn paginated_tool_calls_emit_once_and_reuse_recorded_outcomes() {
    let mut parser = CodexParser::new();
    feed(
        &mut parser,
        "session_meta",
        1000,
        json!({"history_mode":"paginated"}),
    );
    let complete = |parser: &mut CodexParser, item| {
        feed(
            parser,
            "event_msg",
            1500,
            json!({"type":"item_completed","started_at_ms":1200,"item":item}),
        )
    };
    for (item, name, failure) in [
        (
            json!({"type":"CommandExecution","command":["sh","-c","cat notes.txt"],"cwd":"file:///work%20tree","exit_code":0,"aggregated_output":"notes"}),
            "exec_command",
            Some(false),
        ),
        (
            json!({"type":"McpToolCall","server":"docs","tool":"read","arguments":{"file":"notes"},"result":{"isError":true}}),
            "mcp__docs__read",
            Some(true),
        ),
        (
            json!({"type":"McpToolCall","server":"docs","tool":"read","error":"missing"}),
            "mcp__docs__read",
            Some(true),
        ),
        (
            json!({"type":"DynamicToolCall","tool":"lookup","arguments":{"tag":"notes"},"success":false,"content_items":[{"value":"missing"}]}),
            "lookup",
            Some(true),
        ),
        (
            json!({"type":"WebSearch","query":"docs","action":{"type":"search"},"status":"completed"}),
            "web_search",
            Some(false),
        ),
        (
            json!({"type":"Extension","kind":"image_gen.render","prompt":"illustration","result":"iVBORw0KGgo=","failure":"unavailable"}),
            "image_gen.render",
            Some(true),
        ),
        (
            json!({"type":"ImageView","path":"file:///work%20tree/icon.png"}),
            "view_image",
            None,
        ),
    ] {
        let events = complete(&mut parser, item);
        assert_eq!(events[0].timestamp, 1200);
        assert_eq!(events[0].data.tool_name.as_deref(), Some(name));
        assert_eq!(events[1].data.is_error, failure);
    }
    assert!(complete(&mut parser, json!({"type":"CollabAgentToolCall"})).is_empty());
    assert!(
        complete(
            &mut parser,
            json!({"type":"CommandExecution","source":"user_shell"})
        )
        .is_empty()
    );
    feed(
        &mut parser,
        "response_item",
        2000,
        json!({"type":"function_call","name":"exec_command","call_id":"a","arguments":"{}"}),
    );
    assert!(
        complete(
            &mut parser,
            json!({"type":"CommandExecution","id":"a","exit_code":1})
        )
        .is_empty()
    );
    let result = feed(
        &mut parser,
        "response_item",
        2200,
        json!({"type":"function_call_output","call_id":"a","output":[{"type":"input_text","text":"No result"},{"type":"input_image","image_url":"https://example.test/image"}]}),
    );
    assert_eq!(result[0].data.is_error, Some(true));
    assert_eq!(result[0].data.images[0].url, "https://example.test/image");
}

#[test]
fn legacy_codex_tool_status_is_only_read_from_the_envelope() {
    for (output, failed) in [
        (
            "Chunk ID: a\nProcess exited with code 0\nOutput:\nerror",
            Some(false),
        ),
        ("Chunk ID: a\nOutput:\nProcess exited with code 7", None),
        (
            "Wall time: 0.2 seconds\nProcess exited with code 7\nOutput:",
            Some(true),
        ),
        ("Script completed\nOutput: error", Some(false)),
        ("Script failed", Some(true)),
        ("Script running with cell ID 1", None),
        ("Exit code: 9", Some(true)),
        (r#"{"metadata":{"exit_code":9}}"#, Some(true)),
    ] {
        let result = standalone(json!({"type":"function_call_output","output":output}));
        assert_eq!(result[0].data.is_error, failed, "{output}");
    }
    let search = standalone(
        json!({"type":"web_search_call","id":"w","status":"declined","action":{"type":"search","query":"docs"}}),
    );
    assert_eq!(search[0].data.tool_name.as_deref(), Some("web_search"));
    assert_eq!(search[1].data.is_error, Some(true));
    let image = standalone(
        json!({"type":"image_generation_call","result":"iVBORw0KGgo=","revised_prompt":"illustration"}),
    );
    assert_eq!(image[1].data.images[0].media_type, "image/png");
    let shell = standalone(
        json!({"type":"local_shell_call","action":{"command":["ls","-l"],"timeout_ms":500,"working_directory":"/work"}}),
    );
    assert_eq!(
        shell[0].data.tool_input.as_ref().unwrap()["command"],
        "ls -l"
    );
    let agent =
        standalone(json!({"type":"agent_message","author":"peer","content":"Review the tests"}));
    assert_eq!(agent[0].data.role.as_deref(), Some("agent"));
    assert_eq!(agent[0].data.title, None);
}

#[test]
fn claude_and_pi_keep_inline_images_and_reasoning_counts() {
    let mut claude = ClaudeCodeParser::new();
    let prompt = claude.parse_line(br#"{"type":"user","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}}"#).unwrap();
    assert_eq!(
        prompt.last().unwrap().data.images[0].media_type,
        "image/png"
    );
    let answer = claude.parse_line(br#"{"type":"assistant","message":{"content":[{"type":"text","text":"Done"}],"usage":{"input_tokens":4,"output_tokens":7,"output_tokens_details":{"thinking_tokens":3}}}}"#).unwrap();
    let usage = answer.last().unwrap().data.usage.as_ref().unwrap();
    assert_eq!((usage.reasoning, usage.total), (3, 11));
    let pi = parse_pi_line(br#"{"type":"message","message":{"role":"user","content":[{"type":"image","mimeType":"image/png","data":"iVBORw0KGgo="}]}}"#, "").unwrap();
    assert_eq!(pi[0].data.images[0].media_type, "image/png");
}
