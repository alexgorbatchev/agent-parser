use crate::{
    EventData, ParsedEvent,
    common::{event, opt, s},
};
use serde_json::{Map, Value, json};

pub(crate) fn is_tool_item(kind: &str) -> bool {
    matches!(
        kind,
        "CommandExecution"
            | "McpToolCall"
            | "DynamicToolCall"
            | "WebSearch"
            | "Extension"
            | "ImageView"
            | "CollabAgentToolCall"
    )
}

pub(crate) fn item_events(item: &Value, started_at: Option<i64>, ts: i64) -> Vec<ParsedEvent> {
    let kind = s(item, "type");
    if kind == "CollabAgentToolCall" || s(item, "source") == "user_shell" {
        return vec![];
    }
    let (name, input, output) = match kind {
        "CommandExecution" => (
            "exec_command",
            Some(command_input(&item["command"], &file_path(s(item, "cwd")))),
            item["aggregated_output"].as_str().map(str::to_owned),
        ),
        "McpToolCall" => (
            "",
            item["arguments"].as_object().cloned(),
            json_text(&item["result"]).or_else(|| json_text(&item["error"])),
        ),
        "DynamicToolCall" => (
            s(item, "tool"),
            item["arguments"].as_object().cloned(),
            json_text(&item["content_items"]),
        ),
        "WebSearch" => {
            let mut input = Map::from_iter([("query".into(), json!(s(item, "query")))]);
            if item["action"].is_object() {
                input.insert("action".into(), item["action"].clone());
            }
            ("web_search", Some(input), None)
        }
        "Extension" => {
            let input = item
                .as_object()
                .into_iter()
                .flatten()
                .filter(|(key, value)| {
                    !value.is_null()
                        && !matches!(
                            key.as_str(),
                            "type" | "id" | "kind" | "status" | "result" | "results" | "failure"
                        )
                })
                .map(|(key, value)| (key.clone(), value.clone()))
                .collect();
            (s(item, "kind"), Some(input), json_text(&item["results"]))
        }
        "ImageView" => (
            "view_image",
            Some(Map::from_iter([(
                "path".into(),
                json!(file_path(s(item, "path"))),
            )])),
            None,
        ),
        _ => return vec![],
    };
    let id = opt(s(item, "id"));
    let name = if kind == "McpToolCall" {
        format!("mcp__{}__{}", s(item, "server"), s(item, "tool"))
    } else {
        name.to_owned()
    };
    let images = if kind == "Extension" && name.starts_with("image_gen.") {
        crate::images::codex_generated(s(item, "result"))
    } else {
        vec![]
    };
    vec![
        call(
            started_at.filter(|at| *at > 0).unwrap_or(ts),
            name,
            id.clone(),
            input,
        ),
        event(
            "tool_result",
            ts,
            "",
            "",
            EventData {
                role: Some("toolResult".into()),
                tool_call_id: id,
                tool_output: output,
                is_error: item_failed(item),
                images,
                ..Default::default()
            },
        ),
    ]
}

pub(crate) fn call(
    ts: i64,
    name: String,
    id: Option<String>,
    input: Option<Map<String, Value>>,
) -> ParsedEvent {
    event(
        "tool_call",
        ts,
        "",
        "",
        EventData {
            role: Some("assistant".into()),
            tool_name: Some(name),
            tool_call_id: id,
            tool_input: input,
            ..Default::default()
        },
    )
}

pub(crate) fn item_failed(item: &Value) -> Option<bool> {
    let outcome = match s(item, "type") {
        "CommandExecution" => item["exit_code"].as_i64().map(|exit| exit != 0),
        "McpToolCall" if item["result"]["isError"] == true || !item["error"].is_null() => {
            Some(true)
        }
        "Extension" if !item["failure"].is_null() => Some(true),
        "DynamicToolCall" => item["success"].as_bool().map(|success| !success),
        _ => None,
    };
    outcome.or_else(|| status_failed(s(item, "status")))
}

pub(crate) fn status_failed(status: &str) -> Option<bool> {
    match status {
        "completed" => Some(false),
        "failed" | "declined" => Some(true),
        _ => None,
    }
}

pub(crate) fn output_failed(head: &str) -> Option<bool> {
    let first = head.split_once('\n').map_or(head, |(first, _)| first);
    if first.starts_with("Script completed") {
        Some(false)
    } else if first.starts_with("Script failed") || first.starts_with("Script terminated") {
        Some(true)
    } else if let Some(code) = first.strip_prefix("Exit code: ") {
        exit_failed(code)
    } else if first.starts_with("Chunk ID: ") || first.starts_with("Wall time: ") {
        head.lines()
            .take_while(|line| *line != "Output:")
            .find_map(|line| line.strip_prefix("Process exited with code "))
            .and_then(exit_failed)
    } else if head.starts_with('{') {
        let result = serde_json::from_str::<Value>(head).ok()?;
        Some(result["metadata"]["exit_code"].as_i64()? != 0)
    } else {
        None
    }
}

fn exit_failed(code: &str) -> Option<bool> {
    Some(code.trim().parse::<i64>().ok()? != 0)
}

pub(crate) fn command_input(command: &Value, cwd: &str) -> Map<String, Value> {
    let argv: Vec<_> = command
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(Value::as_str)
        .collect();
    let script = match argv.as_slice() {
        [_, "-lc" | "-c", script, ..] => (*script).into(),
        _ => argv.join(" "),
    };
    let mut input = Map::from_iter([
        ("command".into(), json!(script)),
        ("argv".into(), json!(argv)),
    ]);
    if !cwd.is_empty() {
        input.insert("cwd".into(), json!(cwd));
    }
    input
}

fn file_path(value: &str) -> String {
    if let Ok(url) = url::Url::parse(value)
        && url.scheme() == "file"
    {
        return url
            .to_file_path()
            .ok()
            .and_then(|path| path.into_os_string().into_string().ok())
            .unwrap_or_else(|| url.path().to_owned());
    }
    value.to_owned()
}

pub(crate) fn json_text(value: &Value) -> Option<String> {
    if value.is_null() {
        None
    } else {
        Some(
            value
                .as_str()
                .map_or_else(|| value.to_string(), str::to_owned),
        )
    }
}
