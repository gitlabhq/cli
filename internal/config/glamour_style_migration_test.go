//go:build !integration

package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The migration flips a seeded `glamour_style: dark` to `auto` once and never
// reverts a later explicit choice.

func TestMigrateGlamourStyleAutoDetection_FlipsSeededDarkToAuto(t *testing.T) {
	dir := t.TempDir()
	cfg := NewFromStringInDir("glamour_style: dark\n", dir)

	require.NoError(t, migrateGlamourStyleAutoDetection(cfg))

	got, _, err := cfg.GetWithSource("", "glamour_style", false)
	require.NoError(t, err)
	assert.Equal(t, "auto", got)
}

func TestMigrateGlamourStyleAutoDetection_LeavesNonDarkValuesAlone(t *testing.T) {
	for _, style := range []string{"light", "notty", "auto"} {
		t.Run(style, func(t *testing.T) {
			dir := t.TempDir()
			cfg := NewFromStringInDir("glamour_style: "+style+"\n", dir)

			require.NoError(t, migrateGlamourStyleAutoDetection(cfg))

			got, _, err := cfg.GetWithSource("", "glamour_style", false)
			require.NoError(t, err)
			assert.Equal(t, style, got)
		})
	}
}

func TestMigrateGlamourStyleAutoDetection_PersistsTheMigratedFlag(t *testing.T) {
	dir := t.TempDir()
	cfg := NewFromStringInDir("glamour_style: dark\n", dir)

	require.NoError(t, migrateGlamourStyleAutoDetection(cfg))

	assert.Contains(t, persistedFile(t, dir, "config.yml"), "glamour_style: auto")
	assert.Contains(t, persistedFile(t, dir, "config.yml"), "glamour_style_auto_migrated: \"true\"")
}

func TestMigrateGlamourStyleAutoDetection_RunsOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	cfg := NewFromStringInDir("glamour_style: dark\n", dir)

	require.NoError(t, migrateGlamourStyleAutoDetection(cfg))

	// A user who explicitly chose "dark" again after the migration ran must
	// keep that choice: the migration must never re-fire.
	require.NoError(t, cfg.Set("", "glamour_style", "dark"))
	require.NoError(t, cfg.Write())

	require.NoError(t, migrateGlamourStyleAutoDetection(cfg))

	got, _, err := cfg.GetWithSource("", "glamour_style", false)
	require.NoError(t, err)
	assert.Equal(t, "dark", got, "migration must not re-apply once glamour_style_auto_migrated is set")
}

func TestMigrateGlamourStyleAutoDetection_NoOpOnAlreadyMigratedConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := NewFromStringInDir("glamour_style: dark\nglamour_style_auto_migrated: \"true\"\n", dir)

	require.NoError(t, migrateGlamourStyleAutoDetection(cfg))

	got, _, err := cfg.GetWithSource("", "glamour_style", false)
	require.NoError(t, err)
	assert.Equal(t, "dark", got, "a config already marked migrated keeps whatever glamour_style it has")
}

// A read-only or immutable config dir (Nix-managed, containerized home) used
// to make every glab command fail, because Init() returned the write error.
func TestMigrateGlamourStyleAutoDetection_ReadOnlyConfigDirIsNotFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permissions")
	}
	dir := t.TempDir()
	cfg := NewFromStringInDir("glamour_style: dark\n", dir)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	require.NoError(t, migrateGlamourStyleAutoDetection(cfg))

	got, _, err := cfg.GetWithSource("", "glamour_style", false)
	require.NoError(t, err)
	assert.Equal(t, "auto", got, "the in-memory value still applies when it cannot be persisted")
}
