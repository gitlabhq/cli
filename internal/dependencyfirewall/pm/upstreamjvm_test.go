//go:build !integration

package pm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

const mavenSettingsFmt = "<settings xmlns=\"http://maven.apache.org/SETTINGS/1.0.0\"><proxies>%s</proxies></settings>"

func mavenSettingsXML(proxies ...string) string {
	return fmt.Sprintf(mavenSettingsFmt, strings.Join(proxies, ""))
}

// mavenInstall adds the files of a Maven 3.9 installation at home, which
// mavenHome accepts, to files.
func mavenInstall(home string, files map[string]string) map[string]string {
	files[home+"/bin/mvn"] = ""
	files[home+"/bin/m2.conf"] = stockM2Conf
	files[home+"/lib/maven-core-3.9.12.jar"] = ""
	return files
}

// withUserHome points Java's user.home at the temp root, as the JVM reads the
// account's home directory rather than HOME.
func withUserHome(cases []configCase, env string) []configCase {
	for i := range cases {
		cases[i].env = append([]string{env}, cases[i].env...)
	}
	return cases
}

func TestMavenUpstreamProxyFromSettings(t *testing.T) {
	t.Parallel()
	corp := "<proxy><id>corp</id><protocol>https</protocol><host>corp</host><port>3128</port></proxy>"
	other := "<proxy><id>other</id><protocol>https</protocol><host>other</host><port>1</port></proxy>"
	runConfigCases(t, Maven(), withUserHome([]configCase{
		{
			name:   "an active proxy in ~/.m2/settings.xml",
			files:  map[string]string{"home/.m2/settings.xml": mavenSettingsXML(corp)},
			want:   upstreamProxy{Source: "$ROOT/home/.m2/settings.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:   "an http proxy without a port serves https on 8080",
			files:  map[string]string{"home/.m2/settings.xml": mavenSettingsXML("<proxy><host> corp </host></proxy>")},
			want:   upstreamProxy{Source: "$ROOT/home/.m2/settings.xml (proxy default)", URL: "http://corp:8080"},
			wantOK: true,
		},
		{
			name:  "an https proxy wins over an earlier http one, which serves its non-proxy hosts",
			files: map[string]string{"home/.m2/settings.xml": mavenSettingsXML("<proxy><id>plain</id><host>plain</host><port>2</port></proxy>", "<proxy><id>corp</id><protocol>HTTPS</protocol><host>corp</host><port>3128</port><nonProxyHosts>*.internal.example|repo.maven.apache.org||</nonProxyHosts></proxy>")},
			want: upstreamProxy{
				Source: "$ROOT/home/.m2/settings.xml (proxy corp)", URL: "http://corp:3128",
				NonProxyHosts: []string{"*.internal.example", "repo.maven.apache.org"},
				Fallbacks:     []upstreamProxy{{Source: "$ROOT/home/.m2/settings.xml (proxy plain)", URL: "http://plain:2"}},
			},
			wantOK: true,
		},
		{
			name:   "inactive and non-http proxies are skipped",
			files:  map[string]string{"home/.m2/settings.xml": mavenSettingsXML("<proxy><id>off</id><active>yes</active><host>off</host></proxy>", "<proxy><id>socks</id><protocol>socks5</protocol><host>socks</host></proxy>")},
			env:    []string{"HTTPS_PROXY=http://env:1"},
			wantOK: false,
		},
		{
			name:   "-s replaces ~/.m2/settings.xml",
			files:  map[string]string{"home/.m2/settings.xml": mavenSettingsXML(other), "ci/settings.xml": mavenSettingsXML(corp)},
			args:   []string{"-B", "-s", "$ROOT/ci/settings.xml", "package"},
			want:   upstreamProxy{Source: "$ROOT/ci/settings.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:   "--settings=file and the first settings option in MAVEN_ARGS win",
			files:  map[string]string{"a.xml": mavenSettingsXML(corp), "b.xml": mavenSettingsXML(other)},
			env:    []string{"MAVEN_ARGS=--settings=$ROOT/a.xml"},
			args:   []string{"-s$ROOT/b.xml"},
			want:   upstreamProxy{Source: "$ROOT/a.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:   "the project's .mvn/maven.config is not read",
			files:  map[string]string{"proj/.mvn/maven.config": "-s\n$ROOT/a.xml\n", "a.xml": mavenSettingsXML(corp), "home/.m2/settings.xml": mavenSettingsXML(other)},
			want:   upstreamProxy{Source: "$ROOT/home/.m2/settings.xml (proxy other)", URL: "http://other:1"},
			wantOK: true,
		},
		{
			name:   "an ancestor's .mvn/maven.config is not read",
			files:  map[string]string{".mvn/maven.config": "-s\n$ROOT/a.xml\n", "a.xml": mavenSettingsXML(corp), "home/.m2/settings.xml": mavenSettingsXML(other)},
			dir:    "proj/module",
			want:   upstreamProxy{Source: "$ROOT/home/.m2/settings.xml (proxy other)", URL: "http://other:1"},
			wantOK: true,
		},
		{
			name:   "the global settings of the mvn on PATH apply",
			files:  mavenInstall("maven", map[string]string{"maven/conf/settings.xml": mavenSettingsXML(corp)}),
			env:    []string{"PATH=$ROOT/maven/bin"},
			want:   upstreamProxy{Source: "$ROOT/maven/conf/settings.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name: "Homebrew's mvn takes the global settings from libexec",
			files: mavenInstall("brew/Cellar/maven/3.9.12/libexec", map[string]string{
				"brew/Cellar/maven/3.9.12/bin/mvn":                   "",
				"brew/Cellar/maven/3.9.12/libexec/conf/settings.xml": mavenSettingsXML(corp),
			}),
			env:    []string{"PATH=$ROOT/brew/Cellar/maven/3.9.12/bin"},
			want:   upstreamProxy{Source: "$ROOT/brew/Cellar/maven/3.9.12/libexec/conf/settings.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:  "user proxies come first, and replace global ones with the same id",
			files: map[string]string{"home/.m2/settings.xml": mavenSettingsXML(other, "<proxy><id>corp</id><active>false</active><host>off</host></proxy>"), "global.xml": mavenSettingsXML(corp, "<proxy><id>third</id><protocol>https</protocol><host>third</host><port>3</port></proxy>")},
			args:  []string{"-gs", "$ROOT/global.xml"},
			want: upstreamProxy{
				Source: "$ROOT/home/.m2/settings.xml (proxy other)", URL: "http://other:1",
				Fallbacks: []upstreamProxy{{Source: "$ROOT/global.xml (proxy third)", URL: "http://third:3"}},
			},
			wantOK: true,
		},
		{
			name:   "-Dmaven.conf names the global settings directory",
			files:  map[string]string{"conf/settings.xml": mavenSettingsXML(corp)},
			args:   []string{"-Dmaven.conf=$ROOT/conf"},
			want:   upstreamProxy{Source: "$ROOT/conf/settings.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:   "-Duser.home on the command line wins over MAVEN_OPTS",
			files:  map[string]string{"other/.m2/settings.xml": mavenSettingsXML(corp)},
			args:   []string{"-D", "user.home=$ROOT/other"},
			want:   upstreamProxy{Source: "$ROOT/other/.m2/settings.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:   "values expand environment variables and properties, and carry credentials",
			files:  map[string]string{"home/.m2/settings.xml": mavenSettingsXML("<proxy><host>${env.PROXY_HOST}</host><port>3128</port><username>${proxy.user}</username><password>${env.PROXY_PASSWORD}</password></proxy>")},
			env:    []string{"PROXY_HOST=corp", "PROXY_PASSWORD=p@ss"},
			args:   []string{"-Dproxy.user=alice"},
			want:   upstreamProxy{Source: "$ROOT/home/.m2/settings.xml (proxy default)", URL: "http://alice:p%40ss@corp:3128"},
			wantOK: true,
		},
		{
			name:    "an encrypted password fails",
			files:   map[string]string{"home/.m2/settings.xml": mavenSettingsXML("<proxy><host>corp</host><username>alice</username><password>{COQLCE6DU6GtcS5P=}</password></proxy>")},
			wantErr: "the proxy password is encrypted, which glab does not support",
		},
		{
			name: "a missing settings file named with -s is left to Setup",
			args: []string{"-s", "$ROOT/missing.xml"},
		},
		{
			name:   "-s expands ${env.NAME} as Maven does",
			files:  map[string]string{"ci/settings.xml": mavenSettingsXML(corp)},
			env:    []string{"CI_SETTINGS=$ROOT/ci"},
			args:   []string{"-s", "${env.CI_SETTINGS}/settings.xml"},
			want:   upstreamProxy{Source: "$ROOT/ci/settings.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:   "-gs expands ${env.NAME} as Maven does",
			files:  map[string]string{"ci/global.xml": mavenSettingsXML(corp)},
			env:    []string{"CI_GLOBAL=$ROOT/ci"},
			args:   []string{"-gs", "${env.CI_GLOBAL}/global.xml"},
			want:   upstreamProxy{Source: "$ROOT/ci/global.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:   "a settings file declared Latin-1 with ASCII content is read",
			files:  map[string]string{"home/.m2/settings.xml": strings.Replace(mavenSettingsXML(corp), "<settings", "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<settings", 1)},
			want:   upstreamProxy{Source: "$ROOT/home/.m2/settings.xml (proxy corp)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:    "malformed XML fails as in Maven",
			files:   map[string]string{"home/.m2/settings.xml": "<settings><proxies><proxy>"},
			wantErr: "failed to parse",
		},
		{
			name:    "a port that is not a number fails",
			files:   map[string]string{"home/.m2/settings.xml": mavenSettingsXML("<proxy><host>corp</host><port>${env.PORT}</port></proxy>")},
			wantErr: "the proxy port is not a number",
		},
		{
			name:  "an empty -Dmaven.conf puts the global settings at the filesystem root, not the installation",
			files: mavenInstall("maven", map[string]string{"maven/conf/settings.xml": mavenSettingsXML(corp)}),
			env:   []string{"PATH=$ROOT/maven/bin"},
			args:  []string{"-Dmaven.conf="},
		},
		{
			name:   "-s inside the project is not read, and the global settings still apply",
			files:  map[string]string{"proj/ci/settings.xml": mavenSettingsXML(corp), "home/.m2/settings.xml": mavenSettingsXML(corp), "global.xml": mavenSettingsXML(other)},
			args:   []string{"-s", "$ROOT/proj/ci/settings.xml", "-gs", "$ROOT/global.xml"},
			want:   upstreamProxy{Source: "$ROOT/global.xml (proxy other)", URL: "http://other:1"},
			wantOK: true,
		},
		{
			name:  "-gs inside the project is not read",
			files: map[string]string{"proj/global.xml": mavenSettingsXML(corp)},
			args:  []string{"-gs", "$ROOT/proj/global.xml"},
		},
		{
			name:  "a user.home inside the project is not read",
			files: map[string]string{"proj/home/.m2/settings.xml": mavenSettingsXML(corp)},
			args:  []string{"-Duser.home=$ROOT/proj/home"},
		},
		{
			name:  "a maven.conf inside the project is not read",
			files: map[string]string{"proj/conf/settings.xml": mavenSettingsXML(corp)},
			args:  []string{"-Dmaven.conf=$ROOT/proj/conf"},
		},
		{
			name:  "the global settings of an mvn inside the project are not read",
			files: mavenInstall("proj/maven", map[string]string{"proj/maven/conf/settings.xml": mavenSettingsXML(corp)}),
			env:   []string{"PATH=$ROOT/proj/maven/bin"},
		},
		{
			name:  "a settings file in the git work tree is not read from a subdirectory",
			files: map[string]string{"proj/.git/HEAD": "", "proj/ci.xml": mavenSettingsXML(corp)},
			dir:   "proj/sub",
			env:   []string{"MAVEN_ARGS=-s $ROOT/proj/ci.xml"},
		},
	}, "MAVEN_OPTS=-Duser.home=$ROOT/home"))
}

func TestGradleUpstreamProxyFromProperties(t *testing.T) {
	t.Parallel()
	runConfigCases(t, Gradle(), withUserHome([]configCase{
		{
			name: "the Gradle user home's gradle.properties, with credentials and non-proxy hosts",
			files: map[string]string{
				"guh/gradle.properties": "systemProp.https.proxyHost=corp\nsystemProp.https.proxyPort : 3128\nsystemProp.https.proxyUser alice\nsystemProp.https.proxyPassword=s3cr\\\n    et\nsystemProp.http.nonProxyHosts=*.internal.example|repo.*.org|localhost\n",
			},
			want: upstreamProxy{
				Source: "$ROOT/guh/gradle.properties (systemProp.https.proxyHost)", URL: "http://alice:s3cret@corp:3128",
				NonProxyHosts: []string{"*.internal.example", "localhost"},
			},
			wantOK: true,
		},
		{
			name: "the Gradle user home wins over the installation, key by key",
			files: map[string]string{
				"guh/gradle.properties":    "systemProp.https.proxyHost=user\n",
				"gradle/bin/gradle":        "",
				"gradle/gradle.properties": "systemProp.https.proxyHost=installation\nsystemProp.https.proxyPort=3128\n",
			},
			env:    []string{"PATH=$ROOT/gradle/bin"},
			want:   upstreamProxy{Source: "$ROOT/guh/gradle.properties (systemProp.https.proxyHost)", URL: "http://user:3128"},
			wantOK: true,
		},
		{
			name:   "-D on the command line wins, and the port defaults to 443",
			files:  map[string]string{"guh/gradle.properties": "systemProp.https.proxyHost=user\nsystemProp.https.proxyPort=3128\n"},
			args:   []string{"build", "--system-prop", "https.proxyPort=1", "-Dhttps.proxyHost=cli", "-Dhttps.proxyPort=443"},
			want:   upstreamProxy{Source: "-Dhttps.proxyHost", URL: "http://cli:443"},
			wantOK: true,
		},
		{
			name:   "an empty https.proxyHost connects directly",
			files:  map[string]string{"guh/gradle.properties": "systemProp.https.proxyHost=\n"},
			env:    []string{"HTTPS_PROXY=http://env:1"},
			want:   upstreamProxy{Source: "$ROOT/guh/gradle.properties (systemProp.https.proxyHost)"},
			wantOK: true,
		},
		{
			name:   "http.proxyHost alone does not serve https",
			files:  map[string]string{"guh/gradle.properties": "systemProp.http.proxyHost=plain\nsystemProp.https.proxyPort=3128\n"},
			wantOK: false,
		},
		{
			name:   "-g chooses the Gradle user home",
			files:  map[string]string{"other/gradle.properties": "systemProp.https.proxyHost=\\u0063orp\nsystemProp.https.proxyPort=3128\n", "guh/gradle.properties": "systemProp.https.proxyHost=user\n"},
			args:   []string{"-g", "$ROOT/other"},
			want:   upstreamProxy{Source: "$ROOT/other/gradle.properties (systemProp.https.proxyHost)", URL: "http://corp:3128"},
			wantOK: true,
		},
		{
			name:   "an empty -g counts as unset",
			files:  map[string]string{"guh/gradle.properties": "systemProp.https.proxyHost=user\n"},
			args:   []string{"-g", ""},
			want:   upstreamProxy{Source: "$ROOT/guh/gradle.properties (systemProp.https.proxyHost)", URL: "http://user:443"},
			wantOK: true,
		},
		{
			name:   "the installation's gradle.properties applies",
			files:  map[string]string{"gradle/bin/gradle": "", "gradle/gradle.properties": "systemProp.https.proxyHost=corp\n# comment\n! comment\n"},
			env:    []string{"PATH=$ROOT/gradle/bin"},
			want:   upstreamProxy{Source: "$ROOT/gradle/gradle.properties (systemProp.https.proxyHost)", URL: "http://corp:443"},
			wantOK: true,
		},
		{
			name:    "a port that is not a number fails",
			files:   map[string]string{"guh/gradle.properties": "systemProp.https.proxyHost=corp\nsystemProp.https.proxyPort=31x8\n"},
			wantErr: "https.proxyPort is not a number",
		},
		{
			name:  "the project's gradle.properties is not read",
			files: map[string]string{"proj/gradle.properties": "systemProp.https.proxyHost=project\n"},
		},
		{
			name:  "the root project's gradle.properties is not read from a subproject",
			files: map[string]string{"proj/settings.gradle.kts": "", "proj/gradle.properties": "systemProp.https.proxyHost=project\n"},
			dir:   "proj/app",
		},
		{
			name:  "a GRADLE_USER_HOME inside the project is not read",
			files: map[string]string{"proj/.gradle/gradle.properties": "systemProp.https.proxyHost=project\n"},
			env:   []string{"GRADLE_USER_HOME=$ROOT/proj/.gradle"},
		},
		{
			name:  "-g inside the project is not read",
			files: map[string]string{"proj/.gradle/gradle.properties": "systemProp.https.proxyHost=project\n"},
			args:  []string{"--gradle-user-home=$ROOT/proj/.gradle"},
		},
		{
			name:  "-Dgradle.user.home inside the project is not read",
			files: map[string]string{"proj/.gradle/gradle.properties": "systemProp.https.proxyHost=project\n"},
			args:  []string{"-Dgradle.user.home=$ROOT/proj/.gradle"},
		},
		{
			name:  "an empty -Dgradle.user.home does not fall back to GRADLE_USER_HOME",
			files: map[string]string{"proj/gradle.properties": "systemProp.https.proxyHost=project\n", "guh/gradle.properties": "systemProp.https.proxyHost=user\n"},
			args:  []string{"-Dgradle.user.home="},
		},
		{
			name:  "the gradle.properties of a gradle inside the project is not read",
			files: map[string]string{"proj/gradle/bin/gradle": "", "proj/gradle/gradle.properties": "systemProp.https.proxyHost=project\n"},
			env:   []string{"PATH=$ROOT/proj/gradle/bin"},
		},
	}, "GRADLE_USER_HOME=$ROOT/guh"))
}

func TestJVMConfigPathsThatResolveInsideTheProjectAreNotRead(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	project := mavenSettingsXML("<proxy><id>project</id><host>project</host><port>1</port></proxy>")
	outside := mavenSettingsXML("<proxy><id>outside</id><host>outside</host><port>2</port></proxy>")
	writeConfigFiles(t, root, map[string]string{
		"proj/.git/HEAD":                     "",
		"proj/sub/ci.xml":                    project,
		"proj/sub/home/.m2/settings.xml":     project,
		"proj/sub/conf/settings.xml":         project,
		"proj/sub/.gradle/gradle.properties": "systemProp.https.proxyHost=project\n",
		"proj/d/e/.keep":                     "",
		"proj/decoy.xml":                     project,
		"decoy.xml":                          outside,
		"proj/.m2/settings.xml":              project,
		".m2/settings.xml":                   outside,
		"proj/gradle.properties":             "systemProp.https.proxyHost=project\n",
		"gradle.properties":                  "systemProp.https.proxyHost=outside\n",
		"proj/x.xml":                         project,
		"outside/x.xml":                      outside,
		"outside.xml":                        outside,
		"guh/gradle.properties":              "systemProp.https.proxyHost=outside\n",
	}, func(s string) string { return s })
	require.NoError(t, os.Symlink(filepath.Join("d", "e"), filepath.Join(root, "proj", "l")))
	require.NoError(t, os.Symlink(filepath.Join(root, "proj", "sub"), filepath.Join(root, "outside", "link")))
	// Not filepath.Join, which would drop the ".." that the OS applies after
	// the symlink: this names root/proj, not root.
	throughLink := filepath.Join(root, "proj", "l") + filepath.FromSlash("/../..")
	userHome := "MAVEN_OPTS=-Duser.home=" + filepath.Join(root, "home")
	gradleHome := "GRADLE_USER_HOME=" + filepath.Join(root, "home", ".gradle")

	tests := []struct {
		name    string
		manager PackageManager
		cwd     string
		env     []string
		args    []string
		want    upstreamProxy
		wantOK  bool
	}{
		{name: "Maven -s", manager: Maven(), env: []string{userHome}, args: []string{"-s", "ci.xml"}},
		{name: "Maven -gs", manager: Maven(), env: []string{userHome}, args: []string{"-gs", "ci.xml"}},
		{name: "Maven user.home", manager: Maven(), env: []string{"MAVEN_OPTS=-Duser.home=home"}},
		{name: "Maven maven.conf", manager: Maven(), env: []string{userHome}, args: []string{"-Dmaven.conf=conf"}},
		{name: "Maven -s with .. after a symlink", manager: Maven(), env: []string{userHome}, args: []string{"-s", throughLink + filepath.FromSlash("/decoy.xml")}},
		{name: "Maven user.home with .. after a symlink", manager: Maven(), env: []string{"MAVEN_OPTS=-Duser.home=" + throughLink}},
		{name: "Maven -s with .. from a symlinked working directory", manager: Maven(), cwd: "outside/link", env: []string{userHome}, args: []string{"-s", filepath.FromSlash("../x.xml")}},
		{name: "Gradle GRADLE_USER_HOME", manager: Gradle(), env: []string{"GRADLE_USER_HOME=.gradle"}},
		{name: "Gradle -g", manager: Gradle(), env: []string{gradleHome}, args: []string{"-g", ".gradle"}},
		{name: "Gradle gradle.user.home", manager: Gradle(), env: []string{gradleHome}, args: []string{"-Dgradle.user.home=.gradle"}},
		{name: "Gradle GRADLE_USER_HOME with .. after a symlink", manager: Gradle(), env: []string{"GRADLE_USER_HOME=" + throughLink}},
		{
			name:    "Maven -s outside the project",
			manager: Maven(),
			env:     []string{userHome},
			args:    []string{"-s", filepath.FromSlash("../../outside.xml")},
			want:    upstreamProxy{Source: filepath.Join(root, "outside.xml") + " (proxy outside)", URL: "http://outside:2"},
			wantOK:  true,
		},
		{
			name:    "Gradle GRADLE_USER_HOME outside the project",
			manager: Gradle(),
			env:     []string{"GRADLE_USER_HOME=" + filepath.FromSlash("../../guh")},
			want:    upstreamProxy{Source: filepath.FromSlash("../../guh/gradle.properties") + " (systemProp.https.proxyHost)", URL: "http://outside:443"},
			wantOK:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(root, "proj", "sub")
			if tt.cwd != "" {
				dir = filepath.Join(root, filepath.FromSlash(tt.cwd))
			}
			t.Chdir(dir)
			resolver, ok := tt.manager.(upstreamProxyResolver)
			require.True(t, ok)

			got, gotOK, err := resolver.resolveUpstreamProxy(tt.env, dir, tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Not parallel: it swaps the account home that TestMain sets.
func TestEmptyJVMUserHomeIsNotTheAccountHome(t *testing.T) {
	home := t.TempDir()
	writeConfigFiles(t, home, map[string]string{
		".m2/settings.xml":          mavenSettingsXML("<proxy><id>account</id><host>account</host><port>1</port></proxy>"),
		".gradle/gradle.properties": "systemProp.https.proxyHost=account\n",
	}, func(s string) string { return s })
	saved := currentUser
	currentUser = func() (*user.User, error) { return &user.User{HomeDir: home}, nil }
	t.Cleanup(func() { currentUser = saved })

	tests := []struct {
		name    string
		manager PackageManager
		env     []string
		args    []string
	}{
		{name: "Maven, empty -Duser.home", manager: Maven(), args: []string{"-Duser.home=", "verify"}},
		{name: "Gradle, empty GRADLE_USER_HOME", manager: Gradle(), env: []string{"GRADLE_USER_HOME="}},
		{name: "Gradle, empty -Dgradle.user.home", manager: Gradle(), args: []string{"-Dgradle.user.home="}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver, ok := tt.manager.(upstreamProxyResolver)
			require.True(t, ok)

			got, gotOK, err := resolver.resolveUpstreamProxy(tt.env, t.TempDir(), tt.args)
			require.NoError(t, err)
			assert.False(t, gotOK)
			assert.Equal(t, upstreamProxy{}, got)
		})
	}
}

func TestJVMConfigErrorsNameTheFileWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		manager PackageManager
		files   map[string]string
		env     string
		// want is the whole error, with $ROOT for the temp root.
		want string
	}{
		{
			name:    "an entity in settings.xml",
			manager: Maven(),
			files:   map[string]string{"home/.m2/settings.xml": "<settings>\n<proxies><proxy><password>a&s3cret;</password></proxy></proxies></settings>\n"},
			env:     "MAVEN_OPTS=-Duser.home=$ROOT/home",
			want:    "failed to parse $ROOT/home/.m2/settings.xml line 2",
		},
		{
			name:    "a bad escape in gradle.properties",
			manager: Gradle(),
			files:   map[string]string{"guh/gradle.properties": "systemProp.https.proxyPassword=s3cret\\u00zz\n"},
			env:     "GRADLE_USER_HOME=$ROOT/guh",
			want:    "failed to parse $ROOT/guh/gradle.properties: line 1: malformed \\uxxxx escape",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			writeConfigFiles(t, root, tt.files, func(s string) string { return s })
			dir := filepath.Join(root, "proj")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			resolver, ok := tt.manager.(upstreamProxyResolver)
			require.True(t, ok)

			_, _, err = resolver.resolveUpstreamProxy([]string{strings.ReplaceAll(tt.env, "$ROOT", root)}, dir, nil)
			require.EqualError(t, err, strings.ReplaceAll(tt.want, "$ROOT", root))
		})
	}
}

func TestMatchesHostPattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern, host string
		want          bool
	}{
		{"repo.maven.apache.org", "REPO.maven.apache.org", true},
		{"*.apache.org", "repo.maven.apache.org", true},
		{"*.apache.org", "apache.org", false},
		{".apache.org", "repo.apache.org", false},
		{"repo.*.org", "repo.maven.apache.org", true},
		{"*maven*", "repo.maven.apache.org", true},
		{"10.*", "10.1.2.3", true},
		{"*", "anything", true},
		{"repo.maven.apache.org:443", "repo.maven.apache.org", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.host, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, matchesHostPattern([]string{tt.pattern}, tt.host))
		})
	}
}

func TestUpstreamProxyFuncFallsBackForNonProxyHosts(t *testing.T) {
	t.Parallel()
	pick, err := upstreamProxy{
		URL:           "http://corp:3128",
		NonProxyHosts: []string{"*.internal.example", "direct.example"},
		Fallbacks:     []upstreamProxy{{URL: "http://plain:8080", NonProxyHosts: []string{"direct.example"}}},
	}.proxyFunc()
	require.NoError(t, err)

	for host, want := range map[string]string{
		"repo.maven.apache.org":   "http://corp:3128",
		"nexus.internal.example":  "http://plain:8080",
		"direct.example":          "",
		"DIRECT.example":          "",
		"internal.example.evil.x": "http://corp:3128",
	} {
		got, err := pick(httptest.NewRequest(http.MethodGet, "https://"+host+"/x", http.NoBody))
		require.NoError(t, err)
		if want == "" {
			assert.Nil(t, got, host)
		} else {
			assert.Equal(t, want, got.String(), host)
		}
	}
}

func TestUpstreamProxyFuncRejectsBadFallback(t *testing.T) {
	t.Parallel()
	_, err := upstreamProxy{Source: "a", URL: "http://corp:3128", Fallbacks: []upstreamProxy{{Source: "b", URL: "ftp://u:s3cret@x:21"}}}.proxyFunc()
	require.ErrorContains(t, err, "invalid outbound proxy in b")
	assert.NotContains(t, err.Error(), "s3cret")
}

// isolateMaven keeps the developer's own config and proxy variables out of a
// Maven Run test, so Run reads only the settings files the test names.
func isolateMaven(t *testing.T) {
	t.Helper()
	t.Setenv("GITLAB_CI", "") // honor fake mode even when the suite runs in CI
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	isolateUserConfig(t, t.TempDir())
	clearProxyEnv(t)
}

func TestRunSendsMavenTrafficThroughProxyFromSettings(t *testing.T) {
	isolateMaven(t)
	corporate := &refusingProxy{}
	server := httptest.NewServer(corporate)
	t.Cleanup(server.Close)
	corporateURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	project := t.TempDir()
	home := t.TempDir()
	userSettings := filepath.Join(home, "settings.xml")
	require.NoError(t, os.WriteFile(userSettings, []byte(mavenSettingsXML(
		"<proxy><id>corp</id><host>"+corporateURL.Hostname()+"</host><port>"+corporateURL.Port()+"</port><nonProxyHosts>*.internal.example</nonProxyHosts></proxy>",
	)), 0o600))
	globalSettings := filepath.Join(home, "global.xml")
	require.NoError(t, os.WriteFile(globalSettings, []byte("<settings/>"), 0o600))

	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &mavenFetchExecutor{url: "https://repo.maven.apache.org/maven2/org/apache/commons/commons-lang3/3.14.0/commons-lang3-3.14.0.jar"}
	// The fetch fails because the corporate proxy refuses the tunnel; the test
	// only checks where the inspection proxy sent it.
	_ = Run(t.Context(), Maven(), RunOptions{
		IO:        ios,
		Executor:  exec,
		BaseDir:   project,
		ProjectID: "group/proj",
		Args:      []string{"-s", userSettings, "-gs", globalSettings, "dependency:resolve"},
	})

	require.True(t, exec.ran, "mvn must run")
	assert.Equal(t, []string{"repo.maven.apache.org:443"}, corporate.Targets())
}

func TestRunRejectsEncryptedMavenProxyPassword(t *testing.T) {
	isolateMaven(t)
	project := t.TempDir()
	userSettings := filepath.Join(t.TempDir(), "settings.xml")
	require.NoError(t, os.WriteFile(userSettings, []byte(mavenSettingsXML(
		"<proxy><id>corp</id><host>corp</host><username>alice</username><password>{COQLCE6DU6GtcS5P=}</password></proxy>",
	)), 0o600))
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeExecutor{}

	err := Run(t.Context(), Maven(), RunOptions{
		IO:        ios,
		Executor:  exec,
		BaseDir:   project,
		ProjectID: "group/proj",
		Args:      []string{"-s", userSettings, "-gs", userSettings, "package"},
	})

	require.ErrorContains(t, err, "failed to read the maven proxy settings: "+userSettings+" (proxy corp): the proxy password is encrypted")
	assert.NotContains(t, err.Error(), "COQLCE6DU6GtcS5P")
	assert.Nil(t, exec.env, "the package manager must not run")
}

// mavenFetchExecutor stands in for mvn: it reads the proxy from the settings
// file glab passes in MAVEN_ARGS and fetches url through it, trusting the CA
// bundle that file is written next to.
type mavenFetchExecutor struct {
	url string
	ran bool
}

func (*mavenFetchExecutor) LookPath(file string) (string, error) { return "/bin/" + file, nil }

func (m *mavenFetchExecutor) ExecWithIO(ctx context.Context, _ string, _ []string, env []string, _ io.Reader, _, _ io.Writer) error {
	m.ran = true
	settingsPath, ok := mavenOption(strings.Fields(lookupEnv(env, "MAVEN_ARGS")), "-s", "--settings", true)
	if !ok {
		return errors.New("no settings file in MAVEN_ARGS")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return err
	}
	proxies, err := mavenProxiesIn(settingsPath, data, nil, nil)
	if err != nil || len(proxies) == 0 {
		return fmt.Errorf("no proxy in %s: %w", settingsPath, err)
	}
	upstream, err := proxies[0].upstream()
	if err != nil {
		return err
	}
	proxyURL, err := url.Parse(upstream.URL)
	if err != nil {
		return err
	}
	pemBytes, err := os.ReadFile(strings.TrimSuffix(settingsPath, ".settings.xml"))
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return errors.New("failed to load the proxy CA")
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry returned %d", resp.StatusCode)
	}
	return nil
}

func TestMavenUpstreamProxyIgnoresMavenConfig(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	writeConfigFiles(t, root, map[string]string{
		"proj/.git/HEAD":         "",
		"proj/.mvn/maven.config": "-s\n" + filepath.Join(root, "outside.xml") + "\n",
		"outside.xml":            mavenSettingsXML("<proxy><id>outside</id><host>outside</host><port>2</port></proxy>"),
	}, func(s string) string { return s })

	_, ok, err := mavenUpstreamProxy([]string{"MAVEN_OPTS=-Duser.home=" + filepath.Join(root, "home")}, filepath.Join(root, "proj"), []string{"verify"})

	require.NoError(t, err)
	assert.False(t, ok, "a project's maven.config must not choose the file that supplies proxy credentials")
}
