//go:build !integration

package pm

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestBundleConfigCACert(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{`BUNDLE_SSL_CA_CERT: "/a/ca.pem"`, "/a/ca.pem", true},
		{`BUNDLE_SSL_CA_CERT: '/s/ca.pem'`, "/s/ca.pem", true},
		{`BUNDLE_SSL_CA_CERT: /n/ca.pem`, "/n/ca.pem", true},
		{`BUNDLE_SSL_CA_CERT: "/crlf"` + "\r", "/crlf", true},
		{`bundle_ssl_ca_cert: /lower/ca.pem`, "", false},
		{`  BUNDLE_SSL_CA_CERT: "/nested"`, "", false},
		{`# BUNDLE_SSL_CA_CERT: "/comment"`, "", false},
		{`BUNDLE_SSL_VERIFY_MODE: "1"`, "", false},
	}
	for _, c := range cases {
		got, ok := bundleConfigCACert(c.line)
		assert.Equalf(t, c.ok, ok, "line %q", c.line)
		assert.Equalf(t, c.want, got, "line %q", c.line)
	}
}

type bundleProject struct {
	root     string
	config   string
	userPEM  []byte
	caPath   string
	proxyPEM []byte
}

func newBundleProject(t *testing.T, extraConfig string) bundleProject {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	p := bundleProject{root: root, userPEM: newTestAuthority(t).pem(), proxyPEM: newTestAuthority(t).pem()}
	userCA := filepath.Join(root, "corp-ca.pem")
	require.NoError(t, os.WriteFile(userCA, p.userPEM, 0o600))
	p.caPath = filepath.Join(root, "glab-ca.pem")
	require.NoError(t, os.WriteFile(p.caPath, p.proxyPEM, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile"), []byte("source \"https://rubygems.org\"\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".bundle", "plugin"), 0o700))
	p.config = filepath.Join(root, ".bundle", "config")
	content := "---\nBUNDLE_SSL_CA_CERT: \"" + userCA + "\"\n" + extraConfig
	require.NoError(t, os.WriteFile(p.config, []byte(content), 0o600))
	sub := filepath.Join(root, "app")
	require.NoError(t, os.Mkdir(sub, 0o700))
	t.Chdir(sub)
	return p
}

func bundleSetup(t *testing.T, caPath string, parent ...string) (setupResult, *bytes.Buffer) {
	t.Helper()
	ios, _, _, errOut := cmdtest.TestIOStreams()
	res, err := bundleManager{}.Setup(t.Context(), setupInput{parent: parent, caPath: caPath, io: ios})
	require.NoError(t, err)
	return res, errOut
}

func generatedCACert(t *testing.T, appConfig string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(appConfig, "config"))
	require.NoError(t, err)
	var caFile string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if v, ok := bundleConfigCACert(line); ok {
			caFile = v
		}
	}
	require.NotEmpty(t, caFile, "the generated config must set BUNDLE_SSL_CA_CERT")
	return caFile
}

func TestBundleSetupReplacesLocalConfigCACert(t *testing.T) {
	p := newBundleProject(t, "BUNDLE_JOBS: \"3\"\n")
	original, err := os.ReadFile(p.config)
	require.NoError(t, err)

	res, _ := bundleSetup(t, p.caPath)

	appConfig := lookupEnv(res.env, "BUNDLE_APP_CONFIG")
	require.NotEmpty(t, appConfig, "a local BUNDLE_SSL_CA_CERT outranks the environment, so the local config must be replaced")
	generated, err := os.ReadFile(filepath.Join(appConfig, "config"))
	require.NoError(t, err)
	assert.Contains(t, string(generated), "BUNDLE_JOBS: \"3\"\n", "the user's other local settings must be kept")
	caPEM, err := os.ReadFile(generatedCACert(t, appConfig))
	require.NoError(t, err)
	assert.Equal(t, append(append([]byte{}, p.userPEM...), p.proxyPEM...), caPEM, "Bundler must trust the user's CA and the proxy")
	plugins, err := os.Stat(filepath.Join(appConfig, "plugin"))
	require.NoError(t, err)
	assert.True(t, plugins.IsDir(), "local Bundler plugins must stay reachable")

	res.stop()
	assert.NoDirExists(t, appConfig)
	after, err := os.ReadFile(p.config)
	require.NoError(t, err)
	assert.Equal(t, original, after, "an unchanged config must not be rewritten")
}

func TestBundleSetupWritesBackBundlerConfigChanges(t *testing.T) {
	p := newBundleProject(t, "")
	original, err := os.ReadFile(p.config)
	require.NoError(t, err)
	res, _ := bundleSetup(t, p.caPath)
	appConfig := lookupEnv(res.env, "BUNDLE_APP_CONFIG")
	require.NotEmpty(t, appConfig)

	generated, err := os.ReadFile(filepath.Join(appConfig, "config"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(appConfig, "config"), append(generated, "BUNDLE_PATH: \"vendor/bundle\"\n"...), 0o600))
	res.stop()

	after, err := os.ReadFile(p.config)
	require.NoError(t, err)
	assert.Equal(t, string(original)+"BUNDLE_PATH: \"vendor/bundle\"\n", string(after))
}

func TestBundleSetupWarnsWhenCopiedConfigIsUnreadable(t *testing.T) {
	p := newBundleProject(t, "")
	original, err := os.ReadFile(p.config)
	require.NoError(t, err)
	res, errOut := bundleSetup(t, p.caPath)
	appConfig := lookupEnv(res.env, "BUNDLE_APP_CONFIG")
	require.NotEmpty(t, appConfig)
	require.NoError(t, os.Remove(filepath.Join(appConfig, "config")))

	res.stop()

	assert.Contains(t, errOut.String(), "could not save Bundler's changes to "+p.config)
	after, err := os.ReadFile(p.config)
	require.NoError(t, err)
	assert.Equal(t, original, after, "the user's config must be left as it was")
	assert.NoDirExists(t, appConfig)
}

func TestBundleSetupWarnsWhenCopiedConfigDirIsGone(t *testing.T) {
	p := newBundleProject(t, "")
	original, err := os.ReadFile(p.config)
	require.NoError(t, err)
	res, errOut := bundleSetup(t, p.caPath)
	appConfig := lookupEnv(res.env, "BUNDLE_APP_CONFIG")
	require.NotEmpty(t, appConfig)
	require.NoError(t, os.RemoveAll(appConfig))

	res.stop()

	assert.Contains(t, errOut.String(), "could not check "+appConfig+" for files Bundler added")
	after, err := os.ReadFile(p.config)
	require.NoError(t, err)
	assert.Equal(t, original, after)
	assert.NoDirExists(t, filepath.Dir(appConfig), "with nothing left to save, the temporary directory must be removed")
}

func TestBundleSetupKeepsUserConfigWhenCALineCannotBeRestored(t *testing.T) {
	p := newBundleProject(t, "")
	original, err := os.ReadFile(p.config)
	require.NoError(t, err)
	res, errOut := bundleSetup(t, p.caPath)
	appConfig := lookupEnv(res.env, "BUNDLE_APP_CONFIG")
	require.NotEmpty(t, appConfig)
	rewritten := "---\nBUNDLE_SSL_CA_CERT: \"" + generatedCACert(t, appConfig) + "\\\"\"\nBUNDLE_PATH: \"vendor/bundle\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(appConfig, "config"), []byte(rewritten), 0o600))

	res.stop()

	assert.Contains(t, errOut.String(), "could not restore BUNDLE_SSL_CA_CERT")
	after, err := os.ReadFile(p.config)
	require.NoError(t, err)
	assert.Equal(t, original, after, "the user's config must not name glab's temporary CA file")
}

func TestBundleSetupKeepsEntriesBundlerCreates(t *testing.T) {
	p := newBundleProject(t, "")
	res, errOut := bundleSetup(t, p.caPath)
	appConfig := lookupEnv(res.env, "BUNDLE_APP_CONFIG")
	require.NotEmpty(t, appConfig)
	require.NoError(t, os.MkdirAll(filepath.Join(appConfig, "created"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(appConfig, "created", "index"), []byte("x"), 0o600))

	res.stop()

	assert.Empty(t, errOut.String())
	assert.FileExists(t, filepath.Join(p.root, ".bundle", "created", "index"), "an entry Bundler added during the run must be kept")
	assert.NoDirExists(t, appConfig)
}

func TestBundleSetupWarnsWhenACreatedEntryCannotBeSaved(t *testing.T) {
	p := newBundleProject(t, "")
	res, errOut := bundleSetup(t, p.caPath)
	appConfig := lookupEnv(res.env, "BUNDLE_APP_CONFIG")
	require.NotEmpty(t, appConfig)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(appConfig)) })
	require.NoError(t, os.MkdirAll(filepath.Join(appConfig, "created", "new"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(p.root, ".bundle", "created", "existing"), 0o700))

	res.stop()

	left := filepath.Join(appConfig, "created")
	assert.Contains(t, errOut.String(), "could not save created")
	assert.Contains(t, errOut.String(), "it is still in "+left)
	assert.DirExists(t, filepath.Join(left, "new"), "the only copy of what Bundler added must be kept")
	assert.DirExists(t, filepath.Join(p.root, ".bundle", "created", "existing"), "an existing entry must not be replaced")
}

func TestCopyEntryCopiesFilesAndDirectories(t *testing.T) {
	t.Parallel()
	src, dst := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "plugin", "gems"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(src, "plugin", "index"), []byte("idx"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "note"), []byte("n"), 0o700))

	require.NoError(t, copyEntry(filepath.Join(src, "plugin"), filepath.Join(dst, "plugin")))
	require.NoError(t, copyEntry(filepath.Join(src, "note"), filepath.Join(dst, "note")))

	index, err := os.ReadFile(filepath.Join(dst, "plugin", "index"))
	require.NoError(t, err)
	assert.Equal(t, "idx", string(index))
	assert.DirExists(t, filepath.Join(dst, "plugin", "gems"))
	srcInfo, err := os.Stat(filepath.Join(src, "note"))
	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(dst, "note"))
	require.NoError(t, err)
	assert.Equal(t, srcInfo.Mode().Perm(), info.Mode().Perm(), "the copy must keep the file's mode")
	note, err := os.ReadFile(filepath.Join(dst, "note"))
	require.NoError(t, err)
	assert.Equal(t, "n", string(note))
}

func TestCopyEntryRefusesAnExistingTarget(t *testing.T) {
	t.Parallel()
	src, dst := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "note"), []byte("new"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dst, "note"), []byte("old"), 0o600))

	err := copyEntry(filepath.Join(src, "note"), filepath.Join(dst, "note"))

	require.ErrorIs(t, err, fs.ErrExist)
	kept, err := os.ReadFile(filepath.Join(dst, "note"))
	require.NoError(t, err)
	assert.Equal(t, "old", string(kept))
}

func TestBundleSetupFindsLocalConfig(t *testing.T) {
	cases := []struct {
		name   string
		parent func(p bundleProject) []string
		moveTo string
	}{
		{name: "from a subdirectory"},
		{
			name:   "BUNDLE_GEMFILE",
			parent: func(p bundleProject) []string { return []string{"BUNDLE_GEMFILE=" + filepath.Join("..", "Gemfile")} },
		},
		{
			name:   "absolute BUNDLE_APP_CONFIG",
			parent: func(p bundleProject) []string { return []string{"BUNDLE_APP_CONFIG=" + filepath.Join(p.root, "conf")} },
			moveTo: "conf",
		},
		{
			name:   "relative BUNDLE_APP_CONFIG",
			parent: func(bundleProject) []string { return []string{"BUNDLE_APP_CONFIG=conf"} },
			moveTo: "conf",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newBundleProject(t, "")
			var parent []string
			if c.parent != nil {
				parent = c.parent(p)
			}
			if c.moveTo != "" {
				require.NoError(t, os.Rename(filepath.Join(p.root, ".bundle"), filepath.Join(p.root, c.moveTo)))
			}
			res, _ := bundleSetup(t, p.caPath, parent...)
			t.Cleanup(res.stop)
			appConfig := lookupEnv(res.env, "BUNDLE_APP_CONFIG")
			require.NotEmpty(t, appConfig)
			caPEM, err := os.ReadFile(generatedCACert(t, appConfig))
			require.NoError(t, err)
			assert.True(t, bytes.HasPrefix(caPEM, p.userPEM))
		})
	}
}

func TestBundleSetupLeavesOtherConfigsAlone(t *testing.T) {
	cases := []struct {
		name   string
		prep   func(p bundleProject) error
		parent []string
	}{
		{
			name: "no CA in the local config",
			prep: func(p bundleProject) error {
				return os.WriteFile(p.config, []byte("---\nBUNDLE_JOBS: \"3\"\n"), 0o600)
			},
		},
		{
			name: "no local config",
			prep: func(p bundleProject) error { return os.Remove(p.config) },
		},
		{
			name: "no Gemfile",
			prep: func(p bundleProject) error { return os.Remove(filepath.Join(p.root, "Gemfile")) },
		},
		{
			name:   "BUNDLE_APP_CONFIG elsewhere",
			parent: []string{"BUNDLE_APP_CONFIG=elsewhere"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newBundleProject(t, "")
			if c.prep != nil {
				require.NoError(t, c.prep(p))
			}
			res, _ := bundleSetup(t, p.caPath, c.parent...)
			t.Cleanup(res.stop)
			assert.Empty(t, lookupEnv(res.env, "BUNDLE_APP_CONFIG"))
		})
	}
}

func TestBundleSetupWarnsWhenLocalCACertIsUnreadable(t *testing.T) {
	p := newBundleProject(t, "")
	require.NoError(t, os.Remove(filepath.Join(p.root, "corp-ca.pem")))

	res, errOut := bundleSetup(t, p.caPath)
	t.Cleanup(res.stop)

	assert.Contains(t, errOut.String(), "could not read BUNDLE_SSL_CA_CERT")
	caPEM, err := os.ReadFile(generatedCACert(t, lookupEnv(res.env, "BUNDLE_APP_CONFIG")))
	require.NoError(t, err)
	assert.Equal(t, p.proxyPEM, caPEM, "the proxy must still be trusted")
}

func TestRunTrustsProxyDespiteLocalBundleConfig(t *testing.T) {
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GLAB_DF_FAKE_DEFAULT", "allow")
	for _, k := range []string{"BUNDLE_APP_CONFIG", "BUNDLE_GEMFILE"} {
		t.Setenv(k, "")
		require.NoError(t, os.Unsetenv(k))
	}
	p := newBundleProject(t, "")
	ios, _, _, _ := cmdtest.TestIOStreams()
	exec := &fakeExecutor{}
	var caPEM, bundle []byte
	exec.onExec = func() {
		caPEM, _ = os.ReadFile(generatedCACert(t, lookupEnv(exec.env, "BUNDLE_APP_CONFIG")))
		bundle, _ = os.ReadFile(lookupEnv(exec.env, "BUNDLE_SSL_CA_CERT"))
	}

	require.NoError(t, Run(t.Context(), Bundle(), RunOptions{
		IO: ios, Executor: exec, BaseDir: t.TempDir(), ProjectID: "group/proj", Args: []string{"install"},
	}))

	require.NotEmpty(t, bundle)
	assert.True(t, bytes.HasSuffix(caPEM, bundle), "the local config must name a file that holds glab's bundle")
	assert.True(t, bytes.HasPrefix(caPEM, p.userPEM))
}
