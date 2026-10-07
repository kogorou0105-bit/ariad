package ingestion

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/giraffesyo/pdf/pdftest"
)

func TestExtractFileTextAndMarkdown(t *testing.T) {
	for _, name := range []string{"guide.txt", "guide.md"} {
		text, _, err := ExtractFile(name, []byte("  Refunds are available.  "))
		if err != nil || text != "Refunds are available." {
			t.Fatalf("ExtractFile(%q) = %q, %v", name, text, err)
		}
	}
}

func TestExtractFileDOCX(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	part, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(`<w:document xmlns:w="urn:test"><w:body><w:p><w:r><w:t>Shipping policy</w:t></w:r></w:p></w:body></w:document>`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	text, _, err := ExtractFile("policy.docx", buffer.Bytes())
	if err != nil || !strings.Contains(text, "Shipping policy") {
		t.Fatalf("text = %q, err = %v", text, err)
	}
}

func TestExtractFilePDFWithChineseCIDFontAndToUnicode(t *testing.T) {
	content := "BT /F1 12 Tf 72 720 Td " + pdftest.Hex2(1, 2, 3, 4) + " Tj ET"
	cmap := pdftest.ToUnicodeCMap("4 beginbfchar\n<0001> <9000>\n<0002> <6B3E>\n<0003> <653F>\n<0004> <7B56>\nendbfchar")
	data := pdftest.Build(1,
		pdftest.Catalog(2),
		pdftest.Pages(3),
		pdftest.Page(2, 4, "<< /Font << /F1 5 0 R >> >>"),
		pdftest.Stream("", content),
		pdftest.Type0Font(6, 7),
		pdftest.CIDFont("/W [1 [1000 1000 1000 1000]]"),
		cmap,
	)
	text, mediaType, err := ExtractFile("退款政策.pdf", data)
	if err != nil || mediaType != "application/pdf" || !strings.Contains(text, "退款政策") {
		t.Fatalf("text = %q, media type = %q, err = %v", text, mediaType, err)
	}
}

func TestExtractFileRejectsUnsupportedAndOversized(t *testing.T) {
	if _, _, err := ExtractFile("data.csv", []byte("a,b")); !errors.Is(err, ErrUnsupportedFile) {
		t.Fatalf("error = %v", err)
	}
	if _, _, err := ExtractFile("large.txt", make([]byte, MaximumFileSize+1)); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("error = %v", err)
	}
}
