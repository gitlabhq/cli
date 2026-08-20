//go:build !integration

package login

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gradlePropertiesPath(home string) string {
	return filepath.Join(home, ".gradle", "gradle.properties")
}

func TestLoginGradle_CreatesFileWhenAbsent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok1"))

	want := "myAliasUrl=https://ar.example.com\n" +
		"myAliasUsername=__token__\n" +
		"myAliasPassword=tok1\n"

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))

	fileInfo, err := os.Stat(gradlePropertiesPath(home))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	dirInfo, err := os.Stat(filepath.Join(home, ".gradle"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
}

func TestLoginGradle_PreservesUnrelatedContent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "someOther.property=value\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(gradlePropertiesPath(home)), 0o700))
	require.NoError(t, os.WriteFile(gradlePropertiesPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok1"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, "someOther.property=value")
	assert.Contains(t, content, "myAliasUrl=https://ar.example.com")
	assert.Contains(t, content, "myAliasUsername=__token__")
	assert.Contains(t, content, "myAliasPassword=tok1")
}

func TestLoginGradle_UpdatesExistingAliasInPlace(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok1"))
	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok2"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, "myAliasPassword="), "must not duplicate the property")
	assert.Contains(t, content, "myAliasPassword=tok2")
	assert.NotContains(t, content, "myAliasPassword=tok1")
}

// TestLoginGradle_UpdatesSpacedAssignmentInPlace pins that a key written the
// way java.util.Properties also accepts it, with spaces around the "=", is
// recognized as the same property. A "key=" prefix test misses it, appends a
// second assignment, and leaves the superseded token in the file: later
// assignments win, so the login still works and the stale credential is easy to
// miss.
func TestLoginGradle_UpdatesSpacedAssignmentInPlace(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "myAliasUrl = https://ar.example.com\n" +
		"myAliasUsername\t=\t__token__\n" +
		"  myAliasPassword = tok1\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(gradlePropertiesPath(home)), 0o700))
	require.NoError(t, os.WriteFile(gradlePropertiesPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok2"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, "myAliasPassword"), "must not append a second assignment")
	assert.Contains(t, content, "myAliasPassword=tok2")
	assert.NotContains(t, content, "tok1", "the superseded token must not survive the refresh")
}

// TestLoginGradle_LeavesCommentedPropertyAlone guards the other direction of
// gradlePropertyKey: a commented-out assignment is not an assignment, so it
// must neither be rewritten nor stop the real property from being added.
func TestLoginGradle_LeavesCommentedPropertyAlone(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "# myAliasPassword=not-a-real-token\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(gradlePropertiesPath(home)), 0o700))
	require.NoError(t, os.WriteFile(gradlePropertiesPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok1"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, "# myAliasPassword=not-a-real-token")
	assert.Contains(t, content, "myAliasPassword=tok1")
}

func TestLoginGradle_DifferentAliasAddsSeparateEntry(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginGradle("https://ar1.example.com", "aliasOne", "tok1"))
	require.NoError(t, loginGradle("https://ar2.example.com", "aliasTwo", "tok2"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, "aliasOneUrl=https://ar1.example.com")
	assert.Contains(t, content, "aliasOnePassword=tok1")
	assert.Contains(t, content, "aliasTwoUrl=https://ar2.example.com")
	assert.Contains(t, content, "aliasTwoPassword=tok2")
}

// TestLoginGradle_LeavesAContinuationLineAlone pins that a line which
// java.util.Properties reads as the continuation of the property above it is
// not mistaken for an assignment of its own. Rewriting it would bury the token
// in that property's value and leave myAliasPassword unset, so the build would
// authenticate with nothing while the login reported success.
func TestLoginGradle_LeavesAContinuationLineAlone(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "unrelated=first\\\n" +
		"myAliasPassword=still-the-unrelated-value\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(gradlePropertiesPath(home)), 0o700))
	require.NoError(t, os.WriteFile(gradlePropertiesPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok1"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, "myAliasPassword=still-the-unrelated-value", "the continuation line must be left as it was")
	assert.NotContains(t, content, "myAliasPassword=still-the-unrelated-value\\", "and must not be rewritten")

	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	assert.Equal(t, "myAliasPassword=tok1", lines[len(lines)-1], "the real assignment is appended instead")
}

// TestGradleContinuedLines pins which lines count as a continuation: an even
// number of trailing backslashes is an escaped backslash, not a join.
func TestGradleContinuedLines(t *testing.T) {
	lines := []string{
		`a=1\`,    // joins
		`b=2`,     // continuation of a
		`c=3\\`,   // an escaped backslash, no join
		`d=4`,     // an assignment of its own
		"e=5\\\r", // joins, CRLF file
		`f=6`,     // continuation of e
	}

	continued, dangling := gradleContinuedLines(lines)

	assert.Equal(t, []bool{false, true, false, false, false, true}, continued)
	assert.False(t, dangling, "the last line does not leave a continuation open")
}

// TestLoginGradle_EscapesBackslashesInValues pins that a backslash in a value
// is written escaped. Properties reads a lone backslash as an escape, so a
// --registry ending in one joins the line below: the URL property would
// swallow the username assignment, which would then never be defined, and
// Gradle would authenticate with no username after a green check.
func TestLoginGradle_EscapesBackslashesInValues(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginGradle(`https://ar.example.com/a\`, "myAlias", "tok1"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, `myAliasUrl=https://ar.example.com/a\\`, "the trailing backslash must be escaped")
	assert.Contains(t, content, "myAliasUsername=__token__", "the next line must stay an assignment of its own")
	assert.Contains(t, content, "myAliasPassword=tok1")
}

// TestLoginGradle_LeavesAnAssignmentBelowAContinuedCommentAlone pins that a
// comment does not continue into the line below it, which the Properties spec
// is explicit about. Skipping that assignment would append a second one and
// leave the superseded token in the file: later assignments win, so the login
// still works, which is what makes the stale credential easy to miss.
func TestLoginGradle_LeavesAnAssignmentBelowAContinuedCommentAlone(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "# see docs \\\n" + "myAliasPassword=old-live-token\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(gradlePropertiesPath(home)), 0o700))
	require.NoError(t, os.WriteFile(gradlePropertiesPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok1"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, "myAliasPassword="), "the real assignment must be refreshed, not duplicated")
	assert.NotContains(t, content, "old-live-token")
}

// TestLoginGradle_ClosesADanglingContinuationBeforeAppending pins the append
// path for a file whose last line leaves a continuation open. Appending
// straight after it makes the first property the rest of that property's
// value: the property is never defined and the unrelated one is corrupted,
// which breaks every Gradle invocation, not just this login.
func TestLoginGradle_ClosesADanglingContinuationBeforeAppending(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "org.gradle.jvmargs=-Xmx2g \\\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(gradlePropertiesPath(home)), 0o700))
	require.NoError(t, os.WriteFile(gradlePropertiesPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok1"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")

	require.Greater(t, len(lines), 1)
	assert.Equal(t, "org.gradle.jvmargs=-Xmx2g \\", lines[0], "the unrelated property must be left as it was")
	assert.Empty(t, lines[1], "an empty line must close the continuation before the first property")
	assert.Contains(t, string(got), "myAliasUrl=https://ar.example.com")
}

// TestLoginGradle_DropsTheOldValuesContinuationLines pins that refreshing a
// property whose value spanned a continuation takes the whole logical line
// with it. The new value is one line, so leaving the follow-on behind would
// strand the tail of the superseded token in the file as a property of its
// own.
func TestLoginGradle_DropsTheOldValuesContinuationLines(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "myAliasPassword=old-tok\\\n" +
		"en-tail\n" +
		"unrelated=keep-me\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(gradlePropertiesPath(home)), 0o700))
	require.NoError(t, os.WriteFile(gradlePropertiesPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok1"))

	got, err := os.ReadFile(gradlePropertiesPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, "myAliasPassword=tok1")
	assert.NotContains(t, content, "en-tail", "the old value's continuation must go with it")
	assert.Contains(t, content, "unrelated=keep-me", "an unrelated line must survive")
}

// TestLoginGradle_HonoursGradleUserHome pins that the file lands where Gradle
// reads it. On a machine that sets GRADLE_USER_HOME, which is common in CI,
// writing ~/.gradle leaves the credentials in a file no build opens, and the
// command still reports success.
func TestLoginGradle_HonoursGradleUserHome(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	gradleHome := filepath.Join(t.TempDir(), "gradle-home")
	t.Setenv("GRADLE_USER_HOME", gradleHome)

	require.NoError(t, loginGradle("https://ar.example.com", "myAlias", "tok1"))

	got, err := os.ReadFile(filepath.Join(gradleHome, "gradle.properties"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "myAliasPassword=tok1")

	_, err = os.Stat(gradlePropertiesPath(home))
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing may be written under ~/.gradle when GRADLE_USER_HOME is set")
}
