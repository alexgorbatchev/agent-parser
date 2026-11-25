use agent_parser::{
    AgyParser, ClaudeCodeParser, SkillInvocation, SkillSource, parse_codex_line, parse_pi_line,
};
use serde_json::json;

#[test]
fn claude_counts_tool_and_slash_skills_once() {
    let mut parser = ClaudeCodeParser::new();
    let records = [
        json!({"type":"assistant","sessionId":"s","message":{"content":[{"type":"tool_use","id":"1","name":"Skill","input":{"skill":"review"}}]}}),
        json!({"type":"user","sessionId":"s","isMeta":true,"sourceToolUseID":"1","message":{"content":"Base directory for this skill: /home/.claude/skills/review"}}),
        json!({"type":"user","sessionId":"s","message":{"content":"<command-name>/plugin:deploy</command-name>"}}),
        json!({"type":"user","sessionId":"s","isMeta":true,"message":{"content":"Base directory for this skill: /x/skills/deploy\n\nBody"}}),
        json!({"type":"user","sessionId":"s","isMeta":true,"message":{"content":"Base directory for this skill: /x/skills/other"}}),
    ];
    let skills: Vec<_> = records
        .iter()
        .flat_map(|record| parser.parse_line(record.to_string().as_bytes()).unwrap())
        .flat_map(|event| event.data.skills)
        .collect();
    assert_eq!(
        skills,
        [
            skill("review", SkillSource::Model, ""),
            skill("plugin:deploy", SkillSource::User, "/x/skills/deploy"),
            skill("other", SkillSource::User, "/x/skills/other"),
        ]
    );
}

#[test]
fn codex_counts_explicit_mentions_and_successful_model_reads() {
    let mut skills = vec![];
    for item in [
        json!({"type":"UserMessage","content":[{"type":"text","text":"hello"},{"type":"skill","name":"build","path":"skills/build/SKILL.md"}]}),
        json!({"type":"CommandExecution","command":["/bin/zsh","-lc","cat skills/docs/SKILL.md"],"exit_code":0}),
        json!({"type":"command_execution","command":["cat","skills/failed/SKILL.md"],"exit_code":1}),
        json!({"type":"command_execution","command":["cat","skills/user-shell/SKILL.md"],"exit_code":0,"source":"user_shell"}),
    ] {
        let record = json!({"type":"event_msg","payload":{"type":"item_completed","item":item}});
        skills.extend(
            parse_codex_line(record.to_string().as_bytes())
                .unwrap()
                .into_iter()
                .flat_map(|event| event.data.skills),
        );
    }
    assert_eq!(
        skills,
        [
            skill("build", SkillSource::User, "skills/build/SKILL.md"),
            skill("docs", SkillSource::Model, "skills/docs/SKILL.md"),
        ]
    );
    let bad = br#"{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":123}}}"#;
    assert!(parse_codex_line(bad).is_err());
}

#[test]
fn pi_and_antigravity_track_installed_reads_from_the_first_line() {
    let user = br#"{"type":"message","message":{"role":"user","content":[{"type":"text","text":"<skill name=\"start\" location=\"skills/start/SKILL.md\">body</skill>"}]}}"#;
    let tools = br#"{"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","name":"read","arguments":{"path":"C:\\base\\skills\\read\\SKILL.md"}},{"type":"toolCall","name":"read","arguments":{"path":"skills/old/SKILL.md","offset":9}}]}}"#;
    let skills: Vec<_> = [user.as_slice(), tools.as_slice()]
        .into_iter()
        .flat_map(|line| parse_pi_line(line, "/repo").unwrap())
        .flat_map(|event| event.data.skills)
        .collect();
    assert_eq!(
        skills,
        [
            skill("start", SkillSource::User, "skills/start/SKILL.md"),
            skill("read", SkillSource::Model, "C:/base/skills/read/SKILL.md"),
        ]
    );
    let agy = br#"{"type":"PLANNER_RESPONSE","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"skills/demo/SKILL.md"}},{"name":"run_command","args":{"CommandLine":"cat skills/cli/SKILL.md"}},{"name":"edit","args":{"path":"skills/demo/SKILL.md"}}]}"#;
    let skills: Vec<_> = AgyParser::new()
        .parse_line(agy)
        .unwrap()
        .into_iter()
        .flat_map(|event| event.data.skills)
        .collect();
    assert_eq!(
        skills,
        [
            skill("demo", SkillSource::Model, "skills/demo/SKILL.md"),
            skill("cli", SkillSource::Model, "skills/cli/SKILL.md"),
        ]
    );
}

fn skill(name: &str, source: SkillSource, path: &str) -> SkillInvocation {
    SkillInvocation {
        name: name.into(),
        source,
        path: path.into(),
    }
}
