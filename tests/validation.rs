use agent_parser::*;
use serde_json::json;
#[test]
fn rejects_mistyped_known_fields_instead_of_silently_losing_data() {
    for value in [
        json!({"type":false}),
        json!({"cwd":7}),
        json!({"message":[]}),
        json!({"message":{"role":5}}),
        json!({"message":{"usage":{"input_tokens":"5"}}}),
        json!({"isSidechain":"true"}),
    ] {
        assert!(
            ClaudeCodeParser::new()
                .parse_line(&serde_json::to_vec(&value).unwrap())
                .is_err(),
            "{value}"
        )
    }
    for value in [
        json!({"type":5}),
        json!({"message":{"usage":{"input":"5"}}}),
        json!({"message":{"isError":"true"}}),
    ] {
        assert!(
            parse_pi_line(&serde_json::to_vec(&value).unwrap(), "").is_err(),
            "{value}"
        )
    }
    for value in [
        json!({"type":5}),
        json!({"type":"session_meta","payload":{"cwd":5}}),
        json!({"type":"turn_context","payload":{"model":5}}),
        json!({"type":"event_msg","payload":{"message":5}}),
        json!({"type":"event_msg","payload":{"info":{"last_token_usage":{"prompt_tokens":"5"}}}}),
        json!({"type":"response_item","payload":{"arguments":[]}}),
    ] {
        assert!(
            parse_codex_line(&serde_json::to_vec(&value).unwrap()).is_err(),
            "{value}"
        )
    }
}
