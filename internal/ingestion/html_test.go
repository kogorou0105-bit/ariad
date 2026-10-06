package ingestion

import (
	"strings"
	"testing"
)

func TestExtractHTMLRemovesNonContentAndPreservesBlocks(t *testing.T) {
	t.Parallel()
	document := []byte(`<!doctype html><html><head>
<title>  Help Center </title><style>.hidden{display:none}</style><script>alert(1)</script>
</head><body><header>Site header</header><nav>Navigation</nav>
<article><h1>Returns</h1><p>Refunds are <strong>available</strong> for 30 days.</p>
<ul><li>Keep the receipt.</li><li>Use the original payment method.</li></ul></article>
<noscript>Enable scripts</noscript><footer>Footer</footer></body></html>`)

	page, err := ExtractHTML(document)
	if err != nil {
		t.Fatalf("extract HTML: %v", err)
	}
	if page.Title != "Help Center" {
		t.Fatalf("title = %q, want Help Center", page.Title)
	}
	want := strings.Join([]string{
		"Returns",
		"Refunds are available for 30 days.",
		"Keep the receipt.",
		"Use the original payment method.",
	}, "\n")
	if page.Text != want {
		t.Fatalf("text = %q, want %q", page.Text, want)
	}
	for _, excluded := range []string{"alert", "hidden", "Navigation", "Footer", "Enable scripts"} {
		if strings.Contains(page.Text, excluded) {
			t.Fatalf("text contains excluded content %q: %q", excluded, page.Text)
		}
	}
}

func TestExtractHTMLHandlesNestedChineseAndEnglishText(t *testing.T) {
	t.Parallel()
	text, err := ExtractText([]byte(`<main><section><p>中文<strong>内容</strong>连续可读。</p>
<div><p>English <em>content</em> stays readable.</p></div></section></main>`))
	if err != nil {
		t.Fatalf("extract text: %v", err)
	}
	if text != "中文内容连续可读。\nEnglish content stays readable." {
		t.Fatalf("text = %q", text)
	}
}

func TestExtractHTMLEmptyDocumentReturnsEmptyText(t *testing.T) {
	t.Parallel()
	page, err := ExtractHTML([]byte(`<html><head><script>ignored()</script></head><body>  </body></html>`))
	if err != nil {
		t.Fatalf("extract empty HTML: %v", err)
	}
	if page.Text != "" {
		t.Fatalf("text = %q, want empty", page.Text)
	}
}
