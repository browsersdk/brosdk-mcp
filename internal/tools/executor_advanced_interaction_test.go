package tools

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestResolveSelectOptionCriteriaRequiresOneSelector(t *testing.T) {
	if _, err := resolveSelectOptionCriteria(map[string]any{}); err == nil {
		t.Fatal("expected criteria validation error")
	}
}

func TestResolveSelectOptionCriteriaAcceptsIndex(t *testing.T) {
	criteria, err := resolveSelectOptionCriteria(map[string]any{"index": 2})
	if err != nil {
		t.Fatalf("resolveSelectOptionCriteria returned error: %v", err)
	}
	if criteria.Index == nil || *criteria.Index != 2 {
		t.Fatalf("unexpected criteria: %#v", criteria)
	}
}

func TestSelectOptionCriteriaJSONUsesLowercaseKeys(t *testing.T) {
	index := 1
	raw, err := json.Marshal(selectOptionCriteria{
		Value: "beta",
		Label: "Beta",
		Index: &index,
	})
	if err != nil {
		t.Fatalf("marshal selectOptionCriteria returned error: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"value":"beta"`) || !strings.Contains(text, `"label":"Beta"`) || !strings.Contains(text, `"index":1`) {
		t.Fatalf("unexpected marshaled selectOptionCriteria: %s", text)
	}
}

func TestNormalizeFileInputPathsRejectsMissingFile(t *testing.T) {
	if _, err := normalizeFileInputPaths([]string{"definitely-missing-file.txt"}); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestNormalizeFileInputPathsResolvesAbsolutePaths(t *testing.T) {
	temp := t.TempDir()
	file := temp + "/upload.txt"
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	paths, err := normalizeFileInputPaths([]string{file})
	if err != nil {
		t.Fatalf("normalizeFileInputPaths returned error: %v", err)
	}
	if len(paths) != 1 || !strings.Contains(paths[0], "upload.txt") {
		t.Fatalf("unexpected normalized paths: %#v", paths)
	}
}

func TestIsNoDialogOpenError(t *testing.T) {
	if !isNoDialogOpenError(assertErr("No dialog is showing")) {
		t.Fatal("expected no-dialog error to match")
	}
	if isNoDialogOpenError(assertErr("something else")) {
		t.Fatal("unexpected false positive for no-dialog error")
	}
}

func assertErr(message string) error {
	return &staticError{message: message}
}

type staticError struct {
	message string
}

func (e *staticError) Error() string {
	return e.message
}
