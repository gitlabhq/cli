// Measure how much of a change is comment rather than code.
//
// Usage:
//
//	go run ./scripts/comment-ratio -base origin/main
//	go run ./scripts/comment-ratio -base <sha> -head <sha> -per-file
//	go run ./scripts/comment-ratio -base origin/main -max-inline-ratio 0.25
//
// Unlike a per-comment classifier, this makes no judgement about any
// individual comment, so rewording cannot move the number: only writing
// fewer comment lines can. That makes it a volume signal rather than a
// quality one.
//
// Added lines are classified against the full parsed file, so doc comments
// are attributed through go/ast Doc fields rather than guessed from the
// diff hunk. Lines are reported in three classes:
//
//	doc     comment lines attached to a declaration as its godoc
//	inline  every other comment line
//	code    non-blank, non-comment
//
// inline_ratio (inline/code) is the actionable signal. Doc comments are
// counted separately because documenting an exported symbol is required by
// convention and should not read as noise.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	excludeRe = regexp.MustCompile(`(^|/)(vendor|docs|testdata)/|\.pb\.go$|_generated\.go$|(^|/)mock_[^/]*\.go$`)
	hunkRe    = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)
)

type counts struct {
	Code   int `json:"code"`
	Inline int `json:"inline"`
	Doc    int `json:"doc"`
}

type fileResult struct {
	Path string `json:"path"`
	counts
}

func main() {
	base := flag.String("base", "", "revision to diff against (required)")
	head := flag.String("head", "", "revision to diff to; defaults to the working tree")
	asJSON := flag.Bool("json", false, "emit JSON")
	perFile := flag.Bool("per-file", false, "include per-file rows")
	maxRatio := flag.Float64("max-inline-ratio", -1, "exit 1 if inline/code exceeds this")
	flag.Parse()

	if *base == "" {
		fmt.Fprintln(os.Stderr, "comment-ratio: -base is required")
		os.Exit(2)
	}

	added, err := addedLines(*base, *head)
	if err != nil {
		fmt.Fprintf(os.Stderr, "comment-ratio: %v\n", err)
		os.Exit(2)
	}

	var total counts
	var rows []fileResult
	paths := make([]string, 0, len(added))
	for p := range added {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		src, err := fileSource(*head, path)
		if err != nil {
			continue
		}
		kinds, err := classify(path, src)
		if err != nil {
			continue
		}
		var c counts
		for line := range added[path] {
			if line < 1 || line > len(kinds) {
				continue
			}
			switch kinds[line-1] {
			case "code":
				c.Code++
			case "inline":
				c.Inline++
			case "doc":
				c.Doc++
			}
		}
		total.Code += c.Code
		total.Inline += c.Inline
		total.Doc += c.Doc
		if *perFile && (c.Inline > 0 || c.Code > 0) {
			rows = append(rows, fileResult{Path: path, counts: c})
		}
	}

	ratio := -1.0
	if total.Code > 0 {
		ratio = float64(total.Inline) / float64(total.Code)
	}

	if *asJSON {
		out := map[string]any{
			"code": total.Code, "inline": total.Inline, "doc": total.Doc,
			"inline_ratio": round(ratio), "files": rows,
		}
		//nolint:forbidigo // dev script; IOStreams.PrintJSON is for CLI commands
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	} else {
		for _, r := range rows {
			fmt.Printf("%s\tcode=%d inline=%d doc=%d\n", r.Path, r.Code, r.Inline, r.Doc)
		}
		fmt.Printf("# code=%d inline=%d doc=%d inline_ratio=%s\n",
			total.Code, total.Inline, total.Doc, fmtRatio(ratio))
	}

	if *maxRatio >= 0 && ratio > *maxRatio {
		fmt.Fprintf(os.Stderr, "comment-ratio: inline ratio %s exceeds %.2f\n",
			fmtRatio(ratio), *maxRatio)
		os.Exit(1)
	}
}

func addedLines(base, head string) (map[string]map[int]struct{}, error) {
	spec := base
	if head != "" {
		spec = base + "..." + head
	}
	out, err := exec.Command("git", "diff", "--unified=0", "--diff-filter=ACMR", spec, "--", "*.go").Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", spec, err)
	}

	result := map[string]map[int]struct{}{}
	var current string
	for line := range strings.SplitSeq(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			current = strings.TrimPrefix(line, "+++ b/")
			if !strings.HasSuffix(current, ".go") || excludeRe.MatchString(current) {
				current = ""
				continue
			}
			if result[current] == nil {
				result[current] = map[int]struct{}{}
			}
		case current != "" && strings.HasPrefix(line, "@@"):
			m := hunkRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			for i := start; i < start+count; i++ {
				result[current][i] = struct{}{}
			}
		}
	}
	for p, lines := range result {
		if len(lines) == 0 {
			delete(result, p)
		}
	}
	return result, nil
}

func fileSource(head, path string) ([]byte, error) {
	if head == "" {
		return os.ReadFile(path)
	}
	return exec.Command("git", "show", head+":"+path).Output()
}

func classify(path string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	lineOf := func(p token.Pos) int { return fset.Position(p).Line }

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
	kinds := make([]string, len(raw))
	for i, line := range raw {
		n := i + 1
		switch {
		case has(docLines, n):
			kinds[i] = "doc"
		case has(commentLines, n):
			kinds[i] = "inline"
		case strings.TrimSpace(line) == "":
			kinds[i] = "blank"
		default:
			kinds[i] = "code"
		}
	}
	return kinds, nil
}

func round(v float64) float64 {
	if v < 0 {
		return -1
	}
	return float64(int(v*1000+0.5)) / 1000
}

func fmtRatio(v float64) string {
	if v < 0 {
		return "n/a"
	}
	return strconv.FormatFloat(round(v), 'f', 3, 64)
}

func has[K comparable](m map[K]struct{}, k K) bool {
	_, ok := m[k]
	return ok
}
