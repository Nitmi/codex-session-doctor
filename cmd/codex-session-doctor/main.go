package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
)

const version = "1.0.0"

type language string

const (
	langEN language = "en"
	langZH language = "zh"
)

type options struct {
	apply         bool
	scan          bool
	jsonOutput    bool
	allGitMarkers bool
	noVerify      bool
	backupDir     string
	mode          string
	lang          language
	roots         multiFlag
}

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	if value == "" {
		return errors.New("empty value")
	}
	*m = append(*m, value)
	return nil
}

type rootIssue struct {
	Root   string `json:"root"`
	Reason string `json:"reason"`
}

type parseError struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type fileResult struct {
	File              string       `json:"file"`
	Removed          int          `json:"removed"`
	Escaped          int          `json:"escaped"`
	Backup           string       `json:"backup,omitempty"`
	ParseErrors      []parseError `json:"parseErrors,omitempty"`
	WriteError       string       `json:"writeError,omitempty"`
	VerificationErr  string       `json:"verificationError,omitempty"`
	RemainingMarkers int          `json:"remainingMarkers,omitempty"`
}

type summary struct {
	Version             string       `json:"version"`
	Mode                string       `json:"mode"`
	Strategy            string       `json:"strategy"`
	RiskyOnly           bool         `json:"riskyOnly"`
	Roots               []string     `json:"roots"`
	ScannedFiles        int          `json:"scannedFiles"`
	AffectedFiles       int          `json:"affectedFiles"`
	RepairedFiles       int          `json:"repairedFiles"`
	SkippedInvalidFiles int          `json:"skippedInvalidFiles"`
	UnreadableRoots     []rootIssue  `json:"unreadableRoots"`
	RemovedMarkers      int          `json:"removedMarkers"`
	EscapedMarkers      int          `json:"escapedMarkers"`
	Files               []fileResult `json:"files"`
}

type cleanStats struct {
	Removed int
	Escaped int
}

var (
	gitDirectiveRE = regexp.MustCompile(`^\s*::git-(stage|commit|push|pull|fetch|merge|rebase|checkout|branch|tag|reset|stash|diff|status)(\{[^\r\n]*)?\s*$`)
	windowsCwdRE   = regexp.MustCompile(`cwd\s*=\s*"[^"\r\n]*[A-Za-z]:\\[^"\r\n]*"`)
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if len(args) == 0 {
		return interactive(opts.lang, stdin, stdout, stderr)
	}

	result := repairRoots(opts)
	if opts.jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		printSummary(stdout, result, opts.lang)
	}

	if hasWriteFailures(result) {
		return 2
	}
	return 0
}

func parseArgs(args []string) (options, error) {
	opts := options{
		mode: "remove",
		lang: detectLanguage(),
	}

	fs := flag.NewFlagSet("codex-session-doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.apply, "apply", false, "")
	fs.BoolVar(&opts.scan, "scan", false, "")
	fs.BoolVar(&opts.jsonOutput, "json", false, "")
	fs.BoolVar(&opts.allGitMarkers, "all-git-markers", false, "")
	fs.BoolVar(&opts.noVerify, "no-verify", false, "")
	fs.StringVar(&opts.backupDir, "backup-dir", "", "")
	fs.StringVar(&opts.mode, "mode", "remove", "")
	langRaw := fs.String("lang", "", "")
	help := fs.Bool("help", false, "")
	versionFlag := fs.Bool("version", false, "")
	fs.Var(&opts.roots, "root", "")

	if err := fs.Parse(args); err != nil {
		return opts, err
	}

	if *langRaw != "" {
		switch strings.ToLower(*langRaw) {
		case "zh", "zh-cn", "cn":
			opts.lang = langZH
		case "en", "en-us":
			opts.lang = langEN
		default:
			return opts, fmt.Errorf("unsupported --lang: %s", *langRaw)
		}
	}

	if *help {
		fmt.Println(usage(opts.lang))
		os.Exit(0)
	}
	if *versionFlag {
		fmt.Println(version)
		os.Exit(0)
	}

	if opts.mode != "remove" && opts.mode != "escape" {
		return opts, errors.New("--mode must be remove or escape")
	}
	if opts.scan && opts.apply {
		return opts, errors.New("--scan and --apply cannot be used together")
	}
	return opts, nil
}

func usage(lang language) string {
	if lang == langZH {
		return `Codex Session Doctor ` + version + `

用法:
  Codex Session Doctor.exe
  Codex Session Doctor.exe --scan
  Codex Session Doctor.exe --apply

选项:
  --scan                  只扫描，不修改文件
  --apply                 写入修复；每个修改文件都会先备份
  --root <path>           指定 session 根目录，可重复
  --backup-dir <path>     将备份集中保存到指定目录
  --mode remove|escape    删除 marker 或转义为普通文本
  --all-git-markers       修复所有独立 ::git-* marker
  --json                  输出 JSON
  --lang zh|en            指定交互语言`
	}

	return `Codex Session Doctor ` + version + `

Usage:
  Codex Session Doctor.exe
  Codex Session Doctor.exe --scan
  Codex Session Doctor.exe --apply

Options:
  --scan                  Scan only, do not modify files
  --apply                 Write repairs; each changed file is backed up first
  --root <path>           Session root to scan, can be repeated
  --backup-dir <path>     Store backups under this directory
  --mode remove|escape    Remove markers or escape them as plain text
  --all-git-markers       Repair all standalone ::git-* markers
  --json                  Print JSON output
  --lang zh|en            Select language`
}

func interactive(lang language, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	reader := bufio.NewReader(stdin)
	lastExitCode := 0

	for {
		printMenu(stdout, lang)
		choice := readLine(reader)

		switch choice {
		case "1":
			result := repairRoots(options{scan: true, mode: "remove", lang: lang})
			printSummary(stdout, result, lang)
			lastExitCode = exitCode(result)
			continueMenu(stdout, reader, lang)
		case "2":
			fmt.Fprintln(stdout)
			printText(stdout, lang,
				"This will back up every changed JSONL file before writing.",
				"写入前会先备份每个被修改的 JSONL 文件。",
			)
			result := repairRoots(options{apply: true, mode: "remove", lang: lang})
			printSummary(stdout, result, lang)
			lastExitCode = exitCode(result)
			continueMenu(stdout, reader, lang)
		case "3":
			fmt.Fprintln(stdout)
			printTextLn(stdout, lang,
				"This is broader than the default repair and removes all standalone ::git-* markers.",
				"此选项比默认修复更激进，会移除所有独立的 ::git-* marker。",
			)
			printTextLn(stdout, lang,
				"Use it only if scan still reports broken sessions after the default repair.",
				"仅在默认修复后扫描仍显示异常时使用。",
			)
			result := repairRoots(options{apply: true, allGitMarkers: true, mode: "remove", lang: lang})
			printSummary(stdout, result, lang)
			lastExitCode = exitCode(result)
			continueMenu(stdout, reader, lang)
		case "4":
			lang = toggleLanguage(lang)
		case "5":
			return lastExitCode
		default:
			printTextLn(stdout, lang, "Invalid choice.", "无效选项。")
			continueMenu(stdout, reader, lang)
		}
	}
}

func printMenu(w io.Writer, lang language) {
	if lang == langZH {
		fmt.Fprintf(w, "Codex Session Doctor %s\n\n", version)
		fmt.Fprintln(w, "修复 Codex session JSONL 中导致 Windows 渲染崩溃的 git marker。")
		fmt.Fprintln(w, "执行修复前请先关闭 Codex Desktop。")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "1. 只扫描")
		fmt.Fprintln(w, "2. 默认修复受影响 sessions（推荐先试）")
		fmt.Fprintln(w, "3. 激进修复所有独立 git markers（2 不行再试）")
		fmt.Fprintln(w, "4. Switch to English")
		fmt.Fprintln(w, "5. 退出")
		fmt.Fprintln(w)
		fmt.Fprint(w, "请选择 1-5 并按回车: ")
		return
	}

	fmt.Fprintf(w, "Codex Session Doctor %s\n\n", version)
	fmt.Fprintln(w, "Repairs Codex session JSONL files affected by Windows git marker render crashes.")
	fmt.Fprintln(w, "Close Codex Desktop before choosing repair.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "1. Scan only")
	fmt.Fprintln(w, "2. Repair affected sessions (try first)")
	fmt.Fprintln(w, "3. Repair all standalone git markers (only if option 2 is not enough)")
	fmt.Fprintln(w, "4. 切换到中文")
	fmt.Fprintln(w, "5. Exit")
	fmt.Fprintln(w)
	fmt.Fprint(w, "Choose 1-5 and press Enter: ")
}

func toggleLanguage(lang language) language {
	if lang == langZH {
		return langEN
	}
	return langZH
}

func readLine(reader *bufio.Reader) string {
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func continueMenu(w io.Writer, reader *bufio.Reader, lang language) {
	printText(w, lang, "\nPress Enter to return to the menu...", "\n按回车返回菜单...")
	_, _ = reader.ReadString('\n')
}

func detectLanguage() language {
	if override := languageFromString(os.Getenv("CODEX_SESSION_DOCTOR_LANG")); override != "" {
		return override
	}

	if system := detectSystemLanguage(); system != "" {
		return system
	}

	values := []string{os.Getenv("LANG"), os.Getenv("LANGUAGE")}
	for _, value := range values {
		if detected := languageFromString(value); detected != "" {
			return detected
		}
	}
	return langEN
}

func languageFromString(value string) language {
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "zh") || strings.Contains(lower, "chinese") {
		return langZH
	}
	if strings.HasPrefix(lower, "en") || strings.Contains(lower, "english") {
		return langEN
	}
	return ""
}

func repairRoots(opts options) summary {
	roots := resolveRoots(opts.roots)
	result := summary{
		Version:   version,
		Mode:      "scan",
		Strategy:  opts.mode,
		RiskyOnly: !opts.allGitMarkers,
		Roots:     roots,
		Files:     []fileResult{},
	}
	if opts.apply {
		result.Mode = "apply"
	}
	if result.Strategy == "" {
		result.Strategy = "remove"
	}

	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			result.UnreadableRoots = append(result.UnreadableRoots, rootIssue{Root: root, Reason: "not found"})
			continue
		}
		if !info.IsDir() {
			result.UnreadableRoots = append(result.UnreadableRoots, rootIssue{Root: root, Reason: "not a directory"})
			continue
		}

		files, err := listJSONLFiles(root)
		if err != nil {
			result.UnreadableRoots = append(result.UnreadableRoots, rootIssue{Root: root, Reason: err.Error()})
			continue
		}

		for _, file := range files {
			result.ScannedFiles++
			fileResult := repairFile(file, root, opts)
			if len(fileResult.ParseErrors) > 0 {
				result.SkippedInvalidFiles++
				result.Files = append(result.Files, fileResult)
				continue
			}
			if fileResult.Removed+fileResult.Escaped == 0 && fileResult.WriteError == "" && fileResult.VerificationErr == "" {
				continue
			}
			result.AffectedFiles++
			result.RemovedMarkers += fileResult.Removed
			result.EscapedMarkers += fileResult.Escaped
			if opts.apply && fileResult.WriteError == "" && fileResult.VerificationErr == "" {
				result.RepairedFiles++
			}
			result.Files = append(result.Files, fileResult)
		}
	}

	return result
}

func repairFile(file string, root string, opts options) fileResult {
	result := fileResult{File: file}

	data, err := os.ReadFile(file)
	if err != nil {
		result.WriteError = "read failed: " + err.Error()
		return result
	}

	records, parseErrors := parseJSONL(string(data), file)
	if len(parseErrors) > 0 {
		result.ParseErrors = parseErrors
		return result
	}

	nextRecords := make([]any, 0, len(records))
	for _, record := range records {
		cleaned, stats := cleanValue(record, opts)
		result.Removed += stats.Removed
		result.Escaped += stats.Escaped
		nextRecords = append(nextRecords, cleaned)
	}

	if result.Removed+result.Escaped == 0 || !opts.apply {
		return result
	}

	output, err := stringifyJSONL(nextRecords)
	if err != nil {
		result.VerificationErr = err.Error()
		return result
	}

	if !opts.noVerify {
		_, parseErrors := parseJSONL(output, file)
		if len(parseErrors) > 0 {
			result.VerificationErr = "generated JSONL failed validation"
			return result
		}
		remaining := countCandidatesInJSONL(output, opts)
		if remaining > 0 {
			result.RemainingMarkers = remaining
			result.VerificationErr = fmt.Sprintf("generated JSONL still contains %d marker candidate(s)", remaining)
			return result
		}
	}

	backup, err := backupFile(file, root, opts.backupDir)
	if err != nil {
		result.WriteError = err.Error()
		return result
	}
	result.Backup = backup

	if err := os.WriteFile(file, []byte(output), 0o644); err != nil {
		result.WriteError = err.Error()
	}

	return result
}

func parseJSONL(text string, file string) ([]any, []parseError) {
	lines := splitLines(text)
	records := []any{}
	errors := []parseError{}
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			errors = append(errors, parseError{File: file, Line: index + 1, Message: err.Error()})
			continue
		}
		records = append(records, value)
	}
	return records, errors
}

func stringifyJSONL(records []any) (string, error) {
	var builder strings.Builder
	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			return "", err
		}
		builder.Write(data)
		builder.WriteByte('\n')
	}
	return builder.String(), nil
}

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

func cleanValue(value any, opts options) (any, cleanStats) {
	switch typed := value.(type) {
	case string:
		next, stats := cleanString(typed, opts)
		return next, stats
	case []any:
		total := cleanStats{}
		next := make([]any, len(typed))
		for index, item := range typed {
			cleaned, stats := cleanValue(item, opts)
			next[index] = cleaned
			total.Removed += stats.Removed
			total.Escaped += stats.Escaped
		}
		return next, total
	case map[string]any:
		total := cleanStats{}
		next := make(map[string]any, len(typed))
		for key, item := range typed {
			cleaned, stats := cleanValue(item, opts)
			next[key] = cleaned
			total.Removed += stats.Removed
			total.Escaped += stats.Escaped
		}
		return next, total
	default:
		return value, cleanStats{}
	}
}

func cleanString(value string, opts options) (string, cleanStats) {
	lines := splitStringPreservingNewlines(value)
	stats := cleanStats{}
	var builder strings.Builder

	for _, line := range lines {
		isDirective := isTargetDirective(line.Text, opts)
		if !isDirective {
			builder.WriteString(line.Text)
			builder.WriteString(line.Newline)
			continue
		}

		if opts.mode == "escape" {
			stats.Escaped++
			builder.WriteString(escapeDirective(line.Text))
			builder.WriteString(line.Newline)
			continue
		}

		stats.Removed++
	}

	if stats.Removed+stats.Escaped == 0 {
		return value, stats
	}

	output := builder.String()
	if stats.Removed > 0 {
		output = trimBlankEdges(output)
	}
	return output, stats
}

type stringLine struct {
	Text    string
	Newline string
}

func splitStringPreservingNewlines(value string) []stringLine {
	lines := []stringLine{}
	start := 0
	for index := 0; index < len(value); index++ {
		if value[index] == '\n' || value[index] == '\r' {
			newline := value[index : index+1]
			end := index
			if value[index] == '\r' && index+1 < len(value) && value[index+1] == '\n' {
				newline = "\r\n"
				index++
			}
			lines = append(lines, stringLine{Text: value[start:end], Newline: newline})
			start = index + 1
		}
	}
	if start < len(value) {
		lines = append(lines, stringLine{Text: value[start:], Newline: ""})
	}
	if len(lines) == 0 {
		lines = append(lines, stringLine{Text: "", Newline: ""})
	}
	return lines
}

func isTargetDirective(line string, opts options) bool {
	if !gitDirectiveRE.MatchString(line) {
		return false
	}
	if opts.allGitMarkers {
		return true
	}
	return windowsCwdRE.MatchString(line)
}

func escapeDirective(line string) string {
	index := strings.Index(line, "::git-")
	if index < 0 {
		return line
	}
	return line[:index] + `\:\:git-` + line[index+len("::git-"):]
}

func trimBlankEdges(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

func countCandidatesInJSONL(text string, opts options) int {
	records, errors := parseJSONL(text, "")
	if len(errors) > 0 {
		return -1
	}
	total := 0
	for _, record := range records {
		total += countCandidates(record, opts)
	}
	return total
}

func countCandidates(value any, opts options) int {
	switch typed := value.(type) {
	case string:
		for _, line := range splitStringPreservingNewlines(typed) {
			if isTargetDirective(line.Text, opts) {
				return 1
			}
		}
		return 0
	case []any:
		total := 0
		for _, item := range typed {
			total += countCandidates(item, opts)
		}
		return total
	case map[string]any:
		total := 0
		for _, item := range typed {
			total += countCandidates(item, opts)
		}
		return total
	default:
		return 0
	}
}

func resolveRoots(roots []string) []string {
	var result []string
	if len(roots) > 0 {
		result = append(result, roots...)
	} else {
		result = defaultRoots()
	}

	seen := map[string]bool{}
	unique := []string{}
	for _, root := range result {
		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		if !seen[strings.ToLower(abs)] {
			seen[strings.ToLower(abs)] = true
			unique = append(unique, abs)
		}
	}
	return unique
}

func defaultRoots() []string {
	home, _ := os.UserHomeDir()
	roots := []string{}
	if home != "" {
		roots = append(roots, filepath.Join(home, ".codex", "sessions"))
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			roots = append(roots, filepath.Join(local, "OpenAI", "Codex", "sessions"))
		}
		if roaming := os.Getenv("APPDATA"); roaming != "" {
			roots = append(roots, filepath.Join(roaming, "OpenAI", "Codex", "sessions"))
		}
	}
	return roots
}

func listJSONLFiles(root string) ([]string, error) {
	files := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func backupFile(file string, root string, backupDir string) (string, error) {
	stamp := time.Now().UTC().Format("2006-01-02T15-04-05-000Z")
	var backup string

	if backupDir == "" {
		backup = file + ".bak-" + stamp
	} else {
		relative, err := filepath.Rel(root, file)
		if err != nil {
			relative = filepath.Base(file)
		}
		backup = filepath.Join(backupDir, stamp, relative)
	}

	if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
		return "", err
	}

	source, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer source.Close()

	target, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	defer target.Close()

	if _, err := io.Copy(target, source); err != nil {
		return "", err
	}
	return backup, nil
}

func printSummary(w io.Writer, result summary, lang language) {
	if lang == langZH {
		fmt.Fprintf(w, "Codex Session Doctor %s\n", result.Version)
		fmt.Fprintf(w, "模式: %s\n", result.Mode)
		fmt.Fprintf(w, "策略: %s\n", result.Strategy)
		if result.RiskyOnly {
			fmt.Fprintln(w, "风险过滤: 仅 Windows cwd git markers")
		} else {
			fmt.Fprintln(w, "风险过滤: 所有独立 git markers")
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "扫描根目录:")
		for _, root := range result.Roots {
			fmt.Fprintf(w, "  %s\n", root)
		}
		if len(result.UnreadableRoots) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "不可读目录:")
			for _, item := range result.UnreadableRoots {
				fmt.Fprintf(w, "  %s (%s)\n", item.Root, item.Reason)
			}
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "已扫描文件: %d\n", result.ScannedFiles)
		fmt.Fprintf(w, "受影响文件: %d\n", result.AffectedFiles)
		if result.Mode == "apply" {
			fmt.Fprintf(w, "已修复文件: %d\n", result.RepairedFiles)
		}
		fmt.Fprintf(w, "已删除 markers: %d\n", result.RemovedMarkers)
		fmt.Fprintf(w, "已转义 markers: %d\n", result.EscapedMarkers)
		fmt.Fprintf(w, "跳过的无效 JSONL 文件: %d\n", result.SkippedInvalidFiles)
		printFiles(w, result, lang)
		if result.Mode == "scan" && result.AffectedFiles > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "关闭 Codex Desktop 后选择修复即可写入。")
		}
		return
	}

	fmt.Fprintf(w, "Codex Session Doctor %s\n", result.Version)
	fmt.Fprintf(w, "Mode: %s\n", result.Mode)
	fmt.Fprintf(w, "Strategy: %s\n", result.Strategy)
	if result.RiskyOnly {
		fmt.Fprintln(w, "Risk filter: Windows cwd git markers only")
	} else {
		fmt.Fprintln(w, "Risk filter: all standalone git markers")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Roots:")
	for _, root := range result.Roots {
		fmt.Fprintf(w, "  %s\n", root)
	}
	if len(result.UnreadableRoots) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Unreadable roots:")
		for _, item := range result.UnreadableRoots {
			fmt.Fprintf(w, "  %s (%s)\n", item.Root, item.Reason)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Scanned files: %d\n", result.ScannedFiles)
	fmt.Fprintf(w, "Affected files: %d\n", result.AffectedFiles)
	if result.Mode == "apply" {
		fmt.Fprintf(w, "Repaired files: %d\n", result.RepairedFiles)
	}
	fmt.Fprintf(w, "Removed markers: %d\n", result.RemovedMarkers)
	fmt.Fprintf(w, "Escaped markers: %d\n", result.EscapedMarkers)
	fmt.Fprintf(w, "Skipped invalid JSONL files: %d\n", result.SkippedInvalidFiles)
	printFiles(w, result, lang)
	if result.Mode == "scan" && result.AffectedFiles > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Close Codex Desktop, then choose repair to write changes.")
	}
}

func printFiles(w io.Writer, result summary, lang language) {
	if len(result.Files) == 0 {
		return
	}
	fmt.Fprintln(w)
	if lang == langZH {
		fmt.Fprintln(w, "文件:")
	} else {
		fmt.Fprintln(w, "Files:")
	}
	for _, file := range result.Files {
		status := "changed"
		if len(file.ParseErrors) > 0 {
			status = "invalid-jsonl"
		} else if file.WriteError != "" {
			status = "write-error"
		} else if file.VerificationErr != "" {
			status = "verify-error"
		}
		fmt.Fprintf(w, "  %s %d marker(s): %s\n", status, file.Removed+file.Escaped, file.File)
		if file.Backup != "" {
			fmt.Fprintf(w, "    backup: %s\n", file.Backup)
		}
		if file.WriteError != "" {
			fmt.Fprintf(w, "    writeError: %s\n", file.WriteError)
		}
		if file.VerificationErr != "" {
			fmt.Fprintf(w, "    verificationError: %s\n", file.VerificationErr)
		}
		for index, err := range file.ParseErrors {
			if index >= 3 {
				break
			}
			fmt.Fprintf(w, "    parseError line %d: %s\n", err.Line, err.Message)
		}
	}
}

func printText(w io.Writer, lang language, en string, zh string) {
	if lang == langZH {
		fmt.Fprint(w, zh)
		return
	}
	fmt.Fprint(w, en)
}

func printTextLn(w io.Writer, lang language, en string, zh string) {
	if lang == langZH {
		fmt.Fprintln(w, zh)
		return
	}
	fmt.Fprintln(w, en)
}

func hasWriteFailures(result summary) bool {
	for _, file := range result.Files {
		if file.WriteError != "" || file.VerificationErr != "" {
			return true
		}
	}
	return false
}

func exitCode(result summary) int {
	if hasWriteFailures(result) {
		return 2
	}
	return 0
}

func _isWhitespaceOnly(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return !unicode.IsSpace(r)
	}) < 0
}
