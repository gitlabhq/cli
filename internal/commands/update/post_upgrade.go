package update

import (
	"fmt"
	"strings"

	"github.com/hashicorp/go-version"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const LastSeenVersionKey = "last_seen_version"

// MaybeShowPostUpgradeBanner prints a one-time nudge pointing at
// `glab whatsnew` whenever buildInfo.Version is newer than the recorded
// LastSeenVersionKey marker. The default for LastSeenVersionKey is seeded
// in defaultFor() so existing users see the banner the first time they
// run the release that ships this feature. It stays silent in CI and when
// stderr is not a terminal, where a fresh config would otherwise print it on
// every run.
func MaybeShowPostUpgradeBanner(io *iostreams.IOStreams, cfg config.Config, buildInfo api.BuildInfo) {
	maybeShowPostUpgradeBanner(io, cfg, buildInfo, config.InCI())
}

func maybeShowPostUpgradeBanner(io *iostreams.IOStreams, cfg config.Config, buildInfo api.BuildInfo, inCI bool) {
	if !isWhatsNewEnabled(cfg) {
		return
	}
	if buildInfo.CodingAgent != "" || inCI || !io.IsErrTTY {
		return
	}

	currentVersion := strings.TrimSpace(buildInfo.Version)
	current, err := version.NewVersion(currentVersion)
	if err != nil {
		dbg.Debugf("post-upgrade banner: skipping unparseable buildInfo.Version %q: %s", currentVersion, err)
		return
	}

	lastSeen, err := cfg.Get("", LastSeenVersionKey)
	if err != nil {
		return
	}
	lastSeen = strings.TrimSpace(lastSeen)

	seen, err := version.NewVersion(lastSeen)
	if err != nil || !current.GreaterThan(seen) {
		return
	}

	c := io.Color()
	// Leading blank line separates the banner from whatever the user's
	// actual command printed, so e.g. `glab mr list` output and the nudge
	// don't run together.
	io.LogError("")
	io.LogError(c.Yellow(fmt.Sprintf("What's new in glab %s", currentVersion)))
	io.LogError("  Run: glab whatsnew")

	// An upgrade is the only moment a new bundled-skill payload becomes
	// available, so the skill check piggybacks on the banner.
	writeSkillUpdateBlock(io, bundledSkillUpdates(cfg))

	if err := SetLastSeenVersion(cfg, currentVersion); err != nil {
		// Best-effort: if we can't persist, the banner repeats next run —
		// don't disrupt the user's actual command for it.
		io.LogErrorf("  (could not update %s: %s)\n", LastSeenVersionKey, err)
	}
}

func SetLastSeenVersion(cfg config.Config, v string) error {
	if err := cfg.Set("", LastSeenVersionKey, v); err != nil {
		return err
	}
	return cfg.Write()
}

func isWhatsNewEnabled(cfg config.Config) bool {
	// cfg.Get already consults GLAB_SHOW_WHATS_NEW via EnvKeyEquivalence.
	val, err := cfg.Get("", "show_whats_new")
	if err != nil || val == "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "false", "no", "n", "0":
		return false
	}
	return true
}
