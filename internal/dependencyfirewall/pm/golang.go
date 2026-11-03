package pm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/gomodproxy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/proxy"
)

var goFetcherSettings = []string{"GONOPROXY", "GONOSUMDB", "GOPRIVATE", "GOSUMDB", "GOINSECURE"}

var goSettings = append([]string{"GOPROXY"}, goFetcherSettings...)

const goNothingCheckedNotice = "No Go module downloads were checked in this run. Modules already in the module cache, vendored code, and local replace targets are not checked."

type golangManager struct {
	newBackend func(goBin string, proxiedSumDBs []string, envOverrides map[string]string) (http.Handler, func(), error)
	server     *gomodproxy.Server
}

// Golang returns the go package manager. Module zips are checked by a
// loopback Go module proxy rather than the MITM proxy: go and git on macOS
// trust only the system store, so they reject the MITM CA.
func Golang() PackageManager { return &golangManager{newBackend: newGoBackend} }

func newGoBackend(goBin string, proxiedSumDBs []string, envOverrides map[string]string) (http.Handler, func(), error) {
	b, err := gomodproxy.NewBackend(goBin, proxiedSumDBs, envOverrides)
	if err != nil {
		return nil, nil, err
	}
	return b, b.Close, nil
}

func (*golangManager) Name() string   { return "go" }
func (*golangManager) Binary() string { return "go" }

//   - GONOPROXY uses the sentinel "none" because go reads the go env file
//     for an empty value.
//   - GO111MODULE=on closes GOPATH-mode go get, which clones over git.
//   - go skips toolchain checksum verification when GIT_HTTP_USER_AGENT
//     mentions proxy.golang.org.
func (*golangManager) Environment(string) []string {
	return []string{
		"GOPROXY=off",
		"GONOPROXY=none",
		"GO111MODULE=on",
		"GIT_HTTP_USER_AGENT=",
	}
}

func (m *golangManager) Setup(ctx context.Context, in setupInput) (setupResult, error) {
	settings, err := goPreflight(ctx, in.exec, in.bin, in.parent)
	if err != nil {
		return setupResult{}, err
	}
	if settings["GOPROXY"] == "off" {
		return setupResult{env: []string{"GOPROXY=off"}, stop: func() { in.io.LogError(goNothingCheckedNotice) }}, nil
	}

	overrides := map[string]string{"GOPROXY": settings["GOPROXY"]}
	// "none" is a pattern that matches nothing, which keeps an empty list
	// from falling back to GOPRIVATE. GOSUMDB names a database, not a
	// pattern, so an empty one stays empty and go reports it as missing.
	for _, k := range goFetcherSettings {
		v := settings[k]
		if v == "" && k != "GOSUMDB" {
			v = "none"
		}
		overrides[k] = v
	}
	childSumDB, proxied := checksumDB(settings["GOSUMDB"])
	handler, closeBackend, err := m.newBackend(in.bin, proxied, overrides)
	if err != nil {
		return setupResult{}, fmt.Errorf("failed to create the Go module proxy: %w", err)
	}
	server := gomodproxy.New(in.checker, in.projectID, handler)
	if err := server.Start(); err != nil {
		closeBackend()
		return setupResult{}, fmt.Errorf("failed to start the Go module proxy: %w", err)
	}
	m.server = server
	env := []string{"GOPROXY=" + server.BaseURL()}
	if childSumDB != settings["GOSUMDB"] {
		env = append(env, "GOSUMDB="+childSumDB)
	}
	return setupResult{
		env: env,
		stop: func() { //nolint:contextcheck // server shutdown manages its own context internally
			server.Stop()
			closeBackend()
			if server.CheckedZips() == 0 {
				in.io.LogError(goNothingCheckedNotice)
			}
		},
		verdicts: server.Verdicts,
	}, nil
}

// goPreflight runs go env from an empty directory so no go.mod or go.work
// applies, with GOTOOLCHAIN=local so it cannot download a toolchain. GOFLAGS
// is pinned because the user's could carry -changed, which drops every
// setting still at its default from the output.
func goPreflight(ctx context.Context, exec Executor, bin string, parent []string) (map[string]string, error) {
	dir, err := os.MkdirTemp("", "glab-df-go-env-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(dir)

	var stdout, stderr bytes.Buffer
	args := append([]string{"env", "-C", dir, "-json"}, goSettings...)
	env := dedupEnv(append(slices.Clip(parent), "GOTOOLCHAIN=local", "GOFLAGS=-changed=false"))
	if err := exec.ExecWithIO(ctx, bin, args, env, nil, &stdout, &stderr); err != nil {
		return nil, fmt.Errorf("failed to read the Go settings with go env: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var settings map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &settings); err != nil {
		return nil, fmt.Errorf("failed to parse go env output: %w", err)
	}
	return settings, nil
}

// checksumDB returns the GOSUMDB the child uses and the server's
// ProxiedSumDBs entry for it. go looks a database up through the module
// proxy only when GOSUMDB names no URL; an explicit URL it contacts directly,
// through the MITM proxy. So the child gets the key alone and the server
// fetches from the URL. sum.golang.google.cn is go's alias for sum.golang.org
// served from that host.
func checksumDB(gosumdb string) (string, []string) {
	resolved := gosumdb
	if resolved == "sum.golang.google.cn" {
		resolved = "sum.golang.org https://sum.golang.google.cn"
	}
	fields := strings.Fields(resolved)
	if len(fields) == 0 || resolved == "off" {
		return gosumdb, nil
	}
	name, _, _ := strings.Cut(fields[0], "+")
	if len(fields) != 2 {
		return gosumdb, []string{name}
	}
	return fields[0], []string{name + " " + fields[1]}
}

func (m *golangManager) checkedZips() int {
	if m.server == nil {
		return 0
	}
	return m.server.CheckedZips()
}

func (*golangManager) CATrustEnviron(caPath string) []string {
	return []string{"SSL_CERT_FILE=" + caPath}
}

func (*golangManager) ExistingBundleVars() []string { return []string{"SSL_CERT_FILE"} }

func (*golangManager) CleanupCAFiles(string) {}

func (*golangManager) Matcher() proxy.Matcher { return passThroughMatcher{} }

type passThroughMatcher struct{}

func (passThroughMatcher) Match(*http.Request) proxy.Match { return proxy.Match{} }
