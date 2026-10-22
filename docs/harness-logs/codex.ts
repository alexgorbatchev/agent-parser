/**
 * @fileoverview OpenAI Codex CLI Harness Log Specifications & TypeScript Definitions.
 *
 * This file provides the canonical data structure descriptions and comprehensive TypeScript
 * type definitions for parsing, navigating, and validating session transcript logs generated
 * by the OpenAI Codex CLI.
 *
 * Grounded in:
 * - agenthud (Codex Session Schema Spec): https://github.com/neochoon/agenthud/blob/main/docs/schemas/codex-session.md#L1-L220
 * - agenthud (Codex Provider Implementation): https://github.com/neochoon/agenthud/blob/main/src/data/providers/codex.ts#L1-L280
 * - codex-transcripts (Python Transcript Parser): https://github.com/prateek/codex-transcripts/blob/main/codex_transcripts.py#L1-L260
 * - claude-code-session-viewer (Codex Parser): https://github.com/eric050828/claude-code-session-viewer/blob/main/lib/codex-parser.ts#L1-L180
 * - claude-code-session-viewer (Codex Paths): https://github.com/eric050828/claude-code-session-viewer/blob/main/lib/codex-paths.ts#L1-L90
 * - cc-session-core (Codex Parser): https://github.com/Magic-Man-us/cc-session-core/blob/main/src/cc_session_core/codex/
 * - sessions (Codex Provider): https://github.com/nicknisi/sessions/blob/main/src/codex.ts#L1-L160
 */

// ============================================================================
// 1. On-Disk Layout & Path Constants
// ============================================================================

/** Default relative directory under user home where Codex stores session rollouts. */
export const DEFAULT_CODEX_SESSION_ROOT = ".codex/sessions";

/**
 * Standard filesystem locations used by Codex CLI on macOS, Linux, and Windows:
 *
 * 1. Rollout Files (Date-Partitioned JSONL):
 *    `~/.codex/sessions/YYYY/MM/DD/rollout-<ISO8601>-<uuid>.jsonl`
 *    Where `<ISO8601>` replaces colons (`:`) with hyphens (`-`), e.g. `2026-06-12T16-08-36`
 *    and `<uuid>` is a time-ordered UUIDv7 identifying the session.
 *    @see https://github.com/neochoon/agenthud/blob/main/docs/schemas/codex-session.md#L8-L25
 *
 * 2. SQLite Auxiliary Stores (App state / caches, not the primary conversation transcript):
 *    - `~/.codex/state_5.sqlite`: Session metadata and recent history index.
 *    - `~/.codex/memories_1.sqlite`: Long-term memory index.
 *    - `~/.codex/goals_1.sqlite`: Active goal and task tracker state.
 *
 * 3. Subagent Rollout Files:
 *    Subagents are stored as full, independent rollout files in the same `YYYY/MM/DD/` tree.
 *    Linked to the parent via `session_meta.parent_thread_id` and `thread_spawn`.
 *    @see https://github.com/neochoon/agenthud/blob/main/docs/schemas/codex-session.md#L125-L160
 */
export interface ICodexPaths {
  /** Directory path to the sessions root (default: `~/.codex/sessions`). */
  readonly sessionsDir: string;
  /** Optional override path from `CODEX_SESSIONS_DIR` or `CODEX_HOME`. */
  readonly homeDir?: string;
}

/**
 * Parsed components from a Codex rollout filename.
 */
export interface ICodexRolloutFilenameInfo {
  /** The full file path. */
  readonly filePath: string;
  /** Year string (e.g. "2026"). */
  readonly year: string;
  /** Month string (e.g. "06"). */
  readonly month: string;
  /** Day string (e.g. "12"). */
  readonly day: string;
  /** ISO8601 start timestamp string from filename. */
  readonly timestampString: string;
  /** Session UUID extracted from filename. */
  readonly sessionId: string;
}

/**
 * Parses a Codex rollout file path into its constituent parts.
 *
 * @param filePath - The full path to the rollout JSONL file.
 * @returns The parsed filename components or null if not matching the rollout pattern.
 */
export function parseCodexRolloutPath(filePath: string): ICodexRolloutFilenameInfo | null {
  const normalized = filePath.replace(/\\/g, "/");
  const match = normalized.match(
    /\/sessions\/(\d{4})\/(\d{2})\/(\d{2})\/rollout-([^/]+)-([0-9a-fA-F-]+)\.jsonl$/,
  );
  if (!match || !match[1] || !match[2] || !match[3] || !match[4] || !match[5]) {
    // Fallback for flat or non-standard paths
    const filenameMatch = normalized.match(/rollout-([^/]+)-([0-9a-fA-F-]+)\.jsonl$/);
    if (filenameMatch && filenameMatch[1] && filenameMatch[2]) {
      return {
        filePath,
        year: "",
        month: "",
        day: "",
        timestampString: filenameMatch[1],
        sessionId: filenameMatch[2],
      };
    }
    return null;
  }
  return {
    filePath,
    year: match[1],
    month: match[2],
    day: match[3],
    timestampString: match[4],
    sessionId: match[5],
  };
}

// ============================================================================
// 2. Subagent Thread Spawning Models
// ============================================================================

/**
 * Metadata describing how a subagent was spawned by its parent session.
 */
export interface ICodexThreadSpawn {
  /** UUID of the parent thread / session. */
  readonly parent_thread_id: string;
  /** Nesting depth (1 for a direct child). */
  readonly depth: number;
  /** Optional agent path or profile definition. */
  readonly agent_path: string | null;
  /** User-friendly nickname assigned to the subagent (e.g. "Hubble"). */
  readonly agent_nickname: string;
  /** Specialized role assigned to the subagent (e.g. "explorer", "reviewer"). */
  readonly agent_role: string;
}

/**
 * Discriminator indicating whether the session is a root CLI session or a spawned subagent.
 */
export type CodexSessionSource =
  | "cli"
  | {
      readonly subagent: {
        readonly thread_spawn: ICodexThreadSpawn;
      };
    }
  | {
      /** Older format (early 2026): subagent role string directly. */
      readonly subagent: string;
    };

// ============================================================================
// 3. Git Metadata
// ============================================================================

/**
 * Git repository state captured when the session was created.
 */
export interface ICodexGitMeta {
  /** Current Git commit hash. */
  readonly commit_hash: string;
  /** Active Git branch name. */
  readonly branch: string;
  /** Remote origin repository URL. */
  readonly repository_url: string;
}

// ============================================================================
// 4. Token Usage & Cost Metrics
// ============================================================================

/**
 * Detailed token metrics for an individual turn or cumulative session.
 */
export interface ICodexTokenBreakdown {
  /** Total tokens (prompt + completion). */
  readonly total_tokens: number;
  /** Non-cached prompt / input tokens. */
  readonly prompt_tokens?: number;
  /** Generated completion / output tokens. */
  readonly completion_tokens?: number;
  /** Prompt tokens read from cache. */
  readonly cached_prompt_tokens?: number;
  /** Reasoning / internal thinking tokens. */
  readonly reasoning_tokens?: number;
}

/**
 * Rate limit snapshot reported in token count events.
 */
export interface ICodexRateLimits {
  readonly limit_requests?: number;
  readonly remaining_requests?: number;
  readonly reset_requests?: string;
  readonly limit_tokens?: number;
  readonly remaining_tokens?: number;
  readonly reset_tokens?: string;
}

/**
 * Token usage report emitted in `token_count` event messages.
 */
export interface ICodexTokenCountInfo {
  /** Cumulative token spend across the entire session. */
  readonly total_token_usage: ICodexTokenBreakdown;
  /** Token spend for the most recent turn. */
  readonly last_token_usage: ICodexTokenBreakdown;
  /** Maximum context window size in tokens for the current model. */
  readonly model_context_window: number;
}

// ============================================================================
// 5. Tool Call & Function Execution Types
// ============================================================================

/**
 * Parameters for the primary `exec_command` shell execution tool.
 */
export interface ICodexExecCommandArgs {
  /** Shell command line to execute. */
  readonly cmd: string;
  /** Working directory relative to workspace or absolute. */
  readonly workdir?: string;
  /** Maximum execution yield time in milliseconds before backgrounding. */
  readonly yield_time_ms?: number;
  /** Maximum tokens of stdout/stderr to capture before truncation. */
  readonly max_output_tokens?: number;
}

/**
 * Parameters for writing characters to an interactive process stdin.
 */
export interface ICodexWriteStdinArgs {
  /** Characters or input string sent to stdin. */
  readonly chars: string;
  /** Process identifier or session reference. */
  readonly process_id?: string;
}

/**
 * Parameters for the `apply_patch` unified diff file editing tool.
 */
export interface ICodexApplyPatchArgs {
  /** Unified diff hunk or patch content. */
  readonly patch?: string;
  /** Raw patch input string. */
  readonly input?: string;
}

/**
 * Parameters for the `spawn_agent` subagent creation tool.
 */
export interface ICodexSpawnAgentArgs {
  /** Subagent type or role (e.g. "explorer", "reviewer", "writer"). */
  readonly agent_type: string;
  /** Initial task prompt for the subagent. */
  readonly message: string;
  /** Whether to fork the current session's memory/context. */
  readonly fork_context?: boolean;
}

/**
 * Structured output returned when a subagent is successfully spawned.
 */
export interface ICodexSpawnAgentResult {
  /** UUID of the newly created subagent rollout session. */
  readonly agent_id: string;
  /** Nickname assigned to the child agent. */
  readonly nickname: string;
}

/**
 * Parameters for waiting on subagents to finish.
 */
export interface ICodexWaitAgentArgs {
  /** Array of target agent UUIDs. */
  readonly targets: readonly string[];
  /** Timeout in milliseconds. */
  readonly timeout_ms?: number;
}

/**
 * Parameters for closing a completed subagent.
 */
export interface ICodexCloseAgentArgs {
  /** Target agent UUID to terminate. */
  readonly target: string;
}

// ============================================================================
// 6. Record Types (Discriminated by `type`)
// ============================================================================

/**
 * First record in every Codex rollout file: session identity, CWD, provider, and git.
 */
export interface ICodexSessionMetaPayload {
  /** Unique session UUIDv7 matching the filename. */
  readonly id?: string;
  /** Legacy session ID field name used in older rollout versions. */
  readonly session_id?: string;
  /** ISO 8601 creation timestamp. */
  readonly timestamp: string;
  /** Absolute working directory of the project. */
  readonly cwd: string;
  /** Originator binary/client (e.g. "codex-tui", "codex-cli"). */
  readonly originator: string;
  /** Semantic version of Codex CLI (e.g. "0.121.0"). */
  readonly cli_version: string;
  /** Source classification ("cli" or subagent spawn metadata). */
  readonly source: CodexSessionSource;
  /** Model provider name (e.g. "openai"). */
  readonly model_provider: string;
  /** Base system prompt instructions. */
  readonly base_instructions: {
    readonly text: string;
  };
  /** Git repository metadata at time of session creation. */
  readonly git?: ICodexGitMeta;
  /** UUID of parent thread if this session is a subagent. */
  readonly parent_thread_id?: string;
  /** Subagent nickname if applicable (e.g. "Hubble"). */
  readonly agent_nickname?: string;
  /** Subagent role if applicable (e.g. "explorer"). */
  readonly agent_role?: string;
  /** Multi-agent protocol version. */
  readonly multi_agent_version?: string;
}

export interface ICodexSessionMetaRecord {
  readonly type: "session_meta";
  readonly timestamp: string;
  readonly payload: ICodexSessionMetaPayload;
}

/**
 * Per-turn execution context: model settings, sandbox permissions, and approval policy.
 */
export interface ICodexTurnContextPayload {
  readonly turn_id: string;
  readonly cwd: string;
  readonly current_date: string;
  readonly timezone: string;
  /** Active model for this turn (e.g. "gpt-5.4", "o3-mini"). */
  readonly model: string;
  readonly personality: string;
  readonly approval_policy: "on-request" | "never" | "always" | string;
  readonly sandbox_policy: {
    readonly type: string;
    readonly writable_roots: readonly string[];
    readonly network_access: boolean;
    readonly [key: string]: unknown;
  };
  readonly collaboration_mode: {
    readonly mode: "default" | "plan" | string;
    readonly settings?: Record<string, unknown>;
  };
  readonly summary: string;
  readonly truncation_policy: {
    readonly mode: string;
    readonly limit: number;
  };
  readonly realtime_active: boolean;
}

export interface ICodexTurnContextRecord {
  readonly type: "turn_context";
  readonly timestamp: string;
  readonly payload: ICodexTurnContextPayload;
}

// ----------------------------------------------------------------------------
// Event Message Payloads (`type: "event_msg"`)
// ----------------------------------------------------------------------------

export interface ICodexUserMessagePayload {
  readonly type: "user_message";
  /** The user prompt string. */
  readonly message: string;
  readonly images?: readonly unknown[];
  readonly local_images?: readonly unknown[];
  readonly text_elements?: readonly unknown[];
}

export interface ICodexAgentMessagePayload {
  readonly type: "agent_message";
  /** Natural language response generated by the agent. */
  readonly message: string;
}

export interface ICodexReasoningPayload {
  readonly type: "reasoning";
  /** Summary or status of reasoning tokens. */
  readonly summary?: readonly {
    readonly type: string;
    readonly text: string;
  }[];
  /** Encrypted raw reasoning payload (unrecoverable without key). */
  readonly encrypted_content?: string;
}

export interface ICodexTokenCountPayload {
  readonly type: "token_count";
  readonly info: ICodexTokenCountInfo;
  readonly rate_limits?: ICodexRateLimits;
}

export interface ICodexTaskStartedPayload {
  readonly type: "task_started";
  readonly turn_id: string;
  /** Unix seconds in native rollouts; RFC3339 strings also occur. */
  readonly started_at?: number | string | null;
  readonly model_context_window?: number;
  readonly collaboration_mode_kind?: string;
}

export interface ICodexTaskCompletePayload {
  readonly type: "task_complete";
  readonly turn_id: string;
  readonly last_agent_message?: string;
  /** Unix seconds in native rollouts; RFC3339 strings also occur. */
  readonly started_at?: number | string | null;
  readonly completed_at?: number | string | null;
  readonly duration_ms: number;
}

/**
 * Structured error information emitted by Codex in error events.
 * Grounded in upstream `codex-rs/protocol/src/protocol.rs:2068-2071`.
 * Variants include rate limit exhaustion, account usage limits, server overload,
 * and HTTP connection failure with response status codes.
 */
export type CodexErrorInfo =
  | "RateLimitExceeded"
  | "UsageLimitExceeded"
  | "ServerOverloaded"
  | "rate_limit_exceeded"
  | "usage_limit_exceeded"
  | "server_overloaded"
  | {
      readonly HttpConnectionFailed?: {
        readonly http_status_code?: number;
      };
      readonly http_connection_failed?: {
        readonly http_status_code?: number;
      };
    }
  | {
      readonly type: "HttpConnectionFailed" | "http_connection_failed";
      readonly http_status_code?: number;
    };

export interface ICodexErrorPayload {
  readonly type: "error";
  readonly message: string;
  readonly codex_error_info?: CodexErrorInfo | null;
}

/** Agent message completion observed in Codex 0.162.0, including async questions. */
export interface ICodexItemCompletedPayload {
  readonly type: "item_completed";
  readonly thread_id?: string;
  readonly turn_id?: string;
  readonly started_at_ms?: number;
  readonly completed_at_ms?: number;
  readonly item: ICodexCompletedAgentMessage | Readonly<Record<string, unknown>>;
}

export interface ICodexCompletedAgentMessage {
  readonly type: "AgentMessage";
  readonly id?: string;
  readonly content: readonly ICodexAgentMessageText[];
  readonly phase?: string;
  readonly delivery?: string;
  readonly questions?: readonly ICodexAgentQuestion[];
}

export interface ICodexAgentMessageText {
  readonly type: "Text";
  readonly text: string;
}

export interface ICodexAgentQuestion {
  readonly title: string;
  readonly options?: readonly string[] | null;
}

export type CodexEventMsgPayload =
  | ICodexItemCompletedPayload
  | ICodexUserMessagePayload
  | ICodexAgentMessagePayload
  | ICodexReasoningPayload
  | ICodexTokenCountPayload
  | ICodexTaskStartedPayload
  | ICodexTaskCompletePayload
  | ICodexErrorPayload;

export interface ICodexEventMsgRecord {
  readonly type: "event_msg";
  readonly timestamp: string;
  readonly payload: CodexEventMsgPayload;
}

// ----------------------------------------------------------------------------
// Raw Response Item Payloads (`type: "response_item"`)
// ----------------------------------------------------------------------------

export interface ICodexRawMessageTextItem {
  readonly type: "input_text" | "output_text" | "text";
  readonly text: string;
}

export interface ICodexRawMessagePayload {
  readonly type: "message";
  readonly role: "user" | "assistant" | "developer" | "system";
  /** User messages carry attached images as `input_image` items after their text. */
  readonly content: readonly (ICodexRawMessageTextItem | CodexToolOutputImageItem)[];
}

export interface ICodexFunctionCallPayload {
  readonly type: "function_call";
  /** Name of the tool (e.g. "exec_command", "spawn_agent", "apply_patch"). */
  readonly name: string;
  /** Unique invocation ID. */
  readonly call_id: string;
  /** JSON-encoded argument string. */
  readonly arguments?: string;
  /** Alternative parameter payload used in some tool calls or bridges. */
  readonly input?: string | Readonly<Record<string, unknown>>;
}

/** Tool output body serialized directly as a string or an ordered content array.
 * @see https://github.com/openai/codex/blob/main/codex-rs/protocol/src/models.rs
 */
export interface ICodexToolOutputTextItem {
  readonly type: "input_text";
  readonly text: string;
}

export interface ICodexToolOutputImageMetadata {
  readonly type: "input_image";
  readonly detail?: "auto" | "low" | "high" | "original";
}

export interface ICodexInlineImageReference {
  readonly image_url: string;
}

export interface ICodexFileImageReference {
  readonly file_id: string;
}

export type CodexToolOutputImageItem = ICodexToolOutputImageMetadata &
  (ICodexInlineImageReference | ICodexFileImageReference);

export interface ICodexToolOutputAudioItem {
  readonly type: "input_audio";
  readonly audio_url: string;
}

export interface ICodexToolOutputEncryptedItem {
  readonly type: "encrypted_content";
  readonly encrypted_content: string;
}

export type CodexToolOutputContentItem =
  | ICodexToolOutputTextItem
  | CodexToolOutputImageItem
  | ICodexToolOutputAudioItem
  | ICodexToolOutputEncryptedItem;

export type CodexToolOutputBody = string | readonly CodexToolOutputContentItem[];

export interface ICodexFunctionCallOutputPayload {
  readonly type: "function_call_output";
  /** Correlating call ID. */
  readonly call_id: string;
  /** Text output or structured content; normalized toolOutput preserves arrays as JSON. */
  readonly output: CodexToolOutputBody;
}

export interface ICodexCustomToolCallPayload {
  readonly type: "custom_tool_call";
  /** Name of the custom tool or MCP method. */
  readonly name: string;
  /** Unique invocation ID. */
  readonly call_id: string;
  /** JSON-encoded argument string. */
  readonly arguments?: string;
  /** Alternative parameter payload used in some tool calls or bridges. */
  readonly input?: string | Readonly<Record<string, unknown>>;
}

export interface ICodexCustomToolCallOutputPayload {
  readonly type: "custom_tool_call_output";
  /** Correlating call ID. */
  readonly call_id: string;
  /** Text output or structured content; normalized toolOutput preserves arrays as JSON. */
  readonly output: CodexToolOutputBody;
}

export interface ICodexResponseReasoningPayload {
  readonly type: "reasoning";
  readonly summary?: readonly {
    readonly type: string;
    readonly text: string;
  }[];
  readonly encrypted_content?: string;
}

export interface ICodexToolSearchCallPayload {
  readonly type: "tool_search_call";
  readonly call_id: string;
  readonly query: string;
}

export interface ICodexToolSearchOutputPayload {
  readonly type: "tool_search_output";
  readonly call_id: string;
  readonly results: readonly unknown[];
}

export type CodexResponseItemPayload =
  | ICodexRawMessagePayload
  | ICodexFunctionCallPayload
  | ICodexFunctionCallOutputPayload
  | ICodexCustomToolCallPayload
  | ICodexCustomToolCallOutputPayload
  | ICodexResponseReasoningPayload
  | ICodexToolSearchCallPayload
  | ICodexToolSearchOutputPayload;

export interface ICodexResponseItemRecord {
  readonly type: "response_item";
  readonly timestamp: string;
  readonly payload: CodexResponseItemPayload;
}

// ============================================================================
// 7. Full Discriminated Union of All Codex Records
// ============================================================================

/**
 * Union of all line-delimited records that appear in a Codex rollout JSONL file.
 */
export type CodexRecord =
  | ICodexSessionMetaRecord
  | ICodexTurnContextRecord
  | ICodexEventMsgRecord
  | ICodexResponseItemRecord;

// ============================================================================
// 8. Type Guards & Helper Functions
// ============================================================================

/**
 * Checks if a record is the first-line `session_meta` record.
 */
export function isCodexSessionMeta(record: CodexRecord): record is ICodexSessionMetaRecord {
  return record.type === "session_meta";
}

/**
 * Checks if a record is a per-turn context update.
 */
export function isCodexTurnContext(record: CodexRecord): record is ICodexTurnContextRecord {
  return record.type === "turn_context";
}

/**
 * Checks if a record is a UI-facing event message.
 */
export function isCodexEventMsg(record: CodexRecord): record is ICodexEventMsgRecord {
  return record.type === "event_msg";
}

/**
 * Checks if a record is a raw LLM response / function call item.
 */
export function isCodexResponseItem(record: CodexRecord): record is ICodexResponseItemRecord {
  return record.type === "response_item";
}

/**
 * Determines whether a session represents a subagent based on its metadata.
 */
export function isCodexSubagentSession(meta: ICodexSessionMetaPayload): boolean {
  if (meta.parent_thread_id && meta.parent_thread_id.length > 0) {
    return true;
  }
  if (typeof meta.source === "object" && meta.source !== null && "subagent" in meta.source) {
    return true;
  }
  return false;
}

/**
 * Extracts a session title from the first non-synthetic human prompt.
 * Skips internal `<environment_context>` blocks injected automatically by Codex.
 *
 * @param promptText - The prompt string from a user message.
 * @returns Cleaned title string or null if prompt is synthetic environment context.
 */
export function extractCodexTitleFromPrompt(promptText: string): string | null {
  const trimmed = promptText.trim();
  if (trimmed.startsWith("<environment_context>") || trimmed.startsWith("<session_context>")) {
    return null;
  }
  const firstLine = trimmed.split("\n")[0] ?? trimmed;
  return firstLine.slice(0, 100);
}
