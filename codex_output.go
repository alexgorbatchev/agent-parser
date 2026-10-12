package parser

import (
	"bytes"
	"encoding/json"
)

// Codex serializes tool outputs as text or content arrays. Preserve arrays as
// JSON in the normalized string field so media and unknown blocks are not lost.
type rawCodexToolOutput string

func (o *rawCodexToolOutput) UnmarshalJSON(raw []byte) error {
	if bytes.HasPrefix(raw, []byte("[")) {
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return err
		}
		*o = rawCodexToolOutput(compact.String())
		return nil
	}
	return json.Unmarshal(raw, (*string)(o))
}
