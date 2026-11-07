package parser

import (
	"encoding/json"
	"strconv"
	"strings"
)

// codexToolFailed reads whether a Codex tool call failed from the status header
// Codex writes at the start of its output. Codex does not persist a success
// flag in rollouts, so the header is the only record of the outcome. It
// returns nil when the output records no outcome: the call is still running,
// was aborted, or its handler wrote no header (such as MCP results and handler
// error messages).
func codexToolFailed(head string) *bool {
	firstLine, _, _ := strings.Cut(head, "\n")

	// Code-mode exec/wait (core/src/tools/code_mode/mod.rs format_script_status).
	switch {
	case strings.HasPrefix(firstLine, "Script completed"):
		return boolPtr(false)
	case strings.HasPrefix(firstLine, "Script failed"), strings.HasPrefix(firstLine, "Script terminated"):
		return boolPtr(true)
	case strings.HasPrefix(firstLine, "Script running with cell ID"):
		return nil
	}

	// Shell, shell_command and apply_patch (core/src/tools/mod.rs
	// format_exec_output_for_model).
	if code, ok := strings.CutPrefix(firstLine, "Exit code: "); ok {
		return exitCodeFailed(code)
	}

	// Unified exec_command/write_stdin (core/src/tools/context.rs
	// response_header): header lines up to "Output:".
	if strings.HasPrefix(firstLine, "Chunk ID: ") || strings.HasPrefix(firstLine, "Wall time: ") {
		for _, line := range strings.Split(head, "\n") {
			if line == "Output:" {
				break
			}
			if code, ok := strings.CutPrefix(line, "Process exited with code "); ok {
				return exitCodeFailed(code)
			}
		}
		return nil
	}

	// Older rollouts wrote shell output as JSON with exit_code metadata.
	if strings.HasPrefix(head, "{") {
		var legacy struct {
			Metadata *struct {
				ExitCode *int `json:"exit_code"`
			} `json:"metadata"`
		}
		if json.Unmarshal([]byte(head), &legacy) == nil && legacy.Metadata != nil && legacy.Metadata.ExitCode != nil {
			return boolPtr(*legacy.Metadata.ExitCode != 0)
		}
	}
	return nil
}

func exitCodeFailed(code string) *bool {
	n, err := strconv.Atoi(strings.TrimSpace(code))
	if err != nil {
		return nil
	}
	return boolPtr(n != 0)
}
