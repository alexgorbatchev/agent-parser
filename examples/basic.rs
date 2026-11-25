use agent_parser::ClaudeCodeParser;

fn main() -> Result<(), agent_parser::Error> {
    let mut parser = ClaudeCodeParser::new();
    let line = br#"{"type":"user","sessionId":"session","timestamp":1000,"message":{"role":"user","content":"Explain this code"}}"#;
    for event in parser.parse_line(line)? {
        println!("{}: {}", event.timestamp, event.event_type);
    }
    Ok(())
}
