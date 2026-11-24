//go:build !integration

package pm

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Not parallel: it sets PATH and changes the working directory.
func TestMavenJobArgsEnvKeepsTheInstallGlobalMirror(t *testing.T) {
	withSystemMavenRC(t)
	home := filepath.Join(t.TempDir(), "maven")
	mvn := fakeMavenInstall(t, home)
	global := `<settings><mirrors><mirror><id>corp</id><mirrorOf>*</mirrorOf><url>https://nexus.corp.example/repository/maven</url></mirror></mirrors></settings>`
	require.NoError(t, os.WriteFile(filepath.Join(home, "conf", "settings.xml"), []byte(global), 0o600))
	t.Setenv("PATH", filepath.Dir(mvn))
	t.Setenv("MAVEN_ARGS", "")
	// Maven reads user.home from the passwd database, not HOME; pin it so the
	// user-proxy check can't read the running dev's real ~/.m2/settings.xml.
	t.Setenv("MAVEN_OPTS", "-Duser.home="+t.TempDir())
	t.Chdir(t.TempDir())

	path := filepath.Join(t.TempDir(), "job-settings.xml")
	env, err := MavenJobArgsEnv(path, "http://127.0.0.1:9999", func(string, ...any) {})
	require.NoError(t, err)
	assert.Equal(t, "MAVEN_ARGS=-gs "+path, env)

	settings, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(settings), "https://nexus.corp.example/repository/maven")
	assert.Contains(t, string(settings), "<host>127.0.0.1</host>")
}

// Not parallel: it sets PATH and MAVEN_ARGS.
func TestMavenJobArgsEnvWithoutMaven(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("MAVEN_ARGS", "-B")
	// Pin Maven's user.home (not HOME) so the user-proxy check ignores the dev's
	// real ~/.m2/settings.xml.
	t.Setenv("MAVEN_OPTS", "-Duser.home="+t.TempDir())

	path := filepath.Join(t.TempDir(), "job-settings.xml")
	env, err := MavenJobArgsEnv(path, "http://127.0.0.1:9999", func(string, ...any) {})
	require.NoError(t, err)
	assert.Equal(t, "MAVEN_ARGS=-gs "+path+" -B", env)
	settings, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(settings), "<host>127.0.0.1</host>")
}

func TestMavenJobArgsEnvRejectsABadProxyURL(t *testing.T) {
	t.Parallel()
	_, err := MavenJobArgsEnv(filepath.Join(t.TempDir(), "s.xml"), "not a url", func(string, ...any) {})
	assert.Error(t, err)
}

// Not parallel: it sets environment variables and changes the working directory.
func TestMavenJobArgsEnvWarnsWhenTheGlobalSettingsCantBeResolved(t *testing.T) {
	withSystemMavenRC(t)
	mvn := fakeMavenInstall(t, filepath.Join(t.TempDir(), "maven"))
	t.Setenv("PATH", filepath.Dir(mvn))
	t.Setenv("MAVEN_ARGS", "")
	t.Setenv("MAVEN_BASEDIR", "")
	// A moved maven.conf means the install's conf/settings.xml isn't the one
	// mvn reads. Pinning Maven's user.home (not HOME) keeps the user-proxy
	// check off the dev's real ~/.m2/settings.xml, which would add a warning.
	t.Setenv("MAVEN_OPTS", "-Dmaven.conf=/srv/conf -Duser.home="+t.TempDir())
	t.Chdir(t.TempDir())

	var warned []string
	warnf := func(format string, a ...any) { warned = append(warned, fmt.Sprintf(format, a...)) }
	_, err := MavenJobArgsEnv(filepath.Join(t.TempDir(), "s.xml"), "http://127.0.0.1:9999", warnf)
	require.NoError(t, err)
	require.Len(t, warned, 1)
	assert.Contains(t, warned[0], "could not resolve the maven global settings")
}

// Not parallel: it sets PATH and MAVEN_ARGS.
func TestMavenJobArgsEnvWarnsWhenUserSettingsHaveAProxy(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no mvn: skip global-settings resolution
	userSettings := filepath.Join(t.TempDir(), "settings.xml")
	require.NoError(t, os.WriteFile(userSettings, []byte(
		`<settings><proxies><proxy><id>corp</id><active>true</active><protocol>https</protocol><host>proxy.corp</host><port>8080</port></proxy></proxies></settings>`), 0o600))
	t.Setenv("MAVEN_ARGS", "-s "+userSettings)

	var warned []string
	warnf := func(format string, a ...any) { warned = append(warned, fmt.Sprintf(format, a...)) }
	_, err := MavenJobArgsEnv(filepath.Join(t.TempDir(), "job.xml"), "http://127.0.0.1:9999", warnf)
	require.NoError(t, err)
	require.Len(t, warned, 1)
	assert.Contains(t, warned[0], "may bypass the firewall")
}

func TestMavenUserSettingsHasActiveProxy(t *testing.T) {
	t.Parallel()
	write := func(t *testing.T, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "settings.xml")
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"active proxy", `<settings><proxies><proxy><active>true</active><host>h</host></proxy></proxies></settings>`, true},
		{"omitted active counts as active", `<settings><proxies><proxy><host>h</host></proxy></proxies></settings>`, true},
		{"inactive proxy", `<settings><proxies><proxy><active>false</active><host>h</host></proxy></proxies></settings>`, false},
		{"no proxies", `<settings><mirrors></mirrors></settings>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, mavenUserSettingsHasActiveProxy(write(t, tc.body)))
		})
	}
	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		assert.False(t, mavenUserSettingsHasActiveProxy(filepath.Join(t.TempDir(), "nope.xml")))
	})
	t.Run("empty path", func(t *testing.T) {
		t.Parallel()
		assert.False(t, mavenUserSettingsHasActiveProxy(""))
	})
}
