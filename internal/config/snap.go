package config

import "os"

// SnapConnectCommand grants the glab snap access to the OS keyring.
const SnapConnectCommand = "sudo snap connect glab:password-manager-service"

// snapName matches the `name:` field in snap/snapcraft.yaml. snapd sets
// SNAP_NAME to this value for every confined glab process, and to a different
// value when glab merely inherits env from a parent snap (for example, the
// VS Code snap's integrated terminal). Checking the name — not just SNAP —
// prevents a false-positive hint from firing outside the glab snap.
const snapName = "glab"

// SnapConfined reports whether glab is running as the confined snap.
func SnapConfined() bool {
	return os.Getenv("SNAP_NAME") == snapName
}
