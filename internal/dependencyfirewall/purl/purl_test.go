//go:build !integration

package purl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		in          string
		wantType    string
		wantName    string
		wantVersion string
	}{
		{"npm plain", "pkg:npm/left-pad@1.3.0", TypeNpm, "left-pad", "1.3.0"},
		{"npm scoped", "pkg:npm/%40babel/core@7.24.0", TypeNpm, "@babel/core", "7.24.0"},
		{"npm no version", "pkg:npm/left-pad", TypeNpm, "left-pad", ""},
		{"pypi lowercase", "pkg:pypi/requests@2.31.0", TypePypi, "requests", "2.31.0"},
		{"pypi normalized underscore", "pkg:pypi/foo_bar@1.0.0", TypePypi, "foo-bar", "1.0.0"},
		{"pypi normalized dots and mixed case", "pkg:pypi/Zope.Interface@6.0", TypePypi, "zope-interface", "6.0"},
		{"maven", "pkg:maven/org.slf4j/slf4j-api@2.0.13", TypeMaven, "org.slf4j:slf4j-api", "2.0.13"},
		{"maven deep group", "pkg:maven/org.apache.commons/commons-lang3@3.14.0", TypeMaven, "org.apache.commons:commons-lang3", "3.14.0"},
		{"gem", "pkg:gem/rails@7.1.3", TypeGem, "rails", "7.1.3"},
		{"nuget", "pkg:nuget/Newtonsoft.Json@13.0.1", TypeNuget, "Newtonsoft.Json", "13.0.1"},
		{"cargo", "pkg:cargo/serde@1.0.197", TypeCargo, "serde", "1.0.197"},
		{"golang", "pkg:golang/github.com/pkg/errors@v0.9.1", TypeGolang, "github.com/pkg/errors", "v0.9.1"},
		{"golang mixed case keeps case", "pkg:golang/github.com/BurntSushi/toml@v1.4.0", TypeGolang, "github.com/BurntSushi/toml", "v1.4.0"},
		{"golang no namespace", "pkg:golang/example@v1.0.0", TypeGolang, "example", "v1.0.0"},
		{"golang incompatible version", "pkg:golang/github.com/docker/docker@v20.10.7%2Bincompatible", TypeGolang, "github.com/docker/docker", "v20.10.7+incompatible"},
		{"golang subpath and qualifiers ignored", "pkg:golang/github.com/Azure/go-autorest@v14.2.0%2Bincompatible?vcs_url=x#autorest/azure", TypeGolang, "github.com/Azure/go-autorest", "v14.2.0+incompatible"},
		{"golang percent-encoded segment", "pkg:golang/gopkg.in/yaml%2Ev3@v3.0.1", TypeGolang, "gopkg.in/yaml.v3", "v3.0.1"},
		{"golang pkg:// form", "pkg://golang/github.com/BurntSushi/toml@v1.4.0", TypeGolang, "github.com/BurntSushi/toml", "v1.4.0"},
		{"golang @ in qualifier and subpath", "pkg:golang/github.com/BurntSushi/toml@v1.4.0?vcs_url=git%40github.com#a@b", TypeGolang, "github.com/BurntSushi/toml", "v1.4.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := Parse(tc.in)
			require.NoErrorf(t, err, "Parse(%q)", tc.in)
			assert.Equal(t, tc.wantType, p.Type, "Type")
			assert.Equal(t, tc.wantName, p.Name, "Name")
			assert.Equal(t, tc.wantVersion, p.Version, "Version")
		})
	}
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		errWant string
	}{
		{"empty", "", "invalid"},
		{"not a purl", "left-pad@1.3.0", "invalid"},
		{"missing type", "pkg:/foo@1.0", "invalid"},
		{"unsupported type", "pkg:cocoapods/Alamofire@5.9.0", "not supported"},
		{"maven missing group", "pkg:maven/slf4j-api@2.0.13", "namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(tc.in)
			require.Errorf(t, err, "Parse(%q) expected error", tc.in)
			assert.ErrorContains(t, err, tc.errWant)
		})
	}
}
