/**
 * Claude Code Harness Log Specifications & TypeScript Type Definitions
 *
 * This file serves as the canonical data structure description and type specification
 * for Claude Code (`@anthropic-ai/claude-code`) execution logs, session descriptors,
 * and JSONL event transcripts.
 *
 * Primary GitHub Sources & Line References:
 * - parse-cc (TypeScript library): https://github.com/d1ll0n/parse-cc/blob/main/src/types/entries.ts#L1-L290
 * - parse-cc content blocks: https://github.com/d1ll0n/parse-cc/blob/main/src/types/content.ts#L1-L60
 * - parse-cc attachments: https://github.com/d1ll0n/parse-cc/blob/main/src/types/attachments.ts#L1-L120
 * - parse-cc session class: https://github.com/d1ll0n/parse-cc/blob/main/src/Session.ts#L1-L250
 * - claude-session-parser (Rust streaming parser): https://github.com/sevenevesai/claude-session-parser/blob/main/src/lib.rs#L1-L180
 * - claude-code-transcripts (Python parser): https://github.com/simonw/claude-code-transcripts/blob/main/claude_code_transcripts.py#L1-L240
 * - claude-code-log-viewer-cli (Go TUI): https://github.com/JeiKeiLim/claude-code-log-viewer-cli/blob/main/internal/providers/claude/claude.go#L1-L160
 * - claude-code-log-visualizer (JS/TS transcript inspector): https://github.com/dtang27182/claude-code-log-visualizer/blob/main/src/types/transcript.ts#L1-L150
 * - claude-code-session-viewer (JS/TS parser): https://github.com/eric050828/claude-code-session-viewer/blob/main/lib/claude-parser.ts#L1-L220
 */

// ============================================================================
// 1. On-Disk Layout & Path Resolution
// ============================================================================

/**
 * Standard filesystem locations used by Claude Code on macOS and Linux:
 *
 * 1. Active Session Descriptors:
 *    `${XDG_DATA_HOME:-~/.local/share}/ai-registry/claude-code/sessions/<pid>.json`
 *    or fallback: `~/.claude/sessions/<pid>.json`
 *    @see https://github.com/d1ll0n/parse-cc/blob/main/src/discover.ts#L25-L75
 *
 * 2. IPC Sockets & Peer Credentials:
 *    Descriptor specifies `messagingSocketPath` (e.g. `/tmp/cc-socks/<pid>.sock`).
 *    Sibling credential files exist at `~/.claude/sessions/<pid>.<64-hex>.key` (mode 0600).
 *
 * 3. Project Transcripts (JSONL):
 *    `<projects-store>/<slug(cwd)>/<sessionId>.jsonl`
 *    Where `<slug(cwd)>` replaces `/` and `.` with `-` and collapses leading path separators:
 *      - macOS/Linux: `/Users/alice/dev/app` -> `-Users-alice-dev-app`
 *      - Windows: `C:\Users\alice\dev\app`   -> `C--Users-alice-dev-app`
 *    @see https://github.com/d1ll0n/parse-cc/blob/main/src/discover.ts#L110-L150
 *
 * 4. Subagent Transcripts:
 *    - New layout: `<projects-store>/<slug(cwd)>/<sessionId>/subagents/agent-<agentId>.jsonl`
 *    - Legacy layout: `<projects-store>/<slug(cwd)>/agent-<agentId>.jsonl` (filtered by sessionId)
 *    @see https://github.com/d1ll0n/parse-cc/blob/main/src/subagents.ts#L20-L80
 *
 * 5. Global OAuth & Configuration:
 *    `~/.claude.json` containing `oauthAccount`, `organizationUuid`, etc.
 */

export interface IClaudeCodePaths {
  /** Root directory for Claude Code projects, default `~/.claude/projects` */
  projectsDir: string;
  /** Root directory for active session descriptors, default `~/.claude/sessions` */
  sessionsDir: string;
  /** Global configuration file, default `~/.claude.json` */
  configFile: string;
}

// ============================================================================
// 2. Session Descriptor (~/.claude/sessions/<pid>.json)
// ============================================================================

/**
 * Coarse runtime status reported by the Claude Code process in its descriptor.
 */
export type ClaudeSessionStatus = "busy" | "idle" | string;

/**
 * Execution entrypoint through which Claude Code was spawned.
 * - `"cli"`: Interactive terminal session.
 * - `"sdk-cli"`: Headless `claude -p` spawn / Agent SDK runner.
 */
export type ClaudeEntryPoint = "cli" | "sdk-cli" | string;

/**
 * Active session descriptor written to `~/.claude/sessions/<pid>.json`.
 * Contains process-level identity, socket bindings, and coarse liveness status.
 */
export interface IClaudeSessionDescriptor {
  /** OS Process ID of the Claude Code instance */
  pid: number;
  /** Stable UUID identifying the conversational session */
  sessionId: string;
  /** Absolute working directory of the process */
  cwd: string;
  /** Epoch timestamp in milliseconds when the session was created */
  startedAt: number;
  /** Human-readable process start time string (e.g., "Sat Sep 19 01:21:23 2026") */
  procStart: string;
  /** Semantic version string of the Claude Code binary (e.g., "2.1.277") */
  version: string;
  /** Version number of the peer socket protocol */
  peerProtocol: number;
  /** Optional array of peer feature flags supported by this version */
  peerFeatures?: string[];
  /** Session interaction kind (e.g., "interactive" or "headless") */
  kind: "interactive" | "headless" | string;
  /** Invocation entrypoint */
  entrypoint: ClaudeEntryPoint;
  /** Host OS platform domain ("darwin", "linux", "windows") */
  pidDomain: "darwin" | "linux" | "windows" | string;
  /** Absolute path to the local Unix domain socket for IPC messaging */
  messagingSocketPath?: string;
  /** User-visible or AI-derived session name */
  name?: string;
  /** Provenance of the session name ("derived", "custom", etc.) */
  nameSource?: "derived" | "custom" | string;
  /** Timestamp when the session name was assigned */
  nameSince?: number;
  /** Coarse execution status */
  status: ClaudeSessionStatus;
  /** Timestamp in milliseconds of the last heartbeat update */
  updatedAt: number;
  /** Timestamp in milliseconds of the last status change */
  statusUpdatedAt: number;
}

// ============================================================================
// 3. Token Usage & Model Pricing
// ============================================================================

/**
 * Nested cache-creation token breakdown by retention TTL.
 */
export interface IClaudeCacheCreation {
  /** Tokens written into the ephemeral 5-minute cache (1.25x base input rate) */
  ephemeral_5m_input_tokens?: number;
  /** Tokens written into the ephemeral 1-hour cache (2.00x base input rate) */
  ephemeral_1h_input_tokens?: number;
}

/**
 * Nested server-tool execution counts (billable server-side operations).
 */
export interface IClaudeServerToolUse {
  /** Billable web search requests ($0.01 per search) */
  web_search_requests?: number;
  /** Non-billable web fetch requests */
  web_fetch_requests?: number;
}

/**
 * Token usage breakdown attached to assistant messages and usage rollups.
 */
export interface IClaudeUsageMetadata {
  /** Fresh input tokens processed in this turn (not served from cache) */
  input_tokens: number;
  /** Output tokens generated by the model in this turn */
  output_tokens: number;
  /** Tokens read back from the prompt cache (0.1x / 0.025x input rate) */
  cache_read_input_tokens?: number;
  /** Legacy flat total of tokens written to prompt cache */
  cache_creation_input_tokens?: number;
  /** Granular cache-creation breakdown by TTL tier */
  cache_creation?: IClaudeCacheCreation;
  /** Server-side tool execution requests */
  server_tool_use?: IClaudeServerToolUse;
  /** API service tier used for inference (e.g. "standard") */
  service_tier?: string;
  /** Inference speed tier ("standard" | "fast") */
  speed?: "standard" | "fast" | string;
  /** Reasoning effort level if configured */
  effort?: string;
  /** Geographic region where inference was executed */
  inference_geo?: string;
  /** Output tokens dedicated to internal model reasoning / thinking */
  reasoning_output_tokens?: number;
  /** Cumulative total tokens for the turn */
  total_tokens?: number;
}

/**
 * Reason the model ceased generating tokens for a turn.
 */
export type ClaudeStopReason =
  | "end_turn"
  | "tool_use"
  | "max_tokens"
  | "stop_sequence"
  | "refusal"
  | null;

// ============================================================================
// 4. Message Content Blocks (Anthropic API Passthrough)
// ============================================================================

/**
 * Text block representing regular assistant or user text.
 */
export interface IClaudeTextBlock {
  type: "text";
  text: string;
}

/**
 * Extended thinking / reasoning block emitted by reasoning-capable models.
 */
export interface IClaudeThinkingBlock {
  type: "thinking";
  thinking: string;
  /** Cryptographic signature verifying authenticity */
  signature?: string;
}

/**
 * Redacted reasoning block emitted when thinking content is censored.
 */
export interface IClaudeRedactedThinkingBlock {
  type: "redacted_thinking";
  data?: string;
}

/**
 * Tool caller attribution for nested or code-execution dispatch.
 */
export interface IClaudeCaller {
  type?: string;
}

/**
 * Tool use block emitted by the assistant when invoking a tool.
 */
export interface IClaudeToolUseBlock<TInput = Record<string, unknown>> {
  type: "tool_use";
  /** Unique invocation ID (e.g. "toolu_01A0b64b...") */
  id: string;
  /** Tool name (e.g. "Bash", "Read", "Write", "Edit", "Glob", "Grep", "Agent", "AskUserQuestion") */
  name: string;
  /** Tool invocation parameters */
  input: TInput;
  /** Optional caller metadata */
  caller?: IClaudeCaller;
}

/**
 * Tool result block carrying the execution output of a preceding ToolUseBlock.
 * Injected in user-role messages by the Claude Code harness.
 */
export interface IClaudeToolResultBlock<TContent = string | IClaudeContentBlock[]> {
  type: "tool_result";
  /** Matches the `id` of the originating `IClaudeToolUseBlock` */
  tool_use_id: string;
  /** Output payload (text string, image block, or array of sub-blocks) */
  content: TContent;
  /** True if tool execution resulted in an error or non-zero exit code */
  is_error?: boolean;
}

/**
 * Base64 image payload block.
 */
export interface IClaudeImageBlock {
  type: "image";
  source: {
    type: "base64";
    media_type: "image/png" | "image/jpeg" | "image/gif" | "image/webp";
    data: string;
  };
}

/**
 * Model fallback marker recorded when retrying a turn on an alternative model.
 */
export interface IClaudeFallbackBlock {
  type: "fallback";
  from: unknown;
  to: unknown;
}

/**
 * Discriminated union of all content blocks appearing in `message.content`.
 */
export type IClaudeContentBlock =
  | IClaudeTextBlock
  | IClaudeThinkingBlock
  | IClaudeRedactedThinkingBlock
  | IClaudeToolUseBlock
  | IClaudeToolResultBlock
  | IClaudeImageBlock
  | IClaudeFallbackBlock;

// ============================================================================
// 5. Built-in Tool Input & Result Schemas
// ============================================================================

/**
 * Bash command execution tool input.
 */
export interface IBashToolInput {
  command: string;
  description?: string;
  timeout?: number;
  run_in_background?: boolean;
  dangerously_disable_sandbox?: boolean;
}

/**
 * Git operation descriptor extracted from bash command results.
 */
export interface IGitOperationData {
  branch?: { action: string; ref: string };
  commit?: { kind: string; sha: string };
  pr?: { action: string; number: number; url?: string };
  push?: { branch: string };
}

/**
 * Bash command execution tool result.
 */
export interface IBashToolResult {
  stdout: string;
  stderr: string;
  interrupted: boolean;
  is_image?: boolean;
  no_output_expected?: boolean;
  return_code_interpretation?: string;
  exitCode?: number;
  background_task_id?: string;
  assistant_auto_backgrounded?: boolean;
  backgrounded_by_user?: boolean;
  dangerously_disable_sandbox?: boolean;
  git_operation?: IGitOperationData;
  persisted_output_path?: string;
  persisted_output_size?: number;
  stale_read_file_state_hint?: string;
}

/**
 * File read tool input.
 */
export interface IReadToolInput {
  file_path: string;
  limit?: number;
  offset?: number;
}

/**
 * File read tool payload result.
 */
export interface IFileReadPayload {
  file_path?: string;
  content?: string;
  num_lines?: number;
  start_line?: number;
  total_lines?: number;
  type?: string;
  base64?: string;
  dimensions?: unknown;
  original_size?: number;
  truncated_by_token_cap?: boolean;
}

/**
 * File read tool result wrapper.
 */
export interface IReadToolResult {
  type: string;
  file: IFileReadPayload;
}

/**
 * Unified diff hunk descriptor for file edits.
 */
export interface IPatchHunk {
  old_start: number;
  old_lines: number;
  new_start: number;
  new_lines: number;
  lines: string[];
}

/**
 * File edit tool input.
 */
export interface IEditToolInput {
  file_path: string;
  old_string: string;
  new_string: string;
  replace_all?: boolean;
}

/**
 * File edit tool result.
 */
export interface IEditToolResult {
  file_path: string;
  old_string: string;
  new_string: string;
  replace_all: boolean;
  original_file?: string;
  structured_patch: IPatchHunk[];
  user_modified: boolean;
}

/**
 * Multi-file / multi-hunk edit tool input (`MultiEdit`).
 */
export interface IMultiEditHunk {
  old_string: string;
  new_string: string;
  replace_all?: boolean;
}

export interface IMultiEditToolInput {
  file_path: string;
  edits: IMultiEditHunk[];
}

/**
 * Multi-file / multi-hunk edit tool result (`MultiEdit`).
 */
export interface IMultiEditToolResult {
  file_path: string;
  structured_patch?: IPatchHunk[];
  user_modified?: boolean;
}

/**
 * Jupyter notebook cell edit tool input (`NotebookEdit`).
 */
export interface INotebookEditToolInput {
  notebook_path: string;
  new_source: string;
  cell_id?: string;
  cell_index?: number;
  edit_mode?: "replace" | "insert" | "delete" | string;
}

/**
 * Jupyter notebook cell edit tool result (`NotebookEdit`).
 */
export interface INotebookEditToolResult {
  notebook_path: string;
  cell_id?: string;
  cell_index?: number;
  status?: string;
}

/**
 * Jupyter notebook read tool input (`NotebookRead`).
 */
export interface INotebookReadToolInput {
  notebook_path: string;
  cell_index?: number;
}

/**
 * File write / create tool input.
 */
export interface IWriteToolInput {
  file_path: string;
  content: string;
}

/**
 * File write tool result.
 */
export interface IWriteToolResult {
  file_path: string;
  content: string;
  type: string;
  original_file?: string;
  structured_patch: IPatchHunk[];
  user_modified: boolean;
}

/**
 * Glob pattern search tool input.
 */
export interface IGlobToolInput {
  pattern: string;
  path?: string;
}

/**
 * Glob pattern search tool result.
 */
export interface IGlobToolResult {
  filenames: string[];
  duration_ms: number;
  num_files: number;
  truncated?: boolean;
  total_matches?: number;
  count_is_complete?: boolean;
}

/**
 * Grep text search tool input.
 */
export interface IGrepToolInput {
  pattern?: string;
  path?: string;
  output_mode?: string;
  glob?: string;
  type?: string;
  head_limit?: number;
  multiline?: boolean;
  "-A"?: number;
  "-B"?: number;
  "-C"?: number;
  "-i"?: boolean;
  "-n"?: boolean;
}

/**
 * Grep text search tool result.
 */
export interface IGrepToolResult {
  mode?: string;
  num_files?: number;
  filenames?: string[];
  content?: string;
  num_lines?: number;
}

/**
 * Subagent invocation tool input (`Agent` / `Task`).
 */
export interface IAgentToolInput {
  description: string;
  prompt: string;
  subagent_type: string;
  name?: string;
  model?: string;
  isolation?: string;
  run_in_background?: boolean;
}

/**
 * Subagent aggregate tool statistics.
 */
export interface IAgentToolStats {
  read_count: number;
  search_count: number;
  bash_count: number;
  edit_file_count: number;
  lines_added: number;
  lines_removed: number;
  other_tool_count: number;
}

/**
 * Subagent execution tool result (`Agent` / `Task`).
 */
export interface IAgentToolResult {
  status: "completed" | "failed" | "aborted" | string;
  prompt: string;
  agent_id?: string;
  agent_type?: string;
  resolved_model?: string;
  description?: string;
  content?: IClaudeContentBlock[];
  usage?: IClaudeUsageMetadata;
  tool_stats?: IAgentToolStats;
  total_tokens?: number;
  total_duration_ms?: number;
  total_tool_use_count?: number;
  is_async?: boolean;
  output_file?: string;
  can_read_output_file?: boolean;
  // Fleet / Teammate spawn properties
  teammate_id?: string;
  model?: string;
  name?: string;
  color?: string;
  tmux_session_name?: string;
  tmux_window_name?: string;
  tmux_pane_id?: string;
  team_name?: string;
  is_splitpane?: boolean;
  plan_mode_required?: boolean;
}

/**
 * Interactive user question prompt tool input (`AskUserQuestion`).
 */
export interface IAskUserQuestionInput {
  questions: Array<{
    question: string;
    header?: string;
    options?: Array<{ label: string; value: string }>;
    default?: string;
  }>;
}

/**
 * User question answer result (`AskUserQuestion`).
 */
export interface IAskUserQuestionResult {
  questions: unknown[];
  answers: Record<string, string>;
  annotations?: unknown;
  afk_timeout_ms?: number;
}

/**
 * Web search tool input.
 */
export interface IWebSearchToolInput {
  query: string;
}

/**
 * Web search tool result.
 */
export interface IWebSearchToolResult {
  query: string;
  results: Array<{
    title: string;
    url: string;
    snippet: string;
  }>;
  search_count: number;
  duration_seconds: number;
}

/**
 * Web fetch tool input.
 */
export interface IWebFetchToolInput {
  url: string;
  prompt: string;
}

/**
 * Web fetch tool result.
 */
export interface IWebFetchToolResult {
  url: string;
  result: string;
  code: number;
  code_text: string;
  bytes: number;
  duration_ms: number;
}

// ============================================================================
// 6. Attachment Payloads (Harness Metadata & Hooks)
// ============================================================================

export interface IHookSuccessAttachment {
  type: "hook_success";
  command: string;
  content: string;
  durationMs: number;
  exitCode: number;
  hookEvent: string;
  hookName: string;
  stderr: string;
  stdout: string;
  toolUseID: string;
}

export interface IHookNonBlockingErrorAttachment {
  type: "hook_non_blocking_error";
  command: string;
  durationMs: number;
  exitCode: number;
  hookEvent: string;
  hookName: string;
  stderr: string;
  stdout: string;
  toolUseID: string;
}

export interface IHookBlockingErrorAttachment {
  type: "hook_blocking_error";
  blockingError: unknown;
  hookEvent: string;
  hookName: string;
  toolUseID: string;
}

export interface IHookAdditionalContextAttachment {
  type: "hook_additional_context";
  content: unknown[];
  hookEvent: string;
  hookName: string;
  toolUseID: string;
}

export interface IHookSystemMessageAttachment {
  type: "hook_system_message";
  content: string;
  hookEvent: string;
  hookName: string;
  toolUseID: string;
}

export interface ISkillListingAttachment {
  type: "skill_listing";
  content: string;
  isInitial: boolean;
  names: string[];
  skillCount: number;
}

export interface IDeferredToolsDeltaAttachment {
  type: "deferred_tools_delta";
  addedLines: unknown[];
  addedNames: string[];
  readdedNames?: string[];
  removedNames: string[];
  pendingMcpServers?: unknown[];
  needsAuthMcpServers?: unknown[];
}

export interface ICommandPermissionsAttachment {
  type: "command_permissions";
  allowedTools: string[];
}

export interface IOpenedFileInIdeAttachment {
  type: "opened_file_in_ide";
  filename: string;
  displayPath?: string;
}

export interface ISelectedLinesInIdeAttachment {
  type: "selected_lines_in_ide";
  content: string;
  displayPath: string;
  filename: string;
  ideName: string;
  lineEnd: number;
  lineStart: number;
}

export interface IQueuedCommandAttachment {
  type: "queued_command";
  commandMode?: string;
  prompt?: string | unknown[];
  imagePasteIds?: number[];
  isMeta?: boolean;
  origin?: unknown;
  timestamp?: string;
}

export interface IPlanModeAttachment {
  type: "plan_mode";
  isSubAgent: boolean;
  planExists: boolean;
  planFilePath: string;
  reminderType?: string;
}

export interface IPlanFileReferenceAttachment {
  type: "plan_file_reference";
  planContent: string;
  planFilePath: string;
}

export interface ITaskStatusAttachment {
  type: "task_status";
  taskId: string;
  taskType: string;
  status: string;
  description: string;
  outputFilePath: string;
  deltaSummary?: unknown;
}

export interface IUnknownAttachment {
  type: string;
  [key: string]: unknown;
}

/**
 * Union of all structured attachment payloads wrapped in `IAttachmentRecord`.
 */
export type IClaudeAttachmentPayload =
  | IHookSuccessAttachment
  | IHookNonBlockingErrorAttachment
  | IHookBlockingErrorAttachment
  | IHookAdditionalContextAttachment
  | IHookSystemMessageAttachment
  | ISkillListingAttachment
  | IDeferredToolsDeltaAttachment
  | ICommandPermissionsAttachment
  | IOpenedFileInIdeAttachment
  | ISelectedLinesInIdeAttachment
  | IQueuedCommandAttachment
  | IPlanModeAttachment
  | IPlanFileReferenceAttachment
  | ITaskStatusAttachment
  | IUnknownAttachment;

// ============================================================================
// 7. Conversational Base Envelope
// ============================================================================

/**
 * Common envelope fields present on full conversational records
 * (`assistant`, `user`, `attachment`, `system`).
 */
export interface IClaudeConversationalBase {
  /** Unique UUID for this transcript entry */
  uuid?: string;
  /** UUID of the logical parent record in the conversation tree */
  parentUuid?: string | null;
  /** ISO-8601 UTC timestamp string (e.g. "2026-09-19T01:21:23.485Z") */
  timestamp?: string;
  /** Stable UUID of the session */
  sessionId?: string;
  /** Working directory at the time the record was generated */
  cwd?: string;
  /** Active Git branch name at the time the record was generated */
  gitBranch?: string;
  /** Version of the Claude Code harness */
  version?: string;
  /** Slug of the project directory */
  slug?: string;
  /** Entrypoint of invocation ("cli" | "sdk-cli") */
  entrypoint?: ClaudeEntryPoint;
  /** Origin user type ("human" | "agent" | "external") */
  userType?: string;
  /** Flag set to true if the record belongs to a subagent sidechain */
  isSidechain?: boolean;
  /** Identifier of the subagent if this record belongs to a subagent thread */
  agentId?: string;
  /** Source session ID if carried over across sessions */
  sourceSessionId?: string;
}

// ============================================================================
// 8. JSONL Transcript Line Records (Top-Level Discriminated Union)
// ============================================================================

/**
 * Assistant record: The primary turn response from Claude.
 *
 * In streamed multi-block turns (e.g. thinking followed by tool_use or text), Claude Code
 * writes each content block as a separate JSONL line. All streamed block records for the same
 * API response share identical `message.id`, `requestId`, `message.stop_reason`, and cumulative `message.usage`.
 */
export interface IClaudeAssistantRecord extends IClaudeConversationalBase {
  type: "assistant";
  message: {
    role: "assistant";
    /** Anthropic API message ID (e.g. "msg_01...") */
    id: string;
    /** Model identifier (e.g. "claude-haiku-4-5", "claude-sonnet-4-6", "claude-opus-4-6", "<synthetic>") */
    model: string;
    /** Ordered content blocks (thinking, text, tool_use) */
    content: IClaudeContentBlock[];
    /** Stop reason describing the entire API response outcome */
    stop_reason: ClaudeStopReason;
    /** Matched stop sequence string if applicable */
    stop_sequence?: string | null;
    /** Token usage breakdown (cumulative for the request, identical across streamed blocks) */
    usage: IClaudeUsageMetadata;
  };
  effort?: string;
  requestId?: string;
  /**
   * 0-based index of the content block in streamed multi-block assistant responses.
   * When present, Claude Code outputs each content block (e.g. thinking at block 0, tool_use or text
   * at block 1+) as a distinct JSONL line sharing `message.id`, `requestId`, `stop_reason`, and `usage`.
   */
  apiBlockIndex?: number;
  apiErrorStatus?: number;
  error?: string;
  isApiErrorMessage?: boolean;
}

/**
 * User record: Human input, injected tool results, or compaction summaries.
 */
export interface IClaudeUserRecord extends IClaudeConversationalBase {
  type: "user";
  message: {
    role: "user";
    /** String user input or array of content blocks (text, tool_result, image) */
    content: string | IClaudeContentBlock[];
  };
  /** Set to true when this record wraps harness-injected content (e.g. tool results) */
  isMeta?: boolean;
  /** Set to true on context-compaction summary records */
  isCompactSummary?: boolean;
  /** Structured tool-result payload if available */
  toolUseResult?: unknown;
  /** Originating tool use ID if this record answers a specific tool call */
  sourceToolUseID?: string;
  /** UUID of the assistant message containing the originating tool call */
  sourceToolAssistantUUID?: string;
  /** Image paste references */
  imagePasteIds?: number[];
  /** Permission mode in effect when prompt was executed */
  permissionMode?: string;
  /** Active user prompt ID */
  promptId?: string;
}

/**
 * Attachment record: Out-of-band harness state, hook outputs, or IDE context.
 */
export interface IClaudeAttachmentRecord extends IClaudeConversationalBase {
  type: "attachment";
  attachment: IClaudeAttachmentPayload;
}

/**
 * System record: Operational events, timing metrics, and hook summaries.
 */
export interface IClaudeSystemRecord extends IClaudeConversationalBase {
  type: "system";
  /** Subtype categorizing the system event */
  subtype:
    | "init"
    | "turn_duration"
    | "local_command"
    | "stop_hook_summary"
    | "compact_boundary"
    | string;
  content?: string;
  durationMs?: number;
  messageCount?: number;
  isMeta?: boolean;
  level?: string;
  toolUseID?: string;
  hookCount?: number;
  hookInfos?: Array<{ command?: string; durationMs?: number }>;
  hookErrors?: unknown[];
  preventedContinuation?: boolean;
}

/**
 * Last prompt record: Pointer to the most recent user prompt.
 */
export interface IClaudeLastPromptRecord {
  type: "last-prompt";
  leafUuid?: string;
  sessionId?: string;
  lastPrompt: string;
  timestamp?: string;
}

/**
 * AI Title record: Rolling high-level title synthesized for the session.
 * Supports canonical and alias property variations (`aiTitle`, `title`, `ai_title`).
 */
export interface IClaudeAiTitleRecord {
  type: "ai-title";
  aiTitle?: string;
  title?: string;
  ai_title?: string;
  sessionId: string;
}

/**
 * Permission mode record: Tracks changes in permission elevation.
 */
export interface IClaudePermissionModeRecord {
  type: "permission-mode";
  permissionMode: string;
  sessionId: string;
}

/**
 * File history snapshot record: Tracks file backups for undo operations.
 */
export interface IClaudeFileHistorySnapshotRecord {
  type: "file-history-snapshot";
  messageId: string;
  isSnapshotUpdate?: boolean;
  snapshot: {
    messageId?: string;
    trackedFileBackups: Record<
      string,
      {
        backupFileName: string | null;
        version: number;
        backupTime: string;
      }
    >;
    timestamp?: string;
  };
}

/**
 * File history delta record: Incremental file version tracking.
 */
export interface IClaudeFileHistoryDeltaRecord {
  type: "file-history-delta";
  messageId: string;
  snapshotMessageId: string;
  trackingPath: string;
  backup: unknown;
  timestamp: string;
}

/**
 * Queue operation record: Tracks mutation to the command queue.
 */
export interface IClaudeQueueOperationRecord {
  type: "queue-operation";
  operation: string;
  sessionId: string;
  timestamp: string;
  content?: string;
}

/**
 * Agent name record: Assigns human-readable name to subagents.
 * Supports canonical and alias property variations (`agentName`, `name`).
 */
export interface IClaudeAgentNameRecord {
  type: "agent-name";
  agentName?: string;
  name?: string;
  sessionId: string;
}

/**
 * PR Link record: Links the session to an active GitHub PR.
 */
export interface IClaudePrLinkRecord {
  type: "pr-link";
  prNumber: number;
  prRepository: string;
  prUrl: string;
  sessionId: string;
  timestamp: string;
}

/**
 * Worktree state record: Context when running inside disposable Git worktrees.
 */
export interface IClaudeWorktreeStateRecord {
  type: "worktree-state";
  sessionId: string;
  worktreeSession: {
    sessionId: string;
    worktreeName: string;
    worktreePath: string;
    worktreeBranch: string;
    originalBranch: string;
    originalCwd: string;
    originalHeadCommit: string;
  };
}

/**
 * Mode record: Operation mode changes.
 */
export interface IClaudeModeRecord {
  type: "mode";
  mode: string;
  sessionId: string;
}

/**
 * Agent setting record: Flag / configuration state changes.
 */
export interface IClaudeAgentSettingRecord {
  type: "agent-setting";
  agentSetting: string;
  sessionId: string;
}

/**
 * Started record: Subagent / task worker start marker.
 */
export interface IClaudeStartedRecord {
  type: "started";
  agentId: string;
  key: string;
}

/**
 * Result record: Subagent / task worker result marker.
 */
export interface IClaudeResultRecord {
  type: "result";
  agentId: string;
  key: string;
  result: unknown;
}

/**
 * Summary record: End-of-session compaction summary (legacy/export format).
 */
export interface IClaudeSummaryRecord {
  type: "summary";
  summary: string;
  leafUuid: string;
}

/**
 * Custom title record: User-assigned session name.
 */
export interface IClaudeCustomTitleRecord {
  type: "custom-title";
  sessionId: string;
  customTitle: string;
}

/**
 * Progress record: Streaming progress update during long tool or subagent tasks.
 */
export interface IClaudeProgressRecord extends Partial<IClaudeConversationalBase> {
  type: "progress";
  data: { type?: string; agentId?: string; message?: unknown };
  parentToolUseID?: string;
  toolUseID?: string;
}

/**
 * Fallback for unrecognised record types.
 */
export interface IClaudeUnknownRecord {
  type: string;
  [key: string]: unknown;
}

/**
 * Top-level discriminated union of all records that can appear in a Claude Code JSONL transcript.
 */
export type IClaudeLogEntry =
  | IClaudeAssistantRecord
  | IClaudeUserRecord
  | IClaudeAttachmentRecord
  | IClaudeSystemRecord
  | IClaudeLastPromptRecord
  | IClaudeAiTitleRecord
  | IClaudePermissionModeRecord
  | IClaudeFileHistorySnapshotRecord
  | IClaudeFileHistoryDeltaRecord
  | IClaudeQueueOperationRecord
  | IClaudeAgentNameRecord
  | IClaudePrLinkRecord
  | IClaudeWorktreeStateRecord
  | IClaudeModeRecord
  | IClaudeAgentSettingRecord
  | IClaudeStartedRecord
  | IClaudeResultRecord
  | IClaudeSummaryRecord
  | IClaudeCustomTitleRecord
  | IClaudeProgressRecord
  | IClaudeUnknownRecord;

// ============================================================================
// 9. Type Guards & Helper Utilities
// ============================================================================

export function isClaudeAssistantRecord(entry: IClaudeLogEntry): entry is IClaudeAssistantRecord {
  return entry.type === "assistant";
}

export function isClaudeUserRecord(entry: IClaudeLogEntry): entry is IClaudeUserRecord {
  return entry.type === "user";
}

export function isClaudeAttachmentRecord(entry: IClaudeLogEntry): entry is IClaudeAttachmentRecord {
  return entry.type === "attachment";
}

export function isClaudeSystemRecord(entry: IClaudeLogEntry): entry is IClaudeSystemRecord {
  return entry.type === "system";
}

export function isClaudeAiTitleRecord(entry: IClaudeLogEntry): entry is IClaudeAiTitleRecord {
  return entry.type === "ai-title";
}

export function isClaudeCustomTitleRecord(
  entry: IClaudeLogEntry,
): entry is IClaudeCustomTitleRecord {
  return entry.type === "custom-title";
}

export function isClaudeLastPromptRecord(entry: IClaudeLogEntry): entry is IClaudeLastPromptRecord {
  return entry.type === "last-prompt";
}

export function isClaudePermissionModeRecord(
  entry: IClaudeLogEntry,
): entry is IClaudePermissionModeRecord {
  return entry.type === "permission-mode";
}

export function isClaudeFileHistorySnapshotRecord(
  entry: IClaudeLogEntry,
): entry is IClaudeFileHistorySnapshotRecord {
  return entry.type === "file-history-snapshot";
}

export function isClaudeTextBlock(block: IClaudeContentBlock): block is IClaudeTextBlock {
  return block.type === "text";
}

export function isClaudeThinkingBlock(block: IClaudeContentBlock): block is IClaudeThinkingBlock {
  return block.type === "thinking";
}

export function isClaudeToolUseBlock(block: IClaudeContentBlock): block is IClaudeToolUseBlock {
  return block.type === "tool_use";
}

export function isClaudeToolResultBlock(
  block: IClaudeContentBlock,
): block is IClaudeToolResultBlock {
  return block.type === "tool_result";
}
