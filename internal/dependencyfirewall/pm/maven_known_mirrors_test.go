//go:build !integration

package pm

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mirrorsXML(mirrors ...string) []byte {
	body := "<settings xmlns=\"http://maven.apache.org/SETTINGS/1.0.0\">\n  <mirrors>\n"
	for _, m := range mirrors {
		body += "    " + m + "\n"
	}
	return []byte(body + "  </mirrors>\n</settings>\n")
}

func mirrorXML(id, mirrorOf, url string) string {
	return "<mirror><id>" + id + "</id><mirrorOf>" + mirrorOf + "</mirrorOf><url>" + url + "</url></mirror>"
}

const stockHTTPBlocker = `<mirror><id>maven-default-http-blocker</id><mirrorOf>external:http:*</mirrorOf><url>http://0.0.0.0/</url><blocked>true</blocked></mirror>`

func TestMavenKnownMirrors(t *testing.T) {
	nexus := mirrorXML("corp", "*", "https://nexus.example.com/repository/public/")

	for _, tc := range []struct {
		name         string
		user, global []byte
		want         []string
		externalOnly bool
	}{
		{name: "when the user settings have a catch-all mirror", user: mirrorsXML(nexus), want: []string{"https://nexus.example.com/repository/public/"}},
		{
			name: "when the global settings have the catch-all", user: mirrorsXML(mirrorXML("c", "central", "https://central.example.com/maven2/")), global: mirrorsXML(nexus),
			want: []string{"https://central.example.com/maven2/", "https://nexus.example.com/repository/public/"},
		},
		{
			name: "when a mirror after the catch-all names a repository id", user: mirrorsXML(nexus, mirrorXML("c", "central", "https://central.example.com/maven2/")),
			want: []string{"https://nexus.example.com/repository/public/", "https://central.example.com/maven2/"},
		},
		{
			name: "when the catch-all is external:*", user: mirrorsXML(mirrorXML("corp", "external:*", "https://nexus.example.com/repository/public/")),
			want: []string{"https://nexus.example.com/repository/public/"}, externalOnly: true,
		},
		{name: "when Maven's stock blocked mirror is there too", user: mirrorsXML(nexus), global: mirrorsXML(stockHTTPBlocker), want: []string{"https://nexus.example.com/repository/public/"}},
		{name: "when there is no catch-all", user: mirrorsXML(mirrorXML("c", "central", "https://nexus.example.com/repository/public/"))},
		{name: "when the catch-all excludes a repository", user: mirrorsXML(mirrorXML("corp", "*,!internal", "https://nexus.example.com/repository/public/"))},
		{name: "when the catch-all lists other patterns", user: mirrorsXML(mirrorXML("corp", "*, central", "https://nexus.example.com/repository/public/"))},
		{name: "when the external:* mirror is on localhost", user: mirrorsXML(mirrorXML("corp", "external:*", "http://localhost:8081/repository/public/"))},
		{name: "when any mirror's URL comes from a property", user: mirrorsXML(nexus, mirrorXML("c", "central", "https://${env.CENTRAL_HOST}/maven2/"))},
		{name: "when the catch-all's URL comes from a property", user: mirrorsXML(mirrorXML("corp", "*", "${env.NEXUS_URL}"))},
		{name: "when a mirror's path comes from a property", user: mirrorsXML(mirrorXML("corp", "*", "https://nexus.example.com/${env.NEXUS_REPO}/"))},
		{name: "when a mirror's URL isn't http or https", user: mirrorsXML(nexus, mirrorXML("f", "local", "file:///srv/maven/"))},
		{name: "when the global settings can't be parsed", user: mirrorsXML(nexus), global: []byte("<settings><mirrors></settings>")},
		{
			name: "when the catch-all lists the default layout", user: mirrorsXML(`<mirror><id>corp</id><mirrorOf>*</mirrorOf><mirrorOfLayouts>default,legacy</mirrorOfLayouts><url>https://nexus.example.com/repository/public/</url></mirror>`),
			want: []string{"https://nexus.example.com/repository/public/"},
		},
		{
			name: "when the catch-all lists every layout but p2", user: mirrorsXML(`<mirror><id>corp</id><mirrorOf>*</mirrorOf><mirrorOfLayouts>*,!p2</mirrorOfLayouts><url>https://nexus.example.com/repository/public/</url></mirror>`),
			want: []string{"https://nexus.example.com/repository/public/"},
		},
		{
			name: "when the catch-all's layout is padded", user: mirrorsXML(`<mirror><id>corp</id><mirrorOf>*</mirrorOf><mirrorOfLayouts> default </mirrorOfLayouts><url>https://nexus.example.com/repository/public/</url></mirror>`),
			want: []string{"https://nexus.example.com/repository/public/"},
		},
		{
			name: "when a padded user mirror id hides the global catch-all",
			user: mirrorsXML(`<mirror><id> corp </id><mirrorOf>central</mirrorOf><url>https://central.example.com/maven2/</url></mirror>`), global: mirrorsXML(nexus),
		},
		{name: "when the catch-all only mirrors another layout", user: mirrorsXML(`<mirror><id>corp</id><mirrorOf>*</mirrorOf><mirrorOfLayouts>p2</mirrorOfLayouts><url>https://nexus.example.com/repository/public/</url></mirror>`)},
		{name: "when the catch-all excludes the default layout", user: mirrorsXML(`<mirror><id>corp</id><mirrorOf>*</mirrorOf><mirrorOfLayouts>*,!default</mirrorOfLayouts><url>https://nexus.example.com/repository/public/</url></mirror>`)},
		{name: "when the catch-all's layouts come from a property", user: mirrorsXML(`<mirror><id>corp</id><mirrorOf>*</mirrorOf><mirrorOfLayouts>${env.LAYOUTS}</mirrorOfLayouts><url>https://nexus.example.com/repository/public/</url></mirror>`)},
		{
			name: "when a user mirror's id hides the global catch-all",
			user: mirrorsXML(mirrorXML("corp", "central", "https://central.example.com/maven2/")), global: mirrorsXML(nexus),
		},
		{
			name: "when two mirrors without an id hide the global catch-all",
			user: mirrorsXML(`<mirror><mirrorOf>central</mirrorOf><url>https://central.example.com/maven2/</url></mirror>`), global: mirrorsXML(`<mirror><mirrorOf>*</mirrorOf><url>https://nexus.example.com/repository/public/</url></mirror>`),
		},
		{
			name: "when a user mirror without an id hides a global catch-all with id default",
			user: mirrorsXML(`<mirror><mirrorOf>central</mirrorOf><url>https://central.example.com/maven2/</url></mirror>`), global: mirrorsXML(mirrorXML("default", "*", "https://nexus.example.com/repository/public/")),
		},
		{
			name: "when a user mirror's id hides another global mirror", user: mirrorsXML(nexus), global: mirrorsXML(mirrorXML("corp", "central", "https://central.example.com/maven2/")),
			want: []string{"https://nexus.example.com/repository/public/", "https://central.example.com/maven2/"},
		},
		{
			name: "when the user settings repeat a mirror id", user: mirrorsXML(nexus, mirrorXML("corp", "central", "https://central.example.com/maven2/")),
			want: []string{"https://nexus.example.com/repository/public/", "https://central.example.com/maven2/"},
		},
		{name: "when a catch-all repeats an earlier mirror's id", user: mirrorsXML(mirrorXML("corp", "central", "https://central.example.com/maven2/"), nexus)},
		{name: "when a catch-all repeats a blocked mirror's id", user: mirrorsXML(`<mirror><id>corp</id><mirrorOf>central</mirrorOf><url>http://0.0.0.0/</url><blocked>true</blocked></mirror>`, nexus)},
		{name: "when a mirror id comes from a property", user: mirrorsXML(mirrorXML("${env.MIRROR_ID}", "central", "https://central.example.com/maven2/")), global: mirrorsXML(nexus)},
		{name: "when there are no settings at all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			urls, externalOnly, ok := mavenKnownMirrors(tc.user, tc.global)

			if tc.want == nil {
				assert.False(t, ok, "glab can't know every mirror Maven may use, so it must keep guessing")
				return
			}
			require.True(t, ok)
			var got []string
			for _, u := range urls {
				got = append(got, u.String())
			}
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.externalOnly, externalOnly)
		})
	}
}

// stockM2Conf is Maven 3.9's bin/m2.conf.
const stockM2Conf = `main is org.apache.maven.cli.MavenCli from plexus.core

set maven.conf default ${maven.home}/conf

[plexus.core]
load       ${maven.conf}/logging
optionally ${maven.home}/lib/ext/*.jar
load       ${maven.home}/lib/*.jar
`

// fakeMavenInstall creates a Maven 3 install at home and returns its bin/mvn.
func fakeMavenInstall(t *testing.T, home string) string {
	t.Helper()
	for _, dir := range []string{"bin", "conf", "lib"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, dir), 0o700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(home, "bin", "mvn"), []byte("#!/bin/sh\n"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "bin", "m2.conf"), []byte(stockM2Conf), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "lib", "maven-core-3.9.12.jar"), nil, 0o600))
	return filepath.Join(home, "bin", "mvn")
}

// withSystemMavenRC points the launcher's fixed-path rc files at paths.
func withSystemMavenRC(t *testing.T, paths ...string) {
	t.Helper()
	saved := mavenSystemRCFiles
	mavenSystemRCFiles = paths
	t.Cleanup(func() { mavenSystemRCFiles = saved })
}

func TestMavenGlobalSettingsFile(t *testing.T) {
	withSystemMavenRC(t)
	mvn := fakeMavenInstall(t, t.TempDir())
	install, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(mvn)))
	require.NoError(t, err)

	t.Run("when -gs names it", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "g.xml"), []byte("<settings/>"), 0o600))
		for _, args := range [][]string{{"-gs", "g.xml", "verify"}, {"-gs=g.xml", "verify"}, {"--global-settings=g.xml", "verify"}} {
			got, err := mavenGlobalSettingsFile(nil, dir, args, mvn)
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(dir, "g.xml"), got, args)
		}

		got, err := mavenGlobalSettingsFile(nil, dir, []string{"-gsg.xml", "verify"}, mvn)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(install, "conf", "settings.xml"), got, "Maven rejects -gsFILE rather than reading it")
	})

	t.Run("when -gs names a file that doesn't exist", func(t *testing.T) {
		_, err := mavenGlobalSettingsFile(nil, t.TempDir(), []string{"-gs", "typo.xml", "verify"}, mvn)
		assert.Error(t, err, "Maven fails on it, so glab mustn't hand it an empty file instead")
	})

	t.Run("when MAVEN_ARGS or maven.config names it", func(t *testing.T) {
		dir := t.TempDir()
		global := filepath.Join(t.TempDir(), "g.xml")
		require.NoError(t, os.WriteFile(global, []byte("<settings/>"), 0o600))
		got, err := mavenGlobalSettingsFile([]string{"MAVEN_ARGS=-gs " + global}, dir, []string{"verify"}, mvn)
		require.NoError(t, err)
		assert.Equal(t, global, got)

		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".mvn"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".mvn", "maven.config"), []byte("-gs\n"+global+"\n"), 0o600))
		got, err = mavenGlobalSettingsFile(nil, dir, []string{"verify"}, mvn)
		require.NoError(t, err)
		assert.Equal(t, global, got)
	})

	t.Run("when it comes from the install mvn belongs to", func(t *testing.T) {
		root := t.TempDir()
		target := fakeMavenInstall(t, filepath.Join(root, "maven", "apache-maven-3.9.12"))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o700))
		link := filepath.Join(root, "bin", "mvn")
		require.NoError(t, os.Symlink(target, link))
		want, err := filepath.EvalSymlinks(filepath.Join(root, "maven", "apache-maven-3.9.12"))
		require.NoError(t, err)

		got, err := mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, link)

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(want, "conf", "settings.xml"), got, "the launcher resolves its own symlinks to find its install")
	})

	t.Run("when mvn is Homebrew's wrapper", func(t *testing.T) {
		prefix := t.TempDir()
		cellar := filepath.Join(prefix, "Cellar", "maven", "3.9.12")
		fakeMavenInstall(t, filepath.Join(cellar, "libexec"))
		require.NoError(t, os.MkdirAll(filepath.Join(cellar, "bin"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(cellar, "bin", "mvn"), []byte("#!/bin/bash\nexec libexec/bin/mvn \"$@\"\n"), 0o700))
		require.NoError(t, os.MkdirAll(filepath.Join(prefix, "bin"), 0o700))
		link := filepath.Join(prefix, "bin", "mvn")
		require.NoError(t, os.Symlink(filepath.Join(cellar, "bin", "mvn"), link))
		want, err := filepath.EvalSymlinks(filepath.Join(cellar, "libexec"))
		require.NoError(t, err)

		got, err := mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, link)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(want, "conf", "settings.xml"), got, "Homebrew's wrapper runs the install in libexec")

		require.NoError(t, os.MkdirAll(filepath.Join(prefix, "etc"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(prefix, "etc", "mavenrc"), []byte("MAVEN_ARGS=\"-gs /srv/g.xml\"\n"), 0o600))
		_, err = mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, link)
		assert.Error(t, err, "Homebrew's launcher sources its own etc/mavenrc")
	})

	t.Run("when mvn is a later Maven 3", func(t *testing.T) {
		later := t.TempDir()
		mvnLater := fakeMavenInstall(t, later)
		require.NoError(t, os.Rename(filepath.Join(later, "lib", "maven-core-3.9.12.jar"), filepath.Join(later, "lib", "maven-core-3.10.0.jar")))
		laterHome, err := filepath.EvalSymlinks(later)
		require.NoError(t, err)

		got, err := mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, mvnLater)

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(laterHome, "conf", "settings.xml"), got)
	})

	t.Run("when mvn isn't Maven 3.9 or a later 3.x", func(t *testing.T) {
		shim := filepath.Join(t.TempDir(), "mvn")
		require.NoError(t, os.WriteFile(shim, []byte("#!/bin/sh\n"), 0o700))
		_, err := mavenGlobalSettingsFile(nil, t.TempDir(), []string{"-gs", "/etc/g.xml", "verify"}, shim)
		require.Error(t, err, "a shim or wrapper hides the install whose settings Maven reads")

		_, err = mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, filepath.Join(t.TempDir(), "mvn"))
		require.Error(t, err, "mvn has gone")

		maven4 := t.TempDir()
		mvn4 := fakeMavenInstall(t, maven4)
		for _, jar := range []string{"maven-core-4.0.0-rc-7.jar", "maven-core-4.9.0.jar"} {
			require.NoError(t, os.RemoveAll(filepath.Join(maven4, "lib")))
			require.NoError(t, os.MkdirAll(filepath.Join(maven4, "lib"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(maven4, "lib", jar), nil, 0o600))
			_, err = mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, mvn4)
			require.Error(t, err, "Maven 4 also reads project and installation settings: %s", jar)
		}

		older := t.TempDir()
		mvnOlder := fakeMavenInstall(t, older)
		require.NoError(t, os.Rename(filepath.Join(older, "lib", "maven-core-3.9.12.jar"), filepath.Join(older, "lib", "maven-core-3.8.8.jar")))
		_, err = mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, mvnOlder)
		require.Error(t, err, "the launcher only passes MAVEN_ARGS on from 3.9")

		for _, m2 := range []string{"set maven.conf default /srv/conf\n", "set\tmaven.conf default /srv/conf\n"} {
			moved := t.TempDir()
			mvnMoved := fakeMavenInstall(t, moved)
			require.NoError(t, os.WriteFile(filepath.Join(moved, "bin", "m2.conf"), []byte(m2), 0o600))
			_, err = mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, mvnMoved)
			assert.Error(t, err, "bin/m2.conf can move the conf directory: %q", m2)
		}
	})

	t.Run("when the launcher sources an rc file", func(t *testing.T) {
		homeWithRC := func(t *testing.T, body string) string {
			t.Helper()
			home := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(home, ".mavenrc"), []byte(body), 0o600))
			return home
		}
		for _, body := range []string{
			"MAVEN_ARGS=\"-gs /srv/g.xml\"\n",
			"MAVEN_OPTS=\"$MAVEN_OPTS -Xmx1g\"\n",
			". /srv/more-rc\n",
			"JAVA_HOME=$(/usr/libexec/java_home)\n",
			"JAVA_HOME=`/usr/libexec/java_home`\n",
			"JAVA_HOME=\"/usr/lib/jvm/`x`\"\n",
			"JAVA_HOME=\"/usr/lib/jvm/\\\"\nMAVEN_OPTS=\" ;MAVEN_ARGS=-o; X=\"\n",
			"JAVA_HOME=/usr/lib/jvm/java(21\n",
			"JAVA_HOME=/usr/lib/jvm/java-21;MAVEN_ARGS=-o\n",
			"JAVA_HOME=/usr/lib/jvm/java-21&&MAVEN_ARGS=-o\n",
			"JAVA_HOME=/usr/lib/jvm/java-21|sh\n",
			"JAVA_HOME=/usr/lib/jvm/java-21>/tmp/x\n",
			"JAVA_HOME=(/usr/lib/jvm/java-21)\n",
			"true; JAVA_HOME=/usr/lib/jvm/java-21\n",
			"JAVA_HOME=/usr/lib/jvm/java-21; MAVEN_ARGS=-o\n",
			"JAVA_HOME=/usr/lib/jvm/java-21 MAVEN_ARGS=-o\n",
			"exportMAVEN_OPTS=-Xmx1g\n",
			"MAVEN_OPTS='-Xmx2g -Dmaven.conf=/srv/conf'\n",
			"MAVEN_OPTS=-Dmaven.conf=/srv/conf\n",
			"MAVEN_OPTS=\"-Dmaven.conf=/srv/conf\"\n",
			"export MAVEN_OPTS='-Dmaven.conf=/srv/conf'\n",
		} {
			_, err := mavenGlobalSettingsFile([]string{"HOME=" + homeWithRC(t, body)}, t.TempDir(), []string{"verify"}, mvn)
			require.Error(t, err, body)
		}

		// Alpine's maven package ships this /etc/mavenrc.
		alpine := filepath.Join(t.TempDir(), "mavenrc")
		require.NoError(t, os.WriteFile(alpine, []byte("M2_HOME=\"/usr/share/java/maven-3\"\n"), 0o600))
		withSystemMavenRC(t, alpine)
		home := homeWithRC(t, "# local tweaks\n\nexport JAVA_HOME=/usr/lib/jvm/java-21\r\nMAVEN_OPTS='-Xmx2g -XX:+UseG1GC'\n")
		got, err := mavenGlobalSettingsFile([]string{"HOME=" + home}, t.TempDir(), []string{"verify"}, mvn)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(install, "conf", "settings.xml"), got, "these rc files can't change the settings Maven reads")

		withSystemMavenRC(t, filepath.Join(homeWithRC(t, "MAVEN_ARGS=-o\n"), ".mavenrc"))
		_, err = mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, mvn)
		require.Error(t, err, "a system rc file counts too")
		got, err = mavenGlobalSettingsFile([]string{"MAVEN_SKIP_RC=true"}, t.TempDir(), []string{"verify"}, mvn)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(install, "conf", "settings.xml"), got)

		unreadable := filepath.Join(t.TempDir(), "mavenrc")
		require.NoError(t, os.Mkdir(unreadable, 0o700))
		withSystemMavenRC(t, unreadable)
		_, err = mavenGlobalSettingsFile(nil, t.TempDir(), []string{"verify"}, mvn)
		require.Error(t, err, "an rc file glab can't read could change anything")
	})

	t.Run("when the Windows launcher runs an rc file", func(t *testing.T) {
		for _, name := range []string{"mavenrc_pre.bat", "mavenrc_pre.cmd"} {
			profile := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(profile, name), nil, 0o600))
			_, err := mavenGlobalSettingsFile([]string{"USERPROFILE=" + profile}, t.TempDir(), []string{"verify"}, mvn)
			require.Error(t, err, name)
		}
		got, err := mavenGlobalSettingsFile([]string{"USERPROFILE=" + t.TempDir()}, t.TempDir(), []string{"verify"}, mvn)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(install, "conf", "settings.xml"), got)
	})

	t.Run("when the launcher could pick another project directory", func(t *testing.T) {
		for _, tc := range []struct {
			environ []string
			args    []string
		}{
			{nil, []string{"-f", "other/pom.xml", "verify"}},
			{nil, []string{"--file", "other", "verify"}},
			{[]string{"MAVEN_BASEDIR=/srv/other"}, []string{"verify"}},
		} {
			_, err := mavenGlobalSettingsFile(tc.environ, t.TempDir(), tc.args, mvn)
			assert.Error(t, err, "%v %v: that project's .mvn could name another -gs", tc.environ, tc.args)
		}
	})

	t.Run("when an argument is quoted", func(t *testing.T) {
		_, err := mavenGlobalSettingsFile([]string{`MAVEN_ARGS="-gs=/srv/g.xml"`}, t.TempDir(), []string{"verify"}, mvn)
		require.Error(t, err, "Maven strips the quotes and reads -gs")

		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".mvn"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".mvn", "maven.config"), []byte(`'-gs=/srv/g.xml'`+"\n"), 0o600))
		_, err = mavenGlobalSettingsFile(nil, dir, []string{"verify"}, mvn)
		assert.Error(t, err)
	})

	t.Run("when the caller moves maven.conf", func(t *testing.T) {
		for _, tc := range []struct {
			environ []string
			args    []string
		}{
			{[]string{"MAVEN_OPTS=-Dmaven.conf=/srv/conf"}, nil},
			{[]string{"MAVEN_DEBUG_OPTS=-Dmaven.conf=/srv/conf"}, nil},
			{[]string{"JAVA_TOOL_OPTIONS=-Dmaven.conf=/srv/conf"}, nil},
			{[]string{"JDK_JAVA_OPTIONS=-Dmaven.conf=/srv/conf"}, nil},
			{[]string{"_JAVA_OPTIONS=-Dmaven.conf=/srv/conf"}, nil},
			{[]string{"MAVEN_ARGS=-Dmaven.conf=/srv/conf"}, nil},
			{nil, []string{"-D", "maven.conf=/srv/conf"}},
			{nil, []string{"--define=maven.conf=/srv/conf"}},
			{[]string{"MAVEN_OPTS=-Dmaven.conf"}, nil},
			{[]string{`JAVA_TOOL_OPTIONS=-D"maven.conf=/srv/conf"`}, nil},
			{[]string{`JDK_JAVA_OPTIONS="-Dmaven.conf=/srv/conf"`}, nil},
		} {
			_, err := mavenGlobalSettingsFile(tc.environ, t.TempDir(), append(tc.args, "verify"), mvn)
			require.Error(t, err, "%v %v: glab's -gs would override the caller's conf", tc.environ, tc.args)
		}

		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".mvn"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".mvn", "jvm.config"), []byte("-Xmx1g -Dmaven.conf=/srv/conf\n"), 0o600))
		_, err := mavenGlobalSettingsFile(nil, filepath.Join(dir, "module"), []string{"verify"}, mvn)
		require.Error(t, err, ".mvn/jvm.config is passed to the JVM")

		global := filepath.Join(t.TempDir(), "g.xml")
		require.NoError(t, os.WriteFile(global, []byte("<settings/>"), 0o600))
		got, err := mavenGlobalSettingsFile([]string{"MAVEN_OPTS=-Dmaven.conf=/srv/conf"}, t.TempDir(), []string{"-gs", global, "verify"}, mvn)
		require.NoError(t, err)
		assert.Equal(t, global, got, "an explicit -gs wins over maven.conf anyway")
	})

	t.Run("when an option only mentions maven.conf", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".mvn"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".mvn", "maven.config"), []byte("# -Dmaven.conf=/srv/conf\n-B\n"), 0o600))

		got, err := mavenGlobalSettingsFile([]string{"MAVEN_OPTS=-Dtest=org.acme.maven.conf.FooTest"}, dir, []string{"verify"}, mvn)

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(install, "conf", "settings.xml"), got, "Maven skips # lines in maven.config, and a value that merely contains maven.conf sets nothing")
	})

	t.Run("when a comment sits between -gs and its value in maven.config", func(t *testing.T) {
		dir := t.TempDir()
		global := filepath.Join(t.TempDir(), "g.xml")
		require.NoError(t, os.WriteFile(global, []byte("<settings/>"), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".mvn"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".mvn", "maven.config"), []byte("-gs\n# the team's settings\n"+global+"\n"), 0o600))

		got, err := mavenGlobalSettingsFile(nil, dir, []string{"verify"}, mvn)

		require.NoError(t, err)
		assert.Equal(t, global, got, "Maven skips the comment and takes the next line as the value")
	})

	t.Run("when other JVM options would move the global settings", func(t *testing.T) {
		environ := []string{"_JAVA_OPTIONS=-Dclassworlds.conf=/srv/m2.conf -Dmaven.multiModuleProjectDirectory=/srv/other -Duser.dir=/srv"}

		got, err := mavenGlobalSettingsFile(environ, t.TempDir(), []string{"-Dnote=see-.mvn/maven.config", "verify"}, mvn)

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(install, "conf", "settings.xml"), got, "glab passes this file as -gs, which Maven reads first")
	})

	t.Run("when -gs is empty or there is no mvn binary", func(t *testing.T) {
		_, err := mavenGlobalSettingsFile(nil, t.TempDir(), []string{"-gs=", "verify"}, mvn)
		require.Error(t, err)
		_, err = mavenGlobalSettingsFile(nil, t.TempDir(), []string{"-gs", "/etc/g.xml", "verify"}, "")
		assert.Error(t, err)
	})
}

// TestMavenManagerKnownMirrors goes through the manager, since the matcher
// Run hands the proxy is taken before CATrustEnviron reads the settings.
func TestMavenManagerKnownMirrors(t *testing.T) {
	parentPOM := httptest.NewRequest(http.MethodGet, "https://nexus.example.com/repository/public/org/apache/logging/log4j/log4j/2.24.3/log4j-2.24.3.pom", http.NoBody)

	// The last run's MAVEN_ARGS, manager, CA path and mvn binary.
	var (
		mavenArgs []string
		lastM     PackageManager
		lastCA    string
		lastMvn   string
	)
	run := func(t *testing.T, globalXML []byte, args ...string) []string {
		t.Helper()
		for _, name := range []string{"MAVEN_ARGS", "MAVEN_BASEDIR", "MAVEN_OPTS"} {
			t.Setenv(name, "")
		}
		t.Setenv("HOME", t.TempDir())
		t.Setenv("USERPROFILE", t.TempDir())
		withSystemMavenRC(t)
		t.Chdir(t.TempDir())
		install := t.TempDir()
		mvn := fakeMavenInstall(t, install)
		if globalXML != nil {
			require.NoError(t, os.WriteFile(filepath.Join(install, "conf", "settings.xml"), globalXML, 0o600))
		}

		m := Maven()
		mm, ok := m.(*mavenManager)
		require.True(t, ok)
		mm.lookPath = func(string) (string, error) { return mvn, nil }
		matcher := m.Matcher()
		mm.SetArgs(append(args, "-Duser.home="+t.TempDir(), "verify"))
		m.Environment("http://127.0.0.1:52999")
		mavenArgs, lastM, lastCA, lastMvn = nil, m, filepath.Join(t.TempDir(), "ca.pem"), mvn
		for _, kv := range m.CATrustEnviron(lastCA) {
			if v, ok := strings.CutPrefix(kv, "MAVEN_ARGS="); ok {
				mavenArgs = strings.Fields(v)
			}
		}

		match := matcher.Match(parentPOM)
		names := []string{match.Coordinate.Name}
		for _, c := range match.Alternates {
			names = append(names, c.Name)
		}
		return names
	}
	// pinned returns the file the last run passed Maven as -gs, read back.
	pinned := func(t *testing.T) string {
		t.Helper()
		require.GreaterOrEqual(t, len(mavenArgs), 4, "MAVEN_ARGS %q", mavenArgs)
		require.Equal(t, "-s", mavenArgs[0])
		require.Equal(t, "-gs", mavenArgs[2], "glab's -s and -gs come first, so Maven takes them")
		info, err := os.Stat(mavenArgs[3])
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the copy can hold credentials")
		raw, err := os.ReadFile(mavenArgs[3])
		require.NoError(t, err)
		return string(raw)
	}

	t.Run("when the caller's settings have a catch-all mirror", func(t *testing.T) {
		user := writeUserSettings(t, string(mirrorsXML(mirrorXML("corp", "*", "https://nexus.example.com/repository/public/"))))

		assert.Equal(t, []string{"org.apache.logging.log4j:log4j"}, run(t, nil, "-s", user))
		assert.Equal(t, "<settings/>\n", pinned(t), "with no global settings, Maven must read none")
	})

	t.Run("when the install's global settings have it", func(t *testing.T) {
		global := mirrorsXML(mirrorXML("corp", "*", "https://nexus.example.com/repository/public/"))
		t.Setenv("_JAVA_OPTIONS", "-Dclassworlds.conf=/srv/m2.conf -Dmaven.multiModuleProjectDirectory=/srv/other")

		assert.Equal(t, []string{"org.apache.logging.log4j:log4j"}, run(t, global))
		assert.Equal(t, string(global), pinned(t), "Maven reads the global settings glab checked, whatever the JVM options say")

		lastM.CleanupCAFiles(lastCA)
		assert.NoFileExists(t, mavenArgs[3])
	})

	t.Run("when glab can't write the copy for -gs", func(t *testing.T) {
		run(t, mirrorsXML(mirrorXML("corp", "*", "https://nexus.example.com/repository/public/")))
		m := Maven()
		mm, ok := m.(*mavenManager)
		require.True(t, ok)
		mm.lookPath = func(string) (string, error) { return lastMvn, nil }
		mm.SetArgs([]string{"-Duser.home=" + t.TempDir(), "verify"})
		matcher := m.Matcher()

		path, ok := mm.pinGlobalSettings(nil, filepath.Join(t.TempDir(), "missing", "global-settings.xml"))

		assert.False(t, ok)
		assert.Empty(t, path)
		assert.NotEmpty(t, matcher.Match(parentPOM).Alternates, "without the copy Maven might read other mirrors, so glab keeps guessing")
	})

	t.Run("when the catch-all's URL comes from an environment variable", func(t *testing.T) {
		t.Setenv("NEXUS_URL", "https://nexus.example.com/repository/public/")
		global := mirrorsXML(mirrorXML("corp", "*", "${env.NEXUS_URL}"))

		assert.Equal(t, []string{"org.apache.logging.log4j:log4j"}, run(t, global))
		assert.Contains(t, pinned(t), "<url>https://nexus.example.com/repository/public/</url>", "Maven must fetch from the URL glab matches under")
	})

	t.Run("when the caller's catch-all URL comes from an environment variable", func(t *testing.T) {
		t.Setenv("NEXUS_URL", "https://nexus.example.com/repository/public/")
		user := writeUserSettings(t, string(mirrorsXML(mirrorXML("corp", "*", "${env.NEXUS_URL}"))))

		assert.Equal(t, []string{"org.apache.logging.log4j:log4j"}, run(t, nil, "-s", user))
	})

	t.Run("when -gs names the settings with it", func(t *testing.T) {
		global := writeUserSettings(t, string(mirrorsXML(mirrorXML("corp", "*", "https://nexus.example.com/repository/public/"))))

		assert.Equal(t, []string{"org.apache.logging.log4j:log4j"}, run(t, nil, "-gs", global))
	})

	t.Run("when the only mirror isn't a catch-all", func(t *testing.T) {
		user := writeUserSettings(t, string(mirrorsXML(mirrorXML("c", "central", "https://nexus.example.com/repository/public/"))))

		assert.Contains(t, run(t, nil, "-s", user), "log4j:log4j", "without a catch-all the prefix is still guessed")
		assert.NotContains(t, mavenArgs, "-gs", "glab leaves the global settings alone when it guesses")
	})

	t.Run("when glab can't use the caller's settings", func(t *testing.T) {
		// A file the merge can't parse makes glab fall back to its own
		// settings, so Maven never sees the caller's catch-all.
		user := writeUserSettings(t, "<settings>&bogus;"+string(mirrorsXML(mirrorXML("corp", "*", "https://nexus.example.com/repository/public/")))[len("<settings xmlns=\"http://maven.apache.org/SETTINGS/1.0.0\">"):])

		assert.Contains(t, run(t, nil, "-s", user), "log4j:log4j")
	})

	t.Run("when mvn can't be found", func(t *testing.T) {
		t.Setenv("MAVEN_ARGS", "")
		t.Chdir(t.TempDir())
		user := writeUserSettings(t, string(mirrorsXML(mirrorXML("corp", "*", "https://nexus.example.com/repository/public/"))))
		m := Maven()
		mm, ok := m.(*mavenManager)
		require.True(t, ok)
		mm.lookPath = func(string) (string, error) { return "", errors.New("not found") }
		matcher := m.Matcher()
		mm.SetArgs([]string{"-s", user, "-Duser.home=" + t.TempDir(), "verify"})
		m.Environment("http://127.0.0.1:52999")
		m.CATrustEnviron(filepath.Join(t.TempDir(), "ca.pem"))

		assert.NotEmpty(t, matcher.Match(parentPOM).Alternates, "unknown global settings could hold a mirror that wins for some repository")
	})
}

func TestMavenSettingsErrorKeepsTheFileOut(t *testing.T) {
	dec := xml.NewDecoder(strings.NewReader("<settings>\n<password>&hunter2;</password></settings>"))
	var err error
	for err == nil {
		_, err = dec.Token()
	}
	require.Contains(t, err.Error(), "hunter2", "the decoder quotes the file")

	assert.Equal(t, "XML syntax error on line 2", mavenSettingsError(fmt.Errorf("merge: %w", err)))
	assert.Equal(t, "no <settings> element", mavenSettingsError(errors.New("no <settings> element")))
}
