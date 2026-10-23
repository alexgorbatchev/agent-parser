# Live harness streams

Use `NewLiveParser("claude")` for `claude -p --output-format stream-json --verbose --include-partial-messages`, or `NewLiveParser("codex")` for `codex exec --json`. Feed stdout lines to `ParseLine`; this API is separate from persisted transcript parsing.

Complete message events use `Data.Content`. Native delta events use `Data.Delta`; consumers must not concatenate complete snapshots with deltas. Tool events retain their native payload in `Data.Native` where present. Unknown native event types are ignored; malformed known payloads return an error.

`Result()` returns the latest terminal `LiveResult` and whether one has arrived. It retains text presence, JSON structured output (including JSON null), native model/session metadata and reported usage. Usage snapshots replace earlier snapshots. Read the result after draining stdout and waiting for the subprocess: a native terminal record alone does not establish process success. The caller owns cancellation, exit status, schema validation and its own terminal event.

Terminal normalized `run_result` events carry `Data.Result`. Results returned to callers are independent copies. Native error messages remain parser data; callers decide what diagnostic information to expose.
