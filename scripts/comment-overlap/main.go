// Flag comments whose content is inferable from the code they sit next to.
//
// Usage:
//
//	go run ./scripts/comment-overlap ./internal/...
//	go run ./scripts/comment-overlap -threshold 0.6 -sample 25 ./...
//	go run ./scripts/comment-overlap -json ./internal/commands/mr/...
//
// The signal is token coverage: a comment whose words are mostly already
// present in the adjacent code restates that code, so it carries no
// information a reader could not get from the line itself. This is a
// mechanical proxy for "document only what is non-obvious from the code".
//
// Doc comments are skipped by position, using go/ast Doc fields rather than
// text heuristics, so idiomatic godoc on an exported symbol is never flagged
// for repeating its own symbol name. Only inline comments are considered.
//
// Comment tokens are matched against code tokens after camelCase and
// snake_case splitting, so "clear env vars" is recognised as covered by
// env.RemoveVar(...).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	excludeRe = regexp.MustCompile(`(^|/)(vendor|docs|testdata|node_modules)/|\.pb\.go$|_generated\.go$|(^|/)mock_[^/]*\.go$`)
	wordRe    = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*`)

	// A numbered step labels its position in a sequence, which is information
	// the code does not carry even when the label itself restates the call.
	// Removing some steps of a run would leave the remaining numbering broken.
	stepRe = regexp.MustCompile(`^\d+\.\s`)

	stop = map[string]struct{}{
		"the": {}, "a": {}, "an": {}, "to": {}, "of": {}, "in": {},
		"on": {}, "for": {}, "and": {}, "or": {}, "is": {}, "are": {},
		"be": {}, "with": {}, "this": {}, "that": {}, "it": {},
		"as": {}, "at": {}, "by": {}, "from": {}, "into": {}, "we": {},
		"if": {}, "else": {}, "return": {}, "func": {}, "var": {},
		"err": {}, "nil": {},
	}

	// Words that signal rationale rather than description. Exempting these is
	// Orbit's precision tactic; -why-exempt makes it measurable rather than
	// assumed, since the list is common enough to swallow real findings.
	why = map[string]struct{}{
		"because": {}, "so": {}, "since": {}, "otherwise": {},
		"must": {}, "note": {}, "safety": {}, "gotcha": {},
		"invariant": {}, "intentionally": {}, "deliberately": {},
		"avoid": {}, "prevents": {}, "ensures": {}, "would": {},
		"cannot": {}, "never": {}, "always": {}, "only": {},
		"instead": {}, "rather": {}, "workaround": {}, "hack": {},
		"race": {}, "deadlock": {}, "panic": {}, "assumes": {},
		"requires": {}, "needs": {}, "guarantees": {},
	}
)

type finding struct {
	File     string  `json:"file"`
	Line     int     `json:"line"`
	Coverage float64 `json:"coverage"`
	Comment  string  `json:"comment"`
	Code     string  `json:"code"`
}

func main() {
	threshold := flag.Float64("threshold", 0.6, "minimum token coverage to flag")
	whyExempt := flag.Bool("why-exempt", false, "skip comments containing a rationale word")
	sample := flag.Int("sample", 0, "print at most N findings, evenly spaced")
	asJSON := flag.Bool("json", false, "emit JSON")
	maxFindings := flag.Int("max-findings", -1, "exit 1 if findings exceed this; -1 disables the gate")
	flag.Parse()

	targets := flag.Args()
	if len(targets) == 0 {
		targets = []string{"./..."}
	}

	files, err := collect(targets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "comment-overlap: %v\n", err)
		os.Exit(2)
	}

	var found []finding
	scanned := 0
	for _, path := range files {
		fs, err := analyze(path, *threshold, *whyExempt)
		if err != nil {
			continue
		}
		scanned++
		found = append(found, fs...)
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].Coverage != found[j].Coverage {
			return found[i].Coverage > found[j].Coverage
		}
		return found[i].File < found[j].File
	})

	shown := found
	if *sample > 0 && len(found) > *sample {
		shown = nil
		step := float64(len(found)) / float64(*sample)
		for i := 0; i < *sample; i++ {
			shown = append(shown, found[int(float64(i)*step)])
		}
	}

	if *asJSON {
		//nolint:forbidigo // dev script; IOStreams.PrintJSON is for CLI commands
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"files_scanned": scanned, "findings": len(found), "shown": shown,
		})
	} else {
		for _, f := range shown {
			fmt.Printf("%s:%d\t%.2f\t// %s\n\t\t-> %s\n", f.File, f.Line, f.Coverage, f.Comment, f.Code)
		}
		fmt.Fprintf(os.Stderr, "# %d findings across %d files\n", len(found), scanned)
	}

	if *maxFindings >= 0 && len(found) > *maxFindings {
		fmt.Fprintf(os.Stderr,
			"comment-overlap: %d findings exceed the allowed %d; these comments restate the code they sit next to\n",
			len(found), *maxFindings)
		os.Exit(1)
	}
}

func collect(targets []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, t := range targets {
		root := strings.TrimSuffix(strings.TrimSuffix(t, "..."), "/")
		if root == "" || root == "." {
			root = "."
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			// Surfaced rather than skipped: a file this gate cannot read is a
			// file it cannot check, which must not look like a pass.
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || excludeRe.MatchString(path) || has(seen, path) {
				return nil
			}
			seen[path] = struct{}{}
			out = append(out, path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func analyze(path string, threshold float64, whyExempt bool) ([]finding, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	lineOf := func(p token.Pos) int { return fset.Position(p).Line }
	colOf := func(p token.Pos) int { return fset.Position(p).Column }

	docLines := map[int]struct{}{}
	markDoc := func(g *ast.CommentGroup) {
		if g == nil {
			return
		}
		for i := lineOf(g.Pos()); i <= lineOf(g.End()); i++ {
			docLines[i] = struct{}{}
		}
	}
	markDoc(f.Doc)
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			markDoc(d.Doc)
		case *ast.GenDecl:
			markDoc(d.Doc)
		case *ast.TypeSpec:
			markDoc(d.Doc)
		case *ast.ValueSpec:
			markDoc(d.Doc)
		case *ast.Field:
			markDoc(d.Doc)
		}
		return true
	})

	commentLines := map[int]struct{}{}
	for _, g := range f.Comments {
		for i := lineOf(g.Pos()); i <= lineOf(g.End()); i++ {
			commentLines[i] = struct{}{}
		}
	}

	raw := strings.Split(string(src), "\n")
	isCode := func(n int) bool {
		if n < 1 || n > len(raw) {
			return false
		}
		return !has(commentLines, n) && strings.TrimSpace(raw[n-1]) != ""
	}

	var out []finding
	for _, g := range f.Comments {
		start, end := lineOf(g.Pos()), lineOf(g.End())
		if has(docLines, start) {
			continue
		}

		// A block comment with code on both sides annotates that code rather
		// than describing a following statement, as in an unnamed return type.
		if endLine, endCol := lineOf(g.End()), colOf(g.End()); endLine == start &&
			endCol-1 <= len(raw[start-1]) &&
			strings.TrimSpace(raw[start-1][endCol-1:]) != "" {
			continue
		}

		// A trailing comment describes the line it sits on, not the next one.
		var codeLine int
		if colOf(g.Pos()) > 1 && strings.TrimSpace(strings.Split(raw[start-1], "//")[0]) != "" {
			codeLine = start
		} else {
			for n := end + 1; n <= len(raw) && n <= end+3; n++ {
				if isCode(n) {
					codeLine = n
					break
				}
			}
		}
		if codeLine == 0 {
			continue
		}

		text := commentText(g)
		if stepRe.MatchString(text) {
			continue
		}
		ct := tokenize(text)
		if len(ct) < 2 {
			continue
		}
		if whyExempt && hasWhy(text) {
			continue
		}

		codeSrc := raw[codeLine-1]
		if codeLine == start {
			codeSrc = strings.Split(codeSrc, "//")[0]
		}
		cov := coverage(ct, tokenize(codeSrc))
		if cov >= threshold {
			out = append(out, finding{
				File: path, Line: start, Coverage: cov,
				Comment: collapse(text), Code: collapse(codeSrc),
			})
		}
	}
	return out, nil
}

func commentText(g *ast.CommentGroup) string {
	var b strings.Builder
	for _, c := range g.List {
		t := c.Text
		t = strings.TrimPrefix(t, "//")
		t = strings.TrimPrefix(t, "/*")
		t = strings.TrimSuffix(t, "*/")
		b.WriteString(" ")
		b.WriteString(t)
	}
	return strings.TrimSpace(b.String())
}

func tokenize(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range wordRe.FindAllString(s, -1) {
		for _, part := range splitIdent(w) {
			p := strings.ToLower(part)
			if p == "" || has(stop, p) {
				continue
			}
			out[stem(p)] = struct{}{}
		}
	}
	return out
}

// splitIdent breaks Go identifiers into words so comment prose can be matched
// against camelCase and snake_case code tokens.
func splitIdent(s string) []string {
	var parts []string
	for chunk := range strings.SplitSeq(s, "_") {
		start := 0
		runes := []rune(chunk)
		for i := 1; i < len(runes); i++ {
			prev, cur := runes[i-1], runes[i]
			boundary := unicode.IsLower(prev) && unicode.IsUpper(cur)
			if boundary {
				parts = append(parts, string(runes[start:i]))
				start = i
			}
		}
		if start < len(runes) {
			parts = append(parts, string(runes[start:]))
		}
	}
	return parts
}

func stem(s string) string {
	if len(s) > 3 && strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		return strings.TrimSuffix(s, "s")
	}
	return s
}

func coverage(comment, code map[string]struct{}) float64 {
	if len(comment) == 0 {
		return 0
	}
	matched := 0
	for t := range comment {
		if has(code, t) {
			matched++
			continue
		}
		for c := range code {
			// Both sides need length: a one-character code token such as a
			// receiver name would otherwise substring-match any word.
			if len(t) >= 4 && len(c) >= 4 && (strings.Contains(c, t) || strings.Contains(t, c)) {
				matched++
				break
			}
		}
	}
	return float64(matched) / float64(len(comment))
}

func hasWhy(text string) bool {
	for _, w := range wordRe.FindAllString(strings.ToLower(text), -1) {
		if has(why, w) {
			return true
		}
	}
	return strings.Contains(text, "http://") || strings.Contains(text, "https://")
}

func collapse(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 90 {
		s = s[:90] + "…"
	}
	return s
}

func has[K comparable](m map[K]struct{}, k K) bool {
	_, ok := m[k]
	return ok
}
