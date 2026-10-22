package parser

import "strings"

const (
	imageBlockType        = "image" // Claude Code and Pi
	claudeBase64Source    = "base64"
	codexImageContentType = "input_image"
	dataURLPrefix         = "data:"
	dataURLBase64Marker   = ";base64,"
)

// rawClaudeImageSource is the `source` of a Claude Code image block.
type rawClaudeImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// imageFromURL splits a base64 data URL into inline data and keeps any other URL as a reference.
func imageFromURL(url string) Image {
	if rest, ok := strings.CutPrefix(url, dataURLPrefix); ok {
		if mediaType, data, ok := strings.Cut(rest, dataURLBase64Marker); ok {
			return Image{MediaType: mediaType, Data: data}
		}
	}
	return Image{URL: url}
}

func claudeImages(blocks []rawContentBlock) []Image {
	var images []Image
	for _, b := range blocks {
		if b.Type == imageBlockType && b.Source != nil && b.Source.Type == claudeBase64Source {
			images = append(images, Image{MediaType: b.Source.MediaType, Data: b.Source.Data})
		}
	}
	return images
}

// codexImages reads the `input_image` items of a Codex message content or tool output array.
func codexImages(content interface{}) []Image {
	items, ok := content.([]interface{})
	if !ok {
		return nil
	}
	var images []Image
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok || m["type"] != codexImageContentType {
			continue
		}
		if url, ok := m["image_url"].(string); ok && url != "" {
			images = append(images, imageFromURL(url))
		} else if fileID, ok := m["file_id"].(string); ok && fileID != "" {
			images = append(images, Image{FileID: fileID})
		}
	}
	return images
}

func piImages(blocks []rawPiContentBlock) []Image {
	var images []Image
	for _, b := range blocks {
		if b.Type == imageBlockType && b.Data != "" {
			images = append(images, Image{MediaType: b.MimeType, Data: b.Data})
		}
	}
	return images
}
