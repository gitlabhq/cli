// Package securityutils holds help text shared by the security commands.
package securityutils

import "github.com/MakeNowJust/heredoc/v2"

// ProfileHelp lists the identifiers accepted by enable and disable.
var ProfileHelp = heredoc.Docf(`
	For %[1]s<profile>%[1]s, use one of the following identifiers:

	- %[1]ssecret_detection%[1]s: Secret detection.
	- %[1]ssast%[1]s: SAST.
	- %[1]sdependency_scanning%[1]s: Dependency scanning.
	- %[1]sdependency_scanning_post_processing%[1]s: Dependency scanning auto-remediation.
	- %[1]sbusiness_logic%[1]s: Business logic.
	- %[1]sskillguard%[1]s: SkillGuard.
	- %[1]striage_and_remediation_conservative%[1]s: Automated triage and remediation, Conservative preset.
	- %[1]striage_and_remediation_standard%[1]s: Automated triage and remediation, Standard preset.
	- %[1]striage_and_remediation_proactive%[1]s: Automated triage and remediation, Proactive preset.
	- For a custom profile, the numeric part of the profile ID. For example,
	  %[1]s123%[1]s for %[1]sgid://gitlab/Security::ScanProfile/123%[1]s.
`, "`")

// ScanTypeHelp lists the scan types accepted by status.
var ScanTypeHelp = heredoc.Docf(`
	For %[1]s<profile>%[1]s, use one of the following scan types:

	- %[1]ssecret_detection%[1]s: Secret detection.
	- %[1]ssast%[1]s: SAST.
	- %[1]scontainer_scanning%[1]s: Container scanning.
	- %[1]sdependency_scanning%[1]s: Dependency scanning.
	- %[1]sdependency_scanning_post_processing%[1]s: Dependency scanning auto-remediation.
	- %[1]sbusiness_logic%[1]s: Business logic.
	- %[1]sskillguard%[1]s: SkillGuard.
	- %[1]striage_and_remediation%[1]s: Automated triage and remediation, any preset.
`, "`")
