// Package binaries holds the Spec for each binary glab manages, so command
// packages and glab check-update share one definition.
package binaries

import "gitlab.com/gitlab-org/cli/internal/binarymgr"

// All returns every managed binary, in the order glab check-update reports them.
func All() []binarymgr.Spec {
	return []binarymgr.Spec{DuoCLI(), Orbit()}
}
