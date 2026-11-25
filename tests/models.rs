use agent_parser::*;

#[test]
fn events_round_trip_optional_tool_data_and_diff_line_numbers() {
    let event: ParsedEvent = serde_json::from_str(r#"{"eventType":"tool_call","timestamp":123,"cwd":"/project","gitBranch":"main","data":{"toolName":"Edit","toolInput":{"file_path":"src/lib.rs"},"fileModification":{"action":"EDIT","filePath":"src/lib.rs","hunks":[{"lines":[{"type":"del","oldLineNumber":2,"content":"before"},{"type":"add","newLineNumber":2,"content":"after"}]}]},"usage":{"input":2,"output":3,"cacheRead":4,"cacheWrite":5,"reasoning":6,"total":20}}}"#).unwrap();
    assert_eq!(event.data.usage.as_ref().unwrap().total, 20);
    assert_eq!(
        event.data.file_modification.as_ref().unwrap().hunks[0].lines[0].old_line_number,
        Some(2)
    );
    let json = serde_json::to_value(&event).unwrap();
    assert!(json["data"].get("content").is_none());
    assert_eq!(json["data"]["toolInput"]["file_path"], "src/lib.rs");
    assert_eq!(serde_json::from_value::<ParsedEvent>(json).unwrap(), event);
}
