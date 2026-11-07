package parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
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
	Source           string   `json:"source"`
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

	// fields holds the item's raw fields, an extension's input.
	fields map[string]interface{}
}

// parseCodexToolItem reports a completed tool item as a tool_call at the
// item's start and a tool_result at its completion, plus the skills a command
// read. Tool events come only from the stateful parser on paginated history,
// the only history with items. An item that repeats a tool call already recorded as a
// response item emits nothing; its outcome goes to that call's result, which
// Codex writes after the item.
func parseCodexToolItem(kind string, raw json.RawMessage, ts int64, startedAt *int64, p *CodexParser) ([]ParsedEvent, error) {
	var item rawCodexToolItem
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("unmarshaling completed %s: %w", kind, err)
	}
	if err := json.Unmarshal(raw, &item.fields); err != nil {
		return nil, fmt.Errorf("unmarshaling completed %s: %w", kind, err)
	}
	var skills []ParsedEvent
	if kind == "CommandExecution" {
		skills = codexCommandSkills(item, ts)
	}
	if !p.isPaginated() {
		return skills, nil
	}
	if p.isRecordedCall(item.ID) {
		p.recordCallOutcome(item.ID, codexItemOutcome(kind, item))
		return skills, nil
	}
	// Collaboration items only repeat collaboration tool calls, and a
	// user_shell command is one the user ran with !, not an assistant call.
	if kind == "CollabAgentToolCall" || item.Source == "user_shell" {
		return skills, nil
	}

	name, input, output, images := codexToolItemDetails(kind, item)
	callTS := ts
	if startedAt != nil && *startedAt > 0 {
		callTS = *startedAt
	}
	callID := optionalString(item.ID)
	events := []ParsedEvent{
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
				Images:     images,
				IsError:    codexItemOutcome(kind, item),
			},
		},
	}
	return append(events, skills...), nil
}

func codexToolItemDetails(kind string, item rawCodexToolItem) (name string, input map[string]interface{}, output *string, images []Image) {
	switch kind {
	case "CommandExecution":
		input = map[string]interface{}{"command": codexShellScript(item.Command), "argv": item.Command}
		if cwd := codexItemPath(item.CWD); cwd != "" {
			input["cwd"] = cwd
		}
		return "exec_command", input, item.AggregatedOutput, nil
	case "McpToolCall":
		output = codexJSONText(item.Result)
		if output == nil {
			output = codexJSONText(item.Error)
		}
		return "mcp__" + item.Server + "__" + item.Tool, codexJSONObject(item.Arguments), output, nil
	case "DynamicToolCall":
		return item.Tool, codexJSONObject(item.Arguments), codexJSONText(item.ContentItems), nil
	case "WebSearch":
		input = map[string]interface{}{"query": item.Query}
		if action := codexJSONObject(item.Action); action != nil {
			input["action"] = action
		}
		return "web_search", input, nil, nil
	case "Extension":
		return item.Kind, codexExtensionInput(item.fields), codexJSONText(item.Results), codexExtensionImages(item)
	case "ImageView":
		return "view_image", map[string]interface{}{"path": codexItemPath(item.Path)}, nil, nil
	}
	return kind, nil, nil, nil
}

// codexItemOutcome reads whether a completed item failed: from a command's
// exit code, an MCP result's isError or error, an extension's failure, a
// dynamic tool's success, or the item status. It is nil when the item
// records no outcome.
func codexItemOutcome(kind string, item rawCodexToolItem) *bool {
	switch kind {
	case "CommandExecution":
		if item.ExitCode != nil {
			return boolPtr(*item.ExitCode != 0)
		}
	case "McpToolCall":
		if codexMCPResultIsError(item.Result) || codexJSONPresent(item.Error) {
			return boolPtr(true)
		}
	case "Extension":
		if codexJSONPresent(item.Failure) {
			return boolPtr(true)
		}
	case "DynamicToolCall":
		if item.Success != nil {
			return boolPtr(!*item.Success)
		}
	}
	return codexStatusFailed(item.Status)
}

// codexExtensionFields are an extension item's identity and outcome fields;
// the rest describe the call.
var codexExtensionFields = map[string]bool{
	"type": true, "id": true, "kind": true, "status": true,
	"result": true, "results": true, "failure": true,
}

func codexExtensionInput(fields map[string]interface{}) map[string]interface{} {
	input := map[string]interface{}{}
	for k, v := range fields {
		if !codexExtensionFields[k] && v != nil {
			input[k] = v
		}
	}
	return input
}

// codexExtensionImages returns the image an image generation extension
// produced: its result is the image as base64.
func codexExtensionImages(item rawCodexToolItem) []Image {
	if !strings.HasPrefix(item.Kind, "image_gen.") {
		return nil
	}
	var data string
	if json.Unmarshal(item.Result, &data) != nil || data == "" {
		return nil
	}
	head, err := base64.StdEncoding.DecodeString(data[:min(len(data), 64)&^3])
	if err != nil {
		return nil
	}
	return []Image{{MediaType: http.DetectContentType(head), Data: data}}
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
