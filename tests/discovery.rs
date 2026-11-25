use agent_parser::{DiscoverOptions, Harness, discover_sessions, open_transcript};
use std::{fs, io::Read, path::Path};

fn fixture(home: &Path, path: &str, contents: &[u8]) {
    let path = home.join(path);
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    fs::write(path, contents).unwrap();
}

#[test]
fn discovers_nested_and_archived_sessions_without_treating_other_logs_as_transcripts() {
    let temp = tempfile::tempdir().unwrap();
    let home = temp.path();
    let stamp = "2026-01-01T00:00:00Z";
    fixture(
        home,
        ".claude/projects/p/root.jsonl",
        format!(r#"{{"timestamp":"{stamp}","message":{{"content":[{{"type":"tool_use","id":"call"}}]}}}}"#).as_bytes(),
    );
    fixture(
        home,
        ".claude/projects/p/root/subagents/workflows/run/agent-fork.jsonl",
        b"",
    );
    fixture(
        home,
        ".claude/projects/p/root/subagents/workflows/run/agent-fork.meta.json",
        br#"{"isFork":true,"toolUseId":"call"}"#,
    );
    fixture(
        home,
        ".claude/projects/p/root/subagents/workflows/run/journal.jsonl",
        b"",
    );
    fixture(home, ".pi/agent/sessions/p/ts_parent.jsonl", b"");
    fixture(
        home,
        ".pi/agent/sessions/p/ts_parent/parallel/run-1/session.jsonl",
        br#"{"type":"session","id":"run"}"#,
    );
    fixture(
        home,
        ".pi/agent/sessions/p/ts_parent/forks/ts_child.jsonl",
        format!(r#"{{"type":"session","id":"child","timestamp":"{stamp}"}}"#).as_bytes(),
    );
    let compressed = zstd::stream::encode_all(b"{}\n".as_slice(), 1).unwrap();
    fixture(
        home,
        ".codex/archived_sessions/rollout-2026-01-01T00-00-00-archive.jsonl.zst",
        &compressed,
    );
    fixture(home, ".codex/sessions/not-a-rollout.jsonl", b"");
    fixture(
        home,
        ".gemini/antigravity-cli/brain/p/.system_generated/subagents/child.json",
        b"{}",
    );
    fixture(
        home,
        ".gemini/antigravity-cli/brain/child/.system_generated/logs/transcript_full.jsonl",
        b"{}",
    );
    fixture(
        home,
        ".gemini/antigravity-cli/brain/child/.system_generated/logs/transcript.jsonl",
        b"",
    );

    let discovery = discover_sessions(&DiscoverOptions {
        home: home.into(),
        harnesses: Vec::new(),
    });
    assert!(discovery.errors.is_empty(), "{:?}", discovery.errors);
    let sources = discovery.sources;
    assert_eq!(
        sources
            .iter()
            .map(|source| source.harness)
            .collect::<Vec<_>>(),
        [
            Harness::Agy,
            Harness::ClaudeCode,
            Harness::ClaudeCode,
            Harness::Codex,
            Harness::Pi,
            Harness::Pi,
            Harness::Pi,
        ]
    );
    let child = sources
        .iter()
        .find(|source| source.session_id == "child" && source.harness == Harness::Agy)
        .unwrap();
    assert_eq!(child.parent_session_id, "p");
    assert!(child.subagent);
    assert!(child.path.ends_with("transcript_full.jsonl"));
    let fork = sources
        .iter()
        .find(|source| source.session_id == "agent-fork")
        .unwrap();
    assert!(fork.subagent && fork.is_fork);
    assert_eq!(fork.forked_at, Some(1_767_225_600_000));
    let pi_fork = sources
        .iter()
        .find(|source| source.session_id == "child" && source.harness == Harness::Pi)
        .unwrap();
    assert!(pi_fork.is_fork);
    assert_eq!(pi_fork.parent_session_id, "parent");
    assert_eq!(pi_fork.forked_at, fork.forked_at);
    let run = sources
        .iter()
        .find(|source| source.session_id == "run")
        .unwrap();
    assert!(run.subagent);
    assert_eq!(run.parent_session_id, "parent");
    let archive = sources.iter().find(|source| source.archived).unwrap();
    assert_eq!(archive.session_id, "archive");
    let mut text = String::new();
    open_transcript(&archive.path)
        .unwrap()
        .read_to_string(&mut text)
        .unwrap();
    assert_eq!(text, "{}\n");
}

#[test]
fn malformed_fork_metadata_keeps_the_source_and_other_harnesses() {
    let temp = tempfile::tempdir().unwrap();
    let home = temp.path();
    fixture(
        home,
        ".claude/projects/p/root/subagents/agent-incomplete.jsonl",
        b"",
    );
    fixture(
        home,
        ".pi/agent/sessions/p/ts_parent/forks/ts_child.jsonl",
        b"{}",
    );
    fixture(
        home,
        ".pi/agent/sessions/p/ts_parent/parallel/run-1/session.jsonl",
        b"{}",
    );

    let discovery = discover_sessions(&DiscoverOptions {
        home: home.into(),
        harnesses: vec![Harness::Pi, Harness::ClaudeCode],
    });
    assert_eq!(discovery.errors.len(), 3, "{:?}", discovery.errors);
    assert_eq!(discovery.sources.len(), 2);
    assert_eq!(discovery.sources[0].session_id, "agent-incomplete");
    assert_eq!(discovery.sources[1].session_id, "child");
    assert!(discovery.sources[1].is_fork);
    assert_eq!(discovery.sources[1].forked_at, None);
    assert!("invalid".parse::<Harness>().is_err());
    assert_eq!(
        "claude-code".parse::<Harness>().unwrap().to_string(),
        "claude-code"
    );
}
