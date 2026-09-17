// Command govulncheck-to-gitlab converts `govulncheck -format json` output into
// a GitLab Dependency Scanning report.
//
// Only symbol-reachable findings are reported. govulncheck emits each
// vulnerability three times, at module, package and symbol level; the first two
// mean "this version is in our module graph", which is what the SBOM analyzer
// already tells us. A symbol-level finding means our code has a call path into
// the vulnerable function, which is the thing worth acting on.
//
// The exit code is 3 when a reachable finding is not on the ignore list, so a
// caller can gate on it, and 1 when the input could not be processed at all.
// Reporting nothing because govulncheck crashed must not look like success.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	exitFindings = 3
	exitError    = 1

	schemaVersion = "15.2.5"
	reportFile    = "go.mod"

	// The schema's start_time/end_time pattern rejects a timezone suffix, so
	// RFC3339 cannot be used here.
	scanTimeLayout = "2006-01-02T15:04:05"
)

// Accepted reachable vulnerabilities. Every entry needs an issue recording why
// the risk was accepted, so that "the pipeline is green" never silently means
// "somebody added a line here". Modelled on gitlab-org/gitaly's
// tools/govulncheck-filter.
var ignored = map[string]string{
	// "GO-0000-0000": "https://gitlab.com/gitlab-org/cli/-/issues/0",
}

type position struct {
	Filename string `json:"filename"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

type traceFrame struct {
	Module   string    `json:"module"`
	Version  string    `json:"version"`
	Package  string    `json:"package"`
	Function string    `json:"function"`
	Receiver string    `json:"receiver"`
	Position *position `json:"position"`
}

type finding struct {
	OSV          string       `json:"osv"`
	FixedVersion string       `json:"fixed_version"`
	Trace        []traceFrame `json:"trace"`
}

type osvEntry struct {
	ID      string   `json:"id"`
	Aliases []string `json:"aliases"`
	Summary string   `json:"summary"`
	Details string   `json:"details"`
}

type scanConfig struct {
	ScannerName    string `json:"scanner_name"`
	ScannerVersion string `json:"scanner_version"`
	DB             string `json:"db"`
	GoVersion      string `json:"go_version"`
}

type message struct {
	Config  *scanConfig `json:"config"`
	OSV     *osvEntry   `json:"osv"`
	Finding *finding    `json:"finding"`
}

type identifier struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
	URL   string `json:"url,omitempty"`
}

type pkg struct {
	Name string `json:"name"`
}

type dependency struct {
	Package pkg    `json:"package"`
	Version string `json:"version"`
}

type location struct {
	File       string     `json:"file"`
	Dependency dependency `json:"dependency"`
}

type link struct {
	URL string `json:"url"`
}

type vulnerability struct {
	ID          string       `json:"id"`
	Name        string       `json:"name,omitempty"`
	Description string       `json:"description,omitempty"`
	Severity    string       `json:"severity,omitempty"`
	Solution    string       `json:"solution,omitempty"`
	Identifiers []identifier `json:"identifiers"`
	Links       []link       `json:"links,omitempty"`
	Location    location     `json:"location"`
}

type vendor struct {
	Name string `json:"name"`
}

type analyzer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Vendor  vendor `json:"vendor"`
	URL     string `json:"url,omitempty"`
}

type scan struct {
	Analyzer  analyzer `json:"analyzer"`
	Scanner   analyzer `json:"scanner"`
	Type      string   `json:"type"`
	StartTime string   `json:"start_time"`
	EndTime   string   `json:"end_time"`
	Status    string   `json:"status"`
}

type report struct {
	Version         string          `json:"version"`
	Scan            scan            `json:"scan"`
	Vulnerabilities []vulnerability `json:"vulnerabilities"`
}

// decodeStream reads the concatenated JSON objects govulncheck writes. It is a
// stream of separate values rather than an array, so json.Decoder is used
// directly instead of unmarshalling the whole input.
func decodeStream(r io.Reader) ([]message, error) {
	var msgs []message
	dec := json.NewDecoder(r)
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return msgs, nil
			}
			return nil, fmt.Errorf("decode: %w", err)
		}
		msgs = append(msgs, m)
	}
}

func reachable(f *finding) bool {
	for _, t := range f.Trace {
		if t.Function != "" {
			return true
		}
	}
	return false
}

// vulnID derives a stable identifier so re-running the scan does not create a
// new vulnerability record for an unchanged finding.
func vulnID(osv, module, version string) string {
	sum := sha256.Sum256([]byte(osv + "\x00" + module + "\x00" + version))
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func identifiersFor(o *osvEntry) []identifier {
	ids := []identifier{{
		Type:  "go-vuln",
		Name:  o.ID,
		Value: o.ID,
		URL:   "https://pkg.go.dev/vuln/" + o.ID,
	}}
	for _, alias := range o.Aliases {
		switch {
		case strings.HasPrefix(alias, "CVE-"):
			ids = append(ids, identifier{Type: "cve", Name: alias, Value: alias})
		case strings.HasPrefix(alias, "GHSA-"):
			ids = append(ids, identifier{Type: "ghsa", Name: alias, Value: alias})
		}
	}
	return ids
}

func describe(o *osvEntry, f *finding, frame traceFrame) string {
	var b strings.Builder
	if o.Details != "" {
		b.WriteString(o.Details)
	}
	b.WriteString("\n\nReachable call path found by govulncheck:\n")
	for _, t := range f.Trace {
		if t.Function == "" {
			continue
		}
		name := t.Function
		if t.Receiver != "" {
			name = t.Receiver + "." + name
		}
		fmt.Fprintf(&b, "- %s.%s", t.Package, name)
		if t.Position != nil {
			fmt.Fprintf(&b, " (%s:%d)", t.Position.Filename, t.Position.Line)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nFound in %s@%s.", frame.Module, frame.Version)
	return b.String()
}

func convert(msgs []message, now time.Time) (report, []string, error) {
	var cfg scanConfig
	sawConfig := false
	osvs := map[string]*osvEntry{}
	var findings []*finding

	for i := range msgs {
		switch {
		case msgs[i].Config != nil:
			cfg = *msgs[i].Config
			sawConfig = true
		case msgs[i].OSV != nil:
			osvs[msgs[i].OSV.ID] = msgs[i].OSV
		case msgs[i].Finding != nil && reachable(msgs[i].Finding):
			findings = append(findings, msgs[i].Finding)
		}
	}

	// govulncheck always emits a config message first. Without one the input is
	// not a scan that ran, and reporting zero vulnerabilities would turn a
	// crashed scanner into a passing build.
	if !sawConfig {
		return report{}, nil, errors.New("no govulncheck config message in input")
	}

	seen := map[string]struct{}{}
	// Empty rather than nil: a clean scan is the expected outcome, and the
	// schema requires an array, which a nil slice marshals to as null.
	vulns := []vulnerability{}
	var unignored []string

	for _, f := range findings {
		if _, ok := seen[f.OSV]; ok {
			continue
		}
		seen[f.OSV] = struct{}{}

		o, ok := osvs[f.OSV]
		if !ok {
			o = &osvEntry{ID: f.OSV}
		}

		// The first frame is the vulnerable function itself, so it carries the
		// module and version that need upgrading.
		var frame traceFrame
		if len(f.Trace) > 0 {
			frame = f.Trace[0]
		}

		solution := ""
		if f.FixedVersion != "" {
			solution = fmt.Sprintf("Upgrade %s to %s.", frame.Module, f.FixedVersion)
		}

		vulns = append(vulns, vulnerability{
			ID:   vulnID(f.OSV, frame.Module, frame.Version),
			Name: o.Summary,
			// The Go vulnerability database carries no CVSS data, so there is
			// nothing to map a real severity from.
			Severity:    "Unknown",
			Description: describe(o, f, frame),
			Solution:    solution,
			Identifiers: identifiersFor(o),
			Links:       []link{{URL: "https://pkg.go.dev/vuln/" + f.OSV}},
			Location: location{
				File:       reportFile,
				Dependency: dependency{Package: pkg{Name: frame.Module}, Version: frame.Version},
			},
		})

		if _, ok := ignored[f.OSV]; !ok {
			unignored = append(unignored, f.OSV)
		}
	}

	sort.Slice(vulns, func(i, j int) bool { return vulns[i].ID < vulns[j].ID })
	sort.Strings(unignored)

	stamp := now.UTC().Format(scanTimeLayout)
	scanner := analyzer{
		ID:      "govulncheck",
		Name:    "govulncheck",
		Version: strings.TrimPrefix(cfg.ScannerVersion, "v"),
		Vendor:  vendor{Name: "Go"},
		URL:     "https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck",
	}

	return report{
		Version:         schemaVersion,
		Scan:            scan{Analyzer: scanner, Scanner: scanner, Type: "dependency_scanning", StartTime: stamp, EndTime: stamp, Status: "success"},
		Vulnerabilities: vulns,
	}, unignored, nil
}

func main() {
	msgs, err := decodeStream(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "govulncheck-to-gitlab: %v\n", err)
		os.Exit(exitError)
	}

	rep, unignored, err := convert(msgs, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "govulncheck-to-gitlab: %v\n", err)
		os.Exit(exitError)
	}

	// IOStreams.PrintJSON is for glab commands; this is a standalone build
	// script whose stdout is a report file consumed by CI.
	out, err := json.MarshalIndent(rep, "", "  ") //nolint:forbidigo
	if err != nil {
		fmt.Fprintf(os.Stderr, "govulncheck-to-gitlab: %v\n", err)
		os.Exit(exitError)
	}
	if _, err := os.Stdout.Write(append(out, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "govulncheck-to-gitlab: %v\n", err)
		os.Exit(exitError)
	}

	for _, id := range unignored {
		fmt.Fprintf(os.Stderr, "reachable vulnerability not on the ignore list: %s (https://pkg.go.dev/vuln/%s)\n", id, id)
	}
	if len(unignored) > 0 {
		os.Exit(exitFindings)
	}
	for id, issue := range ignored {
		fmt.Fprintf(os.Stderr, "ignored: %s (%s)\n", id, issue)
	}
}
