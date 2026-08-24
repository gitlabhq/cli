package binarymgr

import (
	"fmt"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func consentRequiredError(spec Spec, keySuffix, action string) error {
	env := config.EnvKeyEquivalence(spec.configKey(keySuffix))[0]
	if config.InCI() {
		return fmt.Errorf("cannot %s in CI without confirmation.\nSet %s=true to allow it, or pass --yes", action, env)
	}
	return fmt.Errorf("cannot %s without a terminal.\nPass --yes to confirm, or set %s=true", action, env)
}
