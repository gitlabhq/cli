package proxy

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// PyPIMatcher recognizes PyPI artifact downloads (wheels/sdists served
// from files.pythonhosted.org) and twine uploads (POST multipart to
// upload.pypi.org/legacy/).
type PyPIMatcher struct {
	// limits bounds upload-body inspection. The zero value resolves to the
	// production defaults; tests set small values to exercise spill/over-limit.
	limits peekLimits
}

func (m PyPIMatcher) Match(req *http.Request) Match {
	if req == nil {
		return Match{}
	}
	if req.Method == http.MethodPost {
		return pypiUpload(req, m.limits.orDefault())
	}
	name, version, ok := pypiFile(req.URL.Path)
	if !ok {
		return Match{}
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "pypi", Name: name, Version: version},
		Operation:  policy.Download,
	}
}

// sdistExts are the source-distribution archive suffixes PyPI serves. Modern
// uploads are .tar.gz or .zip (PEP 527), but PyPI still serves the legacy
// .tar.bz2 and .tgz sdists for older packages; all carry the same
// "<name>-<version>" filename shape and are split identically. Recognizing
// every served format matters because an unmatched download is forwarded
// without a policy check (fail-open), so a missing extension is a bypass.
var sdistExts = []string{".tar.gz", ".zip", ".tar.bz2", ".tgz"}

// pypiFile parses a wheel or sdist filename at the end of a download path.
// Only .whl (wheel) and the sdistExts source archives are recognized; other
// formats degrade to no match.
func pypiFile(path string) (string, string, bool) {
	filename := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		filename = path[i+1:]
	}
	// A PEP 658 sidecar (<wheel>.whl.metadata) is often the only request pip
	// makes for a blocked package; treat it as the wheel it describes.
	filename = strings.TrimSuffix(filename, ".metadata")
	if base, ok := strings.CutSuffix(filename, ".whl"); ok {
		fields := strings.Split(base, "-")
		if len(fields) < 2 {
			return "", "", false
		}
		return normalizePyPIName(fields[0]), fields[1], true
	}
	for _, ext := range sdistExts {
		base, ok := strings.CutSuffix(filename, ext)
		if !ok {
			continue
		}
		i := strings.LastIndex(base, "-")
		if i <= 0 {
			return "", "", false
		}
		return normalizePyPIName(base[:i]), base[i+1:], true
	}
	return "", "", false
}

func pypiUpload(req *http.Request, limits peekLimits) Match {
	if req.Body == nil {
		return Match{}
	}
	// twine uploads target the PyPI legacy endpoint (upload.pypi.org/legacy/).
	// Anchor on that path so unrelated multipart POST traffic passing through
	// the proxy is not misclassified as a package upload, the way GemMatcher
	// anchors on "api/v1/gems" and MavenMatcher on the artifact path shape.
	if !strings.HasSuffix(strings.TrimRight(req.URL.Path, "/"), "/legacy") {
		return Match{}
	}
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return Match{}
	}
	peeked := peekUploadBody(req.Body, limits)
	req.Body = peeked.body
	if peeked.overLimit {
		return Match{
			Matched:    true,
			Operation:  policy.Upload,
			Coordinate: policy.Coordinate{Ecosystem: "pypi"},
			Reason:     "upload body too large to inspect for dependency firewall policy",
		}
	}
	name, version := pypiFieldsFromMultipart(peeked.data, params["boundary"])
	if name == "" {
		return Match{}
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "pypi", Name: normalizePyPIName(name), Version: version},
		Operation:  policy.Upload,
	}
}

var pypiNameSeparators = regexp.MustCompile(`[-_.]+`)

// normalizePyPIName applies PEP 503 normalization: lowercase and collapse
// any run of "-", "_", or "." into a single "-".
func normalizePyPIName(name string) string {
	return pypiNameSeparators.ReplaceAllString(strings.ToLower(name), "-")
}

// pypiFieldsFromMultipart returns the "name" and "version" form fields from
// a twine multipart upload body. It reads only the small text fields and
// skips the file content part to avoid buffering large uploads.
func pypiFieldsFromMultipart(body []byte, boundary string) (string, string) {
	if boundary == "" {
		return "", ""
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	var name, version string
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		field := part.FormName()
		if field != "name" && field != "version" {
			_ = part.Close()
			continue
		}
		val, err := io.ReadAll(io.LimitReader(part, 1024))
		if err != nil {
			dbg.Debugf("dependency firewall pypi matcher: failed to read multipart field %q: %v", field, err)
		}
		_ = part.Close()
		switch field {
		case "name":
			name = strings.TrimSpace(string(val))
		case "version":
			version = strings.TrimSpace(string(val))
		}
	}
	return name, version
}
