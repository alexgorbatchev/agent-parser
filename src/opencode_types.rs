use crate::Error;
use serde::{Deserialize, Serialize};

pub(crate) fn output_includes_reasoning(version: &str) -> bool {
    let version = version.strip_prefix('v').unwrap_or(version);
    let (release, prerelease) = version.split_once('-').unwrap_or((version, ""));
    let mut parts = release.split('.');
    let mut numbers = [0; 3];
    for number in &mut numbers {
        let Some(part) = parts.next().and_then(|part| part.parse::<i32>().ok()) else {
            return false;
        };
        *number = part;
    }
    parts.next().is_none()
        && (numbers < [1, 3, 16] || (numbers == [1, 3, 16] && !prerelease.is_empty()))
}
use serde_json::{Map, Value};
pub const DEFAULT_OPENCODE_DB_PATH: &str = ".local/share/opencode/opencode.db";
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodeModelConfig {
    pub id: String,
    #[serde(rename = "providerID", skip_serializing_if = "String::is_empty")]
    pub provider_id: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub variant: String,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodeCacheTokens {
    pub read: i64,
    pub write: i64,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodeTokens {
    pub total: i64,
    pub input: i64,
    pub output: i64,
    #[serde(skip_serializing_if = "zero")]
    pub reasoning: i64,
    pub cache: OpencodeCacheTokens,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodeMessageTiming {
    pub created: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub completed: Option<i64>,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodeMessagePath {
    pub cwd: String,
    pub root: String,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodeMessageData {
    pub role: String,
    #[serde(rename = "modelID", skip_serializing_if = "String::is_empty")]
    pub model_id: String,
    #[serde(rename = "providerID", skip_serializing_if = "String::is_empty")]
    pub provider_id: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub agent: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub mode: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub path: Option<OpencodeMessagePath>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cost: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tokens: Option<OpencodeTokens>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub time: Option<OpencodeMessageTiming>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub finish: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<Value>,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodeToolPartTime {
    pub start: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub end: Option<i64>,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodeToolPartState {
    pub status: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub title: String,
    #[serde(skip_serializing_if = "crate::models::empty_map")]
    pub input: Option<Map<String, Value>>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub output: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "crate::models::empty_map")]
    pub metadata: Option<Map<String, Value>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub time: Option<OpencodeToolPartTime>,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodePartData {
    #[serde(rename = "type")]
    pub part_type: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub text: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub tool: String,
    #[serde(rename = "callID", skip_serializing_if = "String::is_empty")]
    pub call_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state: Option<OpencodeToolPartState>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reason: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cost: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tokens: Option<OpencodeTokens>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub path: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub content: String,
    #[serde(skip_serializing_if = "crate::models::empty_map")]
    pub snapshot: Option<Map<String, Value>>,
    #[serde(skip_serializing_if = "crate::models::empty_map")]
    pub metadata: Option<Map<String, Value>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub time: Option<OpencodeToolPartTime>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub attempt: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<Value>,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct OpencodeSession {
    pub id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub project_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parent_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub slug: Option<String>,
    pub directory: String,
    pub title: String,
    pub version: String,
    pub agent: String,
    pub model: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model_config: Option<OpencodeModelConfig>,
    pub cost: f64,
    pub tokens: OpencodeTokens,
    pub time_created: i64,
    pub time_updated: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub time_archived: Option<i64>,
    pub is_archived: bool,
    pub is_subagent: bool,
    #[serde(skip_serializing_if = "crate::models::empty_map")]
    pub metadata: Option<Map<String, Value>>,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct OpencodeMessage {
    pub id: String,
    pub session_id: String,
    pub time_created: i64,
    pub data: OpencodeMessageData,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct OpencodePart {
    pub id: String,
    pub message_id: String,
    pub session_id: String,
    pub time_created: i64,
    pub data: OpencodePartData,
}
/// Related rows observed within the same SQLite read transaction.
#[derive(Clone, Debug, PartialEq)]
pub struct OpencodeSessionSnapshot {
    pub session: OpencodeSession,
    pub messages: Vec<OpencodeMessage>,
    pub parts: Vec<OpencodePart>,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct OpencodeProject {
    pub id: String,
    pub name: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub worktree: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub vcs: Option<String>,
}
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct OpencodePermission {
    pub id: String,
    pub session_id: String,
    #[serde(rename = "type")]
    pub permission_type: String,
    pub data: Map<String, Value>,
    pub time_created: i64,
}
pub fn parse_opencode_model(raw: &str) -> (String, Option<OpencodeModelConfig>) {
    let text = raw.trim();
    if text.is_empty() {
        return ("unknown".into(), None);
    }
    if let Ok(cfg) = serde_json::from_str::<OpencodeModelConfig>(text)
        && !cfg.id.is_empty()
    {
        return (cfg.id.clone(), Some(cfg));
    }
    (text.into(), None)
}
pub fn parse_opencode_message_data(raw: &str) -> Result<OpencodeMessageData, Error> {
    Ok(serde_json::from_str(raw)?)
}
pub fn parse_opencode_part_data(raw: &str) -> Result<OpencodePartData, Error> {
    Ok(serde_json::from_str(raw)?)
}
fn zero(value: &i64) -> bool {
    *value == 0
}
