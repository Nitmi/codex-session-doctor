package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanStringRemovesRiskyMarkers(t *testing.T) {
	input := strings.Join([]string{
		"Commit completed.",
		`::git-stage{cwd="D:\project\repo"}`,
		`::git-commit{cwd="D:\project\repo"}`,
		`::git-push{cwd="D:\project\repo" branch="main"}`,
		"Pushed.",
	}, "\n")

	output, stats := cleanString(input, options{mode: "remove"})

	if stats.Removed != 3 {
		t.Fatalf("removed = %d, want 3", stats.Removed)
	}
	if output != "Commit completed.\nPushed." {
		t.Fatalf("output = %q", output)
	}
}

func TestCleanStringDefaultDoesNotRemoveInlineOrForwardSlash(t *testing.T) {
	input := strings.Join([]string{
		"Use this example:",
		"`::git-stage{cwd=\"D:\\project\\repo\"}`",
		`::git-stage{cwd="D:/project/repo"}`,
	}, "\n")

	output, stats := cleanString(input, options{mode: "remove"})

	if stats.Removed != 0 || stats.Escaped != 0 {
		t.Fatalf("stats = %+v, want zero", stats)
	}
	if output != input {
		t.Fatalf("output changed: %q", output)
	}
}

func TestCleanStringAllMarkers(t *testing.T) {
	output, stats := cleanString("::git-stage{cwd=\"D:/project/repo\"}\nDone", options{mode: "remove", allGitMarkers: true})

	if stats.Removed != 1 {
		t.Fatalf("removed = %d, want 1", stats.Removed)
	}
	if output != "Done" {
		t.Fatalf("output = %q", output)
	}
}

func TestCleanStringEscape(t *testing.T) {
	output, stats := cleanString("::git-stage{cwd=\"C:\\Users\\me\\repo\"}\nDone", options{mode: "escape"})

	if stats.Escaped != 1 {
		t.Fatalf("escaped = %d, want 1", stats.Escaped)
	}
	if output != "\\:\\:git-stage{cwd=\"C:\\Users\\me\\repo\"}\nDone" {
		t.Fatalf("output = %q", output)
	}
}

func TestRepairRootsApplyAndBackup(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "session.jsonl")
	writeJSONL(t, file, []any{
		map[string]any{
			"type": "task_complete",
			"payload": map[string]any{
				"last_agent_message": "Done\n::git-commit{cwd=\"D:\\repo\"}\n::git-push{cwd=\"D:\\repo\" branch=\"main\"}",
			},
		},
	})

	result := repairRoots(options{apply: true, mode: "remove", roots: []string{root}})

	if result.RepairedFiles != 1 {
		t.Fatalf("repaired = %d, want 1", result.RepairedFiles)
	}
	if result.RemovedMarkers != 2 {
		t.Fatalf("removed = %d, want 2", result.RemovedMarkers)
	}
	if result.Files[0].Backup == "" {
		t.Fatal("missing backup")
	}
	if _, err := os.Stat(result.Files[0].Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	records, parseErrors := parseJSONL(string(data), file)
	if len(parseErrors) > 0 {
		t.Fatalf("parse errors: %+v", parseErrors)
	}
	payload := records[0].(map[string]any)["payload"].(map[string]any)
	if got := payload["last_agent_message"]; got != "Done" {
		t.Fatalf("message = %q, want Done", got)
	}
}

func TestInvalidJSONLDoesNotBlockOtherFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bad.jsonl"), []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, filepath.Join(root, "good.jsonl"), []any{
		map[string]any{"text": "::git-stage{cwd=\"D:\\repo\"}"},
	})

	result := repairRoots(options{apply: true, mode: "remove", roots: []string{root}})

	if result.ScannedFiles != 2 {
		t.Fatalf("scanned = %d, want 2", result.ScannedFiles)
	}
	if result.SkippedInvalidFiles != 1 {
		t.Fatalf("skipped = %d, want 1", result.SkippedInvalidFiles)
	}
	if result.RepairedFiles != 1 {
		t.Fatalf("repaired = %d, want 1", result.RepairedFiles)
	}
}

func TestParseArgsLanguage(t *testing.T) {
	opts, err := parseArgs([]string{"--scan", "--lang", "zh", "--root", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.lang != langZH {
		t.Fatalf("lang = %q, want zh", opts.lang)
	}
	if len(opts.roots) != 1 || opts.roots[0] != "x" {
		t.Fatalf("roots = %+v", opts.roots)
	}
}

func TestLanguageFromString(t *testing.T) {
	if got := languageFromString("zh-CN"); got != langZH {
		t.Fatalf("zh-CN = %q, want zh", got)
	}
	if got := languageFromString("en-US"); got != langEN {
		t.Fatalf("en-US = %q, want en", got)
	}
	if got := languageFromString(""); got != "" {
		t.Fatalf("empty = %q, want empty", got)
	}
}

func writeJSONL(t *testing.T, file string, records []any) {
	t.Helper()
	var builder strings.Builder
	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		builder.Write(data)
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(file, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
