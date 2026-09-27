/**
 * @fileoverview OpenCode (`opencode`) Harness Storage Specification & TypeScript Definitions.
 *
 * This file provides the canonical data structure descriptions and comprehensive TypeScript
 * type definitions for querying, parsing, and validating session state stored in OpenCode's
 * global SQLite database.
 *
 * Grounded in:
 * - OpenCode Storage Engine: https://github.com/anomalyco/opencode/blob/main/packages/opencode/src/storage/db.ts#L1-L150
 * - agenthud (OpenCode Schema Spec): https://github.com/neochoon/agenthud/blob/main/docs/schemas/opencode-session.md#L1-L220
 * - agenthud (OpenCode Provider Implementation): https://github.com/neochoon/agenthud/blob/main/src/data/providers/opencode.ts#L1-L240
 * - opencode-db-search (SQLite CLI): https://github.com/rmk40/opencode-db-search/blob/main/src/db.ts#L1-L110
 * - sessions (OpenCode Provider): https://github.com/nicknisi/sessions/blob/main/src/opencode.ts#L1-L180
 */

// ============================================================================
// 1. Storage Location & Database Architecture
// ============================================================================

/**
 * Standard filesystem locations used by OpenCode:
 *
 * 1. Primary SQLite Database:
 *    `${XDG_DATA_HOME:-~/.local/share}/opencode/opencode.db`
 *    Accompanying WAL sidecars: `opencode.db-wal` and `opencode.db-shm`.
 *    @see https://github.com/neochoon/agenthud/blob/main/docs/schemas/opencode-session.md#L14-L26
 *
 * 2. Channel-Specific Databases (when using custom release channels):
 *    `${XDG_DATA_HOME:-~/.local/share}/opencode/opencode-<channel>.db`
 *    @see https://github.com/anomalyco/opencode/blob/main/packages/opencode/src/storage/db.ts#L10-L28
 *
 * 3. Models Context Catalog Cache:
 *    `~/.cache/opencode/models.json` (contains model context window sizes and pricing).
 *
 * 4. Auth & Configuration:
 *    `${XDG_DATA_HOME:-~/.local/share}/opencode/auth.json`
 *
 * 5. Legacy Storage (Pre-SQLite, deprecated):
 *    `${XDG_DATA_HOME:-~/.local/share}/opencode/storage/{project,session,message,part}/*.json`
 *    @see https://github.com/neochoon/agenthud/blob/main/docs/schemas/opencode-session.md#L28-L45
 */
export const DEFAULT_OPENCODE_DB_PATH = ".local/share/opencode/opencode.db";

/**
 * Connection options required for safe read-only querying of the live OpenCode SQLite database.
 */
export interface IOpencodeDbConnectionOptions {
  /** Path to the SQLite database file. */
  readonly dbPath: string;
  /** Always true: must open in read-only mode to prevent lock contention with the running agent. */
  readonly readonly: true;
  /** Journal mode configured by OpenCode. */
  readonly journalMode: "WAL";
  /** Busy timeout in milliseconds to handle active SQLite write transactions. */
  readonly busyTimeoutMs: 5000;
}

// ============================================================================
// 2. Database Table Row Schemas
// ============================================================================

/**
 * Model configuration stored as JSON text in the `session.model` column.
 */
export interface IOpencodeSessionModelConfig {
  /** Model identifier string (e.g. "gpt-5.5", "claude-sonnet-4"). */
  readonly id: string;
  /** Provider identifier (e.g. "openai", "anthropic", "google"). */
  readonly providerID?: string;
  /** Variant or reasoning effort configuration. */
  readonly variant?: string;
  readonly [key: string]: unknown;
}

/**
 * Raw row shape from the `session` table in `opencode.db`.
 */
export interface IOpencodeSessionRow {
  /** Unique session ID (e.g. "ses_12fad7a2..."). */
  readonly id: string;
  /** Foreign key pointing to `project.id`. */
  readonly project_id: string | null;
  /**
   * Parent session ID. If non-null, this row represents a child subagent / task session.
   */
  readonly parent_id: string | null;
  /** URL-friendly slug. */
  readonly slug: string | null;
  /** Absolute working directory path (primary grouping key). */
  readonly directory: string;
  /** Human-readable session title (e.g. "Fix navigation drawer layout"). */
  readonly title: string | null;
  /** Version of OpenCode that created or last updated the session. */
  readonly version: string | null;
  /** Agent profile name (e.g. "build", "default", "planner"). */
  readonly agent: string | null;
  /** JSON-encoded model settings object (`IOpencodeSessionModelConfig`). */
  readonly model: string | null;
  /** Cumulative cost in USD. */
  readonly cost: number | null;
  /** Cumulative input tokens. */
  readonly tokens_input: number;
  /** Cumulative output tokens. */
  readonly tokens_output: number;
  /** Cumulative reasoning tokens. */
  readonly tokens_reasoning: number;
  /** Cumulative prompt cache read tokens. */
  readonly tokens_cache_read: number;
  /** Cumulative prompt cache write tokens. */
  readonly tokens_cache_write: number;
  /** Epoch timestamp in milliseconds when the session was created. */
  readonly time_created: number;
  /** Epoch timestamp in milliseconds when the session was last updated. */
  readonly time_updated: number;
  /** Epoch timestamp in milliseconds if the session was archived, otherwise null. */
  readonly time_archived: number | null;
  /** Optional JSON-encoded metadata text. */
  readonly metadata: string | null;
}

/**
 * Token breakdown within a single turn message (`message.data.tokens`).
 */
export interface IOpencodeMessageTokenUsage {
  /** Total tokens in this turn (input + output + reasoning + cache.read + cache.write). */
  readonly total: number;
  /** Non-cached prompt / input tokens. */
  readonly input: number;
  /** Completion / output tokens. */
  readonly output: number;
  /** Reasoning / thinking tokens included in completion. */
  readonly reasoning?: number;
  /** Cache read and write token counts. */
  readonly cache: {
    readonly read: number;
    readonly write: number;
  };
}

/**
 * Message lifecycle timestamps (`message.data.time`).
 */
export interface IOpencodeMessageTiming {
  /** Epoch timestamp in milliseconds when message generation started. */
  readonly created: number;
  /** Epoch timestamp in milliseconds when message completed (null/absent while streaming). */
  readonly completed?: number | null;
}

// ============================================================================
// 1b. Upstream Message Error Structures
// ============================================================================

/**
 * Provider authentication error payload.
 * Grounded in upstream OpenCode SDK `types.gen.ts:75-81`.
 * @see https://github.com/anomalyco/opencode/blob/dev/packages/sdk/js/src/gen/types.gen.ts#L75-L81
 */
export interface IOpencodeProviderAuthError {
  readonly name: "ProviderAuthError";
  readonly data: {
    readonly providerID: string;
    readonly message: string;
  };
}

/**
 * Unknown error payload.
 * Grounded in upstream OpenCode SDK `types.gen.ts:83-88`.
 * @see https://github.com/anomalyco/opencode/blob/dev/packages/sdk/js/src/gen/types.gen.ts#L83-L88
 */
export interface IOpencodeUnknownError {
  readonly name: "UnknownError";
  readonly data: {
    readonly message: string;
  };
}

/**
 * Message output length exceeded error payload.
 * Grounded in upstream OpenCode SDK `types.gen.ts:90-95`.
 * @see https://github.com/anomalyco/opencode/blob/dev/packages/sdk/js/src/gen/types.gen.ts#L90-L95
 */
export interface IOpencodeMessageOutputLengthError {
  readonly name: "MessageOutputLengthError";
  readonly data?: {
    readonly [key: string]: unknown;
  };
}

/**
 * Message aborted error payload.
 * Grounded in upstream OpenCode SDK `types.gen.ts:97-102`.
 * @see https://github.com/anomalyco/opencode/blob/dev/packages/sdk/js/src/gen/types.gen.ts#L97-L102
 */
export interface IOpencodeMessageAbortedError {
  readonly name: "MessageAbortedError";
  readonly data: {
    readonly message: string;
  };
}

/**
 * API error payload.
 * Grounded in upstream OpenCode SDK `types.gen.ts:104-115`.
 * @see https://github.com/anomalyco/opencode/blob/dev/packages/sdk/js/src/gen/types.gen.ts#L104-L115
 */
export interface IOpencodeApiError {
  readonly name: "APIError";
  readonly data: {
    readonly message: string;
    readonly statusCode?: number;
    readonly isRetryable: boolean;
    readonly responseHeaders?: {
      readonly [key: string]: string;
    };
    readonly responseBody?: string;
  };
}

/**
 * Union of typed error payloads that can be attached to an assistant message.
 * Grounded in upstream OpenCode SDK `types.gen.ts:125`.
 * @see https://github.com/anomalyco/opencode/blob/dev/packages/sdk/js/src/gen/types.gen.ts#L125
 */
export type OpencodeMessageError =
  | IOpencodeProviderAuthError
  | IOpencodeUnknownError
  | IOpencodeMessageOutputLengthError
  | IOpencodeMessageAbortedError
  | IOpencodeApiError;

/**
 * Structured JSON payload stored in the `message.data` column.
 */
export interface IOpencodeMessageData {
  /** Role of the speaker: "user" or "assistant". */
  readonly role: "user" | "assistant";
  /** Model ID used for this specific message. */
  readonly modelID?: string;
  /** Provider ID used for this message. */
  readonly providerID?: string;
  /** Agent profile active for this turn. */
  readonly agent?: string;
  /** Active execution mode (e.g. "code", "ask"). */
  readonly mode?: string;
  /** Workspace path context. */
  readonly path?: {
    readonly cwd: string;
    readonly root: string;
  };
  /** Estimated or actual cost in USD for this turn. */
  readonly cost?: number;
  /** Token metrics for this turn (present on assistant messages). */
  readonly tokens?: IOpencodeMessageTokenUsage;
  /** Turn timing and lifecycle timestamps. */
  readonly time?: IOpencodeMessageTiming;
  /** Finish reason string ("stop", "tool_use", "length") or null if in progress. */
  readonly finish?: string | null;
  /**
   * Error payload if message generation encountered an error.
   * Grounded in upstream `AssistantMessage.error` union (`types.gen.ts:125`).
   * @see https://github.com/anomalyco/opencode/blob/dev/packages/sdk/js/src/gen/types.gen.ts#L125
   */
  readonly error?: OpencodeMessageError;
}

/**
 * Raw row shape from the `message` table in `opencode.db`.
 */
export interface IOpencodeMessageRow {
  /** Unique message ID. */
  readonly id: string;
  /** Foreign key pointing to `session.id`. */
  readonly session_id: string;
  /** Epoch timestamp in milliseconds when message was inserted. */
  readonly time_created: number;
  /** JSON-encoded `IOpencodeMessageData` text. */
  readonly data: string;
}

// ----------------------------------------------------------------------------
// Part Table Schemas (`part.data` discriminated by `type`)
// ----------------------------------------------------------------------------

export type OpencodeToolExecutionStatus = "pending" | "running" | "completed" | "error";

/**
 * State object for a tool execution part.
 */
export interface IOpencodeToolPartState {
  /** Current execution status. */
  readonly status: OpencodeToolExecutionStatus;
  /** Human-readable title or target summary (e.g. file path "src/index.ts"). */
  readonly title?: string;
  /** Tool invocation input parameters. */
  readonly input?: Record<string, unknown>;
  /** Tool execution string output / result. */
  readonly output?: string;
  /** Tool execution error message if status is "error" or tool execution failed. */
  readonly error?: string;
  /** Optional metadata dictionary. */
  readonly metadata?: Record<string, unknown>;
  /** Execution timestamps. */
  readonly time?: {
    readonly start: number;
    readonly end?: number;
  };
}

/** Text content part. */
export interface IOpencodeTextPartData {
  readonly type: "text";
  readonly text: string;
  readonly time?: {
    readonly start: number;
    readonly end?: number;
  };
  readonly metadata?: Record<string, unknown>;
}

/** Tool invocation and result part. */
export interface IOpencodeToolPartData {
  readonly type: "tool";
  /** Tool name (e.g. "read", "edit", "bash", "glob", "grep", "write"). */
  readonly tool: string;
  /** Unique call ID correlating tool invocation. */
  readonly callID: string;
  /** Detailed state containing inputs, outputs, and status. */
  readonly state: IOpencodeToolPartState;
  readonly metadata?: Record<string, unknown>;
}

/** Extended reasoning / thinking block part. */
export interface IOpencodeReasoningPartData {
  readonly type: "reasoning";
  readonly text: string;
  readonly time?: {
    readonly start: number;
    readonly end?: number;
  };
  readonly metadata?: Record<string, unknown>;
}

/** Turn step start boundary. */
export interface IOpencodeStepStartPartData {
  readonly type: "step-start";
  readonly snapshot?: Record<string, unknown>;
}

/** Turn step finish boundary with snapshot metrics. */
export interface IOpencodeStepFinishPartData {
  readonly type: "step-finish";
  readonly reason?: string;
  readonly cost?: number;
  readonly tokens?: IOpencodeMessageTokenUsage;
  readonly snapshot?: Record<string, unknown>;
}

/** File attachment or patch part. */
export interface IOpencodeFilePartData {
  readonly type: "file" | "patch";
  readonly path?: string;
  readonly content?: string;
  readonly metadata?: Record<string, unknown>;
}

/**
 * Retry part emitted when OpenCode retries an API call with backoff.
 * Grounded in upstream `RetryPart` (`types.gen.ts:364-373`).
 * @see https://github.com/anomalyco/opencode/blob/dev/packages/sdk/js/src/gen/types.gen.ts#L364-L373
 */
export interface IOpencodeRetryPartData {
  readonly type: "retry";
  readonly attempt: number;
  readonly error: IOpencodeApiError;
  readonly time?: {
    readonly created: number;
  };
}

export type OpencodePartData =
  | IOpencodeTextPartData
  | IOpencodeToolPartData
  | IOpencodeReasoningPartData
  | IOpencodeStepStartPartData
  | IOpencodeStepFinishPartData
  | IOpencodeFilePartData
  | IOpencodeRetryPartData;

/**
 * Raw row shape from the `part` table in `opencode.db`.
 */
export interface IOpencodePartRow {
  /** Unique part ID. */
  readonly id: string;
  /** Foreign key pointing to `message.id`. */
  readonly message_id: string;
  /** Foreign key pointing to `session.id`. */
  readonly session_id: string;
  /** Epoch timestamp in milliseconds when the part was created. */
  readonly time_created: number;
  /** JSON-encoded `OpencodePartData` text. */
  readonly data: string;
}

/**
 * Row shape from the `project` table.
 */
export interface IOpencodeProjectRow {
  readonly id: string;
  readonly name: string;
  readonly worktree: string | null;
  readonly vcs: "git" | string | null;
}

/**
 * Row shape from the `permission` table (pending permission prompts).
 */
export interface IOpencodePermissionRow {
  readonly id: string;
  readonly session_id: string;
  readonly type: string;
  readonly data: string;
  readonly time_created: number;
}

// ============================================================================
// 3. Normalized Parsed Domain Models
// ============================================================================

/**
 * Fully parsed OpenCode session representation.
 */
export interface IOpencodeSession {
  readonly id: string;
  readonly projectId: string | null;
  readonly parentId: string | null;
  readonly directory: string;
  readonly title: string;
  readonly version: string;
  readonly agent: string;
  readonly modelId: string;
  readonly costUsd: number;
  readonly tokens: {
    readonly input: number;
    readonly output: number;
    readonly reasoning: number;
    readonly cacheRead: number;
    readonly cacheWrite: number;
    readonly total: number;
  };
  readonly timeCreated: number;
  readonly timeUpdated: number;
  readonly isArchived: boolean;
  readonly isSubagent: boolean;
  readonly isWorking: boolean;
}

// ============================================================================
// 4. SQL Query Helpers & Constants
// ============================================================================

/**
 * SQL queries for querying `opencode.db` safely in read-only mode.
 */
export const OPENCODE_SQL_QUERIES = {
  /** List active/recent sessions sorted by last updated timestamp. */
  SELECT_SESSIONS: `
    SELECT
      id, project_id, parent_id, slug, directory, title,
      version, agent, model, cost, tokens_input, tokens_output,
      tokens_reasoning, tokens_cache_read, tokens_cache_write,
      time_created, time_updated, time_archived, metadata
    FROM session
    ORDER BY time_updated DESC;
  `,

  /** Fetch a specific session by ID. */
  SELECT_SESSION_BY_ID: `
    SELECT
      id, project_id, parent_id, slug, directory, title,
      version, agent, model, cost, tokens_input, tokens_output,
      tokens_reasoning, tokens_cache_read, tokens_cache_write,
      time_created, time_updated, time_archived, metadata
    FROM session
    WHERE id = ?;
  `,

  /** Fetch subagent sessions spawned by a parent session. */
  SELECT_CHILD_SESSIONS: `
    SELECT
      id, project_id, parent_id, slug, directory, title,
      version, agent, model, cost, tokens_input, tokens_output,
      tokens_reasoning, tokens_cache_read, tokens_cache_write,
      time_created, time_updated, time_archived, metadata
    FROM session
    WHERE parent_id = ?
    ORDER BY time_created ASC;
  `,

  /** Fetch messages for a session. */
  SELECT_MESSAGES_BY_SESSION: `
    SELECT id, session_id, time_created, data
    FROM message
    WHERE session_id = ?
    ORDER BY time_created ASC;
  `,

  /** Fetch parts (timeline items) for a session. */
  SELECT_PARTS_BY_SESSION: `
    SELECT id, message_id, session_id, time_created, data
    FROM part
    WHERE session_id = ?
    ORDER BY time_created ASC;
  `,

  /** Check for pending permission requests. */
  SELECT_PENDING_PERMISSIONS: `
    SELECT id, session_id, type, data, time_created
    FROM permission
    WHERE session_id = ?;
  `,
} as const;

// ============================================================================
// 5. Helper Functions & Type Guards
// ============================================================================

/**
 * Safely parses the model ID from `session.model` JSON text.
 */
export function parseOpencodeModelId(rawModelJson: string | null): string {
  if (!rawModelJson) return "unknown";
  try {
    const parsed = JSON.parse(rawModelJson) as Record<string, unknown>;
    if (typeof parsed["id"] === "string") {
      return parsed["id"];
    }
  } catch {
    // Return raw string if not JSON
  }
  return rawModelJson;
}

/**
 * Checks if a part represents a tool execution.
 */
export function isOpencodeToolPart(part: OpencodePartData): part is IOpencodeToolPartData {
  return part.type === "tool";
}

/**
 * Checks if a part represents text output.
 */
export function isOpencodeTextPart(part: OpencodePartData): part is IOpencodeTextPartData {
  return part.type === "text";
}

/**
 * Checks if a part represents reasoning/thinking.
 */
export function isOpencodeReasoningPart(
  part: OpencodePartData,
): part is IOpencodeReasoningPartData {
  return part.type === "reasoning";
}

/**
 * Checks if a part represents a retry attempt.
 */
export function isOpencodeRetryPart(
  part: OpencodePartData,
): part is IOpencodeRetryPartData {
  return part.type === "retry";
}
