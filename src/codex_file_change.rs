use crate::{
    Error, EventData, FileModification, ParsedEvent,
    common::{event, opt},
    file_modification::parse_patch,
};
use serde::Deserialize;
use serde_json::Value;
use std::collections::BTreeMap;

#[derive(Default, Deserialize)]
#[serde(default)]
struct Patch {
    id: String,
    call_id: String,
    status: String,
    success: Option<bool>,
    changes: BTreeMap<String, Change>,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct Change {
    #[serde(rename = "type")]
    kind: String,
    content: String,
    unified_diff: String,
    move_path: Option<String>,
}

pub(crate) fn events(raw: &Value, timestamp: i64) -> Result<Vec<ParsedEvent>, Error> {
    let patch: Patch = serde_json::from_value(raw.clone())?;
    if !(patch.status == "completed" || (patch.status.is_empty() && patch.success != Some(false))) {
        return Ok(vec![]);
    }
    let call = opt(&patch.call_id).or_else(|| opt(&patch.id));
    Ok(patch
        .changes
        .into_iter()
        .filter_map(|(path, change)| {
            let modification = file_change(path, change)?;
            Some(event(
                "file_modification",
                timestamp,
                "",
                "",
                EventData {
                    role: Some("assistant".into()),
                    tool_name: Some("apply_patch".into()),
                    tool_call_id: call.clone(),
                    file_modification: Some(modification),
                    ..Default::default()
                },
            ))
        })
        .collect())
}

fn file_change(path: String, change: Change) -> Option<FileModification> {
    let modification = match change.kind.as_str() {
        "add" | "delete" => FileModification {
            action: if change.kind == "add" {
                "WRITE"
            } else {
                "DELETE"
            }
            .into(),
            file_path: path,
            content: Some(change.content),
            ..Default::default()
        },
        "update" => {
            let hunks = parse_patch(&change.unified_diff, &path, true)
                .map_or_else(Vec::new, |edit| edit.hunks);
            let (file_path, previous_path) = match change.move_path.filter(|path| !path.is_empty())
            {
                Some(destination) => (destination, path),
                None => (path, String::new()),
            };
            FileModification {
                action: "EDIT".into(),
                file_path,
                previous_path,
                hunks,
                ..Default::default()
            }
        }
        _ => return None,
    };
    Some(modification)
}
