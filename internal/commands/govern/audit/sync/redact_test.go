package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fake builds a token at run time, so the source holds nothing that looks like
// a real secret to secret scanners.
func fake(prefix string, n int) string {
	return prefix + strings.Repeat("a1B2", n/4+1)[:n]
}

func TestRedactSecrets(t *testing.T) {
	t.Parallel()

	privateKey := "-----BEGIN OPENSSH " + "PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH " + "PRIVATE KEY-----"
	jwt := "eyJ" + fake("", 20) + ".eyJ" + fake("", 20) + "." + fake("", 20)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"GitLab personal access token", "token " + fake("glpat-", 20) + " here", "token [REDACTED:gitlab_token] here"},
		{"GitLab CI job token", fake("glcbt-", 24), "[REDACTED:gitlab_token]"},
		{"GitHub token", fake("ghp_", 36), "[REDACTED:github_token]"},
		{"AWS access key", "AKIA" + strings.Repeat("A", 16), "[REDACTED:aws_access_key]"},
		{"AWS secret key", "aws_secret_access_key = " + fake("", 40), "aws_secret_access_key = [REDACTED:aws_secret_key]"},
		{"Google API key", fake("AIza", 35), "[REDACTED:google_api_key]"},
		{"Slack token", fake("xoxb-", 20), "[REDACTED:slack_token]"},
		{"Stripe key", fake("sk_live_", 24), "[REDACTED:stripe_key]"},
		{"Anthropic key", fake("sk-ant-", 30), "[REDACTED:ai_api_key]"},
		{"OpenAI key", fake("sk-proj-", 30), "[REDACTED:ai_api_key]"},
		{"GitHub fine-grained token", fake("github_pat_", 60), "[REDACTED:github_token]"},
		{"temporary AWS key", "ASIA" + strings.Repeat("B", 16), "[REDACTED:aws_access_key]"},
		{"Stripe restricted key", fake("rk_live_", 24), "[REDACTED:stripe_key]"},
		{"PyPI token", fake("pypi-", 60), "[REDACTED:pypi_token]"},
		{"Slack app token", fake("xapp-", 20), "[REDACTED:slack_token]"},
		{"GitLab token after a word", "TOKEN_" + fake("glpat-", 20), "TOKEN_[REDACTED:gitlab_token]"},
		{
			"encrypted PEM key",
			"-----BEGIN RSA " + "PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,0A1B\n\nMIIEpAIB\n-----END RSA " + "PRIVATE KEY-----",
			"[REDACTED:private_key]",
		},
		{
			"PGP key with a version header",
			"-----BEGIN PGP " + "PRIVATE KEY BLOCK-----\nVersion: GnuPG v2 (GNU/Linux)\n\nlQOYBF2xYzEBCADxKq",
			"[REDACTED:private_key]",
		},
		{
			"key written with escaped newlines",
			`printf '-----BEGIN RSA ` + `PRIVATE KEY-----\nMIIE\n-----END RSA ` + `PRIVATE KEY-----' > id_rsa`,
			`printf '[REDACTED:private_key]' > id_rsa`,
		},
		{"bearer token with a dot", "Authorization: Bearer ya29." + fake("", 30), "Authorization: Bearer [REDACTED:http_credential]"},
		{"PGP private key", "-----BEGIN PGP " + "PRIVATE KEY BLOCK-----\nlQOYBF\n-----END PGP " + "PRIVATE KEY BLOCK-----", "[REDACTED:private_key]"},
		{"private_token query parameter", "/api/v4/user?private_token=" + fake("", 20) + "&x=1", "/api/v4/user?private_token=[REDACTED:http_credential]&x=1"},
		{"URL password without a user", "redis://:" + fake("", 12) + "@cache:6379", "redis://:[REDACTED:url_password]@cache:6379"},
		{"URL password with an @", "postgres://u:p@" + fake("", 12) + "@db/app", "postgres://u:[REDACTED:url_password]@db/app"},
		{"npm token", fake("npm_", 36), "[REDACTED:npm_token]"},
		{"JWT", "cookie " + jwt, "cookie [REDACTED:jwt]"},
		{"private key", "key:\n" + privateKey + "\nend", "key:\n[REDACTED:private_key]\nend"},
		{"private key cut off before its end", "-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEpAIBAAKCAQEA3x", "[REDACTED:private_key]"},
		{
			"indented key in YAML",
			"tls:\n  key: |\n    -----BEGIN RSA " + "PRIVATE KEY-----\n    MIIEpAIBAAKCAQEA3xYz\n    AbCdEf123==\n",
			"tls:\n  key: |\n    [REDACTED:private_key]\n",
		},
		{
			"key with trailing spaces",
			"-----BEGIN RSA " + "PRIVATE KEY----- \nMIIEpAIBAAKCAQEA3xYz \nAbCd",
			"[REDACTED:private_key]",
		},
		{
			"key with escaped CRLF",
			`"-----BEGIN RSA ` + `PRIVATE KEY-----\r\nMIIEpAIBAAKCAQEA3xYz\r\nAbCd"`,
			`"[REDACTED:private_key]"`,
		},
		{
			"key escaped twice",
			`-----BEGIN RSA ` + `PRIVATE KEY-----\\nMIIEpAIBAAKCAQEA3xYz\\nAbCd`,
			"[REDACTED:private_key]",
		},
		{
			"encrypted key cut off",
			"-----BEGIN RSA " + "PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,0A1B\n\nMIIEpAIBAAKCAQEA3xYz",
			"[REDACTED:private_key]",
		},
		{
			"key header in a sentence",
			"-----BEGIN RSA " + "PRIVATE KEY----- see docs section 3.4 (page 12) for the format.",
			"-----BEGIN RSA " + "PRIVATE KEY----- see docs section 3.4 (page 12) for the format.",
		},
		{
			"key header followed by a long word and prose",
			"-----BEGIN RSA " + "PRIVATE KEY-----\nabcdefghijklmnopqrstuvwxyz see docs section 3.4 for the format.",
			"[REDACTED:private_key] see docs section 3.4 for the format.",
		},
		{
			"cut-off key followed by prose",
			"-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEpAIBAAKCAQEA3xYz\nAbCd\nsee docs section 3.4 for the format.\n\nNext paragraph.",
			"[REDACTED:private_key]\nsee docs section 3.4 for the format.\n\nNext paragraph.",
		},
		{
			"cut-off key in a quoted string",
			`key = "-----BEGIN RSA ` + `PRIVATE KEY-----\nMIIEpAIBAAKCAQEA3xYz\nAbCd" # see docs`,
			`key = "[REDACTED:private_key]" # see docs`,
		},
		{
			"key cut off in its first line",
			"-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEpAIBAAKCAQEA3xYz… [truncated]",
			"[REDACTED:private_key]… [truncated]",
		},
		{
			"key cut off in a later line",
			"-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEpAIBAAKCAQEA3xYz\nAbCdEfGhIjKlMnOp12 [truncated]",
			"[REDACTED:private_key] [truncated]",
		},
		{
			"key in a heredoc",
			"cat <<'EOF' > id\n-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEpAIBAAKCAQEA3xYz\nAbCd\nEOF\n",
			"cat <<'EOF' > id\n[REDACTED:private_key]\nEOF\n",
		},
		{
			"key with escaped slashes",
			`"-----BEGIN RSA ` + `PRIVATE KEY-----\nMIIEpAIBAAKCAQEA3x\/Yz\nAb\/Cd"`,
			`"[REDACTED:private_key]"`,
		},
		{
			"key escaped twice with CRLF",
			`-----BEGIN RSA ` + `PRIVATE KEY-----\\r\\nMIIEpAIBAAKCAQEA3xYz\\r\\nAbCd`,
			"[REDACTED:private_key]",
		},
		{
			"PGP key with a Windows path in a comment",
			"-----BEGIN PGP " + "PRIVATE KEY BLOCK-----\nComment: C:\\keys\\a.asc\n\nlQOYBF2xYzEBCADxKq\n=AbCd",
			"[REDACTED:private_key]",
		},
		{
			"key header in a key's comment",
			"-----BEGIN PGP " + "PRIVATE KEY BLOCK-----\nComment: -----BEGIN PGP " + "PRIVATE KEY BLOCK-----\nlQOYBF2xYzEBCADxKq",
			"[REDACTED:private_key]",
		},
		{
			"cut-off key followed by lines that start with long words",
			"-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEpAIBAAKCAQEA3xYz\nsomeLongIdentifierName1 is used here\nanotherLongIdentifier12 too",
			"[REDACTED:private_key] is used here\nanotherLongIdentifier12 too",
		},
		{
			"key header followed by prose lines",
			"-----BEGIN RSA " + "PRIVATE KEY-----\nis how the file starts.\nThen the body follows.",
			"-----BEGIN RSA " + "PRIVATE KEY-----\nis how the file starts.\nThen the body follows.",
		},
		{
			"bearer header",
			`curl -H "Authorization: Bearer ` + fake("", 30) + `" https://example.com`,
			`curl -H "Authorization: Bearer [REDACTED:http_credential]" https://example.com`,
		},
		{"PRIVATE-TOKEN header", "PRIVATE-TOKEN: " + fake("", 20), "PRIVATE-TOKEN: [REDACTED:http_credential]"},
		{"password in a URL", "git clone https://user:" + fake("", 12) + "@gitlab.com/g/p.git", "git clone https://user:[REDACTED:url_password]@gitlab.com/g/p.git"},

		{"variable in a header", `curl -H "PRIVATE-TOKEN: $GITLAB_TOKEN"`, `curl -H "PRIVATE-TOKEN: $GITLAB_TOKEN"`},
		{"braced variable in a header", "Authorization: Bearer ${TOKEN}", "Authorization: Bearer ${TOKEN}"},
		{"placeholder in a header", "PRIVATE-TOKEN: <your_access_token>", "PRIVATE-TOKEN: <your_access_token>"},
		{"variable in a URL", "https://user:$PASSWORD@gitlab.com", "https://user:$PASSWORD@gitlab.com"},
		{"header named in prose", "the Authorization header is required", "the Authorization header is required"},
		{"header with an explanation", "Authorization: Bearer authentication is required", "Authorization: Bearer authentication is required"},
		{"header set from code", "const authorization = getHeader(req)", "const authorization = getHeader(req)"},
		{"token header from an environment variable", "private-token: process.env.TOKEN", "private-token: process.env.TOKEN"},
		{"kebab-case name after sk-", "npm i sk-learn-pipeline-component-v2", "npm i sk-learn-pipeline-component-v2"},
		{"placeholder in a URL", "https://user:<password>@gitlab.com", "https://user:<password>@gitlab.com"},
		{"SSH remote", "git@gitlab.com:group/project.git and ssh://git@gitlab.com:22/g/p", "git@gitlab.com:group/project.git and ssh://git@gitlab.com:22/g/p"},
		{"code that checks for a key header", `strings.HasPrefix(s, "-----BEGIN RSA ` + `PRIVATE KEY-----") && ok`, `strings.HasPrefix(s, "-----BEGIN RSA ` + `PRIVATE KEY-----") && ok`},
		{"short prefix lookalike", "glpat-short and sk-12", "glpat-short and sk-12"},
		{"word ending in sk", "a task-" + fake("", 30), "a task-" + fake("", 30)},
		{"plain text", "fix the flaky test in sync_test.go", "fix the flaky test in sync_test.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, redactSecrets(tt.in))
		})
	}
}

func TestRedactJSON(t *testing.T) {
	t.Parallel()

	t.Run("keeps input without secrets byte for byte", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"z": 1.50, "a": ["x"]}`)
		assert.Equal(t, string(in), string(redactJSON(in)))
	})

	t.Run("redacts nested string values and keeps numbers", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"command":"echo ` + fake("glpat-", 20) + `","env":{"KEY":"` + fake("ghp_", 36) + `"},"n":12345678901234567890}`)
		assert.JSONEq(t,
			`{"command":"echo [REDACTED:gitlab_token]","env":{"KEY":"[REDACTED:github_token]"},"n":12345678901234567890}`,
			string(redactJSON(in)))
	})

	t.Run("redacts a secret split across JSON escapes", func(t *testing.T) {
		t.Parallel()
		key := "-----BEGIN EC " + "PRIVATE KEY-----\nMHc\n-----END EC " + "PRIVATE KEY-----\n"
		encoded, err := json.Marshal(map[string]string{"content": key})
		require.NoError(t, err)
		assert.JSONEq(t, `{"content":"[REDACTED:private_key]\n"}`, string(redactJSON(encoded)))
	})

	t.Run("redacts values under credential keys", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"headers":{"Authorization":"Bearer ` + fake("", 30) + `","X-Auth-Token":"t0k3n"},"env":{"AWS_SECRET_ACCESS_KEY":"` + fake("", 40) + `","HOME":"/home/me","TOKEN":"$CI_JOB_TOKEN"},"next_page_token":"c2"}`)
		assert.JSONEq(t,
			`{"headers":{"Authorization":"[REDACTED:credential]","X-Auth-Token":"[REDACTED:credential]"},"env":{"AWS_SECRET_ACCESS_KEY":"[REDACTED:credential]","HOME":"/home/me","TOKEN":"$CI_JOB_TOKEN"},"next_page_token":"c2"}`,
			string(redactJSON(in)))
	})

	t.Run("redacts secrets in keys", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"` + fake("glpat-", 20) + `":1}`)
		assert.JSONEq(t, `{"[REDACTED:gitlab_token]":1}`, string(redactJSON(in)))
	})

	t.Run("keeps keys that redact to the same marker", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"` + fake("glpat-", 20) + `":1,"` + fake("glpat-", 21) + `":2}`)
		var out map[string]int
		require.NoError(t, json.Unmarshal(redactJSON(in), &out))
		assert.Len(t, out, 2)
		assert.ElementsMatch(t, []int{1, 2}, []int{out["[REDACTED:gitlab_token]"], out["[REDACTED:gitlab_token]#2"]})
	})

	t.Run("does not send a secret that a duplicate key hides from the walk", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"a":"` + fake("glpat-", 20) + `","a":"other"}`)
		assert.NotContains(t, string(redactJSON(in)), "glpat-")
	})

	t.Run("keeps empty and null input", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, redactJSON(nil))
		assert.Equal(t, "null", string(redactJSON(json.RawMessage("null"))))
	})

	t.Run("redacts input that is not JSON as text", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "not json [REDACTED:gitlab_token]", string(redactJSON(json.RawMessage("not json "+fake("glpat-", 20)))))
	})
}

func TestGoalFromText_RedactsBeforeShortening(t *testing.T) {
	t.Parallel()

	token := fake("glpat-", 20)
	goal := goalFromText(strings.Repeat("x", 250) + " " + token)

	assert.NotContains(t, goal, "glpat-", "a token cut in half is still a leak")
}

func TestRunSync_RedactsSecretsBeforeUpload(t *testing.T) {
	env := newSyncAllEnv(t, true)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-1")

	token := fake("glpat-", 20)
	cwd, err := currentCWD()
	require.NoError(t, err)
	path, err := transcriptPath("sess-1", cwd)
	require.NoError(t, err)
	lines := jsonLines(t,
		map[string]any{
			"type": "user", "sessionId": "sess-1", "uuid": "u-1", "promptId": "p-1",
			"timestamp": time.Now().Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": "use " + token + " to call the API"},
		},
		map[string]any{
			"type": "assistant", "sessionId": "sess-1", "timestamp": time.Now().Format(time.RFC3339),
			"message": map[string]any{"content": []map[string]any{{
				"type": "tool_use", "id": "toolu_1", "name": "Bash",
				"input": map[string]any{"command": `curl -H "PRIVATE-TOKEN: ` + token + `" https://gitlab.com/api/v4/user`},
			}}},
		},
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(lines), 0o644))

	_, err = env.exec("-R my-group/my-project --silent")
	require.NoError(t, err)

	require.Len(t, env.fake.sessionRequests, 1)
	assert.Equal(t, "use [REDACTED:gitlab_token] to call the API", env.fake.sessionRequests[0].Goal)
	uploaded, err := json.Marshal(env.fake.events)
	require.NoError(t, err)
	assert.NotContains(t, string(uploaded), token)
	assert.Contains(t, string(uploaded), "use [REDACTED:gitlab_token] to call the API")
	assert.Contains(t, string(uploaded), `PRIVATE-TOKEN: [REDACTED:gitlab_token]`)
}

func BenchmarkRedactSecrets(b *testing.B) {
	prompt := strings.Repeat("Please refactor the sync loop so it retries on timeouts. ", 150)
	b.SetBytes(int64(len(prompt)))
	for b.Loop() {
		redactSecrets(prompt)
	}
}
