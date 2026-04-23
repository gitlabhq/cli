package list

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	agentutils "gitlab.com/gitlab-org/cli/internal/commands/cluster/agent/agentutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

type options struct {
	io *iostreams.IOStreams

	filesystem bool
	keyring    bool
	agents     []int64
	repo       string
}

type cachedToken = agentutils.CachedToken

//go:embed long.md
var longHelp string

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := options{
		io: f.IO(),
	}

	cmd := &cobra.Command{
		Use:   "list [flags]",
		Short: "List cached GitLab Agent tokens.",
		Long:  longHelp,
		Annotations: map[string]string{
			mcpannotations.Exclude: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run()
		},
	}

	fl := cmd.Flags()
	fl.BoolVar(&opts.filesystem, "filesystem", true, "Include tokens from filesystem cache.")
	fl.BoolVar(&opts.keyring, "keyring", true, "Include tokens from keyring cache.")
	fl.Int64SliceVar(&opts.agents, "agent", nil, "Filter by specific agent IDs.")
	fl.StringVarP(&opts.repo, "repo", "R", "", "Select another repository using the OWNER/REPO format.")

	cmdutils.EnableRepoOverride(cmd, f)

	return cmd
}

func (o *options) run() error {
	var tokens []cachedToken
	var errors []error

	if o.keyring {
		keyringTokens, err := o.getKeyringTokens()
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to read keyring tokens: %w", err))
		} else {
			tokens = append(tokens, keyringTokens...)
		}
	}

	if o.filesystem {
		fsTokens, err := o.getFilesystemTokens()
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to read filesystem tokens: %w", err))
		} else {
			tokens = append(tokens, fsTokens...)
		}
	}

	// Filter by agent IDs if specified
	tokens = agentutils.FilterByAgents(tokens, o.agents)

	if len(tokens) == 0 {
		fmt.Fprintln(o.io.StdOut, "No cached tokens found.")
		for _, err := range errors {
			fmt.Fprintf(o.io.StdErr, "Warning: %v\n", err)
		}
		return nil
	}

	for _, err := range errors {
		fmt.Fprintf(o.io.StdErr, "Warning: %v\n", err)
	}

	o.displayTokens(tokens)
	return nil
}

func (o *options) getKeyringTokens() ([]cachedToken, error) {
	// Unfortunately, the keyring library doesn't provide a way to list all keys
	// We would need to know the agent IDs to construct the cache keys
	// For now, we'll return an empty list and suggest using --agent flag
	return nil, fmt.Errorf("keyring token listing requires --agent flag to specify agent IDs")
}

func (o *options) getFilesystemTokens() ([]cachedToken, error) {
	var tokens []cachedToken

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}

	gitlabCacheDir := filepath.Join(cacheDir, "gitlab")

	// Use traversal-safe root to confine all file operations within the cache directory
	root, err := os.OpenRoot(gitlabCacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No cache directory exists
		}
		return nil, err
	}
	defer func() {
		if cerr := root.Close(); err == nil {
			err = cerr
		}
	}()

	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := dir.Close(); err == nil {
			err = cerr
		}
	}()

	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		token, err := o.readTokenFromRoot(root, entry.Name())
		if err != nil {
			fmt.Fprintf(o.io.StdErr, "Warning: Failed to read token from %s: %v\n", entry.Name(), err)
			continue
		}

		tokens = append(tokens, *token)
	}

	return tokens, nil
}

// readTokenFromRoot reads a token file by name from a confined root
func (o *options) readTokenFromRoot(root *os.Root, name string) (*cachedToken, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	var pat gitlab.PersonalAccessToken
	if err := json.Unmarshal(data, &pat); err != nil {
		return nil, err
	}

	// cache ID is simply the filename in this confined root
	cacheID := name

	// Parse cache ID to extract GitLab URL and agent ID
	gitlabURL, agentID, err := agentutils.ParseCacheID(cacheID)
	if err != nil {
		return nil, err
	}

	token := &cachedToken{
		ID:        cacheID,
		AgentID:   agentID,
		GitLabURL: gitlabURL,
		Token:     &pat,
		Source:    "filesystem",
		Expired:   pat.ExpiresAt != nil && time.Time(*pat.ExpiresAt).Before(time.Now().UTC()),
		Revoked:   pat.Revoked,
	}

	return token, nil
}

func (o *options) displayTokens(tokens []cachedToken) {
	tp := tableprinter.NewTablePrinter()
	tp.AddRow("Agent ID", "GitLab URL", "Token Name", "Source", "Expires At", "Status")

	for _, token := range tokens {
		expiresAt := "Never"
		if token.Token.ExpiresAt != nil {
			expiresAt = time.Time(*token.Token.ExpiresAt).Format(time.RFC3339)
		}

		status := "Valid"
		if token.Expired {
			status = "Expired"
		} else if token.Revoked {
			status = "Revoked"
		}

		tp.AddRow(token.AgentID, token.GitLabURL, token.Token.Name, token.Source, expiresAt, status)
	}

	fmt.Fprint(o.io.StdOut, tp.Render())
}
