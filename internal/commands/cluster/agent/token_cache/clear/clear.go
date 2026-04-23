package clear

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	agentutils "gitlab.com/gitlab-org/cli/internal/commands/cluster/agent/agentutils"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const keyringService = "glab"

type options struct {
	gitlabClient func() (*gitlab.Client, error)
	io           *iostreams.IOStreams

	filesystem bool
	keyring    bool
	revoke     bool
	agents     []int64
	repo       string
}

type cachedToken = agentutils.CachedToken

//go:embed long.md
var longHelp string

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := options{
		io:           f.IO(),
		gitlabClient: f.GitLabClient,
	}

	cmd := &cobra.Command{
		Use:   "clear [flags]",
		Short: "Clear cached GitLab Agent tokens.",
		Long:  longHelp,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run(cmd.Context())
		},
	}

	fl := cmd.Flags()
	fl.BoolVar(&opts.filesystem, "filesystem", true, "Clear tokens from filesystem cache.")
	fl.BoolVar(&opts.keyring, "keyring", true, "Clear tokens from keyring cache.")
	fl.BoolVar(&opts.revoke, "revoke", true, "Revoke tokens on GitLab server before clearing cache.")
	fl.Int64SliceVar(&opts.agents, "agent", nil, "Clear tokens for specific agent IDs only.")
	fl.StringVarP(&opts.repo, "repo", "R", "", "Select another repository using the OWNER/REPO format.")

	cmdutils.EnableRepoOverride(cmd, f)

	return cmd
}

func (o *options) run(ctx context.Context) error {
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
		fmt.Fprintln(o.io.StdOut, "No cached tokens found to clear.")
		for _, err := range errors {
			fmt.Fprintf(o.io.StdErr, "Warning: %v\n", err)
		}
		return nil
	}

	for _, err := range errors {
		fmt.Fprintf(o.io.StdErr, "Warning: %v\n", err)
	}

	fmt.Fprintf(o.io.StdOut, "Found %d cached token(s) to clear.\n", len(tokens))

	// Revoke tokens if requested
	if o.revoke {
		fmt.Fprintln(o.io.StdOut, "Revoking tokens on GitLab server...")
		revokeErrors := o.revokeTokens(ctx, tokens)
		for _, err := range revokeErrors {
			fmt.Fprintf(o.io.StdErr, "Warning: %v\n", err)
		}
	}

	// Clear tokens from cache
	fmt.Fprintln(o.io.StdOut, "Clearing tokens from cache...")
	clearErrors := o.clearTokens(tokens)
	for _, err := range clearErrors {
		fmt.Fprintf(o.io.StdErr, "Error: %v\n", err)
	}

	fmt.Fprintf(o.io.StdOut, "Successfully cleared %d token(s) from cache.\n", len(tokens))
	return nil
}

func (o *options) validate() error {
	if !o.keyring && !o.filesystem {
		return fmt.Errorf("at least one cache source must be enabled (--keyring or --filesystem)")
	}
	return nil
}

func (o *options) getKeyringTokens() ([]cachedToken, error) {
	// Unfortunately, the keyring library doesn't provide a way to list all keys
	// We would need to know the agent IDs to construct the cache keys
	// For now, we'll return an empty list and suggest using --agent flag
	if len(o.agents) == 0 {
		return nil, fmt.Errorf("keyring token clearing requires --agent flag to specify agent IDs")
	}

	client, err := o.gitlabClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create GitLab client: %w", err)
	}

	gitlabInstance := base64.StdEncoding.EncodeToString([]byte(client.BaseURL().String()))

	var tokens []cachedToken
	for _, agentID := range o.agents {
		cacheID := fmt.Sprintf("%s-%d", gitlabInstance, agentID)

		data, err := keyring.Get(keyringService, cacheID)
		if err != nil {
			if errors.Is(err, keyring.ErrNotFound) {
				continue // Token not found in keyring, skip
			}
			return nil, fmt.Errorf("failed to get token from keyring for agent %d: %w", agentID, err)
		}

		var pat gitlab.PersonalAccessToken
		if err := json.Unmarshal([]byte(data), &pat); err != nil {
			return nil, fmt.Errorf("failed to unmarshal token for agent %d: %w", agentID, err)
		}

		token := cachedToken{
			ID:        cacheID,
			AgentID:   agentID,
			GitLabURL: client.BaseURL().String(),
			Token:     &pat,
			Source:    "keyring",
		}

		tokens = append(tokens, token)
	}

	return tokens, nil
}

func (o *options) getFilesystemTokens() ([]cachedToken, error) {
	var tokens []cachedToken

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}

	gitlabCacheDir := filepath.Join(cacheDir, "gitlab")

	// Open the cache directory with os.OpenRoot for traversal-safe access
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

		token, err := o.readTokenFromFile(root, entry.Name())
		if err != nil {
			fmt.Fprintf(o.io.StdErr, "Warning: Failed to read token from %s: %v\n", entry.Name(), err)
			continue
		}
		tokens = append(tokens, *token)
	}

	return tokens, nil
}

func (o *options) readTokenFromFile(root *os.Root, filename string) (*cachedToken, error) {
	file, err := root.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := file.Close(); err == nil {
			err = cerr
		}
	}()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	var pat gitlab.PersonalAccessToken
	if err := json.Unmarshal(data, &pat); err != nil {
		return nil, err
	}

	// Parse cache ID (filename) to extract GitLab URL and agent ID
	gitlabURL, agentID, err := agentutils.ParseCacheID(filename)
	if err != nil {
		return nil, err
	}

	// Get the absolute path for FilePath field (needed for deletion)
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	absPath := filepath.Join(cacheDir, "gitlab", filename)

	token := &cachedToken{
		ID:        filename,
		AgentID:   agentID,
		GitLabURL: gitlabURL,
		Token:     &pat,
		Source:    "filesystem",
		FilePath:  absPath,
	}

	return token, nil
}

func (o *options) revokeTokens(ctx context.Context, tokens []cachedToken) []error {
	client, err := o.gitlabClient()
	if err != nil {
		return []error{fmt.Errorf("failed to create GitLab client: %w", err)}
	}

	var errors []error
	for _, token := range tokens {
		// Skip already revoked tokens
		if token.Token.Revoked {
			fmt.Fprintf(o.io.StdOut, "Token for agent %d is already revoked, skipping.\n", token.AgentID)
			continue
		}

		// Skip expired tokens (they're effectively revoked)
		if token.Token.ExpiresAt != nil && time.Time(*token.Token.ExpiresAt).Before(time.Now().UTC()) {
			fmt.Fprintf(o.io.StdOut, "Token for agent %d is expired, skipping revocation.\n", token.AgentID)
			continue
		}

		fmt.Fprintf(o.io.StdOut, "Revoking token for agent %d...\n", token.AgentID)

		_, err := client.PersonalAccessTokens.RevokePersonalAccessTokenByID(token.Token.ID, gitlab.WithContext(ctx))
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to revoke token for agent %d: %w", token.AgentID, err))
			continue
		}

		fmt.Fprintf(o.io.StdOut, "Successfully revoked token for agent %d.\n", token.AgentID)
	}

	return errors
}

func (o *options) clearTokens(tokens []cachedToken) []error {
	var errs []error

	// Group filesystem tokens to use a single os.OpenRoot
	var fsTokens []cachedToken
	for _, token := range tokens {
		if token.Source != "filesystem" {
			continue
		}
		fsTokens = append(fsTokens, token)
	}

	// Clear filesystem tokens with os.OpenRoot
	if len(fsTokens) > 0 {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get cache directory: %w", err))
		} else {
			gitlabCacheDir := filepath.Join(cacheDir, "gitlab")
			root, err := os.OpenRoot(gitlabCacheDir)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to open cache directory: %w", err))
			} else {
				defer func() {
					if cerr := root.Close(); cerr != nil {
						errs = append(errs, fmt.Errorf("failed to close cache directory: %w", cerr))
					}
				}()

				for _, token := range fsTokens {
					err := root.Remove(token.ID)
					if err != nil && !os.IsNotExist(err) {
						errs = append(errs, fmt.Errorf("failed to delete token file for agent %d: %w", token.AgentID, err))
						continue
					}
					fmt.Fprintf(o.io.StdOut, "Cleared token for agent %d from filesystem.\n", token.AgentID)
				}
			}
		}
	}

	// Clear keyring tokens
	for _, token := range tokens {
		if token.Source != "keyring" {
			continue
		}
		err := keyring.Delete(keyringService, token.ID)
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			errs = append(errs, fmt.Errorf("failed to delete token from keyring for agent %d: %w", token.AgentID, err))
			continue
		}
		fmt.Fprintf(o.io.StdOut, "Cleared token for agent %d from keyring.\n", token.AgentID)
	}

	return errs
}
