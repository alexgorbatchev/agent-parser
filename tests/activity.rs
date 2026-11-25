use agent_parser::{
    agy_activity_timestamp, claude_code_activity_timestamp, codex_activity_timestamp,
    pi_activity_timestamp,
};

#[test]
fn activity_uses_native_envelope_time_and_skips_metadata() {
    let stamp = "2026-10-22T15:55:32.576-07:00";
    let wanted = Some(1_792_709_732_576);
    assert_eq!(
        claude_code_activity_timestamp(
            format!(r#"{{"type":"progress","timestamp":"{stamp}"}}"#).as_bytes()
        )
        .unwrap(),
        wanted
    );
    assert_eq!(
        pi_activity_timestamp(
            format!(
                r#"{{"type":"branch_summary","timestamp":"{stamp}","message":{{"timestamp":123}}}}"#
            )
            .as_bytes()
        )
        .unwrap(),
        wanted
    );
    assert_eq!(codex_activity_timestamp(format!(r#"{{"type":"event_msg","timestamp":"{stamp}","payload":{{"type":"task_complete","completed_at":1792709700}}}}"#).as_bytes()).unwrap(), wanted);
    assert_eq!(
        agy_activity_timestamp(
            format!(r#"{{"type":"PLANNER_RESPONSE","created_at":"{stamp}"}}"#).as_bytes()
        )
        .unwrap(),
        wanted
    );
    assert_eq!(
        claude_code_activity_timestamp(br#"{"type":"queue-operation","timestamp":false}"#).unwrap(),
        None
    );
    assert_eq!(
        pi_activity_timestamp(br#"{"type":"session","timestamp":false}"#).unwrap(),
        None
    );
    assert_eq!(
        codex_activity_timestamp(br#"{"type":"session_meta","timestamp":false}"#).unwrap(),
        None
    );
    assert_eq!(
        agy_activity_timestamp(br#"{"type":"SYSTEM_MESSAGE","created_at":false}"#).unwrap(),
        None
    );
}

#[test]
fn activity_rejects_invalid_times_and_preserves_epoch() {
    for line in [
        b" \n".as_slice(),
        b"null",
        br#"{"type":"user"}"#,
        br#"{"type":"user","timestamp":""}"#,
    ] {
        assert_eq!(claude_code_activity_timestamp(line).unwrap(), None);
    }
    for line in [
        br#"{"type":"user","timestamp":123}"#.as_slice(),
        br#"{"type":"user","timestamp":"2026-10-22T15:55:32"}"#,
        br#"{"type":17}"#,
    ] {
        assert!(claude_code_activity_timestamp(line).is_err());
    }
    assert!(pi_activity_timestamp(b"[]").is_err());
    assert!(codex_activity_timestamp(br#"{"type":"event_msg","payload":[]}"#).is_err());
    assert!(codex_activity_timestamp(br#"{"type":"response_item"}"#).is_err());
    assert_eq!(
        codex_activity_timestamp(br#"{"type":"response_item","payload":null}"#).unwrap(),
        None
    );
    assert_eq!(codex_activity_timestamp(br#"{"type":"response_item","timestamp":"1970-01-01T00:00:00Z","payload":{"type":"tool_search_call"}}"#).unwrap(), Some(0));
    assert_eq!(
        codex_activity_timestamp(br#"{"type":"turn_context","timestamp":"1970-01-01T00:00:00Z"}"#)
            .unwrap(),
        Some(0)
    );
    assert_eq!(
        agy_activity_timestamp(br#"{"type":"GENERIC","created_at":"1970-01-01T00:00:00Z"}"#)
            .unwrap(),
        Some(0)
    );
}
