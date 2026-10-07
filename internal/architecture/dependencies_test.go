package architecture_test

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var domainNames = map[string]struct{}{
	"agent":        {},
	"auth":         {},
	"conversation": {},
	"evaluation":   {},
	"ingestion":    {},
	"knowledge":    {},
	"modelconfig":  {},
	"retrieval":    {},
	"review":       {},
	"runtime":      {},
	"tenant":       {},
}

func TestRepositoryDependencyBoundaries(t *testing.T) {
	repositoryRoot := findRepositoryRoot(t)
	modulePath := readModulePath(t, filepath.Join(repositoryRoot, "go.mod"))

	err := filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		relativePath, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		owner := packageOwner(relativePath)
		imports, err := importsForFile(path)
		if err != nil {
			return err
		}
		for _, importedPath := range imports {
			if violation := validateImport(modulePath, owner, importedPath); violation != "" {
				t.Errorf("%s imports %q: %s", relativePath, importedPath, violation)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository imports: %v", err)
	}
}

func TestValidateImport(t *testing.T) {
	tests := []struct {
		name       string
		owner      string
		importPath string
		wantError  bool
	}{
		{name: "standard library", owner: "agent", importPath: "context"},
		{name: "own subpackage", owner: "agent", importPath: "ariad/internal/agent/model"},
		{name: "other domain public surface", owner: "agent", importPath: "ariad/internal/tenant"},
		{name: "other domain implementation", owner: "agent", importPath: "ariad/internal/tenant/storage", wantError: true},
		{name: "domain imports platform", owner: "agent", importPath: "ariad/internal/platform/database", wantError: true},
		{name: "command imports domain implementation", owner: "cmd", importPath: "ariad/internal/agent/storage", wantError: true},
		{name: "package test imports platform", owner: "agent", importPath: "ariad/internal/platform/database", wantError: true},
		{name: "integration test composes adapter", owner: "test:integration", importPath: "ariad/internal/platform/database"},
		{name: "integration test composes domain implementation", owner: "test:integration", importPath: "ariad/internal/agent/storage"},
		{name: "security test imports platform", owner: "test:security", importPath: "ariad/internal/platform/database", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			violation := validateImport("ariad", test.owner, test.importPath)
			if gotError := violation != ""; gotError != test.wantError {
				t.Fatalf("validateImport() violation = %q, wantError = %v", violation, test.wantError)
			}
		})
	}
}

func validateImport(modulePath, owner, importedPath string) string {
	internalPrefix := modulePath + "/internal/"
	if !strings.HasPrefix(importedPath, internalPrefix) {
		return ""
	}
	if owner == "test:integration" {
		return ""
	}

	parts := strings.Split(strings.TrimPrefix(importedPath, internalPrefix), "/")
	target := parts[0]
	_, isDomain := domainNames[owner]
	if target == "platform" && (isDomain || strings.HasPrefix(owner, "test:")) {
		return "domain and black-box test packages depend on public interfaces, not platform adapters"
	}

	if _, isTargetDomain := domainNames[target]; !isTargetDomain || target == owner || len(parts) == 1 {
		return ""
	}
	return fmt.Sprintf("packages outside %s may import only its public root package", target)
}

func TestPackageOwner(t *testing.T) {
	tests := map[string]string{
		"internal/agent/service.go":          "agent",
		"internal/agent/service_test.go":     "agent",
		"tests/integration/runtime_test.go":  "test:integration",
		"tests/security/isolation_test.go":   "test:security",
		"tests/evals/retrieval_case_test.go": "test:evals",
		"tests/doc.go":                       "test",
		"cmd/api/main.go":                    "cmd",
		"docs/examples/not_a_go_package.go":  "outside",
	}

	for path, want := range tests {
		if got := packageOwner(path); got != want {
			t.Errorf("packageOwner(%q) = %q, want %q", path, got, want)
		}
	}
}

func packageOwner(relativePath string) string {
	parts := strings.Split(filepath.ToSlash(relativePath), "/")
	if len(parts) >= 2 && parts[0] == "internal" {
		return parts[1]
	}
	if len(parts) >= 1 && parts[0] == "cmd" {
		return "cmd"
	}
	if len(parts) >= 3 && parts[0] == "tests" {
		return "test:" + parts[1]
	}
	if len(parts) >= 1 && parts[0] == "tests" {
		return "test"
	}
	return "outside"
}

func importsForFile(path string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	imports := make([]string, 0, len(parsed.Imports))
	for _, spec := range parsed.Imports {
		if spec.Path.Kind != token.STRING {
			continue
		}
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("unquote import in %s: %w", path, err)
		}
		imports = append(imports, value)
	}
	return imports, nil
}

func findRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func readModulePath(t *testing.T, goModPath string) string {
	t.Helper()
	file, err := os.Open(goModPath)
	if err != nil {
		t.Fatalf("open go.mod: %v", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close go.mod: %v", closeErr)
		}
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if modulePath, found := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "module "); found {
			return strings.TrimSpace(modulePath)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	t.Fatal("go.mod has no module directive")
	return ""
}
