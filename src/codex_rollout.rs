use crate::Error;
use serde::{Deserialize, Serialize};
use std::sync::LazyLock;
#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct CodexRolloutFilenameInfo {
    pub file_path: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub year: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub month: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub day: String,
    pub timestamp_string: String,
    pub session_id: String,
}
static PATHS: LazyLock<Vec<regex::Regex>> = LazyLock::new(|| {
    let timestamp = r"(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}-\d{2})?)";
    let id = r"([0-9a-zA-Z_-]+)";
    let uuid = r"([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})";
    [
        format!(r"(?:^|/)sessions/(\d{{4}})/(\d{{2}})/(\d{{2}})/rollout-{timestamp}-{id}\.jsonl$"),
        format!(r"(?:^|/)sessions/(\d{{4}})/(\d{{2}})/(\d{{2}})/rollout-(.+?)-{uuid}\.jsonl$"),
        format!(r"(?:^|/)rollout-{timestamp}-{id}\.jsonl$"),
        format!(r"(?:^|/)rollout-(.+?)-{uuid}\.jsonl$"),
    ]
    .iter()
    .map(|s| regex::Regex::new(s).unwrap())
    .collect()
});
pub fn parse_codex_rollout_path(path: &str) -> Result<CodexRolloutFilenameInfo, Error> {
    let normalized = path.replace('\\', "/");
    for pattern in PATHS.iter() {
        if let Some(c) = pattern.captures(&normalized) {
            let partitioned = c.len() == 6;
            let offset = if partitioned { 4 } else { 1 };
            return Ok(CodexRolloutFilenameInfo {
                file_path: path.into(),
                year: if partitioned {
                    c[1].into()
                } else {
                    String::new()
                },
                month: if partitioned {
                    c[2].into()
                } else {
                    String::new()
                },
                day: if partitioned {
                    c[3].into()
                } else {
                    String::new()
                },
                timestamp_string: c[offset].into(),
                session_id: c[offset + 1].into(),
            });
        }
    }
    Err(Error::Invalid(format!(
        "invalid Codex rollout path: {path:?}"
    )))
}
