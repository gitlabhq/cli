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

func settingsPath(home string) string {
	return filepath.Join(home, ".m2", "settings.xml")
}

func wantServerBlock(alias, token string) string {
	return wantServerBlockIndented("    ", alias, token)
}

// wantServerBlockIndented renders the expected <server> block at an arbitrary
// indentation, for the fixtures that don't use the two-space convention.
func wantServerBlockIndented(indent, alias, token string) string {
	return indent + "<server>\n" +
		indent + "  <id>" + alias + "</id>\n" +
		indent + "  <username>__token__</username>\n" +
		indent + "  <password>" + token + "</password>\n" +
		indent + "</server>\n"
}

func TestLoginMaven_CreatesFileWhenAbsent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginMaven("my-alias", "tok1"))

	want := "<settings>\n" +
		"  <servers>\n" +
		wantServerBlock("my-alias", "tok1") +
		"  </servers>\n" +
		"</settings>\n"

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))

	fileInfo, err := os.Stat(settingsPath(home))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	dirInfo, err := os.Stat(filepath.Join(home, ".m2"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
}

func TestLoginMaven_PreservesUnrelatedContent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"  <servers>\n" +
		wantServerBlock("other-server", "other-secret") +
		"  </servers>\n" +
		"  <mirrors>\n" +
		"    <mirror>\n" +
		"      <id>central-mirror</id>\n" +
		"      <url>https://mirror.example.com/repo</url>\n" +
		"      <mirrorOf>central</mirrorOf>\n" +
		"    </mirror>\n" +
		"  </mirrors>\n" +
		"  <!-- a comment that should survive -->\n" +
		"</settings>\n"

	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginMaven("my-alias", "tok1"))

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, wantServerBlock("other-server", "other-secret"))
	assert.Contains(t, content, "<mirrors>")
	assert.Contains(t, content, "central-mirror")
	assert.Contains(t, content, "https://mirror.example.com/repo")
	assert.Contains(t, content, "<!-- a comment that should survive -->")
	assert.Contains(t, content, wantServerBlock("my-alias", "tok1"))
}

func TestLoginMaven_UpdatesExistingAliasInPlace(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginMaven("my-alias", "tok1"))
	require.NoError(t, loginMaven("my-alias", "tok2"))

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, "<id>my-alias</id>"), "must not duplicate the <server> entry")
	assert.Contains(t, content, "<password>tok2</password>")
	assert.NotContains(t, content, "<password>tok1</password>")
}

func TestLoginMaven_DifferentAliasAddsSeparateEntry(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginMaven("alias-one", "tok1"))
	require.NoError(t, loginMaven("alias-two", "tok2"))

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, wantServerBlock("alias-one", "tok1"))
	assert.Contains(t, content, wantServerBlock("alias-two", "tok2"))
	assert.Equal(t, 1, strings.Count(content, "<id>alias-one</id>"))
	assert.Equal(t, 1, strings.Count(content, "<id>alias-two</id>"))
}

func TestLoginMaven_AddsServersSectionWhenAbsent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	// A settings.xml holding only <mirrors> is common, and used to fail with
	// "no <servers> closing tag found", leaving the user to hand-edit XML.
	fixture := "<settings>\n" +
		"  <mirrors>\n" +
		"    <mirror>\n" +
		"      <id>central-mirror</id>\n" +
		"    </mirror>\n" +
		"  </mirrors>\n" +
		"</settings>\n"

	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginMaven("my-alias", "tok1"))

	want := "<settings>\n" +
		"  <mirrors>\n" +
		"    <mirror>\n" +
		"      <id>central-mirror</id>\n" +
		"    </mirror>\n" +
		"  </mirrors>\n" +
		"  <servers>\n" +
		wantServerBlock("my-alias", "tok1") +
		"  </servers>\n" +
		"</settings>\n"

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))
}

// TestLoginMaven_InsertedBlockFollowsSectionIndentation pins that a block
// inserted before an existing </servers> inherits that line's indentation,
// rather than a hardcoded four spaces that would leave the block misaligned
// with its siblings in a tab-indented file.
func TestLoginMaven_InsertedBlockFollowsSectionIndentation(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"\t<servers>\n" +
		"\t</servers>\n" +
		"</settings>\n"

	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginMaven("my-alias", "tok1"))

	want := "<settings>\n" +
		"\t<servers>\n" +
		wantServerBlockIndented("\t  ", "my-alias", "tok1") +
		"\t</servers>\n" +
		"</settings>\n"

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))
}

// TestLoginMaven_EscapesTokenAsXMLText covers a token carrying characters that
// are markup in XML text content. Writing them literally would produce a
// settings.xml Maven rejects, and one whose <password> element these regexes
// could no longer replace on the next refresh.
func TestLoginMaven_EscapesTokenAsXMLText(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginMaven("my-alias", "a&b<c>d"))

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	assert.Contains(t, string(got), "<password>a&amp;b&lt;c&gt;d</password>")

	// A refresh over the escaped block must still find and replace it, rather
	// than adding a second <server> for the same alias.
	require.NoError(t, loginMaven("my-alias", "tok2"))

	got, err = os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	content := string(got)
	assert.Contains(t, content, "<password>tok2</password>")
	assert.NotContains(t, content, "a&amp;b&lt;c&gt;d")
	assert.Equal(t, 1, strings.Count(content, "<id>my-alias</id>"))
}

// TestLoginMaven_EscapesTokenWhenAddingCredentialsToExistingBlock covers the
// other write path for a token: setMavenServerChild inserting a <password>
// into a user's existing <server> block, which escapes separately from
// mavenServerBlock.
func TestLoginMaven_EscapesTokenWhenAddingCredentialsToExistingBlock(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"  <servers>\n" +
		"    <server>\n" +
		"      <id>my-alias</id>\n" +
		"    </server>\n" +
		"  </servers>\n" +
		"</settings>\n"

	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginMaven("my-alias", "a&b"))

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	assert.Contains(t, string(got), "<password>a&amp;b</password>")
}

func TestLoginMaven_ExpandsSelfClosingServers(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	// <servers/> is valid and means "no servers configured", but it carries no
	// closing tag to insert a block before.
	fixture := "<settings>\n" +
		"  <servers/>\n" +
		"</settings>\n"

	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginMaven("my-alias", "tok1"))

	want := "<settings>\n" +
		"  <servers>\n" +
		wantServerBlock("my-alias", "tok1") +
		"  </servers>\n" +
		"</settings>\n"

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))
}

// TestLoginMaven_LeavesFileIntactOnError pins that an unusable settings.xml is
// never partially rewritten: the caller can fix the file by hand and retry.
func TestLoginMaven_LeavesFileIntactOnError(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<not-settings>\n  <mirrors/>\n</not-settings>\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))

	err := loginMaven("my-alias", "tok1")
	require.ErrorContains(t, err, "</settings>")

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	assert.Equal(t, fixture, string(got), "the file must be left byte-for-byte intact")
}

// TestLoginMaven_RefusesToAddASecondServersSection covers a <servers> element
// whose tags share a line: neither the </servers> nor the <servers/> pattern
// can place a block in it, and adding a whole new section would leave the file
// with two <servers> elements, which is invalid.
func TestLoginMaven_RefusesToAddASecondServersSection(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n  <servers></servers>\n</settings>\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))

	err := loginMaven("my-alias", "tok1")
	require.ErrorContains(t, err, "cannot edit")

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	assert.Equal(t, fixture, string(got))
	assert.Equal(t, 1, strings.Count(string(got), "<servers>"))
}

// TestLoginMaven_PreservesUserAddedChildrenOnRefresh covers the token-refresh
// case: replacing the whole <server> block with the canonical four lines would
// silently drop everything the user added to it.
func TestLoginMaven_PreservesUserAddedChildrenOnRefresh(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"  <servers>\n" +
		"    <server>\n" +
		"      <id>my-alias</id>\n" +
		"      <username>__token__</username>\n" +
		"      <password>tok1</password>\n" +
		"      <!-- keep me -->\n" +
		"      <filePermissions>660</filePermissions>\n" +
		"      <configuration>\n" +
		"        <httpHeaders>\n" +
		"          <property>\n" +
		"            <name>X-Custom</name>\n" +
		"            <value>1</value>\n" +
		"          </property>\n" +
		"        </httpHeaders>\n" +
		"      </configuration>\n" +
		"    </server>\n" +
		"  </servers>\n" +
		"</settings>\n"

	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginMaven("my-alias", "tok2"))

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, "<password>tok2</password>")
	assert.NotContains(t, content, "<password>tok1</password>")
	assert.Contains(t, content, "<!-- keep me -->")
	assert.Contains(t, content, "<filePermissions>660</filePermissions>")
	assert.Contains(t, content, "<name>X-Custom</name>")
	assert.Equal(t, 1, strings.Count(content, "<id>my-alias</id>"))
}

// TestLoginMaven_AddsCredentialsToBlockWithoutThem covers a <server> block a
// user created with only an <id>, for example while setting up a mirror.
func TestLoginMaven_AddsCredentialsToBlockWithoutThem(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"  <servers>\n" +
		"    <server>\n" +
		"      <id>my-alias</id>\n" +
		"    </server>\n" +
		"  </servers>\n" +
		"</settings>\n"

	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))

	require.NoError(t, loginMaven("my-alias", "tok1"))

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	assert.Equal(t, "<settings>\n"+
		"  <servers>\n"+
		wantServerBlock("my-alias", "tok1")+
		"  </servers>\n"+
		"</settings>\n", string(got))
}

// TestLoginMaven_UpdatingSecondAliasPreservesFirst guards against a regex
// that finds <id>alias-two</id> by lazily scanning from the *first*
// <server> tag in the file: if alias-two's block isn't the first one,
// such a regex spans across alias-one's whole block to reach alias-two's
// <id>, and replacing that span silently deletes alias-one's entry.
func TestLoginMaven_UpdatingSecondAliasPreservesFirst(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	require.NoError(t, loginMaven("alias-one", "tok1"))
	require.NoError(t, loginMaven("alias-two", "tok2"))
	// alias-two's <server> block now sits after alias-one's. Refreshing it
	// (a normal token-renewal call) must not disturb alias-one.
	require.NoError(t, loginMaven("alias-two", "tok2-refreshed"))

	got, err := os.ReadFile(settingsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, wantServerBlock("alias-one", "tok1"), "alias-one must survive refreshing alias-two")
	assert.Contains(t, content, wantServerBlock("alias-two", "tok2-refreshed"))
	assert.Equal(t, 1, strings.Count(content, "<id>alias-one</id>"))
	assert.Equal(t, 1, strings.Count(content, "<id>alias-two</id>"))
}

// writeSettings puts fixture at ~/.m2/settings.xml for a home that setHome has
// already pointed at.
func writeSettings(t *testing.T, home, fixture string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(home), []byte(fixture), 0o600))
}

// TestLoginMaven_RefusesOneLineServerBlock covers the shape that used to make
// this command a silent no-op: mavenServerBlockRe matches a one-line block, but
// mavenServerCloseRe needs </server> at the start of a line, so both credential
// inserts were skipped and the caller still printed a success line. Maven was
// left with no credentials and nothing said so.
func TestLoginMaven_RefusesOneLineServerBlock(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n  <servers>\n    <server><id>my-alias</id></server>\n  </servers>\n</settings>\n"
	writeSettings(t, home, fixture)

	err := loginMaven("my-alias", "tok1")
	require.ErrorContains(t, err, "cannot edit")

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	assert.Equal(t, fixture, string(got), "a refusal must leave the file byte-for-byte intact")
}

// TestLoginMaven_RefusesBlockWhoseLastChildSharesTheClosingLine covers the
// partial-write half of the same hole: <username> is on a line of its own so it
// was rewritten, then the <password> insert was skipped, leaving a <server>
// with a username and no password.
func TestLoginMaven_RefusesBlockWhoseLastChildSharesTheClosingLine(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n  <servers>\n    <server>\n      <id>my-alias</id>\n      <username>foo</username></server>\n  </servers>\n</settings>\n"
	writeSettings(t, home, fixture)

	err := loginMaven("my-alias", "tok1")
	require.ErrorContains(t, err, "cannot edit")

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	assert.Equal(t, fixture, string(got), "neither credential may be written when only one can be placed")
	assert.NotContains(t, string(got), "__token__")
}

// TestLoginMaven_SkipsCommentedOutServersSection covers the stock apache-maven
// settings.xml shape: commented-out examples ahead of the live section.
// Inserting before the first </servers> would put the block, and the <id> that
// finds it again, inside the comment, so every later refresh would claim the
// commented block and the live section would stay empty forever.
func TestLoginMaven_SkipsCommentedOutServersSection(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"  <!--\n" +
		"  <servers>\n" +
		"    <server><id>example</id></server>\n" +
		"  </servers>\n" +
		"  -->\n" +
		"  <servers>\n" +
		"  </servers>\n" +
		"</settings>\n"
	writeSettings(t, home, fixture)

	require.NoError(t, loginMaven("my-alias", "tok1"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	content := string(got)

	// The block belongs to the live section, which is the one after the comment.
	_, live, found := strings.Cut(content, "-->")
	require.True(t, found, "the comment must survive")
	assert.Contains(t, live, wantServerBlock("my-alias", "tok1"))
	assert.Contains(t, content, "<server><id>example</id></server>", "the commented example must be untouched")
}

// TestLoginMaven_SkipsCommentedOutServerBlockForSameAlias pins that a
// commented-out block carrying this alias does not absorb the refresh. It used
// to match, so credentials were rewritten inside the comment and the live
// section never got a block.
func TestLoginMaven_SkipsCommentedOutServerBlockForSameAlias(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"  <servers>\n" +
		"    <!--\n" +
		"    <server>\n" +
		"      <id>my-alias</id>\n" +
		"      <username>old</username>\n" +
		"      <password>old-secret</password>\n" +
		"    </server>\n" +
		"    -->\n" +
		"  </servers>\n" +
		"</settings>\n"
	writeSettings(t, home, fixture)

	require.NoError(t, loginMaven("my-alias", "tok1"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	content := string(got)

	assert.Contains(t, content, "<username>old</username>", "the commented block must be untouched")
	assert.Contains(t, content, "<password>old-secret</password>", "the commented block must be untouched")
	assert.Contains(t, content, wantServerBlock("my-alias", "tok1"), "a live block must be added")
	assert.Equal(t, 1, strings.Count(content, "__token__"))
}

// TestLoginMaven_CommentedCredentialDoesNotAbsorbTheRefresh covers the worst of
// the three comment cases, because it reports success while leaving Maven on
// the expiring token: a commented-out <password> ahead of the live one used to
// take the replacement.
func TestLoginMaven_CommentedCredentialDoesNotAbsorbTheRefresh(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"  <servers>\n" +
		"    <server>\n" +
		"      <id>my-alias</id>\n" +
		"      <username>__token__</username>\n" +
		"      <!-- <password>tok0</password> -->\n" +
		"      <password>tok1</password>\n" +
		"    </server>\n" +
		"  </servers>\n" +
		"</settings>\n"
	writeSettings(t, home, fixture)

	require.NoError(t, loginMaven("my-alias", "tok2"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	content := string(got)

	assert.Contains(t, content, "<!-- <password>tok0</password> -->", "the commented credential must be untouched")
	assert.Contains(t, content, "      <password>tok2</password>\n", "the live credential must be the one rewritten")
	assert.NotContains(t, content, "<password>tok1</password>")
}

// TestLoginMaven_AddsSectionWhenServersOnlyExistsInAComment pins that the
// "cannot edit" refusal keys off a live <servers>. A file whose only <servers>
// is commented out has no real one, so the </settings> path applies, and the
// old advice about putting tags on their own lines named tags the user never
// wrote.
func TestLoginMaven_AddsSectionWhenServersOnlyExistsInAComment(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n  <!-- <servers></servers> -->\n</settings>\n"
	writeSettings(t, home, fixture)

	require.NoError(t, loginMaven("my-alias", "tok1"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	content := string(got)

	assert.Contains(t, content, "  <servers>\n")
	assert.Contains(t, content, wantServerBlock("my-alias", "tok1"))
	assert.Contains(t, content, "<!-- <servers></servers> -->", "the comment must survive")
}

// TestLoginMaven_ExpandsSelfClosingServersOnCRLF pins that a CRLF file's line
// separator is consumed whole. The pattern used to stop before the \r, leaving
// a line holding nothing but a carriage return where <servers/> had been.
func TestLoginMaven_ExpandsSelfClosingServersOnCRLF(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	writeSettings(t, home, "<settings>\r\n  <servers/>\r\n</settings>\r\n")

	require.NoError(t, loginMaven("my-alias", "tok1"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	content := string(got)

	assert.Contains(t, content, wantServerBlock("my-alias", "tok1"))
	assert.NotContains(t, content, "\n\r\n", "the <servers/> line must not be left behind as a line holding only a carriage return")
	assert.NotContains(t, content, "<servers/>")
}

// TestLoginMaven_TreatsEmptyFileAsAbsent covers `touch ~/.m2/settings.xml`: an
// empty file has no <servers> section and no </settings> tag, so it used to
// fail with advice about tags that were never there.
func TestLoginMaven_TreatsEmptyFileAsAbsent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	writeSettings(t, home, "")

	require.NoError(t, loginMaven("my-alias", "tok1"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)

	want := "<settings>\n" +
		"  <servers>\n" +
		wantServerBlock("my-alias", "tok1") +
		"  </servers>\n" +
		"</settings>\n"
	assert.Equal(t, want, string(got))
}

// TestLoginMaven_TreatsWhitespaceOnlyFileAsAbsent is the same case one step
// less exact, since an interrupted editor session leaves a newline behind.
func TestLoginMaven_TreatsWhitespaceOnlyFileAsAbsent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	writeSettings(t, home, "\n\n  \n")

	require.NoError(t, loginMaven("my-alias", "tok1"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	assert.Contains(t, string(got), wantServerBlock("my-alias", "tok1"))
}

// TestLoginMaven_ProseCommentNamingServerKeepsTheLiveBlockVisible covers a
// comment that names <server> without closing it, which the stock apache-maven
// settings.xml avoids only because its examples are self-closed. Skipping a
// match that starts inside a comment was not enough: mavenServerBlockRe started
// at the commented <server> and ran to the live block's </server>, so skipping
// it skipped the live block too, and a second <id>my-alias</id> was appended.
// Maven silently keeps the stale first block in that file.
func TestLoginMaven_ProseCommentNamingServerKeepsTheLiveBlockVisible(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	comment := "    <!-- add one <server> entry per registry -->\n"
	fixture := "<settings>\n" +
		"  <servers>\n" +
		comment +
		"    <server>\n" +
		"      <id>my-alias</id>\n" +
		"      <username>__token__</username>\n" +
		"      <password>tok0</password>\n" +
		"    </server>\n" +
		"  </servers>\n" +
		"</settings>\n"
	writeSettings(t, home, fixture)

	require.NoError(t, loginMaven("my-alias", "tok2"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	content := string(got)

	assert.Contains(t, content, comment+wantServerBlock("my-alias", "tok2"), "the block after the comment must be the refreshed one")
	assert.Equal(t, 1, strings.Count(content, "<id>my-alias</id>"), "the live block must be refreshed, not duplicated")
	assert.NotContains(t, content, "tok0")
}

// TestLoginMaven_ProseCommentNamingPasswordDoesNotDuplicateIt is the same hole
// one element down, and the worse half: the commented <password> swallowed the
// live one, so the live element looked absent and a second <password> was
// inserted next to it. Duplicate children make settings.xml non-parseable, so
// Maven fails on every command until the user edits the file by hand.
func TestLoginMaven_ProseCommentNamingPasswordDoesNotDuplicateIt(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"  <servers>\n" +
		"    <server>\n" +
		"      <id>my-alias</id>\n" +
		"      <username>__token__</username>\n" +
		"      <!-- put the <password> below -->\n" +
		"      <password>tok0</password>\n" +
		"    </server>\n" +
		"  </servers>\n" +
		"</settings>\n"
	writeSettings(t, home, fixture)

	require.NoError(t, loginMaven("my-alias", "tok2"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	content := string(got)

	assert.Contains(t, content, "      <password>tok2</password>\n")
	assert.Equal(t, 1, strings.Count(content, "</password>"), "a live <password> must be rewritten, not joined by a second one")
	assert.Contains(t, content, "<!-- put the <password> below -->", "the comment must survive")
}

// TestLoginMaven_MatchesAnIDPaddedWithWhitespace pins that <id> is compared the
// way Maven reads it, which is trimmed. Requiring the exact spelling appended a
// second block with the same id, and Maven keeps using the stale first one.
func TestLoginMaven_MatchesAnIDPaddedWithWhitespace(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n" +
		"  <servers>\n" +
		"    <server>\n" +
		"      <id> my-alias </id>\n" +
		"      <username>__token__</username>\n" +
		"      <password>tok0</password>\n" +
		"    </server>\n" +
		"  </servers>\n" +
		"</settings>\n"
	writeSettings(t, home, fixture)

	require.NoError(t, loginMaven("my-alias", "tok2"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	content := string(got)

	assert.Contains(t, content, "      <id> my-alias </id>\n", "the user's spelling of the id must be left as it is")
	assert.Contains(t, content, "      <password>tok2</password>\n")
	assert.Equal(t, 1, strings.Count(content, "<server>"), "the existing block must be refreshed, not duplicated")
}

// TestLoginMaven_ReportsASettingsCloseTagItCannotUse covers a </settings> that
// shares a line with another element. The error used to say the file had no
// </settings> tag, which sent the user looking for a tag that is right there.
func TestLoginMaven_ReportsASettingsCloseTagItCannotUse(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "<settings>\n  <mirrors/></settings>\n"
	writeSettings(t, home, fixture)

	err := loginMaven("my-alias", "tok1")
	require.ErrorContains(t, err, "it does not start its own line")

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)
	assert.Equal(t, fixture, string(got), "a refusal must leave the file byte-for-byte intact")
}

// TestLoginMaven_IndentsPastACommentSharingTheClosingLine covers the cosmetic
// side of masking comments: the mask blanks a comment ahead of </servers> on the
// same line, so the captured "indentation" is as wide as the comment unless the
// line's real leading whitespace is what gets used.
func TestLoginMaven_IndentsPastACommentSharingTheClosingLine(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	writeSettings(t, home, "<settings>\n  <servers>\n  <!-- one entry per registry --></servers>\n</settings>\n")

	require.NoError(t, loginMaven("my-alias", "tok1"))

	got, readErr := os.ReadFile(settingsPath(home))
	require.NoError(t, readErr)

	want := "<settings>\n" +
		"  <servers>\n" +
		wantServerBlock("my-alias", "tok1") +
		"  <!-- one entry per registry --></servers>\n" +
		"</settings>\n"
	assert.Equal(t, want, string(got))
}
