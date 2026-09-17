package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const configMsg = `{"config":{"scanner_name":"govulncheck","scanner_version":"v1.8.0","db":"https://vuln.go.dev","go_version":"go1.27.0"}}`

func osvMsg(id, alias string) string {
	return `{"osv":{"id":"` + id + `","aliases":["` + alias + `","GHSA-aaaa-bbbb-cccc"],` +
		`"summary":"Something bad in example.com/mod","details":"Longer description."}}`
}

func findingMsg(id, level string) string {
	frame := `{"module":"example.com/mod","version":"v1.0.0"`
	switch level {
	case "symbol":
		frame += `,"package":"example.com/mod/pkg","function":"Vulnerable","receiver":"*T",` +
			`"position":{"filename":"pkg/file.go","line":42,"column":3}`
	case "package":
		frame += `,"package":"example.com/mod/pkg"`
	}
	frame += `}`
	return `{"finding":{"osv":"` + id + `","fixed_version":"v1.2.3","trace":[` + frame + `]}}`
}

func convertString(t *testing.T, input string) (report, []string, error) {
	t.Helper()
	msgs, err := decodeStream(strings.NewReader(input))
	require.NoError(t, err)
	return convert(msgs, time.Date(2026, 9, 16, 17, 4, 5, 0, time.UTC))
}

func TestConvertReportsOnlyReachableFindings(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		configMsg,
		osvMsg("GO-2026-0001", "CVE-2026-1111"),
		osvMsg("GO-2026-0002", "CVE-2026-2222"),
		findingMsg("GO-2026-0001", "module"),
		findingMsg("GO-2026-0001", "package"),
		findingMsg("GO-2026-0001", "symbol"),
		findingMsg("GO-2026-0002", "module"),
	}, "\n")

	rep, unignored, err := convertString(t, input)
	require.NoError(t, err)

	require.Len(t, rep.Vulnerabilities, 1, "module-only findings must not be reported")
	assert.Equal(t, []string{"GO-2026-0001"}, unignored)

	v := rep.Vulnerabilities[0]
	assert.Equal(t, "Something bad in example.com/mod", v.Name)
	assert.Equal(t, "Unknown", v.Severity)
	assert.Equal(t, "Upgrade example.com/mod to v1.2.3.", v.Solution)
	assert.Equal(t, "go.mod", v.Location.File)
	assert.Equal(t, "example.com/mod", v.Location.Dependency.Package.Name)
	assert.Equal(t, "v1.0.0", v.Location.Dependency.Version)
	assert.Contains(t, v.Description, "example.com/mod/pkg.*T.Vulnerable (pkg/file.go:42)")
}

func TestConvertEmitsCVEAndGHSAIdentifiers(t *testing.T) {
	t.Parallel()

	rep, _, err := convertString(t, strings.Join([]string{
		configMsg,
		osvMsg("GO-2026-0001", "CVE-2026-1111"),
		findingMsg("GO-2026-0001", "symbol"),
	}, "\n"))
	require.NoError(t, err)
	require.Len(t, rep.Vulnerabilities, 1)

	types := map[string]string{}
	for _, id := range rep.Vulnerabilities[0].Identifiers {
		types[id.Type] = id.Value
	}
	// The CVE and GHSA aliases are what let GitLab deduplicate this finding
	// against the SBOM analyzer's report for the same vulnerability.
	assert.Equal(t, "GO-2026-0001", types["go-vuln"])
	assert.Equal(t, "CVE-2026-1111", types["cve"])
	assert.Equal(t, "GHSA-aaaa-bbbb-cccc", types["ghsa"])
}

// Not parallel: the subtests swap the package-level ignore list.
func TestConvertIgnoreList(t *testing.T) {
	input := strings.Join([]string{
		configMsg,
		osvMsg("GO-2026-0001", "CVE-2026-1111"),
		findingMsg("GO-2026-0001", "symbol"),
	}, "\n")

	msgs, err := decodeStream(strings.NewReader(input))
	require.NoError(t, err)

	t.Run("not ignored", func(t *testing.T) {
		_, unignored, err := convert(msgs, time.Now())
		require.NoError(t, err)
		assert.Equal(t, []string{"GO-2026-0001"}, unignored)
	})

	t.Run("ignored", func(t *testing.T) {
		original := ignored
		t.Cleanup(func() { ignored = original })
		ignored = map[string]string{"GO-2026-0001": "https://gitlab.com/gitlab-org/cli/-/issues/1"}

		rep, unignored, err := convert(msgs, time.Now())
		require.NoError(t, err)
		assert.Empty(t, unignored, "an ignored finding must not gate the pipeline")
		assert.Len(t, rep.Vulnerabilities, 1, "an ignored finding is still reported")
	})
}

func TestConvertRejectsInputWithoutConfig(t *testing.T) {
	t.Parallel()

	// A crashed govulncheck produces no output. Treating that as a clean scan
	// would report success for a check that never ran.
	_, _, err := convertString(t, "")
	require.Error(t, err)

	_, _, err = convertString(t, osvMsg("GO-2026-0001", "CVE-2026-1111"))
	require.Error(t, err)
}

func TestConvertScanMetadata(t *testing.T) {
	t.Parallel()

	rep, _, err := convertString(t, configMsg)
	require.NoError(t, err)

	assert.Empty(t, rep.Vulnerabilities)
	assert.Equal(t, "dependency_scanning", rep.Scan.Type)
	assert.Equal(t, "success", rep.Scan.Status)
	assert.Equal(t, "1.8.0", rep.Scan.Scanner.Version)
	// The schema's timestamp pattern rejects a timezone suffix.
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$`, rep.Scan.StartTime)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$`, rep.Scan.EndTime)
}

func TestConvertCleanScanMarshalsVulnerabilitiesAsArray(t *testing.T) {
	t.Parallel()

	// A clean scan is the usual outcome, and the schema types vulnerabilities
	// as an array. A nil slice marshals to null, which fails validation, so
	// assert on the encoded form: assert.Empty passes for nil as well.
	rep, _, err := convertString(t, configMsg)
	require.NoError(t, err)
	require.NotNil(t, rep.Vulnerabilities)

	out, err := json.Marshal(rep)
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &decoded))
	assert.JSONEq(t, `[]`, string(decoded["vulnerabilities"]))
}

func TestVulnIDIsStable(t *testing.T) {
	t.Parallel()

	first := vulnID("GO-2026-0001", "example.com/mod", "v1.0.0")
	assert.Equal(t, first, vulnID("GO-2026-0001", "example.com/mod", "v1.0.0"))
	assert.NotEqual(t, first, vulnID("GO-2026-0001", "example.com/mod", "v1.0.1"))
}
