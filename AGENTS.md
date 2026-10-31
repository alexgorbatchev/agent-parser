# agent-parser

Pure Go streaming log parsers, database readers, and canonical specifications for AI coding agent harnesses (Claude Code, Pi, OpenAI Codex, OpenCode).

## Shared commands

- Run all tests: `just test`
- Enforce 90% statement coverage: `just coverage`
- Typecheck canonical harness schemas: `just typecheck`
- Lint and verify module hygiene: `just lint`
- Run all checks (lint, coverage, typecheck): `just check`
- Format Go source: `just fmt`

## Key gotchas
- **Native recaps and async questions**: Claude Code `system/away_summary` emits `summary` with `Data.Content`. Codex completed `AgentMessage` items emit conversational `agent_message` or `question_prompt` when structured questions are present; preserve question tool arguments and call IDs.

- **Images**: Prompt and tool-result images go in `EventData.Images`; text fields keep only text. Codex tool outputs are the exception: `ToolOutput` keeps the raw content array, image data included, so media and unknown blocks are not lost.
- **Faithful parsing**: Parsers decode logs faithfully without external model catalog lookups or synthetic metrics (such as context window fullness calculations); downstream consumers handle catalog enrichment.
- **Pure Go SQLite**: OpenCode SQLite reading uses `modernc.org/sqlite`, registered in `opencode.go` and used through `database/sql` as driver `sqlite`. It needs no CGo or local SQLite C libraries, so consumers can cross-compile with `CGO_ENABLED=0`.
- **Read-only DB access**: OpenCode database handles must strictly be opened in read-only mode (`?mode=ro`) to prevent write locks or interfering with running agents.
- **Claude Code multi-block deduplication**: Claude Code streams assistant content across separate lines sharing the same `requestId`. Parsers must deduplicate usage to avoid multiplying token metrics.
- **Schema sync contract**: All Go types and parsing logic must match the canonical TypeScript definitions in `docs/harness-logs/` (`claude-code.ts`, `pi.ts`, `codex.ts`, `opencode.ts`).

## Boundaries

- **Always**: automatically record all new user instructions in `AGENTS.md` immediately upon receipt (check with user if existing instructions conflict).
- **Always**: any time code is changed such that results from running that code are changed, a test file must be changed as well; 90% code coverage is required.
- **Always**: run `just check` before committing changes.
- **Never**: publish releases, tags, packages, or production deployments automatically without explicit user authorization.
- **Never**: perform external model catalog lookups or synthetic fullness calculations inside parser loops.
- **Never**: add CGo dependencies.

## References

- Canonical harness schemas: `docs/harness-logs/`
- Core types: `types.go`
- Claude Code parser: `claudecode.go`
- Pi parser: `pi.go`
- Codex parser: `codex.go`
- OpenCode parser: `opencode.go`
- File diff normalizer: `file_modification.go`
