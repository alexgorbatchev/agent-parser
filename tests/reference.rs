use agent_parser::*;
use serde_json::Value;
#[test]
fn matches_go_parser_reference_corpus() {
    let mut differences = vec![];
    let mut count = 0;
    for (i, line) in include_str!("fixtures.jsonl").lines().enumerate() {
        let case: Value = serde_json::from_str(line).unwrap();
        let input = serde_json::to_vec(&case["input"]).unwrap();
        let result = match case["harness"].as_str().unwrap() {
            "claudecode" => ClaudeCodeParser::new().parse_line(&input),
            "pi" => parse_pi_line(&input, "/fallback"),
            "codex" => parse_codex_line(&input),
            _ => unreachable!(),
        };
        match result {
            Ok(events) => {
                let actual = serde_json::to_value(events).unwrap();
                if actual != case["events"] {
                    differences.push(format!(
                        "case {} {}\ninput={}\nactual={}\nexpected={}",
                        i + 1,
                        case["harness"],
                        case["input"],
                        actual,
                        case["events"]
                    ))
                }
            }
            Err(error) => differences.push(format!("case {} failed: {error}", i + 1)),
        }
        count += 1;
    }
    assert!(count > 100);
    assert!(
        differences.is_empty(),
        "{} differences:\n{}",
        differences.len(),
        differences.join("\n")
    );
}
