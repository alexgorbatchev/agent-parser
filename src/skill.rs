use crate::{Error, EventData, common::s};
use regex::Regex;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use std::{collections::HashMap, sync::LazyLock};

#[path = "skill_shell.rs"]
mod shell;

/// Origin of a skill invocation recorded by a harness.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum SkillSource {
    Model,
    User,
}

/// One invocation of an installed skill.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct SkillInvocation {
    pub name: String,
    pub source: SkillSource,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub path: String,
}

impl SkillInvocation {
    fn new(name: &str, source: SkillSource, path: &str) -> Self {
        Self {
            name: name.into(),
            source,
            path: path.into(),
        }
    }
}

fn read_skill(file: &str) -> Option<SkillInvocation> {
    let normalized = file.replace('\\', "/");
    let mut components = vec![];
    let absolute = normalized.starts_with('/');
    for part in normalized.split('/') {
        match part {
            "" | "." => {}
            ".." if components.last().is_some_and(|prior| *prior != "..") => {
                components.pop();
            }
            ".." if absolute => {}
            _ => components.push(part),
        }
    }
    let name = match components.as_slice() {
        [.., "skills", name, "SKILL.md"]
        | [.., "skills", ".system", name, "SKILL.md"]
        | [.., "skills", "synced", _, name, "SKILL.md"] => name,
        _ => return None,
    };
    let root = if absolute { "/" } else { "" };
    Some(SkillInvocation::new(
        name,
        SkillSource::Model,
        &format!("{root}{}", components.join("/")),
    ))
}

type ToolInputs = Map<String, Value>;

pub(crate) fn named_tool(input: Option<&ToolInputs>, field: &str) -> Vec<SkillInvocation> {
    input
        .and_then(|input| text_arg(input, field))
        .map(|name| SkillInvocation::new(name, SkillSource::Model, ""))
        .into_iter()
        .collect()
}

fn text_arg<'a>(input: &'a ToolInputs, key: &str) -> Option<&'a str> {
    input
        .get(key)
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|text| !text.is_empty())
}

pub(crate) fn read_tool(input: Option<&ToolInputs>) -> Vec<SkillInvocation> {
    let Some(input) = input else { return vec![] };
    if ["offset", "StartLine", "start_line"].iter().any(|key| {
        input
            .get(*key)
            .and_then(Value::as_f64)
            .is_some_and(|offset| offset > 1.0)
    }) {
        return vec![];
    }
    [
        "path",
        "file_path",
        "Path",
        "AbsolutePath",
        "absolute_path",
        "TargetFile",
    ]
    .into_iter()
    .filter_map(|key| text_arg(input, key))
    .find_map(read_skill)
    .into_iter()
    .collect()
}

pub(crate) fn agy_tool(name: &str, input: Option<&ToolInputs>) -> Vec<SkillInvocation> {
    let Some(input) = input else { return vec![] };
    match name.trim().to_ascii_lowercase().as_str() {
        "read_file" | "view_file" | "client_view_file" | "read" => read_tool(Some(input)),
        "run_command" | "bash" | "shell" => {
            ["CommandLine", "command_line", "commandLine", "command"]
                .into_iter()
                .find_map(|key| text_arg(input, key))
                .map(shell::read_skills)
                .unwrap_or_default()
        }
        _ => vec![],
    }
}

pub(crate) fn pi_prompt(prompt: &str) -> Vec<SkillInvocation> {
    static SKILL: LazyLock<Regex> = LazyLock::new(|| {
        Regex::new(r#"^<skill name="([^"]+)"(?: location="([^"]*)")?"#).expect("valid skill regex")
    });
    SKILL
        .captures(prompt.trim())
        .map(|captures| {
            SkillInvocation::new(
                captures[1].trim(),
                SkillSource::User,
                captures.get(2).map_or("", |path| path.as_str().trim()),
            )
        })
        .into_iter()
        .collect()
}

pub(crate) fn claude_prompt(
    raw: &Value,
    session: &str,
    prompt: &str,
    pending: &mut HashMap<String, String>,
) -> Vec<SkillInvocation> {
    static COMMAND: LazyLock<Regex> = LazyLock::new(|| {
        Regex::new(r"<command-name>/([^<\s]+)</command-name>").expect("valid command regex")
    });
    if let Some(command) = COMMAND.captures(prompt) {
        pending.insert(session.into(), command[1].into());
    } else if raw["isMeta"].as_bool() != Some(true) {
        pending.remove(session);
    } else if s(raw, "sourceToolUseID").is_empty()
        && let Some(path) = prompt
            .strip_prefix("Base directory for this skill: ")
            .and_then(|text| text.lines().next())
            .map(str::trim)
            .filter(|path| !path.is_empty())
    {
        let name = pending.remove(session).unwrap_or_else(|| {
            path.trim_end_matches('/')
                .rsplit('/')
                .next()
                .unwrap_or("/")
                .into()
        });
        return vec![SkillInvocation::new(&name, SkillSource::User, path)];
    }
    vec![]
}

pub(crate) fn codex_item(item: &Value) -> Result<Option<EventData>, Error> {
    let (role, skills) = match s(item, "type") {
        "UserMessage" | "user_message" => {
            let contents: Option<Vec<CodexSkillContent>> =
                serde_json::from_value(item["content"].clone())?;
            let skills = contents
                .unwrap_or_default()
                .into_iter()
                .filter(|part| part.kind == "skill" && !part.name.trim().is_empty())
                .map(|part| SkillInvocation::new(part.name.trim(), SkillSource::User, &part.path))
                .collect();
            ("user", skills)
        }
        "CommandExecution" | "command_execution" => {
            let command: CodexCommand = serde_json::from_value(item.clone())?;
            if command.exit_code != Some(0) || command.source == "user_shell" {
                return Ok(None);
            }
            let script = match command.command.as_slice() {
                [_, flag, script, ..] if matches!(flag.as_str(), "-lc" | "-c") => script.clone(),
                argv => argv.join(" "),
            };
            ("assistant", shell::read_skills(&script))
        }
        _ => return Ok(None),
    };
    Ok((!skills.is_empty()).then(|| EventData {
        role: Some(role.into()),
        skills,
        ..Default::default()
    }))
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct CodexSkillContent {
    #[serde(rename = "type")]
    kind: String,
    name: String,
    path: String,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct CodexCommand {
    command: Vec<String>,
    exit_code: Option<i64>,
    source: String,
}
