package parser

import (
	"reflect"
	"testing"
)

func TestParsedImages(t *testing.T) {
	parseClaude := func(line []byte) ([]ParsedEvent, error) { return NewClaudeCodeParser().ParseLine(line) }
	parseCodex := func(line []byte) ([]ParsedEvent, error) { return NewCodexParser().ParseLine(line) }
	parsePi := func(line []byte) ([]ParsedEvent, error) { return ParsePiLine(line, "/cwd") }
	png := Image{MediaType: "image/png", Data: "iVBORw0KGgo="}

	tests := []struct {
		name       string
		parse      func([]byte) ([]ParsedEvent, error)
		line       string
		eventType  string
		wantPrompt string
		want       []Image
	}{
		{
			name:       "claude prompt with text and image",
			parse:      parseClaude,
			line:       `{"type":"user","uuid":"u-1","timestamp":"2026-10-22T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"What is wrong here?"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}}`,
			eventType:  "turn_start",
			wantPrompt: "What is wrong here?",
			want:       []Image{png},
		},
		{
			name:      "claude prompt with only an image",
			parse:     parseClaude,
			line:      `{"type":"user","uuid":"u-2","timestamp":"2026-10-22T10:00:00.000Z","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"/9j/4AAQ"}}]}}`,
			eventType: "turn_start",
			want:      []Image{{MediaType: "image/jpeg", Data: "/9j/4AAQ"}},
		},
		{
			name:      "claude tool result with a screenshot",
			parse:     parseClaude,
			line:      `{"type":"user","uuid":"u-3","timestamp":"2026-10-22T10:00:00.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"captured"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}}`,
			eventType: "tool_result",
			want:      []Image{png},
		},
		{
			name:       "codex prompt with an inline image",
			parse:      parseCodex,
			line:       `{"timestamp":"2026-10-22T10:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"What is wrong here?"},{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo=","detail":"high"}]}}`,
			eventType:  "turn_start",
			wantPrompt: "What is wrong here?",
			want:       []Image{png},
		},
		{
			name:       "codex prompt with an uploaded file and a remote image",
			parse:      parseCodex,
			line:       `{"timestamp":"2026-10-22T10:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Compare these"},{"type":"input_image","file_id":"file-1"},{"type":"input_image","image_url":"https://example.com/a.png"}]}}`,
			eventType:  "turn_start",
			wantPrompt: "Compare these",
			want:       []Image{{FileID: "file-1"}, {URL: "https://example.com/a.png"}},
		},
		{
			name:      "codex prompt with only an image",
			parse:     parseCodex,
			line:      `{"timestamp":"2026-10-22T10:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo="}]}}`,
			eventType: "turn_start",
			want:      []Image{png},
		},
		{
			name:      "codex tool output with a screenshot",
			parse:     parseCodex,
			line:      `{"timestamp":"2026-10-22T10:00:00.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-1","output":[{"type":"input_text","text":"Script completed"},{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo=","detail":"original"}]}}`,
			eventType: "tool_result",
			want:      []Image{png},
		},
		{
			name:       "pi prompt with an image",
			parse:      parsePi,
			line:       `{"type":"message","timestamp":"2026-10-22T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"What is wrong here?"},{"type":"image","data":"iVBORw0KGgo=","mimeType":"image/png"}]}}`,
			eventType:  "turn_start",
			wantPrompt: "What is wrong here?",
			want:       []Image{png},
		},
		{
			name:      "pi tool result with a screenshot",
			parse:     parsePi,
			line:      `{"type":"message","timestamp":"2026-10-22T10:00:00.000Z","message":{"role":"toolResult","toolCallId":"call-1","toolName":"screenshot","content":[{"type":"text","text":"captured"},{"type":"image","data":"iVBORw0KGgo=","mimeType":"image/png"}]}}`,
			eventType: "tool_result",
			want:      []Image{png},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := tt.parse([]byte(tt.line))
			if err != nil {
				t.Fatalf("parse error = %v", err)
			}
			var found *ParsedEvent
			for i := range events {
				if events[i].EventType == tt.eventType {
					found = &events[i]
				}
			}
			if found == nil {
				t.Fatalf("events = %+v, want a %s event", events, tt.eventType)
			}
			if !reflect.DeepEqual(found.Data.Images, tt.want) {
				t.Fatalf("images = %+v, want %+v", found.Data.Images, tt.want)
			}
			if tt.eventType == "turn_start" && (found.Data.Content == nil || *found.Data.Content != tt.wantPrompt) {
				t.Fatalf("prompt = %v, want %q", found.Data.Content, tt.wantPrompt)
			}
		})
	}
}

func TestParsedImagesAbsentWithoutImages(t *testing.T) {
	events, err := NewClaudeCodeParser().ParseLine([]byte(`{"type":"user","uuid":"u-1","timestamp":"2026-10-22T10:00:00.000Z","message":{"role":"user","content":"Plain prompt"}}`))
	if err != nil {
		t.Fatalf("parse error = %v", err)
	}
	for _, event := range events {
		if event.Data.Images != nil {
			t.Fatalf("%s images = %+v, want none", event.EventType, event.Data.Images)
		}
	}
}

func TestImageFromURL(t *testing.T) {
	tests := []struct {
		name, url string
		want      Image
	}{
		{"base64 data URL", "data:image/png;base64,iVBORw0KGgo=", Image{MediaType: "image/png", Data: "iVBORw0KGgo="}},
		{"remote URL", "https://example.com/a.png", Image{URL: "https://example.com/a.png"}},
		{"data URL without base64", "data:image/svg+xml,%3Csvg%2F%3E", Image{URL: "data:image/svg+xml,%3Csvg%2F%3E"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageFromURL(tt.url); got != tt.want {
				t.Fatalf("imageFromURL(%q) = %+v, want %+v", tt.url, got, tt.want)
			}
		})
	}
}
