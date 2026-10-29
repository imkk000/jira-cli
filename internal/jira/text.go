package jira

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Text string

func (t *Text) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*t = Text(s)
		return nil
	}
	var doc adfNode
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("decode description: %w", err)
	}
	*t = Text(strings.TrimSpace(string(doc.appendText(nil))))
	return nil
}

type adfNode struct {
	Type    string    `json:"type"`
	Text    string    `json:"text"`
	Content []adfNode `json:"content"`
}

func (n adfNode) appendText(dst []byte) []byte {
	switch n.Type {
	case "text":
		dst = append(dst, n.Text...)
	case "hardBreak":
		dst = append(dst, '\n')
	case "listItem":
		dst = append(dst, "- "...)
	}
	for _, child := range n.Content {
		dst = child.appendText(dst)
	}
	switch n.Type {
	case "paragraph", "heading", "codeBlock", "blockquote", "rule":
		dst = append(dst, '\n')
	}
	return dst
}
