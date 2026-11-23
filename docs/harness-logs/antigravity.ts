/**
 * Google Antigravity (`agy`) Harness Log Specifications & TypeScript Type Definitions
 *
 * Canonical data structure description and type specification for Google Antigravity CLI
 * (`agy`) headless stream-json output, on-disk JSONL transcripts, SQLite conversation
 * databases, detached subagent records, and settings schemas.
 *
 * Primary Reference Sources:
 * - pingdotgg/t3code (commit a445025645680d8c282011df5edc774f4551e6d8):
 *   - apps/server/src/provider/acp/AntigravitySessionFiles.ts (conversations/*.db, trajectory_metadata_blob proto)
 *   - apps/server/src/provider/acp/AntigravityChildWork.ts (transcript.jsonl, subagents/*.json, messages/*.json)
 *   - apps/server/src/provider/acp/AntigravityProtocol.ts (tool payload casing, subagent titles, interaction_ prefix)
 *   - apps/server/src/provider/acp/AntigravityEditLocation.ts (replace_file_content / multi_replace_file_content chunks)
 *   - apps/server/src/provider/Drivers/antigravityUsageReader.ts (gen_metadata / steps protobuf token usage)
 *   - apps/server/src/provider/Drivers/AntigravitySkills.ts (skill roots, skills.json, plugins)
 *   - apps/server/src/provider/antigravityAuthSupport.ts (ACP profile layout, GEMINI_HOME)
 * - Official Antigravity CLI 1.5.14 documentation:
 *   - https://antigravity.google/docs/cli/headless.md
 *   - https://antigravity.google/docs/cli/reference.md
 *   - https://antigravity.google/schemas/v1/settings.json
 */

// ============================================================================
// 1. On-Disk Layout & Path Resolution
// ============================================================================

/**
 * Standard filesystem locations used by Antigravity CLI (`agy`) on macOS, Linux, and Windows:
 *
 * 1. CLI Data Directory:
 *    - Default: `~/.gemini/antigravity-cli/`
 *    - ACP profile: `<profileDir>/antigravity-acp/` (configured via `agy acp --gemini-dir <dir> --app-data-dir <name>`)
 *    - Global customization root: `~/.gemini/config/` (`AGENTS.md`, `GEMINI.md`, `rules/`, `skills/`, `hooks.json`, `mcp_config.json`, `plugins/`)
 *    - CLI settings: `~/.gemini/antigravity-cli/settings.json`
 *
 * 2. Persisted JSONL Transcripts (per conversation UUID):
 *    - Full transcript: `<appDataDir>/brain/<conversationId>/.system_generated/logs/transcript_full.jsonl`
 *    - Compact transcript: `<appDataDir>/brain/<conversationId>/.system_generated/logs/transcript.jsonl`
 *      (lines with oversized fields omit them or truncate and list affected keys in `truncated_fields`)
 *
 * 3. Detached Subagents & Inbox Messages (CLI 1.5+):
 *    - Subagent metadata: `<appDataDir>/brain/<parentId>/.system_generated/subagents/<childId>.json`
 *    - Inbox messages: `<appDataDir>/brain/<conversationId>/.system_generated/messages/<messageId>.json`
 *    - Read receipts: `<appDataDir>/brain/<conversationId>/.system_generated/messages/read.json`
 *
 * 4. SQLite Conversation Stores (read-only `?mode=ro`, WAL mode):
 *    - Conversation index: `<appDataDir>/conversation_summaries.db`
 *      Tables: `conversation_summaries`, `conversation_parents`, `conversations_fts`
 *    - Per-conversation trajectory DB: `<appDataDir>/conversations/<conversationId>.db`
 *      Tables: `trajectory_meta`, `steps`, `gen_metadata`, `executor_metadata`,
 *      `parent_references`, `trajectory_metadata_blob`, `battle_mode_infos`
 *    - Legacy sidecar (pre-1.5): `<appDataDir>/conversations/<conversationId>.meta` (`{"cwd": "..."}`)
 *
 * 5. Skill Roots:
 *    - Global: `~/.gemini/config/skills/<name>/SKILL.md`, `~/.gemini/antigravity-cli/skills/<name>/SKILL.md`
 *    - Project: `<cwd>/.gemini/skills/<name>/SKILL.md`, `<cwd>/.agents/skills/<name>/SKILL.md`, `<cwd>/.agent/skills/<name>/SKILL.md`
 */
export interface IAgyPaths {
  /** Root Gemini directory, default `~/.gemini` */
  geminiDir: string;
  /** CLI app data directory, default `~/.gemini/antigravity-cli` */
  appDataDir: string;
  /** Brain directory holding per-conversation transcripts and artifacts */
  brainDir: string;
  /** Conversations directory holding per-conversation SQLite databases */
  conversationsDir: string;
  /** Global conversation summaries index SQLite database */
  summariesDBPath: string;
  /** CLI settings JSON file path */
  settingsFile: string;
}

// ============================================================================
// 2. Persisted Transcript JSONL Records (transcript_full.jsonl / transcript.jsonl)
// ============================================================================

export type AgyTranscriptStepType =
  | "USER_INPUT"
  | "PLANNER_RESPONSE"
  | "GENERIC"
  | "SYSTEM_MESSAGE"
  | string;

export type AgyTranscriptSource = "USER_EXPLICIT" | "MODEL" | "SYSTEM" | string;

export type AgyTranscriptStatus = "DONE" | "RUNNING" | "ERROR" | "CANCELED" | string;

/**
 * Tool call entry embedded in a `PLANNER_RESPONSE` transcript record.
 * Note: In some versions, individual argument values inside `args` may be JSON-encoded strings.
 */
export interface IAgyTranscriptToolCall {
  name: string;
  id?: string;
  args?: Record<string, unknown>;
}

/**
 * Single JSONL line in `transcript_full.jsonl` or `transcript.jsonl`.
 */
export interface IAgyTranscriptRecord {
  step_index: number;
  type: AgyTranscriptStepType;
  source?: AgyTranscriptSource;
  status?: AgyTranscriptStatus;
  /** RFC3339 timestamp string (e.g., "2026-11-22T14:38:26Z") */
  created_at?: string;
  content?: string;
  thinking?: string;
  error?: string;
  model?: string;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  thinking_tokens?: number;
  tool_calls?: IAgyTranscriptToolCall[];
  /** Present in compact `transcript.jsonl` when fields were truncated */
  truncated_fields?: string[];
}

// ============================================================================
// 3. Detached Subagents & Conversation Summaries Metadata
// ============================================================================

/**
 * Detached subagent descriptor stored at:
 * `brain/<parentId>/.system_generated/subagents/<childId>.json`
 */
export interface IAgyDetachedSubagent {
  conversationId?: string;
  state?: string;
  subagentDescriptor?: {
    role?: string;
  };
}

/**
 * Row in `conversation_summaries.db` -> `conversation_summaries`.
 */
export interface IAgyConversationSummaryRow {
  conversation_id: string;
  title: string;
  preview: string;
  step_count: number;
  last_modified_time: string;
  workspace_uris: string;
  status: string;
  source: string;
  project_id: string;
  agent_name: string;
  parent_conversation_id: string;
  nesting_depth: number;
  not_fully_idle: boolean | number;
  killed: boolean | number;
  last_user_input_time: string;
  last_user_input_step_index: number;
  app_data_dir: string;
  group_id: string;
}

/**
 * Field numbers in `conversations/<id>.db` -> `trajectory_metadata_blob.data` (protobuf).
 * Cross-checked against pingdotgg/t3code `AntigravitySessionFiles.ts`.
 */
export interface IAgyTrajectoryMetadataProtoFields {
  /** Field 1 (repeated message): workspaces -> field 1 (string): file:// workspace URI */
  workspacesField: 1;
  /** Field 2 (message): creation timestamp (field 1 seconds, field 2 nanos) */
  createdAtField: 2;
  /** Field 6 (string): conversation/session UUID */
  sessionIdField: 6;
  /** Field 7 (repeated string): file:// workspace URIs */
  workspaceUrisField: 7;
}

// ============================================================================
// 4. Live Headless Stream Protocol (`agy --output-format stream-json`)
// ============================================================================

export type AgyHeadlessStatus =
  | "SUCCESS"
  | "ERROR"
  | "CANCELED"
  | "INTERRUPTED"
  | "INVALID"
  | "WAITING"
  | "RUNNING"
  | string;

export type AgyLiveStepState = "ACTIVE" | "DONE" | string;

export type AgyLiveStepType =
  | "user_input"
  | "agent_response"
  | "tool"
  | "checkpoint"
  | string;

/**
 * NDJSON prompt message written to stdin under `--input-format stream-json`.
 */
export interface IAgyLiveInputMessage {
  event: "user";
  message: {
    content: string | Array<{ type: "text"; text: string }>;
  };
}

export interface IAgyLiveUsage {
  input_tokens: number;
  output_tokens: number;
  thinking_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  total_tokens?: number;
}

export interface IAgyLiveInitPayload {
  cwd?: string;
  tools?: string[];
  permission_mode?: string;
  model?: string;
  agent?: string;
  json_schema?: unknown;
}

export interface IAgyLiveToolError {
  type?: string;
  message?: string;
}

export interface IAgyLiveToolInfo {
  name?: string;
  parameters?: Record<string, unknown>;
  output?: string;
  error?: IAgyLiveToolError;
}

export interface IAgyLiveSubagentEntry {
  type_name?: string;
  role?: string;
  conversation_id?: string;
  log_uri?: string;
  workspace_uris?: string[];
}

export interface IAgyLiveSubagentInfo {
  subagents?: IAgyLiveSubagentEntry[];
}

export interface IAgyLiveStepUpdatePayload {
  conversation_id?: string;
  step_index: number;
  state: AgyLiveStepState;
  step_type: AgyLiveStepType;
  tool_name?: string;
  text_delta?: string;
  thinking_delta?: string;
  duration_seconds?: number;
  usage?: IAgyLiveUsage;
  tool_info?: IAgyLiveToolInfo;
  subagent_info?: IAgyLiveSubagentInfo;
}

export interface IAgyLiveResultPayload {
  conversation_id?: string;
  status: AgyHeadlessStatus;
  response?: string;
  error?: string;
  duration_seconds?: number;
  num_turns?: number;
  structured_output?: unknown;
  json_schema?: unknown;
  usage?: IAgyLiveUsage;
  finished_reason?: string;
}

export interface IAgyLiveInitEvent {
  event: "init";
  conversation_id?: string;
  init: IAgyLiveInitPayload;
}

export interface IAgyLiveStepUpdateEvent {
  event: "step_update";
  conversation_id?: string;
  step_update: IAgyLiveStepUpdatePayload;
}

export interface IAgyLiveResultEvent {
  event: "result";
  conversation_id?: string;
  result: IAgyLiveResultPayload;
}

export type AgyLiveEvent =
  | IAgyLiveInitEvent
  | IAgyLiveStepUpdateEvent
  | IAgyLiveResultEvent;
