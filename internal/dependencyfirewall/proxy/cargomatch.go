package proxy

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// CargoMatcher recognizes crate downloads and publishes. Downloads take two
// shapes: the legacy registry API path
// ("/api/v1/crates/{crate}/{version}/download") and the sparse-index
// download of a "{crate}-{version}.crate" file (default since cargo 1.70).
// The sparse download URL is defined by the registry's config.json "dl"
// template and so varies by registry (crates.io serves
// "static.crates.io/crates/{crate}/{crate}-{version}.crate"; third parties
// such as Artifactory and Cloudsmith use their own path layouts), but the
// requested file is always named "{crate}-{version}.crate". Matching on that
// ".crate" filename keeps the matcher working behind third-party registries.
//
// Publishes are PUT "/api/v1/crates/new". The publish body is a
// length-prefixed frame: [u32 LE json_len][json][u32 LE crate_len][.crate];
// the JSON carries the name and vers.
type CargoMatcher struct {
	limits peekLimits
}

func (m CargoMatcher) Match(req *http.Request) Match {
	if req == nil {
		return Match{}
	}
	if req.Method == http.MethodPut {
		return cargoPublish(req, m.limits.orDefault())
	}
	name, version, result := cargoDownload(req.URL.Path)
	switch result {
	case cargoNotDownload:
		return Match{}
	case cargoUnparseable:
		return unparseableMatch("cargo", policy.Download, "unparseable crate download URL")
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "cargo", Name: name, Version: version},
		Operation:  policy.Download,
	}
}

// cargoDownloadResult classifies a request path relative to the crate
// download shapes: not a download at all, a recognized download shape whose
// coordinate could not be parsed (fail closed), or a parsed coordinate.
type cargoDownloadResult int

const (
	cargoNotDownload cargoDownloadResult = iota
	cargoUnparseable
	cargoParsed
)

// cargoDownload parses a crate download URL. It recognizes two shapes:
//
//   - The "download" endpoint "…/crates/{crate}/{version}/download". This
//     covers both the legacy registry API path
//     "/api/v1/crates/{crate}/{version}/download" and the crates.io sparse
//     form "static.crates.io/crates/{crate}/{version}/download" that cargo
//     builds when the registry's config.json "dl" template has no markers
//     (crates.io's does not), so the "/{crate}/{version}/download" suffix is
//     appended to the "…/crates" base. Anchoring on the "crates" segment and
//     the "download" suffix — rather than a fixed "/api/v1" prefix — matches
//     both without over-fitting to one registry's layout.
//   - The sparse-index download of a "{crate}-{version}.crate" file, used by
//     registries whose "dl" template includes the "{crate}"/"{version}"
//     markers (for example Cloudsmith).
func cargoDownload(path string) (string, string, cargoDownloadResult) {
	if strings.HasSuffix(path, ".crate") {
		return cargoCrateFile(path)
	}
	trimmed := strings.Trim(path, "/")
	parts := strings.Split(trimmed, "/")
	// … crates {crate} {version} download
	if len(parts) < 4 {
		return "", "", cargoNotDownload
	}
	tail := parts[len(parts)-4:]
	if tail[0] != "crates" || tail[3] != "download" {
		return "", "", cargoNotDownload
	}
	// The "…/crates/{crate}/{version}/download" shape is a recognized crate
	// download; empty name/version segments make it unparseable, so fail
	// closed rather than forwarding an in-scope download uninspected.
	if tail[1] == "" || tail[2] == "" {
		return "", "", cargoUnparseable
	}
	return tail[1], tail[2], cargoParsed
}

// cargoCrateFile extracts the coordinate from a "{crate}-{version}.crate"
// filename at the end of a download path. A crate name may contain "-" (for
// example "serde-derive") and a version is a semver value that starts with a
// digit, so the name/version boundary is the first "-" immediately followed
// by a digit.
func cargoCrateFile(path string) (string, string, cargoDownloadResult) {
	filename := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		filename = path[i+1:]
	}
	base := strings.TrimSuffix(filename, ".crate")
	for i := 0; i+1 < len(base); i++ {
		if base[i] != '-' {
			continue
		}
		if next := base[i+1]; next < '0' || next > '9' {
			continue
		}
		name := base[:i]
		version := base[i+1:]
		if name == "" || version == "" {
			return "", "", cargoUnparseable
		}
		return name, version, cargoParsed
	}
	// The ".crate" suffix already marks this as a crate download; a filename
	// this heuristic cannot split into name/version is a recognized-but-
	// unparseable download, so fail closed instead of forwarding it.
	return "", "", cargoUnparseable
}

func cargoPublish(req *http.Request, limits peekLimits) Match {
	if req.Body == nil {
		return Match{}
	}
	path := strings.Trim(req.URL.Path, "/")
	if path != "api/v1/crates/new" && !strings.HasSuffix(path, "/api/v1/crates/new") {
		return Match{}
	}
	peeked := peekUploadBody(req.Body, limits)
	req.Body = peeked.body
	if peeked.overLimit {
		return Match{
			Matched:    true,
			Operation:  policy.Upload,
			Coordinate: policy.Coordinate{Ecosystem: "cargo"},
			Reason:     "upload body too large to inspect for dependency firewall policy",
		}
	}
	name, version, ok := cargoPublishMeta(peeked.data)
	if !ok {
		// The request already matched "PUT …/api/v1/crates/new" exactly, so
		// it is definitely a publish; a frame we cannot decode fails closed
		// rather than falling out of classification.
		return unparseableMatch("cargo", policy.Upload, "unparseable publish metadata")
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "cargo", Name: name, Version: version},
		Operation:  policy.Upload,
	}
}

// cargoPublishMeta reads the leading [u32 LE json_len][json] frame.
func cargoPublishMeta(data []byte) (string, string, bool) {
	if len(data) < 4 {
		return "", "", false
	}
	jsonLen := binary.LittleEndian.Uint32(data[:4])
	if jsonLen == 0 || uint64(jsonLen) > uint64(len(data)-4) {
		return "", "", false
	}
	var meta struct {
		Name string `json:"name"`
		Vers string `json:"vers"`
	}
	if err := json.Unmarshal(data[4:4+jsonLen], &meta); err != nil || meta.Name == "" {
		return "", "", false
	}
	return meta.Name, meta.Vers, true
}
