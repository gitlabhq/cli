package gomodproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

// goFetcher implements goproxy.Fetcher by running go with the user's
// GOPROXY, so go itself applies .netrc and GOAUTH credentials, scheme-less
// and file:// entries, GOINSECURE, and the "," and "|" fallbacks. goproxy's
// GoFetcher fetches proxy entries in process and supports none of the
// credential sources except a password in the URL.
type goFetcher struct {
	goBin string
	env   []string
	dir   string
}

func (f *goFetcher) Query(ctx context.Context, path, query string) (string, time.Time, error) {
	var info struct {
		Version string
		Time    time.Time
	}
	if err := f.run(ctx, &info, "list", "-m", "-json", path+"@"+query); err != nil {
		return "", time.Time{}, err
	}
	return info.Version, info.Time, nil
}

func (f *goFetcher) List(ctx context.Context, path string) ([]string, error) {
	var list struct{ Versions []string }
	if err := f.run(ctx, &list, "list", "-m", "-versions", "-retracted", "-json", path); err != nil {
		return nil, err
	}
	return list.Versions, nil
}

func (f *goFetcher) Download(ctx context.Context, path, version string) (io.ReadSeekCloser, io.ReadSeekCloser, io.ReadSeekCloser, error) {
	var files struct{ Info, GoMod, Zip string }
	if err := f.run(ctx, &files, "mod", "download", "-json", path+"@"+version); err != nil {
		return nil, nil, nil, err
	}
	var opened []*os.File
	for _, name := range []string{files.Info, files.GoMod, files.Zip} {
		file, err := os.Open(name)
		if err != nil {
			for _, o := range opened {
				o.Close()
			}
			return nil, nil, nil, err
		}
		opened = append(opened, file)
	}
	return opened[0], opened[1], opened[2], nil
}

func (f *goFetcher) run(ctx context.Context, out any, args ...string) error {
	cmd := exec.CommandContext(ctx, f.goBin, args...)
	cmd.Env = f.env
	cmd.Dir = f.dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err == nil {
		return json.Unmarshal(stdout, out)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	// go mod download -json reports its failure on stdout; go list on stderr.
	msg := stderr.String()
	var reported struct{ Error string }
	if json.Unmarshal(stdout, &reported) == nil && reported.Error != "" {
		msg = reported.Error
	}
	msg = redactUserinfo(strings.TrimSpace(strings.TrimPrefix(msg, "go: ")))
	if msg == "" {
		msg = err.Error()
	}
	if isNotFound(msg) {
		return notFoundError{msg}
	}
	if failure, ok := ctx.Value(fetchFailureKey{}).(*fetchFailure); ok {
		failure.msg = msg
	}
	return errors.New(msg)
}

// notFoundPhrases are the cmd/go diagnostics for a module or version the
// upstream does not have. goproxy answers them with 404, which lets go try
// the next candidate module path; any other failure ends go's query.
var notFoundPhrases = []string{
	"404 Not Found",
	"410 Gone",
	"unknown revision",
	"invalid version",
	"no matching versions",
	"unrecognized import path",
	"malformed module path",
	"disabled by GOPROXY=off",
	"disabled by GOPRIVATE/GONOPROXY",
	// A file:// entry without the requested file.
	"no such file or directory",
	"The system cannot find the",
}

func isNotFound(msg string) bool {
	return slices.ContainsFunc(notFoundPhrases, func(p string) bool { return strings.Contains(msg, p) })
}

type notFoundError struct{ msg string }

func (e notFoundError) Error() string      { return e.msg }
func (notFoundError) Is(target error) bool { return target == fs.ErrNotExist }

type fetchFailureKey struct{}

type fetchFailure struct{ msg string }

// userinfoPattern matches the userinfo of a URL. go redacts only a URL's
// password, and a token is often passed as the user name.
var userinfoPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s"']+@`)

func redactUserinfo(s string) string {
	return userinfoPattern.ReplaceAllString(s, "${1}xxxxx@")
}
