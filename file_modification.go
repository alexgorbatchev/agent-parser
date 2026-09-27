package parser

import (
	"regexp"
	"strconv"
	"strings"
)

var hunkHeaderRegex = regexp.MustCompile(`@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// NormalizeFileModification parses harness-specific file operations into a canonical FileModification.
func NormalizeFileModification(toolName string, toolInput map[string]interface{}) *FileModification {
	if toolInput == nil {
		return nil
	}

	name := strings.ToLower(strings.TrimSpace(toolName))

	// Check for patch or diff payload first (e.g. Codex apply_patch)
	patchText := extractString(toolInput, "patch", "diff")
	if patchText != "" {
		filePath := extractFilePath(toolInput)
		return parseUnifiedPatch(patchText, filePath)
	}

	filePath := extractFilePath(toolInput)

	// Check write tools
	isWriteTool := name == "write" || name == "filewrite" || name == "write_file" || name == "writefile"
	hasEdits := hasEditKeys(toolInput)

	contentStr, hasContent := toolInput["content"].(string)
	if (isWriteTool || (filePath != "" && !hasEdits)) && hasContent {
		if filePath == "" {
			filePath = "untitled"
		}
		return &FileModification{
			Action:   "WRITE",
			FilePath: filePath,
			Content:  &contentStr,
		}
	}

	// Check for edits array (Pi edits or Claude Code MultiEdit)
	if editsRaw, ok := toolInput["edits"].([]interface{}); ok && len(editsRaw) > 0 {
		var hunks []DiffHunk
		for idx, editItem := range editsRaw {
			if editMap, ok := editItem.(map[string]interface{}); ok {
				oldStr := extractString(editMap, "oldText", "old_string", "old_str")
				newStr := extractString(editMap, "newText", "new_string", "new_str")
				var header string
				if len(editsRaw) > 1 {
					header = "Hunk " + strconv.Itoa(idx+1) + " of " + strconv.Itoa(len(editsRaw))
				}
				hunks = append(hunks, buildHunkFromStrings(oldStr, newStr, header))
			}
		}
		if len(hunks) > 0 {
			if filePath == "" {
				filePath = "untitled"
			}
			return &FileModification{
				Action:   "EDIT",
				FilePath: filePath,
				Hunks:    hunks,
			}
		}
	}

	// Check for single old/new replacement (Claude Code Edit / str_replace_editor)
	oldStr := extractString(toolInput, "old_string", "oldText", "old_str")
	newStr := extractString(toolInput, "new_string", "newText", "new_str")
	if oldStr != "" || newStr != "" {
		if filePath == "" {
			filePath = "untitled"
		}
		return &FileModification{
			Action:   "EDIT",
			FilePath: filePath,
			Hunks:    []DiffHunk{buildHunkFromStrings(oldStr, newStr, "")},
		}
	}

	return nil
}

func extractFilePath(input map[string]interface{}) string {
	keys := []string{"file_path", "path", "filePath", "file", "target"}
	for _, k := range keys {
		if val, ok := input[k]; ok && val != nil {
			if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func hasEditKeys(input map[string]interface{}) bool {
	if _, ok := input["edits"]; ok {
		return true
	}
	for _, k := range []string{"old_string", "oldText", "old_str", "new_string", "newText", "new_str", "patch", "diff"} {
		if s, ok := input[k].(string); ok && s != "" {
			return true
		}
	}
	return false
}

func extractString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if val, ok := m[k]; ok && val != nil {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func buildHunkFromStrings(oldStr, newStr, header string) DiffHunk {
	var lines []DiffLine
	oldLineNum := 1
	newLineNum := 1

	if oldStr != "" {
		for _, line := range splitLines(oldStr) {
			num := oldLineNum
			lines = append(lines, DiffLine{
				Type:          "del",
				OldLineNumber: &num,
				Content:       line,
			})
			oldLineNum++
		}
	}

	if newStr != "" {
		for _, line := range splitLines(newStr) {
			num := newLineNum
			lines = append(lines, DiffLine{
				Type:          "add",
				NewLineNumber: &num,
				Content:       line,
			})
			newLineNum++
		}
	}

	return DiffHunk{
		Header: header,
		Lines:  lines,
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func parseUnifiedPatch(patchText string, fallbackPath string) *FileModification {
	lines := splitLines(patchText)
	filePath := fallbackPath
	var hunks []DiffHunk
	var currentHunk *DiffHunk
	oldLine := 1
	newLine := 1

	for _, rawLine := range lines {
		if strings.HasPrefix(rawLine, "diff --git ") {
			parts := strings.Fields(rawLine)
			if len(parts) >= 4 {
				bPart := parts[3]
				filePath = strings.TrimPrefix(bPart, "b/")
			}
			continue
		}
		if strings.HasPrefix(rawLine, "+++ b/") {
			filePath = strings.TrimSpace(strings.TrimPrefix(rawLine, "+++ b/"))
			continue
		}
		if strings.HasPrefix(rawLine, "--- ") {
			continue
		}
		if strings.HasPrefix(rawLine, "@@ ") {
			if currentHunk != nil {
				hunks = append(hunks, *currentHunk)
			}
			header := strings.TrimSpace(rawLine)
			currentHunk = &DiffHunk{
				Header: header,
				Lines:  []DiffLine{},
			}
			matches := hunkHeaderRegex.FindStringSubmatch(rawLine)
			if len(matches) >= 3 {
				if o, err := strconv.Atoi(matches[1]); err == nil {
					oldLine = o
				}
				if n, err := strconv.Atoi(matches[2]); err == nil {
					newLine = n
				}
			}
			continue
		}

		if currentHunk == nil {
			continue
		}

		if strings.HasPrefix(rawLine, "-") {
			num := oldLine
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:          "del",
				OldLineNumber: &num,
				Content:       rawLine[1:],
			})
			oldLine++
		} else if strings.HasPrefix(rawLine, "+") {
			num := newLine
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:          "add",
				NewLineNumber: &num,
				Content:       rawLine[1:],
			})
			newLine++
		} else if strings.HasPrefix(rawLine, " ") || rawLine == "" {
			var content string
			if len(rawLine) > 0 {
				content = rawLine[1:]
			}
			oNum := oldLine
			nNum := newLine
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:          "ctx",
				OldLineNumber: &oNum,
				NewLineNumber: &nNum,
				Content:       content,
			})
			oldLine++
			newLine++
		}
	}

	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	if filePath == "" {
		filePath = "untitled"
	}

	return &FileModification{
		Action:   "EDIT",
		FilePath: filePath,
		Hunks:    hunks,
	}
}
