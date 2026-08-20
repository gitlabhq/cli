package login

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/fsx"
)

// mavenSettingsTemplate is written when no ~/.m2/settings.xml exists yet.
const mavenSettingsTemplate = "<settings>\n  <servers>\n  </servers>\n</settings>\n"

// mavenServersCloseRe locates the closing </servers> tag, capturing its
// indentation, so a new <server> block can be inserted just before it and
// indented one level deeper than the section it joins.
var mavenServersCloseRe = regexp.MustCompile(`(?m)^([ \t]*)</servers>`)

// mavenServersSelfClosingRe matches a self-closing <servers/>, capturing its
// indentation. The element is valid and means "no servers configured", but it
// has no closing tag to insert a block before, so it has to be expanded into
// a real section instead.
//
// The trailing class accepts \r so a CRLF file's line separator is consumed
// whole. Without it the \n is left behind on its own and the expansion leaves
// a line holding nothing but a carriage return.
var mavenServersSelfClosingRe = regexp.MustCompile(`(?m)^([ \t]*)<servers[ \t]*/>[ \t\r]*\n?`)

// mavenSettingsCloseRe locates the closing </settings> tag, capturing its
// indentation, so a whole <servers> section can be added to a settings.xml
// that has none — a file holding only <mirrors> or <profiles> is common.
var mavenSettingsCloseRe = regexp.MustCompile(`(?m)^([ \t]*)</settings>`)

// mavenSettingsCloseAnyRe matches a </settings> tag wherever it sits, including
// one sharing a line with another element. It only shapes the error message for
// a file mavenSettingsCloseRe cannot place a section in.
var mavenSettingsCloseAnyRe = regexp.MustCompile(`</settings>`)

// mavenServersOpenRe matches a <servers> opening tag, self-closing or not,
// anywhere in the file. It guards the "add a whole section" path: a file whose
// <servers> element the line-anchored patterns above can't place a block into
// (both tags on one line, for example) already has one, and adding a second
// would produce invalid XML.
var mavenServersOpenRe = regexp.MustCompile(`<servers[ \t]*/?>`)

// mavenServerBlockRe matches a single <server>...</server> block. Since
// </server> tags don't nest, a lazy match from one <server> to the nearest
// following </server> always captures exactly one block, never spanning
// into a neighboring block — unlike a regex that also requires a specific
// <id> to appear within the same lazy span, which can cross an unrelated
// block's boundary when the target block isn't the first one in the file.
var mavenServerBlockRe = regexp.MustCompile(`[ \t]*<server>[\s\S]*?</server>\n?`)

// mavenServerCloseRe locates a single block's </server> tag at the start of
// its own line, capturing the leading indentation so a child element
// inserted before it lines up with the block's existing children.
var mavenServerCloseRe = regexp.MustCompile(`(?m)^([ \t]*)</server>`)

var (
	mavenUsernameRe = regexp.MustCompile(`(?s)<username>.*?</username>`)
	mavenPasswordRe = regexp.MustCompile(`(?s)<password>.*?</password>`)
)

// mavenCommentRe matches one XML comment. XML forbids "--" inside a comment, so
// a lazy scan to the first "-->" always ends at that comment's real terminator.
var mavenCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

// mavenMaskComments returns a copy of content with every byte inside an XML
// comment replaced by a space, keeping line separators so line-anchored
// patterns still see the same lines, and keeping length so an index into the
// copy addresses the same byte in content. Every pattern in this file is
// matched against the copy and every slice is taken from content.
//
// Every pattern here is otherwise comment-blind, which is not a theoretical
// concern: the settings.xml that ships with apache-maven consists almost
// entirely of commented-out <server> and <servers> examples. Without masking,
// the first </servers> in such a file is inside a comment, so the new block is
// inserted there, and the <id> it writes then makes findMavenServerBlock claim
// the commented block on every later refresh, so the live section never
// receives credentials at all.
//
// Masking rather than discarding matches that start inside a comment, which is
// what this used to do: the lazy patterns can start at a tag named in a prose
// comment ("add one <server> entry per registry") and run to the live element's
// closing tag, so discarding that match discards the live element with it. The
// live <server> block then looks absent and gets a duplicate <id> appended,
// where Maven silently keeps the stale first block, and a live <password> gets
// a second <password> inserted next to it, which makes settings.xml
// non-parseable. Masking removes the commented tag instead of the match around
// it, so the live element is still found.
func mavenMaskComments(content string) string {
	masked := []byte(content)
	for _, span := range mavenCommentRe.FindAllStringIndex(content, -1) {
		for i := span[0]; i < span[1]; i++ {
			if masked[i] != '\n' && masked[i] != '\r' {
				masked[i] = ' '
			}
		}
	}
	return string(masked)
}

// mavenIndent returns the indentation to give a generated element, from the
// [start, end) span a pattern captured as the leading whitespace of its line.
// start is the start of that line, since every pattern capturing an indent here
// is anchored with ^.
//
// The span is captured from the mask, where a comment sharing the line
// ("<!-- x --></servers>") reads as blanks, so it can cover bytes that are not
// whitespace in content at all. Only the line's real leading whitespace is
// kept: otherwise the new element is indented by the width of somebody's
// comment.
func mavenIndent(content string, start, end int) string {
	indent := content[start:end]
	if i := strings.IndexFunc(indent, func(r rune) bool { return r != ' ' && r != '\t' }); i >= 0 {
		indent = indent[:i]
	}
	return indent
}

// mavenXMLText escapes the characters that cannot appear literally in XML text
// content. Only the credential values go through it: the token comes straight
// from the server response, and a token format that ever carried an "&" would
// otherwise write XML that Maven rejects and that these regexes can no longer
// find on the next refresh.
//
// alias is deliberately not escaped. registryAliasRe restricts an explicit
// --registry-alias to [a-zA-Z0-9._-] and defaultRegistryAlias sanitizes a
// derived one, so there is nothing to escape today. If that ever loosens, the
// <id> written here and the idRe that finds the block again in
// upsertMavenServer have to start escaping together, or a refresh would stop
// matching the block it wrote.
var mavenXMLText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// loginMaven configures ~/.m2/settings.xml with a <server> block keyed by
// <id>alias</id> so that Maven authenticates against the registry using HTTP
// Basic auth ("__token__" / token). If a <server> with this alias's <id>
// already exists, its credentials are updated in place; everything else in
// the file is preserved byte-for-byte, including on the error path.
//
// The registry URL is not a parameter: Maven's <server> block carries only the
// alias/token pair, and the URL itself lives in the caller's pom.xml or
// settings <repositories>, keyed by the same alias.
func loginMaven(alias, token string) error {
	home, err := homeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, ".m2")
	path := filepath.Join(dir, "settings.xml")

	content, err := os.ReadFile(path)
	switch {
	case err == nil:
		// An existing but empty file has no <servers> section and no
		// </settings> tag to add one to, so it would otherwise fail with
		// "found neither a <servers> section nor a </settings> tag". Treated
		// like a missing file instead: `touch ~/.m2/settings.xml`, or an
		// interrupted editor session, is a plausible way to get here.
		if len(bytes.TrimSpace(content)) == 0 {
			content = []byte(mavenSettingsTemplate)
		}
	case os.IsNotExist(err):
		content = []byte(mavenSettingsTemplate)
	default:
		return fmt.Errorf("reading %s: %w", path, err)
	}

	updated, err := upsertMavenServer(string(content), alias, token)
	if err != nil {
		return fmt.Errorf("updating %s: %w", path, err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := fsx.WriteOwnerOnly(path, []byte(updated)); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// upsertMavenServer returns content with a <server> block for alias that
// authenticates as "__token__"/token, handling the settings.xml shapes that
// occur in practice: an existing block for this alias, an existing <servers>
// section, a self-closing <servers/>, and no <servers> element at all.
func upsertMavenServer(content, alias, token string) (string, error) {
	// \s* around the alias because Maven trims element text, so
	// "<id> my-alias </id>" is the same id as "<id>my-alias</id>". Matching only
	// the untrimmed spelling appended a second block with the same id, and Maven
	// keeps using the stale first one.
	idRe := regexp.MustCompile(`<id>\s*` + regexp.QuoteMeta(alias) + `\s*</id>`)
	masked := mavenMaskComments(content)

	if loc := findMavenServerBlock(masked, idRe); loc != nil {
		block, err := updateMavenServerCredentials(content[loc[0]:loc[1]], masked[loc[0]:loc[1]], token)
		if err != nil {
			return "", err
		}
		return content[:loc[0]] + block + content[loc[1]:], nil
	}

	if m := mavenServersCloseRe.FindStringSubmatchIndex(masked); m != nil {
		indent := mavenIndent(content, m[2], m[3]) + "  "
		return content[:m[0]] + mavenServerBlock(alias, token, indent) + content[m[0]:], nil
	}

	if m := mavenServersSelfClosingRe.FindStringSubmatchIndex(masked); m != nil {
		indent := mavenIndent(content, m[2], m[3])
		return content[:m[0]] + mavenServersSection(alias, token, indent) + content[m[1]:], nil
	}

	// Only a live <servers> blocks the "add a whole section" path. A file whose
	// only <servers> is inside a comment has no real one, so adding a section
	// before </settings> is correct there, and refusing would hand the user
	// advice about tags they never wrote.
	if mavenServersOpenRe.MatchString(masked) {
		return "", errors.New("found a <servers> element this command cannot edit; put its <servers> and </servers> tags on their own lines, or add the <server> block by hand")
	}

	if m := mavenSettingsCloseRe.FindStringSubmatchIndex(masked); m != nil {
		indent := mavenIndent(content, m[2], m[3]) + "  "
		return content[:m[0]] + mavenServersSection(alias, token, indent) + content[m[0]:], nil
	}

	// A </settings> that shares a line with something else exists but is not
	// where mavenSettingsCloseRe looks, so it needs its own message: saying the
	// file has no </settings> tag when it plainly has one sends the user looking
	// for the wrong problem.
	if mavenSettingsCloseAnyRe.MatchString(masked) {
		return "", errors.New("found a </settings> tag this command cannot use: it does not start its own line; put </settings> on its own line, or add the <servers> section by hand")
	}

	return "", errors.New("found neither a <servers> section nor a </settings> tag to add one to")
}

// updateMavenServerCredentials rewrites only <username> and <password> inside
// a single <server> block, so a user's own children — <configuration> HTTP
// headers, <filePermissions>, comments — survive. Replacing the whole block
// with the canonical four lines would silently delete them on every routine
// token refresh.
// Both children are placed or neither is: on error the caller drops the
// half-updated block, so a <server> is never left with a rewritten <username>
// and a stale or missing <password>.
//
// masked is block with its comment interiors blanked, per mavenMaskComments.
func updateMavenServerCredentials(block, masked, token string) (string, error) {
	block, masked, err := setMavenServerChild(block, masked, mavenUsernameRe, "username", "__token__")
	if err != nil {
		return "", err
	}
	block, _, err = setMavenServerChild(block, masked, mavenPasswordRe, "password", token)
	if err != nil {
		return "", err
	}
	return block, nil
}

// setMavenServerChild replaces the first <name> element in block with value, or
// inserts one just before </server> when the block has none. It returns the
// updated block along with masked kept in step with it, so a second call sees a
// mask that still matches the block it is handed. It returns an error when it
// can place neither element, which happens when the block's </server> does not
// start its own line, as in a one-line <server><id>x</id></server>.
//
// That error is the point: mavenServerBlockRe matches such a block happily
// while mavenServerCloseRe cannot, so returning the block untouched reported a
// successful login that had written nothing at all.
//
// Only the first match is replaced, not all of them, so a nested element using
// the same tag name is left alone. The residual limitation is that the first
// match is assumed to be the <server>'s own child: a block that put a
// <configuration> containing its own <username> ahead of the server's would
// have the wrong one rewritten. Matching on indentation instead would be
// stricter but has the worse failure mode: a miss inserts a second <username>,
// and duplicate elements are invalid where editing a nested tag is merely
// wrong.
func setMavenServerChild(block, masked string, re *regexp.Regexp, name, value string) (string, string, error) {
	element := "<" + name + ">" + mavenXMLText.Replace(value) + "</" + name + ">"

	// Matching masked, not block, so a commented-out <password> ahead of the live
	// one cannot absorb the replacement: that would leave the live element
	// holding the expiring token while the command reported success. The
	// replacement text is live, so splicing it into masked as well keeps the two
	// strings the same length and the mask correct for the next call.
	if loc := re.FindStringIndex(masked); loc != nil {
		return block[:loc[0]] + element + block[loc[1]:], masked[:loc[0]] + element + masked[loc[1]:], nil
	}

	m := mavenServerCloseRe.FindStringSubmatchIndex(masked)
	if m == nil {
		return "", "", fmt.Errorf("found a <server> block this command cannot edit: its <%s> is missing and its </server> tag does not start a line; put </server> on its own line, or set <%s> by hand", name, name)
	}
	insert := mavenIndent(block, m[2], m[3]) + "  " + element + "\n"

	return block[:m[0]] + insert + block[m[0]:], masked[:m[0]] + insert + masked[m[0]:], nil
}

// mavenServersSection renders a whole <servers> section, indented at indent,
// holding a single <server> block for alias.
func mavenServersSection(alias, token, indent string) string {
	return indent + "<servers>\n" +
		mavenServerBlock(alias, token, indent+"  ") +
		indent + "</servers>\n"
}

// mavenServerBlock renders a canonical <server> block, indented at indent.
func mavenServerBlock(alias, token, indent string) string {
	return indent + "<server>\n" +
		indent + "  <id>" + alias + "</id>\n" +
		indent + "  <username>__token__</username>\n" +
		indent + "  <password>" + mavenXMLText.Replace(token) + "</password>\n" +
		indent + "</server>\n"
}

// findMavenServerBlock returns the [start, end) byte range of the single
// <server>...</server> block in masked whose body matches idRe, or nil if no
// block matches. It scans block-by-block (via mavenServerBlockRe's
// non-crossing match) rather than matching <id> and </server> in one lazy
// span, so a target block that isn't the first <server> in the file can
// never consume a preceding, unrelated block.
//
// masked is the whole file with its comment interiors blanked, per
// mavenMaskComments, so a commented-out example carrying this alias does not get
// its credentials rewritten in place while the live section goes without. The
// returned range still addresses the original file.
func findMavenServerBlock(masked string, idRe *regexp.Regexp) []int {
	for _, loc := range mavenServerBlockRe.FindAllStringIndex(masked, -1) {
		if idRe.MatchString(masked[loc[0]:loc[1]]) {
			return loc
		}
	}
	return nil
}
