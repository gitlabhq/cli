package verdict

import "fmt"

type Verdict string

const (
	// Allowed is the zero-value verdict: the coordinate is permitted. It is
	// the empty string so an unset Verdict reads as "allowed".
	Allowed Verdict = ""
	Blocked Verdict = "blocked"
	Warning Verdict = "warning"
)

// BlockedExitCode is the process exit code a Dependency Firewall command
// returns when the outcome is a block. It is distinct from the generic-error 1
// and cobra's misuse 2, so callers and scripts can tell "blocked by policy"
// apart from "the command failed". Defined here, the one package every df
// command already imports, so the package and ci-summary commands share a
// single definition and cannot drift.
const BlockedExitCode = 3

type Entry struct {
	Package   string  `json:"package"`
	Version   string  `json:"version,omitempty"`
	Verdict   Verdict `json:"verdict"`
	Reason    string  `json:"reason,omitempty"`
	Status    int     `json:"status,omitempty"`
	Timestamp string  `json:"timestamp,omitempty"`
}

func (e Entry) Key() string {
	return fmt.Sprintf("%s@%s:%s", e.Package, e.Version, e.Verdict)
}
