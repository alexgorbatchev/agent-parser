use agent_parser::*;
use serde_json::{Value, json};
fn timeline(error: Option<Value>, finish: Option<&str>, parts: Vec<Value>) -> Vec<ParsedEvent> {
    let session = OpencodeSession {
        id: "s".into(),
        model: "m".into(),
        time_created: 1000,
        ..Default::default()
    };
    let message = OpencodeMessage {
        id: "a".into(),
        time_created: 2000,
        data: OpencodeMessageData {
            role: "assistant".into(),
            error,
            finish: finish.map(str::to_owned),
            ..Default::default()
        },
        ..Default::default()
    };
    let parts = parts
        .into_iter()
        .enumerate()
        .map(|(i, data)| OpencodePart {
            message_id: "a".into(),
            time_created: 2100 + i as i64,
            data: serde_json::from_value(data).unwrap(),
            ..Default::default()
        })
        .collect::<Vec<_>>();
    reconstruct_session_events(&session, &[message], &parts)
}
#[test]
fn terminal_error_payloads_choose_meaningful_messages() {
    for (error, text) in [
        (json!(" raw "), "raw"),
        (json!({"data":{"exception":"exception"}}), "exception"),
        (json!({"data":{"error":"inner"}}), "inner"),
        (json!({"data":"data text"}), "data text"),
        (json!({"message":"message"}), "message"),
        (json!({"error":{"message":"recursive"}}), "recursive"),
        (json!({"exception":"exception"}), "exception"),
        (json!({"text":"text"}), "text"),
        (json!({"details":"details"}), "details"),
        (json!({"name":"NamedError"}), "NamedError"),
        (json!({"unknown":123}), "{\"unknown\":123}"),
        (json!(["first",{"message":"second"}]), "first\nsecond"),
        (json!(42), "42"),
    ] {
        let events = timeline(Some(error), None, vec![]);
        assert_eq!(events.last().unwrap().event_type, "error");
        assert_eq!(events.last().unwrap().data.content.as_deref(), Some(text))
    }
    for empty in [
        Value::Null,
        json!(" "),
        json!({}),
        json!([]),
        json!({"data":{"error":[]}}),
    ] {
        let events = timeline(Some(empty), None, vec![]);
        assert_eq!(events.len(), 1)
    }
    let events = timeline(None, Some("error"), vec![]);
    assert_eq!(
        events.last().unwrap().data.content.as_deref(),
        Some("error")
    );
}
#[test]
fn cancellation_is_not_reported_as_terminal_error() {
    for error in [
        json!("MessageAbortedError"),
        json!({"name":"MessageAbortedError","data":{"message":"cancelled"}}),
        json!({"error":{"message":"MessageAbortedError: stopped"}}),
        json!({"data":{"name":"MessageAbortedError"}}),
    ] {
        assert!(
            timeline(Some(error), Some("error"), vec![])
                .iter()
                .all(|e| e.event_type != "error")
        )
    }
}
#[test]
fn retry_headers_support_numeric_dates_and_invalid_values() {
    for (headers, reset, seconds) in [
        (json!({"RETRY-AFTER":2.5}), 4500, 2),
        (
            json!({"retry_after":"Thu, 01 Jan 1970 00:00:10 GMT"}),
            10000,
            8,
        ),
        (json!({"retry-after":"1970-01-01T00:00:10Z"}), 10000, 8),
    ] {
        let events = timeline(
            Some(json!({"data":{"statusCode":429,"message":"wait","headers":headers}})),
            Some("error"),
            vec![],
        );
        let error = events.last().unwrap();
        assert_eq!(error.data.is_rate_limit, Some(true));
        assert_eq!(error.data.rate_limit_reset_at, Some(reset));
        assert_eq!(error.data.retry_after_seconds, Some(seconds))
    }
    let events = timeline(
        Some(json!({"status":500,"headers":{"retry-after":"invalid","other":"value"}})),
        Some("error"),
        vec![],
    );
    assert_eq!(events.last().unwrap().data.is_rate_limit, Some(false));
    let events = timeline(
        None,
        Some("error"),
        vec![
            json!({"type":"retry","error":{"data":{"statusCode":429,"message":"try later","responseHeaders":{"Retry-After":"3"}}}}),
        ],
    );
    let errors = events
        .iter()
        .filter(|e| e.event_type == "error")
        .collect::<Vec<_>>();
    assert_eq!(errors.len(), 2);
    assert_eq!(errors[0].data.content.as_deref(), Some("try later"));
    assert_eq!(errors[1].data.rate_limit_reset_at, Some(5100));
    let events = timeline(
        None,
        Some("error"),
        vec![json!({"type":"retry","error":"upstream failed"})],
    );
    assert_eq!(
        events.last().unwrap().data.content.as_deref(),
        Some("upstream failed")
    );
}
#[test]
fn tool_states_errors_and_streamed_text_are_preserved() {
    let events = timeline(
        None,
        Some("error"),
        vec![
            json!({"type":"reasoning","text":"plan"}),
            json!({"type":"text","text":"answer"}),
            json!({"type":"tool","tool":"shell","callID":"id","state":{"status":"error","error":"command failed","title":"Command"}}),
        ],
    );
    assert_eq!(events[1].event_type, "thinking");
    assert_eq!(events[2].data.content.as_deref(), Some("answer"));
    let result = events
        .iter()
        .find(|e| e.event_type == "tool_result")
        .unwrap();
    assert_eq!(result.data.is_error, Some(true));
    assert_eq!(result.data.tool_output.as_deref(), Some("command failed"));
    assert_eq!(
        events.last().unwrap().data.content.as_deref(),
        Some("command failed")
    );
    for state in [None, Some(json!({"status":"running"}))] {
        let mut part = json!({"type":"tool","tool":"shell","error":{"message":"part failed"}});
        if let Some(state) = state {
            part["state"] = state
        }
        let events = timeline(None, Some("error"), vec![part]);
        assert_eq!(
            events.last().unwrap().data.content.as_deref(),
            Some("part failed")
        );
        assert!(!events.iter().any(|e| e.event_type == "tool_result"))
    }
    let events = timeline(
        None,
        None,
        vec![
            json!({"type":"tool"}),
            json!({"type":"text","text":""}),
            json!({"type":"retry"}),
            json!({"type":"unknown"}),
        ],
    );
    assert_eq!(events.len(), 1);
}
#[test]
fn model_parsing_handles_unknown_and_invalid_configs() {
    for (raw, id) in [
        ("", "unknown"),
        ("plain", "plain"),
        ("{}", "{}"),
        ("{bad}", "{bad}"),
    ] {
        let (model, cfg) = parse_opencode_model(raw);
        assert_eq!(model, id);
        assert!(cfg.is_none())
    }
}
