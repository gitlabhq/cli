package serviceaccountutils

import (
	"context"
	"fmt"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

// ListTokens returns every personal access token of a service account, or only
// the active ones.
func ListTokens(ctx context.Context, client *gitlab.Client, scope Scope, accountID int64, active bool) ([]*gitlab.PersonalAccessToken, error) {
	var state *string
	if active {
		state = new("active")
	}
	return listTokens(ctx, client, scope, accountID, state, nil)
}

func listTokens(ctx context.Context, client *gitlab.Client, scope Scope, accountID int64, state, search *string) ([]*gitlab.PersonalAccessToken, error) {
	if scope.Group != "" {
		opts := &gitlab.ListServiceAccountPersonalAccessTokensOptions{
			ListOptions: gitlab.ListOptions{PerPage: perPage},
			State:       state,
			Search:      search,
		}
		return gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.PersonalAccessToken, *gitlab.Response, error) {
			return client.Groups.ListServiceAccountPersonalAccessTokens(scope.Group, accountID, opts, p, gitlab.WithContext(ctx))
		})
	}

	opts := &gitlab.ListProjectServiceAccountPersonalAccessTokensOptions{
		ListOptions: gitlab.ListOptions{PerPage: perPage},
		State:       state,
		Search:      search,
	}
	return gitlab.ScanAndCollect(func(p gitlab.PaginationOptionFunc) ([]*gitlab.PersonalAccessToken, *gitlab.Response, error) {
		return client.Projects.ListProjectServiceAccountPersonalAccessTokens(scope.Project, accountID, opts, p, gitlab.WithContext(ctx))
	})
}

// ResolveTokenID returns the ID of the service account token a <token-id|name>
// argument names. Only a name costs a request, and it matches active tokens
// only, so a rotated or revoked token of the same name is not a candidate.
func ResolveTokenID(ctx context.Context, client *gitlab.Client, scope Scope, accountID int64, ref string) (int64, error) {
	id, isID, err := parseID(ref, "token")
	if err != nil || isID {
		return id, err
	}

	// search is a partial, case-insensitive match, so the exact name is checked here.
	tokens, err := listTokens(ctx, client, scope, accountID, new("active"), &ref)
	if err != nil {
		return 0, err
	}

	var matches []int64
	for _, t := range tokens {
		if t.Name == ref {
			matches = append(matches, t.ID)
		}
	}

	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("no active token named %q for service account %d in %s", ref, accountID, scope)
	case 1:
		return matches[0], nil
	default:
		return 0, fmt.Errorf("multiple active tokens named %q for service account %d; use the token ID instead", ref, accountID)
	}
}

// PrintToken prints the details of a newly created or rotated token to stderr
// and only its value to stdout, so a script can capture the value alone. The
// stdout write error is returned, not dropped as io.LogInfo would, so a failed
// write cannot leave a script with an empty value and exit code 0.
func PrintToken(io *iostreams.IOStreams, t *gitlab.PersonalAccessToken) error {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), t.ID)
	table.AddRow(c.Bold("Name"), t.Name)
	table.AddRow(c.Bold("Description"), OrDash(t.Description))
	table.AddRow(c.Bold("Scopes"), FormatScopes(t.Scopes))
	table.AddRow(c.Bold("Expires At"), FormatDate(t.ExpiresAt))
	io.LogErrorf("%s", table.Render())
	_, err := fmt.Fprintln(io.StdOut, t.Token) //nolint:forbidigo // write error must propagate to the caller
	return err
}

// FormatScopes renders token scopes as a comma-separated list, or "-".
func FormatScopes(scopes []string) string {
	if len(scopes) == 0 {
		return "-"
	}
	return strings.Join(scopes, ", ")
}

// FormatDate renders a token's expiry date, or "-" for a token that does not
// expire.
func FormatDate(d *gitlab.ISOTime) string {
	if d == nil {
		return "-"
	}
	return d.String()
}

// FormatTime renders a timestamp such as a token's last use, or "-".
func FormatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
