// Package purl parses Package URL (PURL) strings into the subset the
// Dependency Firewall check command supports: npm, pypi, maven, gem, nuget,
// cargo, and golang. It wraps github.com/package-url/packageurl-go and
// normalises each PURL type's name to its registry form.
package purl

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/package-url/packageurl-go"
)

// Package is a parsed PURL restricted to the types this command supports.
// Name is the registry-form name (npm scope prefix retained, PEP 503
// canonical for pypi, maven "group:artifact" coordinate). Version may be
// empty when the PURL does not include one.
type Package struct {
	Type    string
	Name    string
	Version string
}

// Supported PURL type identifiers. Values match the package-url spec.
// Declared as var to match the upstream library, which exposes these as
// mutable package-level variables rather than untyped constants.
var (
	TypeNpm    = packageurl.TypeNPM
	TypePypi   = packageurl.TypePyPi
	TypeMaven  = packageurl.TypeMaven
	TypeGem    = packageurl.TypeGem
	TypeNuget  = packageurl.TypeNuget
	TypeCargo  = packageurl.TypeCargo
	TypeGolang = packageurl.TypeGolang
)

// supportedTypes lists every PURL type this command handles. It drives the
// user-facing error message when an unsupported PURL is passed. It is
// unexported so importers cannot mutate the backing array.
var supportedTypes = []string{TypeNpm, TypePypi, TypeMaven, TypeGem, TypeNuget, TypeCargo, TypeGolang}

// Parse converts a PURL string into a supported Package. It returns an error
// on parse failures, unsupported types, or type-specific validation failures
// (for example a maven PURL without a namespace).
func Parse(s string) (Package, error) {
	if strings.TrimSpace(s) == "" {
		return Package{}, errors.New("invalid PURL: empty")
	}

	u, err := packageurl.FromString(s)
	if err != nil {
		return Package{}, fmt.Errorf("invalid PURL %q: %w", s, err)
	}
	if u.Type == "" {
		return Package{}, fmt.Errorf("invalid PURL %q: missing type", s)
	}
	if u.Name == "" {
		return Package{}, fmt.Errorf("invalid PURL %q: missing name", s)
	}

	if !slices.Contains(supportedTypes, u.Type) {
		return Package{}, fmt.Errorf("PURL type %q is not supported by the dependency firewall check (supported: %s)",
			u.Type, strings.Join(supportedTypes, ", "))
	}

	var name string
	if u.Type == TypeGolang {
		name, err = golangModulePath(s)
	} else {
		name, err = normalizeName(u.Type, u.Namespace, u.Name)
	}
	if err != nil {
		return Package{}, fmt.Errorf("invalid PURL %q: %w", s, err)
	}

	return Package{Type: u.Type, Name: name, Version: u.Version}, nil
}

// golangModulePath rebuilds the module path from the raw PURL with its case
// kept. packageurl-go lowercases golang namespaces and names as the PURL spec
// says, but Go module paths are case-sensitive.
func golangModulePath(s string) (string, error) {
	_, rest, _ := strings.Cut(s, ":")
	_, rest, _ = strings.Cut(strings.TrimLeft(rest, "/"), "/")
	rest, _, _ = strings.Cut(rest, "#")
	rest, _, _ = strings.Cut(rest, "?")
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[:at]
	}
	var segments []string
	for segment := range strings.SplitSeq(rest, "/") {
		if segment == "" {
			continue
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return "", err
		}
		segments = append(segments, decoded)
	}
	return strings.Join(segments, "/"), nil
}

// pypiNameNormaliser collapses runs of `[-_.]` to a single `-` per PEP 503.
var pypiNameNormaliser = regexp.MustCompile(`[-_.]+`)

func normalizeName(typ, namespace, name string) (string, error) {
	switch typ {
	case TypeNpm:
		if namespace != "" {
			// packageurl-go strips the leading "@" from an npm scope
			// namespace on parse; restore it so the registry-form name is
			// "@scope/name".
			ns := namespace
			if !strings.HasPrefix(ns, "@") {
				ns = "@" + ns
			}
			return ns + "/" + name, nil
		}
		return name, nil
	case TypePypi:
		lower := strings.ToLower(name)
		return pypiNameNormaliser.ReplaceAllString(lower, "-"), nil
	case TypeMaven:
		if namespace == "" {
			return "", errors.New("maven PURLs must include a namespace (the group ID)")
		}
		return namespace + ":" + name, nil
	case TypeGem:
		return name, nil
	default:
		return name, nil
	}
}
