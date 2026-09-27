// Package parser provides streaming log parsers and database readers for AI agent harnesses (Claude Code, Pi, OpenAI Codex, OpenCode).
//
// Attributions & Prior Art:
//   - Claude Code JSONL log parsing models and buffer sizing are derived from and inspired by:
//     Chitragupt (https://github.com/sonnes/chitragupt) - reader/claude/claude.go
//     OpenUsage (https://github.com/janekbaraniewski/openusage) - internal/providers/claude_code/
//     Claude Agent SDK Golang (https://github.com/ProjAnvil/claude-agent-sdk-golang)
//   - Pi session discovery, tree structure, and log models are derived from and inspired by:
//     Nick Nisi Sessions (https://github.com/nicknisi/sessions) - src/pi-tree.ts
//     OpenUsage (https://github.com/janekbaraniewski/openusage) - internal/providers/pi/session.go
//   - OpenAI Codex CLI transcript rollout parser is derived from and inspired by:
//     AgentHUD (https://github.com/neochoon/agenthud) - docs/schemas/codex-session.md
//     Codex Transcripts (https://github.com/prateek/codex-transcripts) - codex_transcripts.py
//     Claude Code Session Viewer (https://github.com/eric050828/claude-code-session-viewer) - lib/codex-parser.ts
//     Sessions (https://github.com/nicknisi/sessions) - src/codex.ts
//   - OpenCode SQLite storage reader, schema mappings, and turn reconstruction are derived from and inspired by:
//     OpenCode Storage Engine (https://github.com/anomalyco/opencode) - packages/opencode/src/storage/db.ts
//     agenthud (https://github.com/neochoon/agenthud) - src/data/providers/opencode.ts
//     opencode-db-search (https://github.com/rmk40/opencode-db-search) - src/db.ts
//     Nick Nisi Sessions (https://github.com/nicknisi/sessions) - src/opencode.ts
package parser
