package parser

import (
	"testing"
)

func TestNormalizeFileModification_NonFileTools_ReturnNil(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		toolInput map[string]interface{}
	}{
		{
			name:      "nil toolInput",
			toolName:  "write",
			toolInput: nil,
		},
		{
			name:      "empty map",
			toolName:  "write",
			toolInput: map[string]interface{}{},
		},
		{
			name:     "Bash command",
			toolName: "Bash",
			toolInput: map[string]interface{}{
				"command": "echo hello",
			},
		},
		{
			name:     "AskUserQuestion",
			toolName: "AskUserQuestion",
			toolInput: map[string]interface{}{
				"question": "Do you want to proceed?",
			},
		},
		{
			name:     "post_message with content",
			toolName: "post_message",
			toolInput: map[string]interface{}{
				"content": "Build succeeded",
			},
		},
		{
			name:     "echo with content",
			toolName: "echo",
			toolInput: map[string]interface{}{
				"content": "some text",
			},
		},
		{
			name:     "comment with content",
			toolName: "comment",
			toolInput: map[string]interface{}{
				"content": "Looks good to me!",
			},
		},
		{
			name:     "read with path but no content or edits",
			toolName: "read",
			toolInput: map[string]interface{}{
				"path": "main.go",
			},
		},
		{
			name:     "grep with pattern and path",
			toolName: "grep",
			toolInput: map[string]interface{}{
				"path":    "src/",
				"pattern": "foo.*bar",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeFileModification(tt.toolName, tt.toolInput)
			if got != nil {
				t.Errorf("NormalizeFileModification(%q, %v) = %+v, want nil", tt.toolName, tt.toolInput, got)
			}
		})
	}
}

func TestNormalizeFileModification_PositiveCases(t *testing.T) {
	t.Run("explicit write tool without path defaults to untitled", func(t *testing.T) {
		got := NormalizeFileModification("write", map[string]interface{}{
			"content": "hello world\n",
		})
		if got == nil {
			t.Fatal("expected non-nil FileModification")
		}
		if got.Action != "WRITE" {
			t.Errorf("got action %q, want WRITE", got.Action)
		}
		if got.FilePath != "untitled" {
			t.Errorf("got filePath %q, want 'untitled'", got.FilePath)
		}
		if got.Content == nil || *got.Content != "hello world\n" {
			t.Errorf("got content %v, want 'hello world\\n'", got.Content)
		}
	})

	t.Run("write tool with file_path", func(t *testing.T) {
		got := NormalizeFileModification("FileWrite", map[string]interface{}{
			"file_path": "src/app.go",
			"content":   "package main\n",
		})
		if got == nil {
			t.Fatal("expected non-nil FileModification")
		}
		if got.Action != "WRITE" || got.FilePath != "src/app.go" {
			t.Errorf("unexpected FileModification: %+v", got)
		}
	})

	t.Run("generic tool with path and content is classified as write", func(t *testing.T) {
		got := NormalizeFileModification("save", map[string]interface{}{
			"path":    "docs/readme.md",
			"content": "# Docs\n",
		})
		if got == nil {
			t.Fatal("expected non-nil FileModification")
		}
		if got.Action != "WRITE" || got.FilePath != "docs/readme.md" {
			t.Errorf("unexpected FileModification: %+v", got)
		}
	})

	t.Run("single replacement edit", func(t *testing.T) {
		got := NormalizeFileModification("Edit", map[string]interface{}{
			"file_path":  "config.json",
			"old_string": `"port": 8080`,
			"new_string": `"port": 9090`,
		})
		if got == nil {
			t.Fatal("expected non-nil FileModification")
		}
		if got.Action != "EDIT" || got.FilePath != "config.json" {
			t.Errorf("unexpected FileModification: %+v", got)
		}
		if len(got.Hunks) != 1 {
			t.Fatalf("expected 1 hunk, got %d", len(got.Hunks))
		}
	})

	t.Run("multi-hunk edits array", func(t *testing.T) {
		got := NormalizeFileModification("edit", map[string]interface{}{
			"path": "server.ts",
			"edits": []interface{}{
				map[string]interface{}{
					"oldText": "const a = 1;",
					"newText": "const a = 2;",
				},
				map[string]interface{}{
					"oldText": "const b = 1;",
					"newText": "const b = 2;",
				},
			},
		})
		if got == nil {
			t.Fatal("expected non-nil FileModification")
		}
		if got.Action != "EDIT" || got.FilePath != "server.ts" {
			t.Errorf("unexpected FileModification: %+v", got)
		}
		if len(got.Hunks) != 2 {
			t.Fatalf("expected 2 hunks, got %d", len(got.Hunks))
		}
	})

	t.Run("unified patch", func(t *testing.T) {
		patch := "--- a/file.txt\n+++ b/file.txt\n@@ -1,2 +1,2 @@\n-old line\n+new line\n"
		got := NormalizeFileModification("apply_patch", map[string]interface{}{
			"path":  "file.txt",
			"patch": patch,
		})
		if got == nil {
			t.Fatal("expected non-nil FileModification")
		}
		if got.Action != "EDIT" || got.FilePath != "file.txt" {
			t.Errorf("unexpected FileModification: %+v", got)
		}
		if len(got.Hunks) != 1 {
			t.Fatalf("expected 1 hunk, got %d", len(got.Hunks))
		}
	})
}
