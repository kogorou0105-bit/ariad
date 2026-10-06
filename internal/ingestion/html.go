package ingestion

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

var discardedElements = map[string]struct{}{
	"head": {}, "script": {}, "style": {}, "noscript": {},
	"nav": {}, "header": {}, "footer": {},
}

var blockElements = map[string]struct{}{
	"article": {}, "aside": {}, "blockquote": {}, "br": {}, "dd": {}, "div": {},
	"dl": {}, "dt": {}, "h1": {}, "h2": {}, "h3": {}, "h4": {}, "h5": {},
	"h6": {}, "hr": {}, "li": {}, "main": {}, "ol": {}, "p": {}, "pre": {},
	"section": {}, "table": {}, "td": {}, "th": {}, "tr": {}, "ul": {},
}

// ExtractedPage is normalized, readable content parsed from one HTML document.
type ExtractedPage struct {
	Title string
	Text  string
}

// ExtractHTML removes non-content elements and preserves block boundaries as
// newlines so knowledge's paragraph splitter receives useful input.
func ExtractHTML(document []byte) (ExtractedPage, error) {
	root, err := html.Parse(bytes.NewReader(document))
	if err != nil {
		return ExtractedPage{}, err
	}
	extractor := textExtractor{}
	extractor.walk(root)
	return ExtractedPage{
		Title: extractDocumentTitle(root),
		Text:  normalizeLines(extractor.builder.String()),
	}, nil
}

// ExtractText returns only the normalized readable body text.
func ExtractText(document []byte) (string, error) {
	page, err := ExtractHTML(document)
	return page.Text, err
}

type textExtractor struct {
	builder      strings.Builder
	pendingSpace bool
	lineOpen     bool
}

func (e *textExtractor) walk(node *html.Node) {
	if node.Type == html.ElementNode {
		name := strings.ToLower(node.Data)
		if _, discarded := discardedElements[name]; discarded {
			return
		}
		if _, block := blockElements[name]; block {
			e.breakLine()
		}
	}
	if node.Type == html.TextNode {
		e.appendText(node.Data)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		e.walk(child)
	}
	if node.Type == html.ElementNode {
		if _, block := blockElements[strings.ToLower(node.Data)]; block {
			e.breakLine()
		}
	}
}

func (e *textExtractor) appendText(value string) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return
	}
	leadingSpace, _ := utf8.DecodeRuneInString(value)
	trailingRune, _ := utf8.DecodeLastRuneInString(value)
	if e.lineOpen && (e.pendingSpace || unicode.IsSpace(leadingSpace)) {
		e.builder.WriteByte(' ')
	}
	e.builder.WriteString(strings.Join(fields, " "))
	e.pendingSpace = unicode.IsSpace(trailingRune)
	e.lineOpen = true
}

func (e *textExtractor) breakLine() {
	e.pendingSpace = false
	if e.lineOpen {
		e.builder.WriteByte('\n')
		e.lineOpen = false
	}
}

func extractDocumentTitle(root *html.Node) string {
	var find func(*html.Node) string
	find = func(node *html.Node) string {
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "title") {
			return strings.Join(strings.Fields(textContent(node)), " ")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if title := find(child); title != "" {
				return title
			}
		}
		return ""
	}
	return find(root)
}

func textContent(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func normalizeLines(value string) string {
	lines := strings.Split(value, "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return strings.Join(normalized, "\n")
}
