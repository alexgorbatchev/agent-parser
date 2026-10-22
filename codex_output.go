package parser

import (
	"bytes"
	"encoding/json"
)

// Codex serializes tool outputs as text or content arrays. Preserve arrays as
// JSON in the normalized text so media and unknown blocks are not lost, and
// read their images.
type rawCodexToolOutput struct {
	text   string
	images []Image
}

func (o *rawCodexToolOutput) UnmarshalJSON(raw []byte) error {
	if !bytes.HasPrefix(raw, []byte("[")) {
		return json.Unmarshal(raw, &o.text)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return err
	}
	var items []interface{}
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	*o = rawCodexToolOutput{text: compact.String(), images: codexImages(items)}
	return nil
}
