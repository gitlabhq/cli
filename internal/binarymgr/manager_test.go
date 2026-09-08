//go:build !integration

package binarymgr

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// noMorePages returns a response that signals gitlab.ScanAndCollect to stop.
func noMorePages() *gitlab.Response {
	return &gitlab.Response{NextPage: 0}
}

// testSpec is a minimal Spec used by manager-level tests. The duo and orbit
// command packages have their own integration coverage of full specs.
func testSpec() Spec {
	return Spec{
		DisplayName:        "Test CLI",
		ProjectID:          "12345",
		PackageName:        "test-cli",
		ConfigPrefix:       "test_cli",
		EnvVarPrefix:       "GLAB_TEST_CLI",
		MaxCompatibleMajor: 8,
		SupportedOS:        []string{"darwin", "linux", "windows"},
		NormalizeArch: func(goos, goarch string) (string, error) {
			switch goarch {
			case "amd64":
				return "x64", nil
			case "arm64":
				return "arm64", nil
			}
			return "", ErrUnsupportedPlatform
		},
		AssetName:     func(os, arch string) string { return "test-" + os + "-" + arch },
		InstalledName: func(os string) string { return "test" },
	}
}

func TestManager_isBinaryValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setupFile func(t *testing.T) string
		want      bool
	}{
		{
			name: "valid executable file",
			setupFile: func(t *testing.T) string {
				t.Helper()
				p := filepath.Join(t.TempDir(), "bin")
				require.NoError(t, os.WriteFile(p, []byte("fake binary"), 0o755))
				return p
			},
			want: true,
		},
		{
			name: "non-executable file",
			setupFile: func(t *testing.T) string {
				t.Helper()
				p := filepath.Join(t.TempDir(), "bin")
				require.NoError(t, os.WriteFile(p, []byte("fake binary"), 0o644))
				return p
			},
			want: false,
		},
		{
			name: "non-existent file",
			setupFile: func(t *testing.T) string {
				t.Helper()
				return "/nonexistent/bin"
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
			m := NewManager(ios, testSpec())
			assert.Equal(t, tc.want, m.isBinaryValid(tc.setupFile(t)))
		})
	}
}

func TestManager_verifyChecksum(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.bin")
	testContent := []byte("test content for checksum")
	require.NoError(t, os.WriteFile(testFile, testContent, 0o644))

	hash := sha256.New()
	hash.Write(testContent)
	expected := hex.EncodeToString(hash.Sum(nil))

	tests := []struct {
		name             string
		filePath         string
		expectedChecksum string
		wantsErr         bool
	}{
		{"valid checksum", testFile, expected, false},
		{"invalid checksum", testFile, "invalid_checksum", true},
		{"empty checksum (skipped)", testFile, "", false},
		{"non-existent file", "/nonexistent/file", expected, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
			m := NewManager(ios, testSpec())
			err := m.verifyChecksum(tc.filePath, tc.expectedChecksum)
			if tc.wantsErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestManager_CheckForUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		latestVersion       string
		currentVersion      string
		spec                Spec
		wantHasUpdate       bool
		wantLatestVersion   string
		wantNewMajorVersion string
	}{
		{
			name:              "compatible major, newer version available",
			latestVersion:     "8.5.0",
			currentVersion:    "8.0.0",
			spec:              testSpec(),
			wantHasUpdate:     true,
			wantLatestVersion: "8.5.0",
		},
		{
			name:              "compatible major, already up to date",
			latestVersion:     "8.5.0",
			currentVersion:    "8.5.0",
			spec:              testSpec(),
			wantHasUpdate:     false,
			wantLatestVersion: "8.5.0",
		},
		{
			name:                "incompatible major, update blocked",
			latestVersion:       "9.0.0",
			currentVersion:      "8.5.0",
			spec:                testSpec(),
			wantHasUpdate:       false,
			wantNewMajorVersion: "9.0.0",
		},
		{
			name:           "no major cap, newer major still treated as available",
			latestVersion:  "9.0.0",
			currentVersion: "8.5.0",
			spec: func() Spec {
				s := testSpec()
				s.MaxCompatibleMajor = 0
				return s
			}(),
			wantHasUpdate:     true,
			wantLatestVersion: "9.0.0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.com"))
			testClient.MockPackages.EXPECT().
				ListProjectPackages(tc.spec.ProjectID, gomock.Any(), gomock.Any(), gomock.Any()).
				Return([]*gitlab.Package{{ID: 1, Version: tc.latestVersion}}, noMorePages(), nil)

			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
			m := &Manager{io: ios, spec: tc.spec, client: testClient.Client}

			check, err := m.CheckForUpdate(t.Context(), tc.currentVersion, time.Time{}, true)
			require.NoError(t, err)
			assert.Equal(t, tc.wantHasUpdate, check.HasUpdate)
			assert.Equal(t, tc.wantLatestVersion, check.LatestVersion)
			assert.Equal(t, tc.wantNewMajorVersion, check.NewMajorVersion)
		})
	}
}

func TestManager_fetchLatestPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		packages    []*gitlab.Package
		wantVersion string
		wantErr     string
	}{
		{
			name: "picks highest semver, not lexicographic (created_at order)",
			// Order the API returns with OrderBy=created_at,Sort=desc: most
			// recent first. 8.100.0 is the newest release and the highest
			// semver — lexicographically it sorts below 8.99.0.
			packages: []*gitlab.Package{
				{ID: 1, Version: "8.100.0"},
				{ID: 2, Version: "8.99.0"},
				{ID: 3, Version: "8.98.0"},
			},
			wantVersion: "8.100.0",
		},
		{
			name: "picks highest semver regardless of position",
			// Simulates a hotfix released after a higher minor: 8.99.1 is
			// most recent (position 0) but 8.100.0 is the higher semver.
			packages: []*gitlab.Package{
				{ID: 1, Version: "8.99.1"},
				{ID: 2, Version: "8.100.0"},
				{ID: 3, Version: "8.99.0"},
			},
			wantVersion: "8.100.0",
		},
		{
			name: "skips unparseable versions",
			packages: []*gitlab.Package{
				{ID: 1, Version: "latest"},
				{ID: 2, Version: "8.100.0"},
				{ID: 3, Version: "not-a-version"},
			},
			wantVersion: "8.100.0",
		},
		{
			name:     "errors when registry is empty",
			packages: []*gitlab.Package{},
			wantErr:  "no packages found",
		},
		{
			name: "errors when no version parses",
			packages: []*gitlab.Package{
				{ID: 1, Version: "latest"},
				{ID: 2, Version: "snapshot"},
			},
			wantErr: "no packages with parseable versions found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := testSpec()
			testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.com"))
			testClient.MockPackages.EXPECT().
				ListProjectPackages(spec.ProjectID, gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tc.packages, noMorePages(), nil)

			ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
			m := &Manager{io: ios, spec: spec, client: testClient.Client}

			pkg, err := m.fetchLatestPackage(t.Context())
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantVersion, pkg.Version)
		})
	}
}

func TestManager_fetchLatestPackage_paginates(t *testing.T) {
	t.Parallel()

	// Verify gitlab.ScanAndCollect walks pages: the highest semver lives on
	// page 2, so a single-page scan would miss it and return the wrong value.
	spec := testSpec()
	testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.com"))

	page1 := []*gitlab.Package{
		{ID: 1, Version: "8.99.1"},
		{ID: 2, Version: "8.99.0"},
	}
	page2 := []*gitlab.Package{
		{ID: 3, Version: "8.100.0"},
		{ID: 4, Version: "8.98.0"},
	}

	gomock.InOrder(
		testClient.MockPackages.EXPECT().
			ListProjectPackages(spec.ProjectID, gomock.Any(), gomock.Any(), gomock.Any()).
			Return(page1, &gitlab.Response{NextPage: 2}, nil),
		testClient.MockPackages.EXPECT().
			ListProjectPackages(spec.ProjectID, gomock.Any(), gomock.Any(), gomock.Any()).
			Return(page2, noMorePages(), nil),
	)

	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	m := &Manager{io: ios, spec: spec, client: testClient.Client}

	pkg, err := m.fetchLatestPackage(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "8.100.0", pkg.Version)
}

func TestManager_fetchPackageAsset_majorVersionBlocked(t *testing.T) {
	t.Parallel()

	spec := testSpec()
	testClient := gitlabtesting.NewTestClient(t, gitlab.WithBaseURL("https://gitlab.com"))
	testClient.MockPackages.EXPECT().
		ListProjectPackages(spec.ProjectID, gomock.Any(), gomock.Any(), gomock.Any()).
		Return([]*gitlab.Package{{ID: 1, Version: "9.0.0"}}, noMorePages(), nil)

	ios, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))
	m := &Manager{io: ios, spec: spec, client: testClient.Client}

	_, err := m.fetchPackageAsset(t.Context(), Platform{OS: "linux", Arch: "x64"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a newer glab")
}

func TestManager_validateBinaryPath(t *testing.T) {
	t.Parallel()

	spec := testSpec()

	t.Run("non-existent path", func(t *testing.T) {
		t.Parallel()
		err := validateBinaryPath("/nonexistent/path", spec)
		require.Error(t, err)
		// Error must name both configuration sources and start with a
		// lowercase word so fang's leading-token title-casing does not
		// mangle the env-var name.
		assert.Contains(t, err.Error(), "GLAB_TEST_CLI_BINARY_PATH")
		assert.Contains(t, err.Error(), "test_cli_binary_path")
		assert.Contains(t, err.Error(), "was not found")
		assert.Truef(t, strings.HasPrefix(err.Error(), "custom"), "error should start with lowercase to survive fang title-casing, got %q", err.Error())
	})

	t.Run("directory rejected", func(t *testing.T) {
		t.Parallel()
		err := validateBinaryPath(t.TempDir(), spec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is a directory")
	})

	t.Run("non-executable file rejected (unix)", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "bin")
		require.NoError(t, os.WriteFile(f, []byte("#!/bin/sh\n"), 0o644))
		err := validateBinaryPath(f, spec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not executable")
		assert.Contains(t, err.Error(), "chmod +x")
	})

	t.Run("executable file accepted", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "bin")
		require.NoError(t, os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755))
		require.NoError(t, validateBinaryPath(f, spec))
	})
}
