use crate::common::first;
use crate::{DiffHunk, DiffLine, FileModification};
use serde_json::{Map, Value};
use std::sync::LazyLock;
static HUNK: LazyLock<regex::Regex> =
    LazyLock::new(|| regex::Regex::new(r"@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@").unwrap());
/// Normalize a single file write or edit. Multi-file patches are rejected rather than assigning all hunks to the last file.
pub fn normalize_file_modification(
    tool_name: &str,
    input: Option<&Map<String, Value>>,
) -> Option<FileModification> {
    let v = Value::Object(input?.clone());
    let path = first(
        &v,
        &[
            "file_path",
            "path",
            "filePath",
            "file",
            "target",
            "Path",
            "TargetFile",
            "target_file",
            "AbsolutePath",
            "absolute_path",
        ],
    )
    .trim();
    let path = if path.is_empty() { "untitled" } else { path };
    let patch = first(&v, &["patch", "diff"]);
    if !patch.is_empty() {
        return parse_patch(patch, path);
    }
    let edits = ["edits", "ReplacementChunks", "replacement_chunks"]
        .iter()
        .find_map(|key| v.get(*key));
    let has_edits = edits.is_some()
        || !first(
            &v,
            &[
                "old_string",
                "oldText",
                "old_str",
                "new_string",
                "newText",
                "new_str",
                "Target",
                "TargetContent",
                "target_content",
                "Replacement",
                "ReplacementContent",
                "replacement_content",
            ],
        )
        .is_empty();
    if let Some(content) = ["content", "Content", "CodeContent", "code_content"]
        .iter()
        .find_map(|key| v[*key].as_str())
        && (matches!(
            tool_name.trim().to_lowercase().as_str(),
            "write" | "filewrite" | "write_file" | "writefile" | "write_to_file"
        ) || (path != "untitled" && !has_edits))
    {
        return Some(FileModification {
            action: "WRITE".into(),
            file_path: path.into(),
            content: Some(content.into()),
            hunks: vec![],
        });
    }
    let mut hunks = vec![];
    if let Some(edits) = edits.and_then(Value::as_array).filter(|a| !a.is_empty()) {
        for (i, e) in edits.iter().enumerate().filter(|(_, e)| e.is_object()) {
            hunks.push(build_hunk(
                first(
                    e,
                    &[
                        "oldText",
                        "old_string",
                        "old_str",
                        "TargetContent",
                        "target_content",
                        "Target",
                    ],
                ),
                first(
                    e,
                    &[
                        "newText",
                        "new_string",
                        "new_str",
                        "ReplacementContent",
                        "replacement_content",
                        "Replacement",
                    ],
                ),
                if edits.len() > 1 {
                    format!("Hunk {} of {}", i + 1, edits.len())
                } else {
                    String::new()
                },
            ))
        }
    }
    if hunks.is_empty() {
        let old = first(
            &v,
            &[
                "old_string",
                "oldText",
                "old_str",
                "Target",
                "TargetContent",
                "target_content",
            ],
        );
        let new = first(
            &v,
            &[
                "new_string",
                "newText",
                "new_str",
                "Replacement",
                "ReplacementContent",
                "replacement_content",
            ],
        );
        if old.is_empty() && new.is_empty() {
            return None;
        }
        hunks.push(build_hunk(old, new, String::new()))
    }
    Some(FileModification {
        action: "EDIT".into(),
        file_path: path.into(),
        hunks,
        content: None,
    })
}
fn lines(text: &str) -> Vec<&str> {
    if text.is_empty() {
        vec![]
    } else {
        let mut lines = text.split('\n').collect::<Vec<_>>();
        if lines.last() == Some(&"") {
            lines.pop();
        }
        lines
    }
}
fn build_hunk(old: &str, new: &str, header: String) -> DiffHunk {
    let mut lines_out = vec![];
    for (kind, text) in [("del", old), ("add", new)] {
        for (i, line) in lines(text).into_iter().enumerate() {
            lines_out.push(DiffLine {
                line_type: kind.into(),
                old_line_number: (kind == "del").then_some(i as i64 + 1),
                new_line_number: (kind == "add").then_some(i as i64 + 1),
                content: line.into(),
            })
        }
    }
    DiffHunk {
        header,
        lines: lines_out,
    }
}
fn parse_patch(patch: &str, fallback: &str) -> Option<FileModification> {
    let mut path = fallback.to_owned();
    let mut paths = std::collections::HashSet::new();
    let mut hunks = vec![];
    let mut current: Option<DiffHunk> = None;
    let (mut old, mut new) = (1, 1);
    for line in patch.split_terminator('\n') {
        if let Some(rest) = line.strip_prefix("diff --git ") {
            if let Some(p) = rest.split_whitespace().nth(1) {
                path = p.strip_prefix("b/").unwrap_or(p).to_owned();
                paths.insert(path.clone());
            }
            continue;
        }
        if let Some(p) = line.strip_prefix("+++ b/") {
            path = p.trim().to_owned();
            paths.insert(path.clone());
            continue;
        }
        if line.starts_with("--- ") {
            continue;
        }
        if line.starts_with("@@ ") {
            if let Some(h) = current.take() {
                hunks.push(h)
            }
            current = Some(DiffHunk {
                header: line.trim().into(),
                lines: vec![],
            });
            if let Some(c) = HUNK.captures(line) {
                old = c[1].parse().ok()?;
                new = c[2].parse().ok()?;
            }
            continue;
        }
        let Some(h) = current.as_mut() else { continue };
        let (kind, text, old_num, new_num) = if let Some(s) = line.strip_prefix('-') {
            let n = old;
            old += 1;
            ("del", s, Some(n), None)
        } else if let Some(s) = line.strip_prefix('+') {
            let n = new;
            new += 1;
            ("add", s, None, Some(n))
        } else if line.starts_with(' ') || line.is_empty() {
            let o = old;
            let n = new;
            old += 1;
            new += 1;
            (
                "ctx",
                line.strip_prefix(' ').unwrap_or(line),
                Some(o),
                Some(n),
            )
        } else {
            continue;
        };
        h.lines.push(DiffLine {
            line_type: kind.into(),
            content: text.into(),
            old_line_number: old_num,
            new_line_number: new_num,
        });
    }
    if let Some(h) = current {
        hunks.push(h)
    }
    if paths.len() > 1 {
        return None;
    }
    Some(FileModification {
        action: "EDIT".into(),
        file_path: path,
        hunks,
        content: None,
    })
}
