package sync

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// secretPattern finds one kind of secret. When re has a capturing group, only
// that group is replaced, so the label around it, such as a header name, stays
// readable.
type secretPattern struct {
	kind string
	// hints are lowercase strings that every match contains. A text holding
	// none of them is skipped without running re, which is much slower.
	hints []string
	re    *regexp.Regexp
	// tokenLike, when set, keeps a match that reads like code or prose rather
	// than a credential.
	tokenLike bool
	// mixedCase, with tokenLike, also keeps a match without both upper and
	// lower case letters, such as a kebab-case name.
	mixedCase bool
	// digit, when set, leaves a value without a digit as it is, so a type name
	// such as ApiKeyType isn't redacted.
	digit bool
	// named means re matches a name and the separator after it, and the value
	// that follows is read by assignedValue. The value is only replaced when
	// the name is a credentialKey.
	named bool
	// extend, when set, returns where a match ending at end really ends.
	extend func(s string, end int) int
}

const (
	keyLineBreak  = `[ \t]*(?:\r?\n|\\r\\n|\\n|\\\\r\\\\n|\\\\n)`
	keyHeaderLine = `(?:Proc-Type|DEK-Info|Version|Comment|Hash|Charset):(?:[^\r\n\\]|\\[^rn\\])*`
	keyBase64     = `(?:[A-Za-z0-9+/=]|\\/)`
)

// keyLine matches the next line of a private key at the start of a text.
var keyLine = regexp.MustCompile(`\A(?:` + keyLineBreak + `)+[ \t]*(?:` + keyHeaderLine + `|(` + keyBase64 + `+))`)

// extendKey returns where the private key that a match ending at end starts
// ends. It takes whole lines that look like a key, so prose after the key ends
// it. A base64 line of 16 or more characters can also end where the text was
// cut off, such as at "…" or " [truncated]". The key then ends there, because
// keyLine only matches after a line break. A shorter one must have at least
// 4 characters and end at a line break, a quote or the end of the text, so a
// word such as EOF on the next line is kept.
func extendKey(s string, end int) int {
	for {
		m := keyLine.FindStringSubmatchIndex(s[end:])
		if m == nil {
			return end
		}
		if n := m[3] - m[2]; m[2] >= 0 && n < 16 && (n < 4 || !endsKeyLine(s[end+m[1]:])) {
			return end
		}
		end += m[1]
	}
}

func endsKeyLine(rest string) bool {
	rest = strings.TrimLeft(rest, " \t")
	return rest == "" || strings.ContainsRune("\r\n\\\"'`", rune(rest[0]))
}

// secretPatterns are formats that are secrets wherever they appear. They are
// best effort: a secret in a format not listed here is sent as it is.
// Values that start with $ are left alone, because they name a variable rather
// than hold its value, and so are placeholders and ones an earlier pattern
// already replaced.
var secretPatterns = []secretPattern{
	{
		kind:  "private_key",
		hints: []string{"private key"},
		re:    regexp.MustCompile(`(?s)-----BEGIN[A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----.*?-----END[A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`),
	},
	// A key cut off before its END line. It starts only when the line after
	// the BEGIN line looks like a key, a PEM or PGP header or base64, so a
	// BEGIN line quoted in code or prose is left alone.
	{
		kind:  "private_key",
		hints: []string{"private key"},
		re: regexp.MustCompile(`-----BEGIN[A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----` +
			`(?:` + keyLineBreak + `)+[ \t]*(?:` + keyHeaderLine + `|` + keyBase64 + `{16,})`),
		extend: extendKey,
	},
	{
		kind:  "gitlab_token",
		hints: []string{"glpat-", "gloas-", "gldt-", "glrt-", "glrtr-", "glcbt-", "glptt-", "glft-", "glimt-", "glagent-", "glsoat-", "glffct-", "glwt-"},
		re:    regexp.MustCompile(`gl(?:pat|oas|dt|rt|rtr|cbt|ptt|ft|imt|agent|soat|ffct|wt)-[0-9A-Za-z_\-.]{20,}`),
	},
	{
		kind:  "github_token",
		hints: []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"},
		re:    regexp.MustCompile(`\b(?:gh[pousr]_[0-9A-Za-z]{36,}|github_pat_[0-9A-Za-z_]{40,})`),
	},
	{
		kind:  "aws_access_key",
		hints: []string{"akia", "asia"},
		re:    regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	},
	{
		kind:  "aws_secret_key",
		hints: []string{"aws_secret_access_key"},
		re:    regexp.MustCompile(`(?i)aws_secret_access_key["']?\s*[:=]\s*["']?([0-9A-Za-z/+=]{40})`),
	},
	{
		kind:  "google_api_key",
		hints: []string{"aiza"},
		re:    regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}`),
	},
	{
		kind:  "slack_token",
		hints: []string{"xox", "xapp-", "hooks.slack.com"},
		re:    regexp.MustCompile(`\b(?:xox[abposre]-[0-9A-Za-z\-]{10,}|xapp-[0-9A-Za-z\-]{10,}|https://hooks\.slack\.com/services/[0-9A-Za-z/]+)`),
	},
	{
		kind:  "stripe_key",
		hints: []string{"_live_", "_test_"},
		re:    regexp.MustCompile(`\b[rs]k_(?:live|test)_[0-9A-Za-z]{16,}`),
	},
	{
		kind:      "ai_api_key",
		hints:     []string{"sk-"},
		re:        regexp.MustCompile(`\bsk-[0-9A-Za-z_\-]{20,}`),
		tokenLike: true,
		mixedCase: true,
	},
	{
		kind:  "npm_token",
		hints: []string{"npm_"},
		re:    regexp.MustCompile(`\bnpm_[0-9A-Za-z]{36}\b`),
	},
	{
		kind:  "pypi_token",
		hints: []string{"pypi-"},
		re:    regexp.MustCompile(`\bpypi-[0-9A-Za-z_\-]{50,}`),
	},
	{
		kind:  "jwt",
		hints: []string{"eyj"},
		re:    regexp.MustCompile(`\beyJ[0-9A-Za-z_\-]{10,}\.eyJ[0-9A-Za-z_\-]{10,}\.[0-9A-Za-z_\-]{10,}`),
	},
	{
		kind:      "http_credential",
		hints:     []string{"authorization"},
		re:        regexp.MustCompile(`(?i)\b(?:proxy-)?authorization["']?\s*:\s*["']?(?:bearer|basic|token)\s+([^\s"'$<{\[][^\s"']{7,})`),
		tokenLike: true,
	},
	{
		kind:      "http_credential",
		hints:     []string{"private-token", "private_token", "job-token", "job_token", "deploy-token", "deploy_token", "x-api-key", "x-gitlab-token", "access_token"},
		re:        regexp.MustCompile(`(?i)\b(?:private[-_]token|job[-_]token|deploy[-_]token|x-api-key|x-gitlab-token|access_token)["']?\s*[:=]\s*["']?([^\s"'$<{\[&][^\s"'&]{7,})`),
		tokenLike: true,
	},
	{
		kind:  "url_password",
		hints: []string{"://"},
		re:    regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://[^\s/:@"']*:([^\s/@"'$<{\[][^\s/"']*)@`),
	},
	// A value assigned to a credential-like name in text, such as
	// MY_API_TOKEN=... in a command or password: ... in a YAML file. The value
	// must have a digit, so Optional[str] or getToken(ctx) aren't taken for
	// one, though a call whose name has a digit, such as sha256(x), is.
	{
		kind:  "credential",
		hints: []string{"token", "secret", "passw", "passphrase", "pwd", "key", "authorization"},
		re:    regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_\-]*)(?:\\?["'])?[ \t]*(?::=|[:=])[ \t]*`),
		digit: true,
		named: true,
	},
}

// sensitiveKey matches names whose value is a credential, such as headers or
// environment variables in tool arguments, or variables assigned in text,
// including prefixed ones such as OPENAI_API_KEY, STRIPE_SECRET_KEY,
// PGPASSWORD and MYSQL_PWD. Pagination tokens are cursors, not credentials,
// so they are kept.
var (
	sensitiveKey  = regexp.MustCompile(`(?i)^(?:(?:proxy[-_])?authorization|token|secret|passphrase|.*(?:password|passwd)|(?:.*[-_])?(?:api[-_]?key|secret[-_]key|private[-_]key|pwd)|.*[-_]secret|.*[-_]secret[-_]access[-_]key|.*[-_]token)$`)
	paginationKey = regexp.MustCompile(`(?i)^(?:next[-_])?page[-_]token$`)
)

// workingDirectory reports whether name is PWD holding a path, the shell's
// working directory, rather than a password as in an ODBC connection string.
func workingDirectory(name, value string) bool {
	return strings.EqualFold(name, "pwd") && (strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~"))
}

func credentialKey(name string) bool {
	return sensitiveKey.MatchString(name) && !paginationKey.MatchString(name)
}

func redactedMarker(kind string) string {
	return "[REDACTED:" + kind + "]"
}

// redactSecrets returns s with the secrets that secretPatterns find replaced
// by a marker that names their kind.
func redactSecrets(s string) string {
	lower := strings.ToLower(s)
	for _, p := range secretPatterns {
		if !containsAny(lower, p.hints) {
			continue
		}
		s = replaceSecret(s, p)
		lower = strings.ToLower(s)
	}
	return s
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// replaceSecret replaces each match of p in s, or only its first capturing
// group when it has one, or its second when p is named.
func replaceSecret(s string, p secretPattern) string {
	matches := secretMatches(s, p)
	if matches == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		switch {
		case p.named:
			start, end = m[4], m[5]
		case p.re.NumSubexp() > 0:
			start, end = m[2], m[3]
		}
		// A match inside a secret that was already replaced is part of it.
		if start < last {
			continue
		}
		if p.extend != nil {
			end = p.extend(s, end)
		}
		if start < 0 || (p.tokenLike && !looksLikeToken(s[start:end], p.mixedCase)) {
			continue
		}
		if p.digit && !strings.ContainsAny(s[start:end], "0123456789") {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(redactedMarker(p.kind))
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// secretMatches returns the matches of p in s. A named pattern's matches are
// a name, then the value, as a group each. re only matches names, so a name
// inside another value, as in args: --token=..., is still found.
func secretMatches(s string, p secretPattern) [][]int {
	if !p.named {
		return p.re.FindAllStringSubmatchIndex(s, -1)
	}
	var matches [][]int
	for _, m := range p.re.FindAllStringSubmatchIndex(s, -1) {
		name := m[2]
		// In escaped text such as user=bob\npassword=..., the name starts
		// after the letter of the escape.
		if name > 0 && s[name-1] == '\\' && strings.IndexByte("nrt", s[name]) >= 0 &&
			(!credentialKey(s[name:m[3]]) || paginationKey.MatchString(s[name+1:m[3]])) {
			name++
		}
		if !credentialKey(s[name:m[3]]) {
			continue
		}
		if start, end, ok := assignedValue(s, m[1]); ok && !workingDirectory(s[name:m[3]], s[start:end]) {
			matches = append(matches, []int{name, end, name, m[3], start, end})
		}
	}
	return matches
}

// maxCredentialLength bounds the value read after a credential-like name, so
// long text without spaces can't make redaction slow. Only the start of a
// longer value is replaced.
const maxCredentialLength = 256

// assignedValue returns where the value at i in s starts and ends. A quoted
// value, also one whose quotes are escaped as in \"...\", runs to its closing
// quote, and any other value to a space, quote, backslash, bracket or
// separator. It reports false when the value starts with $, <, {, [, &, ( or
// =, or has fewer than 8 characters.
func assignedValue(s string, i int) (int, int, bool) {
	var quote byte
	escaped := false
	switch {
	case i < len(s) && (s[i] == '"' || s[i] == '\''):
		quote = s[i]
		i++
	case i+1 < len(s) && s[i] == '\\' && (s[i+1] == '"' || s[i+1] == '\''):
		quote, escaped = s[i+1], true
		i += 2
	}
	start := i
	if i >= len(s) || strings.IndexByte(" \t\r\n\\\"'$<{[&(=", s[i]) >= 0 {
		return 0, 0, false
	}
	if quote != 0 {
		return quotedValue(s, start, quote, escaped)
	}
	for i < len(s) && strings.IndexByte(" \t\r\n\\\"'&,;|()[]{}<>", s[i]) < 0 {
		if i-start == maxCredentialLength {
			return start, i, true
		}
		i++
	}
	if i-start < 8 {
		return 0, 0, false
	}
	return start, i, true
}

// quotedValue returns where the value that starts at start in s ends, at its
// closing quote, stepping over escaped characters. A value cut off by the end
// of s or a line break ends there.
func quotedValue(s string, start int, quote byte, escaped bool) (int, int, bool) {
	i := start
	for i < len(s) && s[i] != '\r' && s[i] != '\n' && i-start < maxCredentialLength {
		if s[i] == quote && !escaped {
			break
		}
		if s[i] == '\\' && i+1 < len(s) {
			if s[i+1] == '\r' || s[i+1] == '\n' || (escaped && s[i+1] == quote) {
				break
			}
			i += 2
			continue
		}
		i++
	}
	if i-start < 8 {
		return 0, 0, false
	}
	return start, min(i, len(s)), true
}

// looksLikeToken reports whether s mixes cases or has a digit, as generated
// credentials do, and isn't a call or member access, as code is. It tells
// "Bearer authentication", "process.env.TOKEN" and "sk-learn-pipeline-v2"
// apart from real values. With mixedCase, a digit alone is not enough.
func looksLikeToken(s string, mixedCase bool) bool {
	if strings.Contains(s, "(") || (strings.Contains(s, ".") && !strings.ContainsAny(s, "0123456789")) {
		return false
	}
	var upper, lower, digit bool
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	if mixedCase {
		return upper && lower
	}
	return digit || (upper && lower)
}

// redactJSON returns input with the secrets in its keys and string values
// replaced, or input unchanged when it holds none, so key order and formatting
// are kept.
func redactJSON(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return input
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		// Not JSON, so it can't be walked. It is redacted as text instead,
		// which may leave it invalid; it is omitted later then.
		return json.RawMessage(redactSecrets(string(input)))
	}
	redacted, changed := redactValue(v, "")
	// A secret the walk didn't see, such as one in a duplicate key that
	// decoding dropped, still means input must not be sent as it is.
	if !changed && redactSecrets(string(input)) == string(input) {
		return input
	}
	encoded, err := json.Marshal(redacted) //nolint:forbidigo // encoding, not output
	if err != nil {
		dbg.Debugf("could not encode redacted tool arguments: %v", err)
		return json.RawMessage(redactSecrets(string(input)))
	}
	return encoded
}

func redactValue(v any, key string) (any, bool) {
	switch v := v.(type) {
	case string:
		if v != "" && !strings.HasPrefix(v, "$") && credentialKey(key) && !workingDirectory(key, v) {
			return redactedMarker("credential"), true
		}
		r := redactSecrets(v)
		return r, r != v
	case []any:
		changed := false
		for i, e := range v {
			var c bool
			v[i], c = redactValue(e, key)
			changed = changed || c
		}
		return v, changed
	case map[string]any:
		out := make(map[string]any, len(v))
		changed := false
		for k, e := range v {
			rk := redactSecrets(k)
			if rk != k {
				rk = unusedKey(out, rk)
			}
			re, c := redactValue(e, k)
			out[rk] = re
			changed = changed || c || rk != k
		}
		return out, changed
	}
	return v, false
}

// unusedKey returns key, numbered when out already has it, so that keys that
// redact to the same marker don't overwrite each other.
func unusedKey(out map[string]any, key string) string {
	unused := key
	for n := 2; ; n++ {
		if _, ok := out[unused]; !ok {
			return unused
		}
		unused = key + "#" + strconv.Itoa(n)
	}
}

// redactSessionData replaces the secrets in the prompts and error messages
// that glab sends from data. The goal is redacted when it is read, because it
// is shortened then, and tool arguments when their size is checked, because a
// marker can be longer than the secret it replaces.
func redactSessionData(data *sessionData) {
	for i := range data.Prompts {
		data.Prompts[i].Text = redactSecrets(data.Prompts[i].Text)
	}
	for i := range data.ToolResults {
		data.ToolResults[i].Error = redactSecrets(data.ToolResults[i].Error)
	}
}
