package cmdutils

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// SecretFile is a credential that a command reads from a file, standard
// input or an environment variable. It has no flag that takes the value
// itself, because flag values end up in shell history and the process list.
type SecretFile struct {
	flag  string
	label string
	env   string
	file  string
}

// AddSecretFileFlag registers flag on cmd. The flag names the file holding
// the secret that label describes, and origin completes the flag's help with
// where the secret comes from. Without the flag, the secret comes from the
// environment variable env, which is added to cmd's help:environment section.
func AddSecretFileFlag(cmd *cobra.Command, flag, label, env, origin string) *SecretFile {
	s := &SecretFile{flag: flag, label: label, env: env}
	cmd.Flags().StringVar(&s.file, flag, "", fmt.Sprintf("Path to a file with the %s %s. Use - to read from standard input. Defaults to the %s environment variable.", label, origin, env))
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations["help:environment"] += fmt.Sprintf("%s: the %s %s, used when --%s is not set.\n", env, label, origin, flag)
	return s
}

// IsSet reports whether the secret has a source: a file or a non-empty
// environment variable.
func (s *SecretFile) IsSet() bool {
	return s.file != "" || os.Getenv(s.env) != ""
}

// File returns the path the secret is read from, - for standard input, or ""
// if it comes from the environment variable.
func (s *SecretFile) File() string {
	return s.file
}

// Env returns the environment variable the secret falls back to.
func (s *SecretFile) Env() string {
	return s.env
}

// FromStdin reports whether the secret is read from standard input.
func (s *SecretFile) FromStdin() bool {
	return s.file == "-"
}

// Read returns the secret without surrounding whitespace.
func (s *SecretFile) Read(stdin io.Reader) (string, error) {
	var value string
	switch s.file {
	case "":
		value = os.Getenv(s.env)
		if value == "" {
			return "", &FlagError{Err: fmt.Errorf("pass --%s or set %s", s.flag, s.env)}
		}
	case "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read the %s from standard input: %w", s.label, err)
		}
		value = string(b)
	default:
		b, err := os.ReadFile(s.file)
		if err != nil {
			return "", fmt.Errorf("failed to read --%s: %w", s.flag, err)
		}
		value = string(b)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("the %s is empty", s.label)
	}
	return value, nil
}
