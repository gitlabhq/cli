package test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"testing"

	"gitlab.com/gitlab-org/cli/internal/run"
)

// TODO copypasta from command package
type CmdOut struct {
	OutBuf, ErrBuf *bytes.Buffer
}

func (c CmdOut) String() string {
	return c.OutBuf.String()
}

func (c CmdOut) Stderr() string {
	return c.ErrBuf.String()
}

// OutputStub implements a simple utils.Runnable
type OutputStub struct {
	Out   []byte
	Error error
}

func (s OutputStub) Output() ([]byte, error) {
	if s.Error != nil {
		return s.Out, s.Error
	}
	return s.Out, nil
}

func (s OutputStub) Run() error {
	if s.Error != nil {
		return s.Error
	}
	return nil
}

type CmdStubber struct {
	Stubs []*OutputStub
	Count int
	Calls []*exec.Cmd
}

func InitCmdStubber() (*CmdStubber, func()) {
	cs := CmdStubber{}
	teardown := run.SetPrepareCmd(createStubbedPrepareCmd(&cs))
	return &cs, teardown
}

func (cs *CmdStubber) Stub(desiredOutput string) {
	// TODO maybe have some kind of command mapping but going simple for now
	cs.Stubs = append(cs.Stubs, &OutputStub{[]byte(desiredOutput), nil})
}

func (cs *CmdStubber) StubError(errText string) {
	// TODO support error types beyond CmdError
	stderrBuff := bytes.NewBufferString(errText)
	args := []string{"stub"} // TODO make more real?
	err := errors.New(errText)
	cs.Stubs = append(cs.Stubs, &OutputStub{Error: &run.CmdError{
		Stderr: stderrBuff,
		Args:   args,
		Err:    err,
	}})
}

func createStubbedPrepareCmd(cs *CmdStubber) func(*exec.Cmd) run.Runnable {
	return func(cmd *exec.Cmd) run.Runnable {
		cs.Calls = append(cs.Calls, cmd)
		call := cs.Count
		cs.Count += 1
		if call >= len(cs.Stubs) {
			panic(fmt.Sprintf("more execs than stubs. most recent call: %v", cmd))
		}
		return cs.Stubs[call]
	}
}

type T interface {
	Helper()
	Errorf(string, ...any)
}

func ExpectLines(t T, output string, lines ...string) {
	t.Helper()
	var r *regexp.Regexp
	for _, l := range lines {
		r = regexp.MustCompile(l)
		if !r.MatchString(output) {
			t.Errorf("output did not match regexp /%s/\n> output\n%s\n", r, output)
			return
		}
	}
}

func ClearEnvironmentVariables(t *testing.T) {
	t.Helper()

	// prevent using environment variables for test
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	t.Setenv("GITLAB_ACCESS_TOKEN", "")
	t.Setenv("OAUTH_TOKEN", "")
	t.Setenv("USERNAME", "")
}

func GetHostOrSkip(tb testing.TB) string {
	tb.Helper()

	glTestHost := os.Getenv("GITLAB_TEST_HOST")
	if glTestHost == "" || os.Getenv("GITLAB_TOKEN_TEST") == "" {
		// since token requires `api` privileges we only run integration tests in the canonical project
		ciNamespace := os.Getenv("CI_PROJECT_NAMESPACE")
		if os.Getenv("CI") == "true" && (ciNamespace == "gitlab-org/cli" || ciNamespace == "gitlab-org/security/cli") {
			tb.Log("Expected GITLAB_TEST_HOST and GITLAB_TOKEN_TEST to be set in CI. Marking as failed.")
			tb.Fail()
		}
		tb.Skip("Set GITLAB_TEST_HOST and GITLAB_TOKEN_TEST to run this integration test")
	}
	tb.Setenv("GITLAB_TOKEN", os.Getenv("GITLAB_TOKEN_TEST"))
	return glTestHost
}

func ReturnBuffer(old *os.File, r *os.File, w *os.File) string {
	outC := make(chan string)
	// copy the output in a separate goroutine so printing can't block indefinitely
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		outC <- buf.String()
	}()

	// back to normal state
	w.Close()
	os.Stdout = old // restoring the real stdout
	out := <-outC

	return out
}
