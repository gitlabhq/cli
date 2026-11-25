//go:build !integration

package pm

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
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
// settings file it points at, and that file's contents, plus what it warned.
type mavenRun struct {
	args         string
	settingsPath string
	settings     string
	warnings     string
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
	t.Setenv("MAVEN_BASEDIR", "")

	ca := selfSignedTestCA(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, caPEM(t, ca), 0o600))

	m := mavenWithoutInstall(t)
	if a, ok := m.(argsManager); ok {
		a.SetArgs(args)
	}
	var warnings strings.Builder
	if w, ok := m.(warnManager); ok {
		w.SetWarn(func(format string, args ...any) { fmt.Fprintf(&warnings, format, args...) })
	}
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
	return mavenRun{args: mavenArgs, settingsPath: settingsPath, settings: string(raw), warnings: warnings.String()}
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
	isolateUserConfig(t, t.TempDir())
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

	t.Run("when the caller's mirror URL comes from an environment variable", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("CI_API_V4_URL", "https://gitlab.example.com/api/v4")
		userPath := writeUserSettings(t, plainHTTPSettings("${env.CI_API_V4_URL}/virtual_registries/packages/maven/42"))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assert.Contains(t, r.settings, "<url>https://gitlab.example.com/api/v4/virtual_registries/packages/maven/42</url>",
			"Maven must fetch from the URL glab checked")
	})
}

// TestMavenPlainHTTPMirrorIsMerged pins that a plain-HTTP mirror survives the
// merge, now that the proxy serves absolute-form http:// requests itself.
func TestMavenPlainHTTPMirrorIsMerged(t *testing.T) {
	t.Setenv("MAVEN_ARGS", "")
	userPath := writeUserSettings(t, strings.Replace(userSettingsXML, "https://nexus.example.com", "http://nexus.example.com", 1))

	r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

	assertProxyAndUserContent(t, r.settings, "52999")
	assert.Contains(t, r.settings, "<url>http://nexus.example.com/repository/central</url>")
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
		assert.Empty(t, r.warnings, "a missing default settings.xml is common and not worth a warning")
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
			"glab merges its proxy into user settings only, so the global file's content must not end up in them")
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

// ignoredSettingsXML is a settings file a test expects Maven not to read.
const ignoredSettingsXML = `<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">
  <mirrors>
    <mirror>
      <id>ignored</id>
      <mirrorOf>*</mirrorOf>
      <url>https://ignored.example.com/</url>
    </mirror>
  </mirrors>
</settings>
`

// writeMavenConfig writes dir/.mvn/maven.config, which holds one argument
// per line.
func writeMavenConfig(t *testing.T, dir string, args ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".mvn"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".mvn", "maven.config"), []byte(strings.Join(args, "\n")+"\n"), 0o600))
}

// TestRunWarnsWhenTheCallersSettingsAreDropped goes through Run, since Run is
// what hands the manager glab's stderr.
func TestRunWarnsWhenTheCallersSettingsAreDropped(t *testing.T) {
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	t.Setenv("MAVEN_ARGS", "")
	// Valid XML, so the proxy reader accepts it, but with no element to
	// insert the proxies into, so the merge can't use it.
	userPath := writeUserSettings(t, "<settings/>")
	ios, _, _, stderr := cmdtest.TestIOStreams()

	require.NoError(t, Run(t.Context(), mavenWithoutInstall(t), RunOptions{
		IO:        ios,
		Executor:  &fakeExecutor{},
		BaseDir:   t.TempDir(),
		ProjectID: "group/proj",
		Args:      []string{"-s", userPath, "verify"},
	}))

	assert.Contains(t, stderr.String(), fmt.Sprintf("warning: could not merge the Dependency Firewall proxy into the Maven settings at %q,", userPath))
}

// TestMavenProjectBaseDirectory pins that .mvn/maven.config is read from the
// directory mvn's launcher picks, not only from glab's working directory.
func TestMavenProjectBaseDirectory(t *testing.T) {
	t.Run("when -f names a POM in a module with its own .mvn", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		userPath := writeUserSettings(t, userSettingsXML)
		root := t.TempDir()
		module := filepath.Join(root, "services", "api")
		writeMavenConfig(t, module, "-s", userPath)
		require.NoError(t, os.WriteFile(filepath.Join(module, "pom.xml"), []byte("<project/>\n"), 0o600))
		t.Chdir(root)

		r := mavenSettingsFor(t, []string{"-f", filepath.Join("services", "api", "pom.xml"), "-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when --file names a module directory", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		userPath := writeUserSettings(t, userSettingsXML)
		module := t.TempDir()
		writeMavenConfig(t, module, "-s", userPath)
		t.Chdir(t.TempDir())

		r := mavenSettingsFor(t, []string{"--file", module, "-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when -f names a POM in a project with no .mvn directory", func(t *testing.T) {
		// With no .mvn to find, the launcher keeps the POM's own directory.
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		project := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(project, "pom.xml"), []byte("<project/>\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(project, "settings.xml"), []byte(userSettingsXML), 0o600))
		t.Chdir(t.TempDir())

		r := mavenSettingsFor(t, []string{
			"-f", filepath.Join(project, "pom.xml"),
			"-s", "${maven.multiModuleProjectDirectory}/settings.xml",
			"-Duser.home=" + t.TempDir(), "verify",
		})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when -f is the last argument", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		userPath := writeUserSettings(t, userSettingsXML)
		cwd := t.TempDir()
		writeMavenConfig(t, cwd, "-s", userPath)
		t.Chdir(cwd)

		r := mavenSettingsFor(t, []string{"-Duser.home=" + t.TempDir(), "-f"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when -f is only in MAVEN_ARGS", func(t *testing.T) {
		// The launcher looks for -f on its own command line, not in MAVEN_ARGS.
		t.Setenv("MAVEN_BASEDIR", "")
		userPath := writeUserSettings(t, userSettingsXML)
		module := t.TempDir()
		writeMavenConfig(t, module, "-s", writeUserSettings(t, ignoredSettingsXML))
		t.Setenv("MAVEN_ARGS", "-f "+module)
		cwd := t.TempDir()
		writeMavenConfig(t, cwd, "-s", userPath)
		t.Chdir(cwd)

		r := mavenSettingsFor(t, []string{"-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
		assert.NotContains(t, r.settings, "ignored.example.com")
	})

	t.Run("when MAVEN_BASEDIR is set", func(t *testing.T) {
		// mavenSettingsFor clears MAVEN_BASEDIR, so pass it in directly.
		userPath := writeUserSettings(t, userSettingsXML)
		base := t.TempDir()
		writeMavenConfig(t, base, "-s", userPath)
		cwd := t.TempDir()
		writeMavenConfig(t, cwd, "-s", writeUserSettings(t, ignoredSettingsXML))

		got, _ := mavenUserSettingsFile([]string{"MAVEN_BASEDIR=" + base}, cwd, []string{"-Duser.home=" + t.TempDir(), "verify"})

		assert.Equal(t, userPath, got)
	})

	for _, target := range []string{filepath.Join("link", "pom.xml"), "link"} {
		t.Run("when -f names "+target+" through a symlinked directory", func(t *testing.T) {
			// The launcher resolves the -f directory with pwd -P, so it searches
			// above the link's target, not above the link.
			t.Setenv("MAVEN_ARGS", "")
			userPath := writeUserSettings(t, userSettingsXML)
			root := t.TempDir()
			writeMavenConfig(t, root, "-s", userPath)
			module := filepath.Join(root, "services", "api")
			require.NoError(t, os.MkdirAll(module, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(module, "pom.xml"), []byte("<project/>\n"), 0o600))
			cwd := t.TempDir()
			writeMavenConfig(t, cwd, "-s", writeUserSettings(t, ignoredSettingsXML))
			if err := os.Symlink(module, filepath.Join(cwd, "link")); err != nil {
				t.Skipf("can't create a symlink: %v", err)
			}
			t.Chdir(cwd)

			r := mavenSettingsFor(t, []string{"-f", target, "-Duser.home=" + t.TempDir(), "verify"})

			assertProxyAndUserContent(t, r.settings, "52999")
			assert.NotContains(t, r.settings, "ignored.example.com")
		})
	}

	for _, flag := range []string{"-f=", "--file="} {
		t.Run("when "+flag+" names a module", func(t *testing.T) {
			// The launcher only honours the separated -f and --file forms.
			t.Setenv("MAVEN_ARGS", "")
			userPath := writeUserSettings(t, userSettingsXML)
			module := t.TempDir()
			writeMavenConfig(t, module, "-s", writeUserSettings(t, ignoredSettingsXML))
			cwd := t.TempDir()
			writeMavenConfig(t, cwd, "-s", userPath)
			t.Chdir(cwd)

			r := mavenSettingsFor(t, []string{flag + module, "-Duser.home=" + t.TempDir(), "verify"})

			assertProxyAndUserContent(t, r.settings, "52999")
			assert.NotContains(t, r.settings, "ignored.example.com")
		})
	}

	t.Run("when mvn runs from a subdirectory of the project without -f", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		userPath := writeUserSettings(t, userSettingsXML)
		root := t.TempDir()
		writeMavenConfig(t, root, "-s", userPath)
		sub := filepath.Join(root, "services", "api")
		require.NoError(t, os.MkdirAll(sub, 0o700))
		t.Chdir(sub)

		r := mavenSettingsFor(t, []string{"-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})
}

// TestMavenSettingsPathExpressions pins the ${...} expressions Maven expands
// in a -s value before it reads the file.
func TestMavenSettingsPathExpressions(t *testing.T) {
	t.Run("when maven.config and the command line both name -s", func(t *testing.T) {
		cwd := physicalTempDir(t)
		writeMavenConfig(t, cwd, "-s", "config.xml")

		got, _ := mavenUserSettingsFile(nil, cwd, []string{"-s", "cli.xml", "verify"})

		assert.Equal(t, filepath.Join(cwd, "cli.xml"), got)
	})

	t.Run("when MAVEN_ARGS and the command line set the same -D property", func(t *testing.T) {
		cwd := physicalTempDir(t)

		got, _ := mavenUserSettingsFile([]string{"MAVEN_ARGS=-Dfoo=FROMARGS"}, cwd, []string{"-Dfoo=FROMCLI", "-s", "${foo}/a.xml"})

		assert.Equal(t, filepath.Join(cwd, "FROMCLI", "a.xml"), got)
	})

	t.Run("when -s uses ${session.rootDirectory} and the project has a .mvn directory", func(t *testing.T) {
		root := physicalTempDir(t)
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn"), 0o700))
		sub := filepath.Join(root, "app")
		require.NoError(t, os.MkdirAll(sub, 0o700))

		got, _ := mavenUserSettingsFile(nil, sub, []string{"-s", "${session.rootDirectory}/.mvn/settings.xml"})

		assert.Equal(t, filepath.Join(root, ".mvn", "settings.xml"), got)
	})

	t.Run("when -s uses ${session.rootDirectory} and MAVEN_BASEDIR is set, which Maven ignores for it", func(t *testing.T) {
		root := physicalTempDir(t)
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn"), 0o700))
		other := physicalTempDir(t)
		require.NoError(t, os.MkdirAll(filepath.Join(other, ".mvn"), 0o700))

		got, _ := mavenUserSettingsFile([]string{"MAVEN_BASEDIR=" + other}, root, []string{"-s", "${session.rootDirectory}/s.xml"})

		assert.Equal(t, filepath.Join(root, "${session.rootDirectory}", "s.xml"), got)
	})

	t.Run("when -s uses ${session.rootDirectory} and MAVEN_ARGS names another project with -f", func(t *testing.T) {
		root := physicalTempDir(t)
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn"), 0o700))

		got, _ := mavenUserSettingsFile([]string{"MAVEN_ARGS=-f " + filepath.Join(physicalTempDir(t), "pom.xml")}, root, []string{"-s", "${session.rootDirectory}/s.xml"})

		assert.Equal(t, filepath.Join(root, "${session.rootDirectory}", "s.xml"), got, "Maven finds the root from that -f, which the launcher ignores")
	})

	t.Run("when -s uses ${session.rootDirectory} without a .mvn directory, which Maven 3.9 rejects", func(t *testing.T) {
		cwd := physicalTempDir(t)
		got, _ := mavenUserSettingsFile(nil, cwd, []string{"-s", "${session.rootDirectory}/s.xml"})
		assert.Equal(t, filepath.Join(cwd, "${session.rootDirectory}", "s.xml"), got)
	})

	t.Run("when maven.config names the settings with ${maven.multiModuleProjectDirectory}", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		root := t.TempDir()
		writeMavenConfig(t, root, "-s", "${maven.multiModuleProjectDirectory}/.mvn/settings.xml")
		require.NoError(t, os.WriteFile(filepath.Join(root, ".mvn", "settings.xml"), []byte(userSettingsXML), 0o600))
		// Run from below the project so the base directory differs from
		// the working directory.
		sub := filepath.Join(root, "app")
		require.NoError(t, os.MkdirAll(sub, 0o700))
		t.Chdir(sub)

		r := mavenSettingsFor(t, []string{"-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when maven.config names the settings with ${session.rootDirectory}", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		root := t.TempDir()
		writeMavenConfig(t, root, "-s", "${session.rootDirectory}/.mvn/settings.xml")
		require.NoError(t, os.WriteFile(filepath.Join(root, ".mvn", "settings.xml"), []byte(userSettingsXML), 0o600))
		sub := filepath.Join(root, "app")
		require.NoError(t, os.MkdirAll(sub, 0o700))
		t.Chdir(sub)

		r := mavenSettingsFor(t, []string{"-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when maven.config names the settings with ${env.NAME}", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		root := t.TempDir()
		writeMavenConfig(t, root, "-s", "${env.DF_TEST_SETTINGS_ROOT}/.mvn/settings.xml")
		require.NoError(t, os.WriteFile(filepath.Join(root, ".mvn", "settings.xml"), []byte(userSettingsXML), 0o600))
		t.Setenv("DF_TEST_SETTINGS_ROOT", root)
		t.Chdir(root)

		r := mavenSettingsFor(t, []string{"-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when -s uses ${user.home} set with -D", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		home := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(home, "corp-settings.xml"), []byte(userSettingsXML), 0o600))
		t.Chdir(t.TempDir())

		r := mavenSettingsFor(t, []string{"-Duser.home=" + home, "-s", "${user.home}/corp-settings.xml", "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when -s uses ${user.home} without -D", func(t *testing.T) {
		// The package's TestMain stubs the current user, so compare with the
		// home javaUserHome reports rather than the machine's.
		home := javaUserHome(nil, nil)
		require.NotEmpty(t, home)

		got, _ := mavenUserSettingsFile(nil, t.TempDir(), []string{"-s", "${user.home}/corp-settings.xml"})

		assert.Equal(t, filepath.Join(home, "corp-settings.xml"), got)
	})

	t.Run("when -s uses ${user.dir}", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		dir := physicalTempDir(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "corp-settings.xml"), []byte(userSettingsXML), 0o600))
		t.Chdir(dir)

		r := mavenSettingsFor(t, []string{"-s", "${user.dir}/corp-settings.xml", "-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when -s uses ${env.NAME} that is set but empty", func(t *testing.T) {
		// Maven expands it to "", where an unset variable stays as written.
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("DF_TEST_EMPTY_VARIABLE", "")
		path := writeUserSettings(t, userSettingsXML)
		t.Chdir(t.TempDir())

		r := mavenSettingsFor(t, []string{"-s", "${env.DF_TEST_EMPTY_VARIABLE}" + path, "-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	t.Run("when maven.config and the command line set the same -D property", func(t *testing.T) {
		// Maven applies the command line's -D over maven.config's.
		cwd := physicalTempDir(t)
		writeMavenConfig(t, cwd, "-Dfoo=FROMCONFIG")

		got, _ := mavenUserSettingsFile(nil, cwd, []string{"-Dfoo=FROMCLI", "-s", "${foo}/a.xml", "verify"})

		assert.Equal(t, filepath.Join(cwd, "FROMCLI", "a.xml"), got)
	})

	t.Run("when maven.config and MAVEN_ARGS set the same -D property", func(t *testing.T) {
		cwd := physicalTempDir(t)
		writeMavenConfig(t, cwd, "-Dfoo=FROMCONFIG")

		got, _ := mavenUserSettingsFile([]string{"MAVEN_ARGS=-Dfoo=FROMARGS"}, cwd, []string{"-s", "${foo}/a.xml", "verify"})

		assert.Equal(t, filepath.Join(cwd, "FROMARGS", "a.xml"), got)
	})

	t.Run("when -s uses a -D system property", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("MAVEN_BASEDIR", "")
		dir := physicalTempDir(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.xml"), []byte(userSettingsXML), 0o600))
		t.Chdir(t.TempDir())

		r := mavenSettingsFor(t, []string{"-Dcorp.settings.dir=" + dir, "-s", "${corp.settings.dir}/settings.xml", "-Duser.home=" + t.TempDir(), "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
	})

	for _, expr := range []string{"${no.such.property}", "${env.DF_TEST_UNSET_VARIABLE}"} {
		t.Run("when -s uses "+expr+" that can't be resolved", func(t *testing.T) {
			// Maven leaves it as written, so the file it names doesn't exist.
			t.Setenv("MAVEN_ARGS", "")
			t.Setenv("MAVEN_BASEDIR", "")
			dir := physicalTempDir(t)
			t.Chdir(dir)
			args := []string{"-s", expr + "/settings.xml", "-Duser.home=" + t.TempDir(), "verify"}
			want := filepath.Join(dir, expr, "settings.xml")

			got, explicit := mavenUserSettingsFile(nil, dir, args)
			assert.Equal(t, want, got)
			assert.True(t, explicit)

			r := mavenSettingsFor(t, args)
			assertFirewallProxyActive(t, r.settings, "52999")
			assert.NotContains(t, r.settings, "<mirrors>")
			assert.Equal(t, missingSettingsWarning(want), r.warnings)
		})
	}
}

// missingSettingsWarning is what glab prints for a settings file named with
// -s that it can't find.
func missingSettingsWarning(path string) string {
	return fmt.Sprintf("warning: could not find the Maven settings file %q named with -s or --settings, so mvn runs with the firewall's own settings and that file's mirrors, credentials and profiles don't apply\n", path)
}

// TestMavenMissingSettingsWarning pins that a settings file named with -s
// that doesn't exist is reported, wherever the -s came from, while a missing
// default settings.xml stays quiet.
func TestMavenMissingSettingsWarning(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.xml")
	tests := []struct {
		name      string
		mavenArgs string
		config    []string
		args      []string
	}{
		{name: "when the command line names it with -s", args: []string{"-s", missing}},
		{name: "when the command line names it with --settings=", args: []string{"--settings=" + missing}},
		{name: "when MAVEN_ARGS names it", mavenArgs: "-s " + missing},
		{name: "when .mvn/maven.config names it", config: []string{"-s", missing}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MAVEN_ARGS", tc.mavenArgs)
			cwd := t.TempDir()
			if tc.config != nil {
				writeMavenConfig(t, cwd, tc.config...)
			}
			t.Chdir(cwd)

			r := mavenSettingsFor(t, append(tc.args, "-Duser.home="+t.TempDir(), "verify"))

			assertFirewallProxyActive(t, r.settings, "52999")
			assert.Equal(t, missingSettingsWarning(missing), r.warnings)
		})
	}

	t.Run("when the default settings.xml doesn't exist", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Chdir(t.TempDir())

		r := mavenSettingsFor(t, []string{"-Duser.home=" + t.TempDir(), "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.Empty(t, r.warnings)
	})
}

// TestMavenSettingsWarningsQuoteThePath pins that a settings path reaches
// stderr only %q-quoted, so control characters in it can't drive the terminal.
func TestMavenSettingsWarningsQuoteThePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file names can't hold control characters")
	}
	const name = "evil\x1b[2J\n\u202e.xml"

	t.Run("when the file doesn't exist", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		path := filepath.Join(t.TempDir(), name)

		r := mavenSettingsFor(t, []string{"-s", path, "-Duser.home=" + t.TempDir(), "verify"})

		assert.Equal(t, missingSettingsWarning(path), r.warnings)
		assertQuotedOnly(t, r.warnings)
	})

	t.Run("when the file can't be merged", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		path := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.WriteFile(path, []byte("<settings><mirrors></settings>"), 0o600))

		r := mavenSettingsFor(t, []string{"-s", path, "-Duser.home=" + t.TempDir(), "verify"})

		assert.Equal(t, fmt.Sprintf("warning: could not merge the Dependency Firewall proxy into the Maven settings at %q, so mvn runs with the firewall's own settings and that file's mirrors, credentials and profiles don't apply\n", path), r.warnings)
		assertQuotedOnly(t, r.warnings)
	})
}

// assertQuotedOnly checks the control characters in the test's file name
// appear in warning only as escapes.
func assertQuotedOnly(t *testing.T, warning string) {
	t.Helper()
	assert.Contains(t, warning, `/evil\x1b[2J\n\u202e.xml"`)
	assert.NotContains(t, warning, "\x1b")
	assert.NotContains(t, warning, "\u202e")
	assert.Equal(t, 1, strings.Count(warning, "\n"), "only the trailing newline may be raw")
}

// TestMergeMavenProxiesReadsWhatMavenReads pins the encodings and entities
// Maven accepts in a settings file, and where the merge still gives up.
func TestMergeMavenProxiesReadsWhatMavenReads(t *testing.T) {
	const proxies = "    <proxy><id>df</id></proxy>\n"

	for _, enc := range []string{"ISO-8859-1", "windows-1252"} {
		t.Run("when the file declares "+enc+" and holds only ASCII", func(t *testing.T) {
			head := `<?xml version="1.0" encoding="` + enc + `"?>` + "\n<settings>\n" +
				"  <mirrors>\n    <mirror><id>corp</id><mirrorOf>*</mirrorOf><url>https://nexus.example.com/</url></mirror>\n  </mirrors>\n" +
				"  <proxies>"
			tail := "\n  </proxies>\n</settings>\n"

			got, err := mergeMavenProxies(writeUserSettings(t, head+tail), proxies)

			require.NoError(t, err)
			assert.Equal(t, head+proxies+tail, string(got))
		})
	}

	for _, decl := range []string{`encoding="ISO-2022-JP"`, `encoding = "ISO-2022-JP"`, "encoding =\t'ISO-2022-JP'", `encoding="UTF-16"`, `encoding="Shift_JIS"`} {
		t.Run("when the file declares "+decl, func(t *testing.T) {
			// In ISO-2022-JP, Java reads the bytes after ESC $ B as two-byte
			// characters, so Maven sees one comment where Go sees <proxies>.
			_, err := mergeMavenProxies(writeUserSettings(t, `<?xml version="1.0" `+decl+`?>`+
				"\n<settings>\n<!--\x1b$B--><proxies></proxies><!--\x1b(B-->\n</settings>\n"), proxies)

			assert.ErrorContains(t, err, "unsupported encoding")
		})
	}

	for _, prolog := range []string{
		"<?xm\nl version=\"1.0\" encoding=\"ISO-2022-JP\"?>",
		"<?xm\rl version=\"1.0\" encoding=\"ISO-2022-JP\"?>",
		"<?xm\r\nl version=\"1.0\" encoding=\"ISO-2022-JP\"?>",
		"<?xm\nl version=\"1.0\" encoding\v=\v\"ISO-2022-JP\"?>",
		"<?xm\nl version=\"1.0\" encoding\f=\f\"ISO-2022-JP\"?>",
	} {
		t.Run(fmt.Sprintf("when Maven joins the lines of %q into one declaration", prolog), func(t *testing.T) {
			_, err := mergeMavenProxies(writeUserSettings(t, prolog+
				"\n<settings>\n<!--\x1b$B--><proxies></proxies><!--\x1b(B-->\n</settings>\n"), proxies)

			assert.ErrorContains(t, err, "unsupported encoding")
		})
	}

	for _, prolog := range []string{
		"<?xm\nl version=\"1.0\" encoding=\"UTF-8\"?>",
		"<?xml version=\"1.0\"\nencoding=\"ISO-8859-1\"?>",
		"<?xml version=\"1.0\"\r\nencoding=\"UTF-8\"?>\r",
	} {
		t.Run(fmt.Sprintf("when Maven joins the lines of %q into an ASCII-compatible declaration", prolog), func(t *testing.T) {
			head := prolog + "\n<settings>\r\n  <proxies>"
			tail := "\r\n  </proxies>\r\n</settings>\r\n"

			got, err := mergeMavenProxies(writeUserSettings(t, head+tail), proxies)

			require.NoError(t, err)
			assert.Equal(t, head+proxies+tail, string(got))
		})
	}

	t.Run("when the declaration hides its encoding from glab's own check", func(t *testing.T) {
		// The ">" inside standalone ends the prolog glab reads, so Go's charset
		// hook is what refuses the encoding.
		_, err := mergeMavenProxies(writeUserSettings(t, `<?xml version="1.0" standalone=">" encoding="ISO-2022-JP"?>`+
			"\n<settings>\n<!--\x1b$B--><proxies></proxies><!--\x1b(B-->\n</settings>\n"), proxies)

		assert.ErrorContains(t, err, "unsupported encoding")
	})

	t.Run("when the file declares UTF8, which Java reads as UTF-8", func(t *testing.T) {
		head := `<?xml version="1.0" encoding="UTF8"?>` + "\n<settings>\n  <proxies>"
		tail := "\n  </proxies>\n</settings>\n"

		got, err := mergeMavenProxies(writeUserSettings(t, head+tail), proxies)

		require.NoError(t, err)
		assert.Equal(t, head+proxies+tail, string(got))
	})

	t.Run("when a Latin-1 file has a raw non-ASCII byte after <proxies>", func(t *testing.T) {
		// Parsing stops at the insertion point, so later bytes are copied
		// without being decoded.
		head := `<?xml version="1.0" encoding="ISO-8859-1"?>` + "\n<settings>\n  <proxies>"
		tail := "\n  </proxies>\n  <servers><server><id>corp</id><username>Jos\xe9</username></server></servers>\n</settings>\n"

		got, err := mergeMavenProxies(writeUserSettings(t, head+tail), proxies)

		require.NoError(t, err)
		assert.Equal(t, head+proxies+tail, string(got))
	})

	t.Run("when a Latin-1 file has a raw non-ASCII byte before <proxies>", func(t *testing.T) {
		_, err := mergeMavenProxies(writeUserSettings(t, `<?xml version="1.0" encoding="ISO-8859-1"?>`+
			"\n<settings>\n  <servers><server><id>corp</id><username>Jos\xe9</username></server></servers>\n  <proxies>\n  </proxies>\n</settings>\n"), proxies)

		assert.ErrorContains(t, err, "invalid UTF-8")
	})

	t.Run("when the file uses an HTML entity such as &nbsp;", func(t *testing.T) {
		src := "<settings>\n  <servers><server><id>corp</id><username>svc&nbsp;user</username></server></servers>\n</settings>\n"

		got, err := mergeMavenProxies(writeUserSettings(t, src), proxies)

		require.NoError(t, err)
		assert.Equal(t, strings.Replace(src, "</settings>", "  <proxies>\n"+proxies+"  </proxies>\n</settings>", 1), string(got))
	})
}

// TestMavenUserSettingsFallback pins when the caller's settings are replaced
// by the firewall's own, and that the caller is told.
func TestMavenUserSettingsFallback(t *testing.T) {
	t.Run("when the merge error would quote the file", func(t *testing.T) {
		// Go's decoder quotes the text after a bad "&", here part of a password.
		t.Setenv("MAVEN_ARGS", "")
		t.Setenv("GLAB_DEBUG", "true")
		var debugLog strings.Builder
		log.SetOutput(&debugLog)
		t.Cleanup(func() { log.SetOutput(os.Stderr) })
		userPath := writeUserSettings(t, "<settings>\n  <servers><server><id>corp</id><password>abc&hunter2!</password></server></servers>\n  <proxies/>\n</settings>\n")

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assert.Contains(t, r.warnings, "could not merge")
		assert.NotContains(t, r.warnings, "hunter2")
		assert.Contains(t, debugLog.String(), "could not merge the maven settings")
		assert.NotContains(t, debugLog.String(), "hunter2", "a CI log with GLAB_DEBUG on must not show the password")
	})

	t.Run("when a Latin-1 file has a raw non-ASCII byte before <proxies>", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, `<?xml version="1.0" encoding="ISO-8859-1"?>`+"\n"+
			strings.Replace(userSettingsXML, "<username>svc</username>", "<username>Jos\xe9</username>", 1))

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.NotContains(t, r.settings, "nexus.example.com", "a file the merge can't parse is replaced, not copied")
		assert.Contains(t, r.warnings, fmt.Sprintf("warning: could not merge the Dependency Firewall proxy into the Maven settings at %q, so mvn runs with the firewall's own settings and that file's mirrors, credentials and profiles don't apply\n", userPath))
		assert.NotContains(t, r.warnings, "invalid UTF-8", "the decoder's message stays in the debug log")
		assert.Contains(t, r.warnings, "mirrors, credentials and profiles don't apply")
		assert.True(t, strings.HasSuffix(r.warnings, "\n"))
	})

	t.Run("when the settings file is malformed", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, "<settings><mirrors></settings>")

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.Contains(t, r.warnings, fmt.Sprintf("warning: could not merge the Dependency Firewall proxy into the Maven settings at %q, so mvn runs with the firewall's own settings and that file's mirrors, credentials and profiles don't apply\n", userPath))
		assert.NotContains(t, r.warnings, "XML syntax error", "the decoder's message stays in the debug log")
	})

	t.Run("when the file declares ISO-2022-JP", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		userPath := writeUserSettings(t, `<?xml version="1.0" encoding = "ISO-2022-JP"?>`+
			"\n<settings>\n<!--\x1b$B--><proxies></proxies><!--\x1b(B-->\n</settings>\n")

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assertFirewallProxyActive(t, r.settings, "52999")
		assert.NotContains(t, r.settings, "\x1b", "a file glab can't read as Maven does is replaced, not copied")
		assert.Contains(t, r.warnings, fmt.Sprintf("warning: could not merge the Dependency Firewall proxy into the Maven settings at %q", userPath))
	})

	t.Run("when the warning function is nil", func(t *testing.T) {
		userPath := writeUserSettings(t, "<settings><mirrors></settings>")
		path := filepath.Join(t.TempDir(), "settings.xml")

		assert.NotPanics(t, func() { writeMavenProxySettings(path, "http://127.0.0.1:52999", userPath, true, nil, nil) })
	})

	t.Run("when the settings file starts like Maven's own template", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		src := `<?xml version="1.0" encoding="UTF-8"?>

<!--
Licensed to the Apache Software Foundation (ASF) under one
or more contributor license agreements.  See the NOTICE file
distributed with this work for additional information
regarding copyright ownership.  The ASF licenses this file
to you under the Apache License, Version 2.0 (the
"License"); you may not use this file except in compliance
with the License.  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing,
software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
KIND, either express or implied.  See the License for the
specific language governing permissions and limitations
under the License.
-->
<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:schemaLocation="http://maven.apache.org/SETTINGS/1.2.0 https://maven.apache.org/xsd/settings-1.2.0.xsd">
  <proxies>
    <!-- proxy
    <proxy>
      <id>optional</id>
      <active>true</active>
      <protocol>http</protocol>
      <host>proxy.host.net</host>
      <port>80</port>
    </proxy>
    -->
  </proxies>
` + strings.TrimPrefix(userSettingsXML, `<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">`+"\n")
		userPath := writeUserSettings(t, src)

		r := mavenSettingsFor(t, []string{"-s", userPath, "verify"})

		assertProxyAndUserContent(t, r.settings, "52999")
		assert.Empty(t, r.warnings)
		head, tail, _ := strings.Cut(src, "<proxies>")
		assert.True(t, strings.HasPrefix(r.settings, head+"<proxies>"), "the prolog and licence must be copied as is:\n%s", r.settings)
		assert.True(t, strings.HasSuffix(r.settings, tail), "the rest of the file must be copied as is:\n%s", r.settings)
	})
}

func TestMavenSetupStopsOnAMissingSettingsFile(t *testing.T) {
	existing := writeUserSettings(t, userSettingsXML)
	missing := filepath.Join(t.TempDir(), "missing.xml")
	tests := []struct {
		name      string
		mavenArgs string
		config    []string
		args      []string
		wantErr   bool
	}{
		{name: "when -s names a file that doesn't exist", args: []string{"-s", missing}, wantErr: true},
		{name: "when -s names a directory", args: []string{"-s", filepath.Dir(missing)}, wantErr: true},
		{name: "when -s is empty", args: []string{"-s", ""}, wantErr: true},
		{name: "when -s is an empty variable, which only warns", args: []string{"-s", "${env.DF_TEST_EMPTY_VARIABLE}"}},
		{name: "when -s names a path under a file", args: []string{"-s", filepath.Join(existing, "x.xml")}, wantErr: true},
		{name: "when -settings names a file that exists", args: []string{"-settings", existing}},
		{name: "when -settings= names a file that exists", args: []string{"-settings=" + existing}},
		{name: "when MAVEN_ARGS double-quotes a file that exists", mavenArgs: `-s "` + existing + `"`},
		{name: "when .mvn/maven.config double-quotes a file that exists", config: []string{"-s", `"` + existing + `"`}},
		{name: "when MAVEN_ARGS single-quotes a file that exists", mavenArgs: "-s '" + existing + "'"},
		{name: "when mvn only prints its version", args: []string{"-s", missing, "--version"}},
		{name: "when mvn only prints its version with -v", args: []string{"-v", "-s", missing}},
		{name: "when mvn only prints its help", args: []string{"-s", missing, "-h"}},
		{name: "when MAVEN_ARGS asks for the version", mavenArgs: "--version", args: []string{"-s", missing}},
		{name: "when -V shows the version and builds", args: []string{"-V", "-s", missing}, wantErr: true},
		{name: "when -show-version is spelled in full", args: []string{"-show-version"}},
		{name: "when -strict-checksums is spelled in full", args: []string{"-strict-checksums"}},
		{name: "when -strict-checksums has a value", args: []string{"-strict-checksums=validate"}},
		{name: "when -show abbreviates a long option", args: []string{"-show"}, wantErr: true},
		{name: "when a cluster starts with -v", args: []string{"-vq", "-s", missing}},
		{name: "when a cluster starts with -h", args: []string{"-hX", "-s", missing}},
		{name: "when a cluster has -v after another flag", args: []string{"-qv", "-s", missing}, wantErr: true},
		{name: "when --version has a value", args: []string{"--version=x", "-s", missing}},
		{name: "when --v stands for --version", args: []string{"--v", "-s", missing}},
		{name: "when --version is double-quoted in MAVEN_ARGS", mavenArgs: `"--version"`, args: []string{"-s", missing}},
		{name: "when MAVEN_ARGS opens a quote before -v", mavenArgs: `"-v`, args: []string{"-s", missing}},
		{name: "when .mvn/maven.config asks for the version", config: []string{"--version"}, args: []string{"-s", missing}},
		{name: "when --vers isn't a long option", args: []string{"--vers", "-s", missing}, wantErr: true},
		{name: "when --settings= names a file that doesn't exist", args: []string{"--settings=" + missing}, wantErr: true},
		{name: "when MAVEN_ARGS names a file that doesn't exist", mavenArgs: "-s " + missing, wantErr: true},
		{name: "when .mvn/maven.config names a file that doesn't exist", config: []string{"-s", missing}, wantErr: true},
		{name: "when -s names a file that exists", args: []string{"-s", existing}},
		{name: "when -s has an expression glab can't expand", args: []string{"-s", "${no.such.property}/settings.xml"}},
		{name: "when the default settings.xml doesn't exist"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateMavenLauncher(t)
			t.Setenv("MAVEN_ARGS", tc.mavenArgs)
			t.Setenv("DF_TEST_EMPTY_VARIABLE", "")
			cwd := t.TempDir()
			if tc.config != nil {
				writeMavenConfig(t, cwd, tc.config...)
			}
			t.Chdir(cwd)

			res, err := mavenSetup(t, mavenWithoutInstall(t), append(tc.args, "-Duser.home="+t.TempDir(), "verify"))

			if tc.wantErr {
				assert.ErrorContains(t, err, "named with -s or --settings doesn't exist")
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, res.stop, "Run calls stop once the child exits")
		})
	}
}

func mavenSetup(t *testing.T, m PackageManager, args []string) (setupResult, error) {
	t.Helper()
	a, ok := m.(argsManager)
	require.True(t, ok)
	a.SetArgs(args)
	s, ok := m.(setupManager)
	require.True(t, ok, "the Maven manager checks the settings file before mvn starts")
	return s.Setup(t.Context(), setupInput{})
}

func TestRunStopsWhenTheNamedSettingsFileIsMissing(t *testing.T) {
	isolateMavenLauncher(t)
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	t.Chdir(t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing.xml")
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeExecutor{}
	ran := false
	exec.onExec = func() { ran = true }

	err := Run(t.Context(), mavenWithoutInstall(t), RunOptions{
		IO:        ios,
		Executor:  exec,
		BaseDir:   t.TempDir(),
		ProjectID: "group/proj",
		Args:      []string{"-s", missing, "-Duser.home=" + t.TempDir(), "verify"},
	})

	require.ErrorContains(t, err, fmt.Sprintf("%q named with -s or --settings doesn't exist", missing))
	assert.False(t, ran, "mvn must not start, as it wouldn't on its own")
}

func TestMavenInfoOnlyRunDoesNotWarn(t *testing.T) {
	t.Setenv("MAVEN_ARGS", "")
	t.Chdir(t.TempDir())

	r := mavenSettingsFor(t, []string{"-s", filepath.Join(t.TempDir(), "missing.xml"), "--version"})

	assertFirewallProxyActive(t, r.settings, "52999")
	assert.Empty(t, r.warnings, "mvn only prints its version, so it never reads the settings file")
}

// physicalTempDir is t.TempDir with symlinks resolved, as Java's user.dir is.
func physicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

// isolateMavenLauncher keeps the machine's Maven environment and rc files out
// of a test, so only the test's own arguments choose the settings files.
func isolateMavenLauncher(t *testing.T) {
	t.Helper()
	withSystemMavenRC(t)
	t.Setenv("HOME", t.TempDir())
	for _, v := range []string{"MAVEN_ARGS", "MAVEN_BASEDIR", "MAVEN_SKIP_RC", "MAVEN_OPTS", "MAVEN_DEBUG_OPTS", "JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "JDK_JAVA_OPTIONS", "USERPROFILE"} {
		t.Setenv(v, "")
	}
}

// TestMavenSetupLeavesUncertainPathsToTheWarning pins paths Maven 3.9.12
// resolves differently from a plain join, so glab must not stop the build.
func TestMavenSetupLeavesUncertainPathsToTheWarning(t *testing.T) {
	missing := filepath.Join(physicalTempDir(t), "missing.xml")
	run := func(t *testing.T, args ...string) error {
		t.Helper()
		_, err := mavenSetup(t, mavenWithoutInstall(t), append(args, "verify"))
		return err
	}

	t.Run("when an absolute -s ends in a separator", func(t *testing.T) {
		isolateMavenLauncher(t)
		t.Chdir(t.TempDir())
		ok := writeUserSettings(t, userSettingsXML)
		assert.NoError(t, run(t, "-s", ok+"/"), "java.io.File drops the trailing separator")
	})

	t.Run("when the working directory is reached through a symlink", func(t *testing.T) {
		isolateMavenLauncher(t)
		root := physicalTempDir(t)
		require.NoError(t, os.MkdirAll(filepath.Join(root, "real", "proj"), 0o700))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "real", "shared"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "real", "shared", "ok.xml"), []byte(userSettingsXML), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "link"), 0o700))
		require.NoError(t, os.Symlink(filepath.Join(root, "real", "proj"), filepath.Join(root, "link", "proj")))
		t.Chdir(filepath.Join(root, "link", "proj"))
		assert.NoError(t, run(t, "-s", "../shared/ok.xml"), "Java resolves .. against the physical directory")
	})

	t.Run("when MAVEN_ARGS holds a glob", func(t *testing.T) {
		isolateMavenLauncher(t)
		t.Chdir(t.TempDir())
		t.Setenv("MAVEN_ARGS", "-s "+filepath.Join(t.TempDir(), "ok*.xml"))
		assert.NoError(t, run(t), "the launcher expands MAVEN_ARGS unquoted")
	})

	t.Run("when --s names the file", func(t *testing.T) {
		isolateMavenLauncher(t)
		cwd := physicalTempDir(t)
		t.Chdir(cwd)
		ok := writeUserSettings(t, userSettingsXML)

		got, explicit := mavenUserSettingsFile(os.Environ(), cwd, []string{"--s", ok, "verify"})
		assert.Equal(t, ok, got, "commons-cli strips both dashes")
		assert.True(t, explicit)
		require.Error(t, run(t, "--s", missing))
		assert.Error(t, run(t, "--s="+missing))
	})
}
