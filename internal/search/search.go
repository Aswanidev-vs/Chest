package search

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Aswanidev-vs/chest/internal/classifier"
	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/charlievieth/fastwalk"
	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

// SearchOptions defines all query parameters for search
type SearchOptions struct {
	Pattern       string
	RootPath      string
	Type          string   // Category (image, video, doc, etc.)
	Extensions    []string // e.g. ["mp4", "mkv"]
	SizeCondition string   // e.g. ">1GB", "<50MB"
	DateCondition string   // e.g. ">30d", "<7d"
	ContentQuery  string   // Grep content inside file
	IgnoreCase    bool
	Regex         bool
	Exact         bool
	Fuzzy         bool
	IncludeHidden bool
	Limit         int
}

// SearchMatch represents a single matching file and optional content snippets
type SearchMatch struct {
	models.File
	Score         int
	ContentMatch  string
	ContentLineNo int
}

// Engine performs high-speed filesystem search
type Engine struct {
	opts SearchOptions
}

// New creates a new Search Engine
func New(opts SearchOptions) *Engine {
	if !opts.Regex && !opts.Exact && opts.Pattern != "" {
		opts.Fuzzy = true
	}
	return &Engine{opts: opts}
}

// Search executes the search concurrently using fastwalk and fzf matching
func (e *Engine) Search() ([]SearchMatch, error) {
	root := e.opts.RootPath
	if root == "" {
		root = "."
	}
	root = filepath.Clean(root)

	// Pre-compile regex if needed
	var re *regexp.Regexp
	if e.opts.Regex && e.opts.Pattern != "" {
		var err error
		pat := e.opts.Pattern
		if e.opts.IgnoreCase {
			pat = "(?i)" + pat
		}
		re, err = regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("invalid regex '%s': %w", e.opts.Pattern, err)
		}
	}

	// Prepare pattern runes for fzf
	var patternRunes []rune
	if e.opts.Pattern != "" {
		patternRunes = []rune(e.opts.Pattern)
	}

	// Normalize extensions
	extMap := make(map[string]struct{})
	for _, ext := range e.opts.Extensions {
		for _, part := range strings.Split(ext, ",") {
			p := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(part), "."))
			if p != "" {
				extMap[p] = struct{}{}
			}
		}
	}

	var (
		mu      sync.Mutex
		matches []SearchMatch
	)

	walkConf := fastwalk.Config{
		Follow: false,
	}

	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip unreadable safely
		}
		if path == root {
			return nil
		}

		name := d.Name()

		// Hidden check
		if !e.opts.IncludeHidden && isHiddenEntry(path, name, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		// Fast extension check
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		if len(extMap) > 0 {
			if _, ok := extMap[ext]; !ok {
				return nil
			}
		}

		// Category check
		cat := classifier.ClassifyExtension(ext)
		if e.opts.Type != "" {
			reqType := strings.ToLower(strings.TrimSpace(e.opts.Type))
			if strings.ToLower(cat) != reqType {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		// Size check
		if e.opts.SizeCondition != "" && !matchesSizeCondition(info.Size(), e.opts.SizeCondition) {
			return nil
		}

		// Date check
		if e.opts.DateCondition != "" && !matchesDateCondition(info.ModTime(), e.opts.DateCondition) {
			return nil
		}

		// Filename / path pattern match
		score := 1
		if e.opts.Pattern != "" {
			matched, matchScore := e.matchNameOrPath(name, path, patternRunes, re)
			if !matched {
				return nil
			}
			score = matchScore
		}

		// Content grep check (if requested)
		var (
			contentSnippet string
			contentLineNo  int
		)
		if e.opts.ContentQuery != "" {
			hasContent, snippet, lineNo := searchFileContent(path, e.opts.ContentQuery, e.opts.IgnoreCase)
			if !hasContent {
				return nil
			}
			contentSnippet = snippet
			contentLineNo = lineNo
		}

		relPath, _ := filepath.Rel(root, path)

		match := SearchMatch{
			File: models.File{
				Path:      path,
				RelPath:   relPath,
				Name:      name,
				Extension: ext,
				Size:      info.Size(),
				ModTime:   info.ModTime(),
				IsDir:     false,
				Category:  cat,
			},
			Score:         score,
			ContentMatch:  contentSnippet,
			ContentLineNo: contentLineNo,
		}

		mu.Lock()
		matches = append(matches, match)
		mu.Unlock()

		return nil
	}

	if err := fastwalk.Walk(&walkConf, root, walkFn); err != nil {
		return nil, err
	}

	// Sort results: highest score first, then newest
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].ModTime.After(matches[j].ModTime)
	})

	// Apply limit if requested
	if e.opts.Limit > 0 && len(matches) > e.opts.Limit {
		matches = matches[:e.opts.Limit]
	}

	return matches, nil
}

func (e *Engine) matchNameOrPath(name, path string, patternRunes []rune, re *regexp.Regexp) (bool, int) {
	if re != nil {
		if re.MatchString(name) || re.MatchString(path) {
			return true, 100
		}
		return false, 0
	}

	caseSensitive := !e.opts.IgnoreCase
	targetChars := util.ToChars([]byte(name))

	if e.opts.Exact {
		res, _ := algo.ExactMatchNaive(caseSensitive, true, true, &targetChars, patternRunes, false, nil)
		if res.Start >= 0 {
			return true, res.Score
		}
		return false, 0
	}

	// Default: fzf FuzzyMatchV2
	res, _ := algo.FuzzyMatchV2(caseSensitive, true, true, &targetChars, patternRunes, false, nil)
	if res.Start >= 0 {
		return true, res.Score
	}

	// Fallback to testing relative path if name didn't match
	pathChars := util.ToChars([]byte(filepath.ToSlash(path)))
	resPath, _ := algo.FuzzyMatchV2(caseSensitive, true, true, &pathChars, patternRunes, false, nil)
	if resPath.Start >= 0 {
		return true, resPath.Score / 2 // Slight penalty for matching path instead of filename
	}

	return false, 0
}

func searchFileContent(filePath, query string, ignoreCase bool) (bool, string, int) {
	file, err := os.Open(filePath)
	if err != nil {
		return false, "", 0
	}
	defer file.Close()

	var (
		queryBytes = []byte(query)
		scanner    = bufio.NewScanner(file)
		lineNum    = 0
	)

	if ignoreCase {
		queryBytes = bytes.ToLower(queryBytes)
	}

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		checkLine := line
		if ignoreCase {
			checkLine = bytes.ToLower(line)
		}

		if bytes.Contains(checkLine, queryBytes) {
			snippet := strings.TrimSpace(string(line))
			if len(snippet) > 120 {
				snippet = snippet[:117] + "..."
			}
			return true, snippet, lineNum
		}
	}

	return false, "", 0
}

func matchesSizeCondition(size int64, cond string) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return true
	}

	op := ">="
	numStr := cond

	if strings.HasPrefix(cond, ">=") {
		op = ">="
		numStr = strings.TrimPrefix(cond, ">=")
	} else if strings.HasPrefix(cond, "<=") {
		op = "<="
		numStr = strings.TrimPrefix(cond, "<=")
	} else if strings.HasPrefix(cond, ">") {
		op = ">"
		numStr = strings.TrimPrefix(cond, ">")
	} else if strings.HasPrefix(cond, "<") {
		op = "<"
		numStr = strings.TrimPrefix(cond, "<")
	} else if strings.HasPrefix(cond, "=") {
		op = "="
		numStr = strings.TrimPrefix(cond, "=")
	}

	bytesVal, err := parseBytes(numStr)
	if err != nil {
		return true
	}

	switch op {
	case ">":
		return size > bytesVal
	case ">=":
		return size >= bytesVal
	case "<":
		return size < bytesVal
	case "<=":
		return size <= bytesVal
	case "=":
		return size == bytesVal
	default:
		return size >= bytesVal
	}
}

func parseBytes(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	// Ordered by length descending so multi-char suffixes like "KB" match before "B"
	type unit struct {
		suffix string
		mult   int64
	}
	units := []unit{
		{"TB", 1024 * 1024 * 1024 * 1024},
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"T", 1024 * 1024 * 1024 * 1024},
		{"G", 1024 * 1024 * 1024},
		{"M", 1024 * 1024},
		{"K", 1024},
		{"B", 1},
	}

	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			rawNum := strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			val, err := strconv.ParseFloat(rawNum, 64)
			if err != nil {
				return 0, err
			}
			return int64(val * float64(u.mult)), nil
		}
	}

	return strconv.ParseInt(s, 10, 64)
}

func matchesDateCondition(modTime time.Time, cond string) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return true
	}

	now := time.Now()
	op := "<" // default: older than
	valStr := cond

	if strings.HasPrefix(cond, ">") {
		op = ">"
		valStr = strings.TrimPrefix(cond, ">")
	} else if strings.HasPrefix(cond, "<") {
		op = "<"
		valStr = strings.TrimPrefix(cond, "<")
	}

	valStr = strings.TrimSpace(valStr)

	// Check duration like 30d, 24h, 1y
	if strings.HasSuffix(valStr, "d") || strings.HasSuffix(valStr, "D") {
		days, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(valStr), "d"))
		if err == nil {
			targetTime := now.AddDate(0, 0, -days)
			if op == ">" {
				return modTime.After(targetTime) // newer than X days
			}
			return modTime.Before(targetTime) // older than X days
		}
	}

	// Try parsing standard dates like 2026-01-01
	parsedDate, err := time.Parse("2006-01-02", valStr)
	if err == nil {
		if op == ">" {
			return modTime.After(parsedDate)
		}
		return modTime.Before(parsedDate)
	}

	return true
}

func isHiddenEntry(path, name string, d fs.DirEntry) bool {
	if strings.HasPrefix(name, ".") && name != "." && name != ".." {
		return true
	}

	info, err := d.Info()
	if err == nil && info.Sys() != nil {
		if winInfo, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
			return winInfo.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
		}
	}

	return false
}
