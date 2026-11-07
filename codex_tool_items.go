package parser

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// rawCodexToolItem holds the fields of the completed tool items Codex records
// in paginated history (codex-rs/protocol/src/items.rs TurnItem). Tool calls
// made from code-mode scripts are recorded only as these items; tool calls
// recorded as response items repeat as an item with the call's ID.
type rawCodexToolItem struct {
	ID     string `json:"id"`
	Status string `json:"status"`

	// CommandExecution
	Command          []string `json:"command"`
	CWD              string   `json:"cwd"`
	ExitCode         *int     `json:"exit_code"`
	AggregatedOutput *string  `json:"aggregated_output"`

	// McpToolCall and DynamicToolCall
	Server       string          `json:"server"`
	Tool         string          `json:"tool"`
	Arguments    json.RawMessage `json:"arguments"`
	Result       json.RawMessage `json:"result"`
	Error        json.RawMessage `json:"error"`
	ContentItems json.RawMessage `json:"content_items"`
	Success      *bool           `json:"success"`

	// WebSearch and Extension
	Kind    string          `json:"kind"`
	Query   string          `json:"query"`
	Action  json.RawMessage `json:"action"`
	Results json.RawMessage `json:"results"`
	Failure json.RawMessage `json:"failure"`

	// ImageView
	Path string `json:"path"`
}

// parseCodexToolItem reports a completed tool item as a tool_call at the
// item's start and a tool_result at its completion. Items exist only in
// paginated history; the stateful parser skips items that repeat a tool call
// it already reported from a response item.
func parseCodexToolItem(kind string, raw json.RawMessage, ts int64, startedAt *int64, p *CodexParser) ([]ParsedEvent, error) {
	if !p.isPaginated() {
		return nil, nil
	}
	var item rawCodexToolItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("unmarshaling completed %s: %w", kind, err)
	}
	if p.isRecordedCall(item.ID) {
		return nil, nil
	}

	name, input, output, failed := codexToolItemDetails(kind, item)
	callTS := ts
	if startedAt != nil && *startedAt > 0 {
		callTS = *startedAt
	}
	callID := optionalString(item.ID)
	return []ParsedEvent{
		{
			EventType: "tool_call",
			Timestamp: callTS,
			Data: EventData{
				Role:       stringPtr("assistant"),
				ToolName:   stringPtr(name),
				ToolCallID: callID,
				ToolInput:  input,
			},
		},
		{
			EventType: "tool_result",
			Timestamp: ts,
			Data: EventData{
				Role:       stringPtr("toolResult"),
				ToolCallID: callID,
				ToolOutput: output,
				IsError:    failed,
			},
		},
	}, nil
}

func codexToolItemDetails(kind string, item rawCodexToolItem) (name string, input map[string]interface{}, output *string, failed *bool) {
	switch kind {
	case "CommandExecution":
		input = map[string]interface{}{"command": codexShellScript(item.Command), "argv": item.Command}
		if cwd := codexItemPath(item.CWD); cwd != "" {
			input["cwd"] = cwd
		}
		return "exec_command", input, item.AggregatedOutput, codexCommandFailed(item)
	case "McpToolCall":
		failed = codexStatusFailed(item.Status)
		if failed != nil && !*failed && (codexMCPResultIsError(item.Result) || codexJSONPresent(item.Error)) {
			failed = boolPtr(true)
		}
		output = codexJSONText(item.Result)
		if output == nil {
			output = codexJSONText(item.Error)
		}
		return "mcp__" + item.Server + "__" + item.Tool, codexJSONObject(item.Arguments), output, failed
	case "DynamicToolCall":
		if item.Success != nil {
			failed = boolPtr(!*item.Success)
		} else {
			failed = codexStatusFailed(item.Status)
		}
		return item.Tool, codexJSONObject(item.Arguments), codexJSONText(item.ContentItems), failed
	case "WebSearch":
		return "web_search", codexSearchInput(item), nil, nil
	case "Extension":
		input = codexSearchInput(item)
		input["kind"] = item.Kind
		failed = codexStatusFailed(item.Status)
		if codexJSONPresent(item.Failure) {
			failed = boolPtr(true)
		}
		output = codexJSONText(item.Results)
		if output == nil {
			output = codexJSONText(item.Result)
		}
		return item.Kind, input, output, failed
	case "ImageView":
		return "view_image", map[string]interface{}{"path": codexItemPath(item.Path)}, nil, nil
	}
	return kind, nil, nil, nil
}

// codexCommandFailed reads a command's outcome from its exit code, or from its
// status when it has none.
func codexCommandFailed(item rawCodexToolItem) *bool {
	if item.ExitCode != nil {
		return boolPtr(*item.ExitCode != 0)
	}
	return codexStatusFailed(item.Status)
}

// codexStatusFailed maps a completed item's status: "completed" succeeded,
// "failed" and "declined" failed, anything else is unknown.
func codexStatusFailed(status string) *bool {
	switch status {
	case "completed":
		return boolPtr(false)
	case "failed", "declined":
		return boolPtr(true)
	}
	return nil
}

func codexMCPResultIsError(result json.RawMessage) bool {
	var r struct {
		IsError bool `json:"isError"`
	}
	return json.Unmarshal(result, &r) == nil && r.IsError
}

func codexSearchInput(item rawCodexToolItem) map[string]interface{} {
	input := map[string]interface{}{}
	if item.Query != "" {
		input["query"] = item.Query
	}
	if action := codexJSONObject(item.Action); action != nil {
		input["action"] = action
	}
	return input
}

// codexShellScript returns the script of a `shell -lc script` argv, or the
// argv joined with spaces.
func codexShellScript(argv []string) string {
	if len(argv) >= 3 && (argv[1] == "-lc" || argv[1] == "-c") {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

// codexItemPath turns the file URL Codex writes for item paths into a path.
func codexItemPath(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Scheme == "file" {
		return u.Path
	}
	return raw
}

func codexJSONPresent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

func codexJSONText(raw json.RawMessage) *string {
	if !codexJSONPresent(raw) {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return &text
	}
	compact := strings.TrimSpace(string(raw))
	return &compact
}

func codexJSONObject(raw json.RawMessage) map[string]interface{} {
	var obj map[string]interface{}
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	return obj
}
