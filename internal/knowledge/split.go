package knowledge

import (
	"strings"
	"unicode"
)

func splitText(text string, maximumRunes int) []string {
	paragraphs := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	chunks := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		trimmed := strings.TrimSpace(paragraph)
		if trimmed == "" {
			continue
		}
		runes := []rune(trimmed)
		for len(runes) > maximumRunes {
			cut := maximumRunes
			for cut > maximumRunes/2 && !unicode.IsSpace(runes[cut-1]) {
				cut--
			}
			if cut == maximumRunes/2 {
				cut = maximumRunes
			}
			chunks = append(chunks, strings.TrimSpace(string(runes[:cut])))
			runes = runes[cut:]
		}
		if remainder := strings.TrimSpace(string(runes)); remainder != "" {
			chunks = append(chunks, remainder)
		}
	}
	return chunks
}
