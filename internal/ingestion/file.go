package ingestion

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	pdf "github.com/giraffesyo/pdf"
)

const MaximumFileSize int64 = 10 << 20

var (
	ErrUnsupportedFile = errors.New("supported file types are PDF, DOCX, TXT, and Markdown")
	ErrFileTooLarge    = fmt.Errorf("file exceeds the %d MiB limit", MaximumFileSize>>20)
)

func ExtractFile(name string, data []byte) (string, string, error) {
	return ExtractFileContext(context.Background(), name, data)
}

func ExtractFileContext(ctx context.Context, name string, data []byte) (string, string, error) {
	if int64(len(data)) > MaximumFileSize {
		return "", "", ErrFileTooLarge
	}
	ext := strings.ToLower(filepath.Ext(name))
	var text, mediaType string
	var err error
	switch ext {
	case ".txt":
		text, mediaType = string(data), "text/plain"
	case ".md", ".markdown":
		text, mediaType = string(data), "text/markdown"
	case ".docx":
		text, err = extractDOCX(data)
		mediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".pdf":
		text, err = extractPDF(ctx, data)
		mediaType = "application/pdf"
	default:
		return "", "", ErrUnsupportedFile
	}
	if err != nil {
		return "", mediaType, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", mediaType, errors.New("file contains no extractable text")
	}
	return text, mediaType, nil
}

func extractDOCX(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("invalid DOCX: %w", err)
	}
	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		body, err := file.Open()
		if err != nil {
			return "", err
		}
		defer func() { _ = body.Close() }()
		decoder := xml.NewDecoder(body)
		var builder strings.Builder
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			switch value := token.(type) {
			case xml.CharData:
				builder.Write(value)
			case xml.EndElement:
				if value.Name.Local == "p" {
					builder.WriteByte('\n')
				}
			}
		}
		return builder.String(), nil
	}
	return "", errors.New("invalid DOCX: document body is missing")
}

func extractPDF(ctx context.Context, data []byte) (string, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", errors.New("invalid PDF header")
	}
	document, err := pdf.Extract(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("extract PDF text: %w", err)
	}
	return document.Text(), nil
}
