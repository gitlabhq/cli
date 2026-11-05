//go:build !integration

package pm

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

const userSettingsXML = `<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">
  <mirrors>
    <mirror>
      <id>corp</id>
      <mirrorOf>*</mirrorOf>
      <url>https://nexus.example.com/repository/central</url>
    </mirror>
  </mirrors>
  <servers>
    <server>
      <id>corp</id>
      <username>svc</username>
      <password>secret</password>
    </server>
  </servers>
</settings>
`

// complexSettingsXML is a representative settings.xml (servers, mirrors,
// profiles, repositories, pluginRepositories together), not modeled on any
// specific customer's configuration.
const complexSettingsXML = `<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:schemaLocation="http://maven.apache.org/SETTINGS/1.0.0 http://maven.apache.org/xsd/settings-1.0.0.xsd">

   <!-- <localRepository>D:\maven\repository</localRepository> -->

  <servers>   		 
  	<server>
  	  <id>corporate</id>
          <username>*********</username>
          <password>*********</password>
  	</server>
  </servers>

  <mirrors>
  	<mirror>
  		<id>corporate</id>
  		<url>https://nexus.example-corp.internal/repository/public/</url>
  		<mirrorOf>*</mirrorOf>
  	</mirror>
  </mirrors>
  
  <profiles>		
  	<profile>
  		<id>corporate</id>
  		<properties>
  			<corp.artifact.deploy.repository.id>corporate</corp.artifact.deploy.repository.id>			<corp.artifact.deploy.repository.url>https://nexus.example-corp.internal/repository/corporate/</corp.artifact.deploy.repository.url>	
          </properties>
          <repositories>
  			<repository>
  				<id>corporate</id>
  				<url>https://nexus.example-corp.internal/repository/public/</url>
  			</repository>
          </repositories>
  		<pluginRepositories>
              <pluginRepository>
                  <id>corporate</id>
                  <url>https://nexus.example-corp.internal/repository/public/</url>
              </pluginRepository>
          </pluginRepositories>
      </profile>	
  </profiles>	
  <activeProfiles>
    <activeProfile>corporate</activeProfile>
  </activeProfiles>
</settings>
`

// mavenRun is what the manager hands to mvn: the MAVEN_ARGS it emits, the
// settings file it points at, and that file's contents.
type mavenRun struct {
	args         string
	settingsPath string
	settings     string
	warnings     []string
}

// mavenWithoutInstall returns the Maven manager with the machine's own Maven
// install kept out of the global settings.
func mavenWithoutInstall(t *testing.T) PackageManager {
	t.Helper()
	m := Maven()
	mm, ok := m.(*mavenManager)
	require.True(t, ok)
	mm.lookPath = func(string) (string, error) { return "", errors.New("no mvn in tests") }
	return m
}

// mavenSettingsFor runs the manager's CA-trust wiring the way Run does.
func mavenSettingsFor(t *testing.T, args []string) mavenRun {
	t.Helper()

	ca := selfSignedTestCA(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, caPEM(t, ca), 0o600))

	m := mavenWithoutInstall(t)
	if a, ok := m.(argsManager); ok {
		a.SetArgs(args)
	}
	var warnings []string
	w, ok := m.(warnManager)
	require.True(t, ok, "maven must take Run's stderr logger")
	w.SetWarn(func(format string, a ...any) { warnings = append(warnings, fmt.Sprintf(format, a...)) })
	m.Environment("http://127.0.0.1:52999")
	var mavenArgs string
	for _, e := range m.CATrustEnviron(caPath) {
		if v, ok := strings.CutPrefix(e, "MAVEN_ARGS="); ok {
			mavenArgs = v
		}
	}
	require.NotEmpty(t, mavenArgs, "MAVEN_ARGS must carry the -s that routes mvn through the proxy")

	settingsPath := caPath + ".settings.xml"
	raw, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	return mavenRun{args: mavenArgs, settingsPath: settingsPath, settings: string(raw), warnings: warnings}
}

// assertFirewallSettingsWin checks that the firewall's settings file is the
// one Maven will read first.
func assertFirewallSettingsWin(t *testing.T, r mavenRun, userPath string) {
	t.Helper()
	ours := strings.Index(r.args, r.settingsPath)
	require.NotEqual(t, -1, ours, "MAVEN_ARGS must name the firewall's settings file: %q", r.args)
	if theirs := strings.Index(r.args, userPath); theirs != -1 {
		assert.Less(t, ours, theirs,
			"the firewall's -s must precede the caller's, or Maven reads theirs: %q", r.args)
	}
}

// assertFirewallProxyActive checks the firewall's proxy is a child of
// <proxies>, not merely present in the file.
func assertFirewallProxyActive(t *testing.T, settings string, wantPort string) {
	t.Helper()
	var doc struct {
		Proxies struct {
			Proxy []struct {
				Host string `xml:"host"`
				Port string `xml:"port"`
			} `xml:"proxy"`
		} `xml:"proxies"`
	}
	require.NoError(t, xml.Unmarshal([]byte(settings), &doc), "the merged settings must parse")
	require.NotEmpty(t, doc.Proxies.Proxy, "the firewall proxy must be inside <proxies>:\n%s", settings)
	assert.Equal(t, "127.0.0.1", doc.Proxies.Proxy[0].Host)
	if wantPort != "" {
		assert.Equal(t, wantPort, doc.Proxies.Proxy[0].Port)
	} else {
		assert.NotEmpty(t, doc.Proxies.Proxy[0].Port)
	}
}

// assertProxyAndUserContent checks the merge keeps the user's settings while
// still routing through the firewall.
func assertProxyAndUserContent(t *testing.T, settings string, wantPort string) {
	t.Helper()
	assertFirewallProxyActive(t, settings, wantPort)
	assert.Contains(t, settings, "nexus.example.com", "the user's mirror must survive the merge")
	assert.Contains(t, settings, "<username>svc</username>", "the user's server credentials must survive the merge")
}

func writeUserSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.xml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// TestRunMergesSettingsNamedOnTheCommandLine goes through Run itself, since
// that's what makes a command-line -s visible to the manager at all.
func TestRunMergesSettingsNamedOnTheCommandLine(t *testing.T) {
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	t.Setenv("MAVEN_ARGS", "")
	userPath := writeUserSettings(t, userSettingsXML)
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeExecutor{}
	// Run removes the generated settings file once the child exits, so read it
	// from the hook that stands in for the child.
	var merged string
	exec.onExec = func() {
		for _, e := range exec.env {
			v, ok := strings.CutPrefix(e, "MAVEN_ARGS=")
			if !ok {
				continue
			}
			path, ok := mavenOption(strings.Fields(v), "-s", "--settings", false)
			require.True(t, ok, "MAVEN_ARGS %q has no -s", v)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			merged = string(data)
		}
	}

	require.NoError(t, Run(t.Context(), mavenWithoutInstall(t), RunOptions{
		IO:        ios,
		Executor:  exec,
		BaseDir:   t.TempDir(),
		ProjectID: "group/proj",
		Args:      []string{"-s", userPath, "verify"},
	}))

	require.NotEmpty(t, merged, "Run must pass mvn a settings file: %v", exec.env)
	assertProxyAndUserContent(t, merged, "")
}

// plainHTTPSettings returns a settings file whose only repository is the
// mirror at url, with credentials alongside.
func plainHTTPSettings(url string) string {
	return `<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">
  <mirrors>
    <mirror>
      <id>corp</id>
      <mirrorOf>*</mirrorOf>
      <url>` + url + `</url>
    </mirror>
  </mirrors>
  <servers>
    <server>
      <id>corp</id>
      <username>svc</username>
      <password>secret</password>
    </server>
  </servers>
</settings>
`
}

// TestMavenPlainHTTPRepositories pins the fallback for repositories the proxy
// can't route yet: it only accepts CONNECT, so a kept http:// mirror would
// turn every download from it into a 405.
func TestMavenPlainHTTPRepositories(t *testing.T) {
	t.Run("when the user's mirror is plain HTTP", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, plainHTTPSettings("http://nexus.example.com:8081/repository/public/"))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.NotContains(t, r.settings, "nexus.example.com", "the plain-HTTP mirror must not reach mvn")
		require.Len(t, r.warnings, 1)
		assert.Contains(t, r.warnings[0], `mirror "corp"`)
		assert.Contains(t, r.warnings[0], fmt.Sprintf("%q", userPath))
		assert.NotContains(t, r.warnings[0], "nexus.example.com", "the warning names the mirror, never its URL")
	})

	t.Run("when a profile's plugin repository is plain HTTP", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, strings.Replace(complexSettingsXML,
			"<pluginRepository>\n                  <id>corporate</id>\n                  <url>https://",
			"<pluginRepository>\n                  <id>corporate</id>\n                  <url>http://", 1))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.NotContains(t, r.settings, "nexus.example-corp.internal")
		require.Len(t, r.warnings, 1)
		assert.Contains(t, r.warnings[0], `plugin repository "corporate"`)
	})

	t.Run("when a profile's repository is plain HTTP", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, strings.Replace(complexSettingsXML,
			"<repository>\n  \t\t\t\t<id>corporate</id>\n  \t\t\t\t<url>https://",
			"<repository>\n  \t\t\t\t<id>corporate</id>\n  \t\t\t\t<url>http://", 1))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assert.NotContains(t, r.settings, "nexus.example-corp.internal")
		require.Len(t, r.warnings, 1)
	})

	t.Run("when the URL is padded or upper case", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, plainHTTPSettings("\n        HTTP://nexus.example.com/repository/public/\n      "))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		require.Len(t, r.warnings, 1)
		assert.Contains(t, r.warnings[0], `mirror "corp"`)
	})

	t.Run("when the scheme comes from a property", func(t *testing.T) {
		for _, u := range []string{"${env.NEXUS_URL}", "${repo.scheme}://nexus.example.com/repository/public/", "http://${env.NEXUS_HOST}/repository/public/"} {
			t.Run(u, func(t *testing.T) {
				t.Setenv("MAVEN_ARGS", "")
				t.Setenv("NEXUS_URL", "")
				t.Setenv("NEXUS_HOST", "")
				userPath := writeUserSettings(t, plainHTTPSettings(u))

				r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

				require.Len(t, r.warnings, 1, "a scheme glab can't see may be plain HTTP")
				assert.NotContains(t, r.settings, "<mirror>")
			})
		}
	})

	t.Run("when only the host comes from a property behind https", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("NEXUS_HOST", "")
		userPath := writeUserSettings(t, plainHTTPSettings("https://${env.NEXUS_HOST}/repository/public/"))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assert.Empty(t, r.warnings)
		assert.Contains(t, r.settings, "${env.NEXUS_HOST}", "the caller's settings must still be merged")
	})

	t.Run("when the mirror URL comes from an environment variable", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("CI_API_V4_URL", "https://gitlab.example.com/api/v4")
		userPath := writeUserSettings(t, plainHTTPSettings("${env.CI_API_V4_URL}/virtual_registries/packages/maven/42"))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assert.Empty(t, r.warnings, "glab can see the scheme once it expands the variable")
		assert.Contains(t, r.settings, "<url>https://gitlab.example.com/api/v4/virtual_registries/packages/maven/42</url>",
			"Maven must fetch from the URL glab checked")
	})

	t.Run("when the environment variable holds a plain-HTTP URL", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("NEXUS_URL", "http://nexus.example.com/repository/public/")
		userPath := writeUserSettings(t, plainHTTPSettings("${env.NEXUS_URL}"))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		require.Len(t, r.warnings, 1)
		assert.NotContains(t, r.settings, "nexus.example.com", "the plain-HTTP mirror must not reach mvn")
	})

	t.Run("when the only plain-HTTP mirror is Maven's blocked http-blocker", func(t *testing.T) {
		// Maven 3.8.1+ ships this mirror in conf/settings.xml, so copies of the
		// stock file carry it next to the caller's real mirror.
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, strings.Replace(userSettingsXML, "<mirrors>", `<mirrors>
    <mirror>
      <id>maven-default-http-blocker</id>
      <mirrorOf>external:http:*</mirrorOf>
      <name>Pseudo repository to mirror external repositories initially using HTTP.</name>
      <url>http://0.0.0.0/</url>
      <blocked>true</blocked>
    </mirror>`, 1))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assert.Empty(t, r.warnings)
		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when the plain-HTTP URL carries credentials or a token", func(t *testing.T) {
		for _, u := range []string{
			"http://svc:hunter2@nexus.example.com/repository/public/",
			"http://hunter2@nexus.example.com/repository/public/",
			"http://nexus.example.com/repository/public/?access_token=hunter2",
			"http://svc:hunter2/x@nexus.example.com/repository/public/",
			"http://svc:hunter2#x@nexus.example.com/repository/public/",
			"http://svc:hunter2?x@nexus.example.com/repository/public/",
			"http://nexus.example.com/repository/public/?user=a@b.com&amp;access_token=hunter2",
			"http://nexus.example.com/repository/public/#email=a@b&amp;t=hunter2",
			"http://nexus.example.com/repository/public/?auth=svc@hunter2",
		} {
			t.Run(u, func(t *testing.T) {
				t.Setenv("MAVEN_ARGS", "")
				userPath := writeUserSettings(t, plainHTTPSettings(u))

				r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

				require.Len(t, r.warnings, 1)
				assert.NotContains(t, r.warnings[0], "hunter2", "the warning must not print a credential")
				assert.NotContains(t, r.warnings[0], "nexus.example.com", "the warning names the mirror, never its URL")
				assert.NotContains(t, r.warnings[0], "/repository/public")
				assert.Contains(t, r.warnings[0], `mirror "corp"`)
			})
		}
	})

	t.Run("when a blocked plain-HTTP mirror is followed by an unblocked one", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, `<settings>
  <mirrors>
    <mirror><id>blocker</id><mirrorOf>external:http:*</mirrorOf><url>http://0.0.0.0/</url><blocked> TRUE </blocked></mirror>
    <mirror><id>corp</id><mirrorOf>*</mirrorOf><url>http://nexus.example.com/repository/public/</url></mirror>
  </mirrors>
</settings>`)

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		require.Len(t, r.warnings, 1)
		assert.Contains(t, r.warnings[0], `mirror "corp"`, "the second mirror must not inherit the first one's <blocked>")
	})

	t.Run("when the id holds a newline", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, strings.Replace(plainHTTPSettings("http://nexus.example.com/repository/public/"),
			"<id>corp</id>\n      <mirrorOf>", "<id>corp\nwarning: forged</id>\n      <mirrorOf>", 1))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		require.Len(t, r.warnings, 1)
		assert.Equal(t, 1, strings.Count(r.warnings[0], "\n"), "the id must not be able to start a line of its own")
	})

	t.Run("when a mirror with an id is followed by a plain-HTTP one without", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, `<settings>
  <mirrors>
    <mirror><id>first</id><mirrorOf>central</mirrorOf><url>https://nexus.example.com/repository/public/</url></mirror>
    <mirror><mirrorOf>*</mirrorOf><url>http://nexus.example.com/repository/public/</url></mirror>
  </mirrors>
</settings>`)

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		require.Len(t, r.warnings, 1)
		assert.Contains(t, r.warnings[0], "a mirror with no <id>", "the second mirror must not inherit the first one's id")
	})

	t.Run("when an element inside the mirror has its own id", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, `<settings>
  <mirrors>
    <mirror><id>own</id><name><id>nested</id></name><mirrorOf>*</mirrorOf><url>http://nexus.example.com/repository/public/</url></mirror>
  </mirrors>
</settings>`)

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		require.Len(t, r.warnings, 1)
		assert.Contains(t, r.warnings[0], `the mirror "own"`)
	})

	t.Run("when the id wraps another element's text", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, `<settings>
  <mirrors>
    <mirror><id><url>http://nexus.example.com/?t=hunter2</url></id><mirrorOf>*</mirrorOf><url>http://nexus.example.com/repository/public/</url></mirror>
  </mirrors>
</settings>`)

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		require.Len(t, r.warnings, 1)
		assert.NotContains(t, r.warnings[0], "hunter2")
		assert.Contains(t, r.warnings[0], "a mirror with no <id>")
	})

	t.Run("when an http:// URL sits in a server's free-form configuration", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, strings.Replace(userSettingsXML, "<password>secret</password>",
			"<password>secret</password>\n      <configuration><url>http://wagon.example.com/</url></configuration>", 1))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assert.Empty(t, r.warnings)
		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when the settings can be merged but not scanned to the end", func(t *testing.T) {
		// The merge stops parsing at <proxies/>, so a later byte the decoder
		// rejects (but Maven accepts) must not hide the mirror after it.
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, "<settings>\n  <proxies/>\n  <mirrors>\n    <mirror><id>corp</id><name>caf\xe9</name><mirrorOf>*</mirrorOf>"+
			"<url>http://nexus.example.com/repository/public/</url></mirror>\n  </mirrors>\n</settings>\n")

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.NotContains(t, r.settings, "nexus.example.com", "a file that can't be scanned must fall back")
		require.Len(t, r.warnings, 1)
		assert.Contains(t, r.warnings[0], fmt.Sprintf("could not check %q", userPath))
	})

	t.Run("when the scan error would quote the file", func(t *testing.T) {
		// Go's decoder quotes the text after a bad "&", here part of a password.
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, "<settings>\n  <proxies/>\n  <servers><server><id>corp</id><password>abc&hunter2!</password></server></servers>\n</settings>\n")
		t.Setenv("GLAB_DEBUG", "true")
		var debugLog bytes.Buffer
		log.SetOutput(&debugLog)
		t.Cleanup(func() { log.SetOutput(os.Stderr) })

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		require.Len(t, r.warnings, 1)
		assert.NotContains(t, r.warnings[0], "hunter2")
		assert.Contains(t, debugLog.String(), "XML syntax error on line", "the debug log says where the file broke")
		assert.NotContains(t, debugLog.String(), "hunter2", "a CI log with GLAB_DEBUG on must not show the password")
	})

	t.Run("when an http:// URL is only a property or a namespace", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, strings.Replace(complexSettingsXML,
			"<properties>", "<properties>\n  \t\t\t<site.url>http://docs.example-corp.internal/</site.url>", 1))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assert.Empty(t, r.warnings, "only repository and mirror URLs carry artifact traffic")
		assert.Contains(t, r.settings, "nexus.example-corp.internal", "the caller's settings must still be merged")
	})
}

func TestMavenExpandEnvURLs(t *testing.T) {
	environ := []string{
		"CI_API_V4_URL=https://gitlab.example.com/api/v4",
		"HOST=nexus.example.com",
		"QUERY=https://nexus.example.com/r?a=1&b=<2>",
		"NESTED=${env.HOST}",
		"EMPTY=",
	}
	for _, tc := range []struct{ name, url, want string }{
		{name: "when the URL comes from a variable", url: "${env.CI_API_V4_URL}/virtual_registries/packages/maven/42", want: "https://gitlab.example.com/api/v4/virtual_registries/packages/maven/42"},
		{name: "when only the host does", url: "https://${env.HOST}/repository/public/", want: "https://nexus.example.com/repository/public/"},
		{name: "when the URL is padded", url: "\n        https://${env.HOST}/repository/public/\n      ", want: "https://nexus.example.com/repository/public/"},
		{name: "when the value needs escaping", url: "${env.QUERY}", want: "https://nexus.example.com/r?a=1&amp;b=&lt;2&gt;"},
		{name: "when the variable is unset", url: "https://${env.MISSING}/repository/public/"},
		{name: "when the variable is empty", url: "https://${env.EMPTY}/repository/public/"},
		{name: "when the value holds an expression", url: "https://${env.NESTED}/repository/public/"},
		{name: "when another property is mixed in", url: "https://${env.HOST}/${repo.path}/"},
		{name: "when a comment splits the URL", url: "https://${env.HOST}<!-- old -->/repository/public/"},
		{name: "when the URL has no expression", url: "\n        https://nexus.example.com/repository/public/\n      "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := plainHTTPSettings(tc.url)
			want := raw
			if tc.want != "" {
				want = strings.Replace(raw, "<url>"+tc.url+"</url>", "<url>"+tc.want+"</url>", 1)
			}

			assert.Equal(t, want, string(mavenExpandEnvURLs([]byte(raw), environ)))
		})
	}

	t.Run("when profile repositories come from a variable too", func(t *testing.T) {
		raw := strings.ReplaceAll(complexSettingsXML, "<url>https://nexus.example-corp.internal/", "<url>https://${env.CORP_HOST}/")
		require.Equal(t, 3, strings.Count(raw, "${env.CORP_HOST}"), "a mirror, a repository and a plugin repository")

		got := mavenExpandEnvURLs([]byte(raw), []string{"CORP_HOST=nexus.example-corp.internal"})

		assert.Equal(t, complexSettingsXML, string(got), "the rest of the file, the deploy URL property included, is copied as it is")
	})

	t.Run("when a variable sits outside a repository URL", func(t *testing.T) {
		raw := strings.NewReplacer(
			"<id>corp</id>\n      <mirrorOf>", "<id>${env.HOST}</id>\n      <mirrorOf>",
			"<password>secret</password>", "<password>${env.HOST}</password><configuration><url>https://${env.HOST}/</url></configuration>",
		).Replace(plainHTTPSettings("https://nexus.example.com/repository/public/"))
		require.Equal(t, 3, strings.Count(raw, "${env.HOST}"))

		assert.Equal(t, raw, string(mavenExpandEnvURLs([]byte(raw), environ)), "Maven still interpolates everything glab doesn't check")
	})

	t.Run("when the settings don't parse", func(t *testing.T) {
		raw := "<settings><mirrors><mirror><url>https://${env.HOST}/</url></mirrors></settings>"

		assert.Equal(t, raw, string(mavenExpandEnvURLs([]byte(raw), environ)), "glab's own parse of the file reports the error")
	})
}

// TestRunWarnsAboutPlainHTTPRepositories goes through Run, since that's what
// hands the manager the stderr logger.
func TestRunWarnsAboutPlainHTTPRepositories(t *testing.T) {
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	t.Setenv("MAVEN_ARGS", "")
	userPath := writeUserSettings(t, plainHTTPSettings("http://nexus.example.com:8081/repository/public/"))
	ios, _, _, stderr := cmdtest.TestIOStreams()

	require.NoError(t, Run(t.Context(), mavenWithoutInstall(t), RunOptions{
		IO:        ios,
		Executor:  &fakeExecutor{},
		BaseDir:   t.TempDir(),
		ProjectID: "group/proj",
		Args:      []string{"-s", userPath, "verify"},
	}))

	assert.Contains(t, stderr.String(), `names the mirror "corp", whose URL is or may be plain HTTP`)
	assert.Contains(t, stderr.String(), fmt.Sprintf("%q", userPath))
}

// TestMergeMavenProxiesReportsTheCause pins that each way a merge can fail
// keeps its cause, so the debug log says why the caller's file was dropped.
func TestMergeMavenProxiesReportsTheCause(t *testing.T) {
	t.Run("when the file doesn't exist", func(t *testing.T) {
		_, err := mergeMavenProxies(filepath.Join(t.TempDir(), "missing.xml"), "")
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("when the XML is malformed", func(t *testing.T) {
		_, err := mergeMavenProxies(writeUserSettings(t, "<settings><mirrors></settings>"), "")
		assert.ErrorContains(t, err, "XML syntax error")
	})

	t.Run("when the settings element is self-closed", func(t *testing.T) {
		_, err := mergeMavenProxies(writeUserSettings(t, "<settings/>"), "")
		assert.ErrorContains(t, err, "self-closed <settings/>")
	})

	t.Run("when there is no settings element", func(t *testing.T) {
		_, err := mergeMavenProxies(writeUserSettings(t, "<!-- nothing here -->"), "")
		assert.ErrorContains(t, err, "no <settings> element")
	})
}

func TestMavenUserSettings(t *testing.T) {
	t.Run("when the user passes -s on the command line", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, userSettingsXML)

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
		assertFirewallSettingsWin(t, r, userPath)
	})

	t.Run("when the user passes --settings= on the command line", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, userSettingsXML)

		r := mavenSettingsFor(t, []string{"--settings=" + userPath, "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when the user sets -s through MAVEN_ARGS", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		userPath := writeUserSettings(t, userSettingsXML)
		t.Setenv("MAVEN_ARGS", "-s "+userPath)

		r := mavenSettingsFor(t, []string{"verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
		assertFirewallSettingsWin(t, r, userPath)
	})

	t.Run("when -s is set on the command line and MAVEN_ARGS disagree", func(t *testing.T) {
		// MAVEN_ARGS wins: Maven expands it before the command line and
		// takes the first occurrence of a repeated option.
		fromEnv := writeUserSettings(t, userSettingsXML)
		fromCLI := writeUserSettings(t, `<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">
  <mirrors>
    <mirror>
      <id>ignored</id>
      <mirrorOf>*</mirrorOf>
      <url>https://ignored.example.com/</url>
    </mirror>
  </mirrors>
</settings>
`)
		t.Setenv("MAVEN_ARGS", "-s "+fromEnv)

		r := mavenSettingsFor(t, []string{"-s", fromCLI, "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
		assert.NotContains(t, r.settings, "ignored.example.com",
			"the command-line -s loses to MAVEN_ARGS, so its file must not be the one merged")
	})

	t.Run("when the user has no -s but a default settings.xml in .m2", func(t *testing.T) {
		// Maven reads user.home from the password database, not HOME.
		home := t.TempDir()
		t.Setenv("MAVEN_ARGS", "")
		require.NoError(t, os.MkdirAll(filepath.Join(home, ".m2"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, ".m2", "settings.xml"), []byte(userSettingsXML), 0o600))

		r := mavenSettingsFor(t, []string{"-Duser.home=" + home, "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when a project sets -s in .mvn/maven.config", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, userSettingsXML)
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".mvn"), 0o700))
		// maven.config holds one argument per line, not "-s <path>".
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".mvn", "maven.config"), []byte("-s\n"+userPath+"\n"), 0o600))
		t.Chdir(dir)

		r := mavenSettingsFor(t, []string{"verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when the user has no settings at all", func(t *testing.T) {
		// Maven takes user.home from the password database; HOME alone
		// doesn't steer it.
		t.Setenv("MAVEN_ARGS", "")

		r := mavenSettingsFor(t, []string{"-Duser.home=" + t.TempDir(), "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.NotContains(t, r.settings, "<mirrors>", "no settings to merge means the firewall's own file")
	})

	t.Run("when the user settings define their own active proxy", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, `<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">
  <proxies>
    <proxy>
      <id>corp-proxy</id>
      <active>true</active>
      <protocol>https</protocol>
      <host>proxy.example.com</host>
      <port>8080</port>
    </proxy>
  </proxies>
</settings>
`)

		r := mavenSettingsFor(t, []string{"-s", userPath})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.Contains(t, r.settings, "proxy.example.com",
			"the caller's proxy must survive: the merge is not allowed to be destructive")
		assert.Less(t, strings.Index(r.settings, "<host>127.0.0.1</host>"), strings.Index(r.settings, "proxy.example.com"),
			"the firewall proxy must come first: Maven uses the first active proxy per protocol")
	})

	t.Run("when the user attaches the path to the short option", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, userSettingsXML)

		r := mavenSettingsFor(t, []string{"-s" + userPath, "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when the user settings hold a self-closed proxies element", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, `<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">
  <proxies/>
  <mirrors>
    <mirror>
      <id>corp</id>
      <mirrorOf>*</mirrorOf>
      <url>https://nexus.example.com/repository/central</url>
    </mirror>
  </mirrors>
</settings>
`)

		r := mavenSettingsFor(t, []string{"-s", userPath})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.Contains(t, r.settings, "nexus.example.com", "an empty proxies element must not cost the caller their file")
	})

	t.Run("when the user settings cannot be read", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MAVEN_ARGS", "")

		r := mavenSettingsFor(t, []string{"-s", filepath.Join(t.TempDir(), "missing.xml")})

		assertFirewallProxyActive(t, r.settings, "52999")
	})

	t.Run("when the user passes -gs (global settings) instead of -s", func(t *testing.T) {
		// -gs names global settings, which glab reads but doesn't merge its
		// proxy into, so the proxy still goes in glab's own user settings.
		t.Setenv("MAVEN_ARGS", "")
		globalPath := writeUserSettings(t, complexSettingsXML)

		r := mavenSettingsFor(t, []string{"-gs", globalPath, "-Duser.home=" + t.TempDir(), "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.NotContains(t, r.settings, "nexus.example-corp.internal",
			"glab does not read -gs, so its own generated settings should not pick up the global file's content")
	})

	t.Run("when the user's settings file has servers, mirrors, profiles, repositories and pluginRepositories together", func(t *testing.T) {
		// A complex, representative settings.xml, not a simplified fixture:
		// proves the byte-for-byte merge survives that complexity untouched.
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, complexSettingsXML)

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.Contains(t, r.settings, "nexus.example-corp.internal",
			"the mirror, profile, and deploy-repository properties must survive the merge")
		assert.Contains(t, r.settings, "<pluginRepositories>",
			"pluginRepositories is not a <proxies> sibling the merge should touch, and must pass through untouched")
		assertFirewallSettingsWin(t, r, userPath)
	})
}
