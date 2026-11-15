package proxy

import (
	"mime"
	"net/http"
	"strings"
)

// CompositeMatcher runs several ecosystem matchers against a request and
// returns the first that recognizes it as in-scope. It lets one long-lived
// proxy cover every supported ecosystem at once — as the job-level
// `glab dependency-firewall job-proxy` does — instead of the per-run model where
// each package-manager command installs a single ecosystem's matcher.
//
// The first Matched result wins and short-circuits, so a matcher that reads an
// upload body is never run once an earlier matcher has already claimed the
// request. A request no matcher claims passes straight through (Matched false),
// exactly as a single matcher's non-match does.
type CompositeMatcher []Matcher

func (c CompositeMatcher) Match(req *http.Request) Match {
	for _, m := range c {
		if res := m.Match(req); res.Matched {
			return res
		}
	}
	return Match{}
}

// DefaultMatchers returns one matcher per supported ecosystem, for a job-level
// proxy that must recognize artifacts from any of them. The managers' own
// Matcher() methods return these same zero-value structs; listing them once
// here avoids running the npm (or PyPI) matcher several times for its family of
// managers (npm/pnpm/yarn; pip/poetry/uv/twine/pipenv).
func DefaultMatchers() CompositeMatcher {
	return CompositeMatcher{
		jsonPublishNPMMatcher{},
		registryPathPyPIMatcher{},
		MavenMatcher{},
		GemMatcher{},
		CargoMatcher{},
		NuGetMatcher{},
	}
}

// registryPathPyPIMatcher is PyPIMatcher for a proxy that carries a whole job's
// traffic. A bare sdist suffix (.tar.gz, .zip, .tgz, .tar.bz2) is common to any
// archive, so on a job-wide proxy a plain curl of a toolchain tarball (for
// example go.dev/dl/go1.22.3.linux-amd64.tar.gz) would be claimed as a PyPI
// sdist and evaluated against a made-up coordinate; a transient API error on it
// then fails the job closed. Claim a generic-archive download only when the path
// carries a "/packages/" segment — the layout files.pythonhosted.org and the
// GitLab PyPI registry both serve under. A .whl (and the twine upload POST) is
// python-specific, so it is still claimed on any path.
type registryPathPyPIMatcher struct{ PyPIMatcher }

func (m registryPathPyPIMatcher) Match(req *http.Request) Match {
	if req != nil && req.Method != http.MethodPost &&
		hasGenericSdistExt(req.URL.Path) && !strings.Contains(req.URL.Path, "/packages/") {
		return Match{}
	}
	return m.PyPIMatcher.Match(req)
}

func hasGenericSdistExt(path string) bool {
	for _, ext := range sdistExts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// jsonPublishNPMMatcher is NPMMatcher for a proxy that carries a whole job's
// traffic. NPMMatcher reads the body of every PUT, which is right when only npm
// is behind the proxy but would claim (and could block) any other upload in the
// job, such as a Maven deploy, a cargo publish or a curl -T. npm, pnpm and yarn
// send a publish as a JSON packument, so only a JSON PUT is inspected as one.
type jsonPublishNPMMatcher struct{ NPMMatcher }

func (m jsonPublishNPMMatcher) Match(req *http.Request) Match {
	if req != nil && req.Method == http.MethodPut {
		mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			return Match{}
		}
	}
	return m.NPMMatcher.Match(req)
}
