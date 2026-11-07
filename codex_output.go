package parser

import (
	"bytes"
	"encoding/json"
)

// Codex serializes tool outputs as text or content arrays. Preserve arrays as
// JSON in the normalized text so media and unknown blocks are not lost, and
// read their images. head is the text Codex writes its status header into: the
// whole text output, or the first text item of a content array.
type rawCodexToolOutput struct {
	text   string
	head   string
	images []Image
}

func (o *rawCodexToolOutput) UnmarshalJSON(raw []byte) error {
	if !bytes.HasPrefix(raw, []byte("[")) {
		if err := json.Unmarshal(raw, &o.text); err != nil {
			return err
		}
		o.head = o.text
		return nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return err
	}
	var items []interface{}
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	*o = rawCodexToolOutput{text: compact.String(), head: firstCodexText(items), images: codexImages(items)}
	return nil
}

// firstCodexText returns the text of a content array's first item when that
// item is text.
func firstCodexText(items []interface{}) string {
	if len(items) == 0 {
		return ""
	}
	item, ok := items[0].(map[string]interface{})
	if !ok {
		return ""
	}
	if kind, _ := item["type"].(string); kind != "input_text" && kind != "output_text" {
		return ""
	}
	text, _ := item["text"].(string)
	return text
}
