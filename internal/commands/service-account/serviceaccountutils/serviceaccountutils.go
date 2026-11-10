// Package serviceaccountutils holds the scope resolution, account lookup and
// display code shared by the service-account subcommands.
package serviceaccountutils

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
)

// Scope is the group or the project whose service accounts a command acts on.
// Exactly one of Group and Project is set.
type Scope struct {
	Group   string
	Project string
}

func (s Scope) String() string {
	if s.Group != "" {
		return "group " + s.Group
	}
	return "project " + s.Project
}

// NewClient resolves the scope and builds the API client for it. group is the
// value returned by cmdutils.GroupOverride; when it is empty the base repository
// is the scope.
func NewClient(
	baseRepo func() (glrepo.Interface, error),
	apiClient func(repoHost string) (*api.Client, error),
	group string,
) (*gitlab.Client, Scope, error) {
	// The group scope works outside a repository, so a missing base repo is only
	// fatal for the project scope. Fail before building a client that would be
	// discarded, so the caller sees why the repository was needed.
	repo, repoErr := baseRepo()
	if group == "" && repoErr != nil {
		return nil, Scope{}, repoErr
	}

	var repoHost string
	if repoErr == nil {
		repoHost = repo.RepoHost()
	}

	c, err := apiClient(repoHost)
	if err != nil {
		return nil, Scope{}, err
	}

	if group != "" {
		return c.Lab(), Scope{Group: group}, nil
	}
	return c.Lab(), Scope{Project: repo.FullName()}, nil
}

// Account is the part of a service account common to the group and the project
// API types, used for text output.
type Account struct {
	ID               int64
	Username         string
	Name             string
	Email            string
	UnconfirmedEmail string
}

// FromGroup converts a group service account to an Account.
func FromGroup(a *gitlab.GroupServiceAccount) Account {
	return Account{ID: a.ID, Username: a.UserName, Name: a.Name, Email: a.Email, UnconfirmedEmail: a.UnconfirmedEmail}
}

// FromProject converts a project service account to an Account.
func FromProject(a *gitlab.ProjectServiceAccount) Account {
	return Account{ID: a.ID, Username: a.Username, Name: a.Name, Email: a.Email, UnconfirmedEmail: a.UnconfirmedEmail}
}

// OrDash renders an empty field as "-", matching the other token and
// deploy-token tables.
func OrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// PrintDetails prints a service account as a two-column table. The unconfirmed
// email row appears only while a custom email address awaits confirmation.
func PrintDetails(io *iostreams.IOStreams, a Account) {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow(c.Bold("ID"), a.ID)
	table.AddRow(c.Bold("Username"), a.Username)
	table.AddRow(c.Bold("Name"), OrDash(a.Name))
	table.AddRow(c.Bold("Email"), OrDash(a.Email))
	if a.UnconfirmedEmail != "" {
		table.AddRow(c.Bold("Unconfirmed Email"), a.UnconfirmedEmail)
	}
	io.LogInfof("%s", table.Render())
}

const perPage = 100

func groupPages(ctx context.Context, client *gitlab.Client, group string) func(gitlab.PaginationOptionFunc) ([]*gitlab.GroupServiceAccount, *gitlab.Response, error) {
	opts := &gitlab.ListServiceAccountsOptions{ListOptions: gitlab.ListOptions{PerPage: perPage}}
	return func(p gitlab.PaginationOptionFunc) ([]*gitlab.GroupServiceAccount, *gitlab.Response, error) {
		return client.Groups.ListServiceAccounts(group, opts, p, gitlab.WithContext(ctx))
	}
}

func projectPages(ctx context.Context, client *gitlab.Client, project string) func(gitlab.PaginationOptionFunc) ([]*gitlab.ProjectServiceAccount, *gitlab.Response, error) {
	opts := &gitlab.ListProjectServiceAccountsOptions{ListOptions: gitlab.ListOptions{PerPage: perPage}}
	return func(p gitlab.PaginationOptionFunc) ([]*gitlab.ProjectServiceAccount, *gitlab.Response, error) {
		return client.Projects.ListProjectServiceAccounts(project, opts, p, gitlab.WithContext(ctx))
	}
}

// List returns every service account in the scope, both as the raw API slice
// for JSON output and as Accounts for text output.
func List(ctx context.Context, client *gitlab.Client, scope Scope) (any, []Account, error) {
	if scope.Group != "" {
		raw, err := gitlab.ScanAndCollect(groupPages(ctx, client, scope.Group))
		if err != nil {
			return nil, nil, err
		}
		accounts := make([]Account, 0, len(raw))
		for _, a := range raw {
			accounts = append(accounts, FromGroup(a))
		}
		return raw, accounts, nil
	}

	raw, err := gitlab.ScanAndCollect(projectPages(ctx, client, scope.Project))
	if err != nil {
		return nil, nil, err
	}
	accounts := make([]Account, 0, len(raw))
	for _, a := range raw {
		accounts = append(accounts, FromProject(a))
	}
	return raw, accounts, nil
}

// ParseID reports whether an <id|username> argument is an ID. A numeric
// argument is always an ID, and is rejected when it is below 1 or does not fit
// in an int64.
func ParseID(ref string) (int64, bool, error) {
	id, err := strconv.ParseInt(ref, 10, 64)
	switch {
	case err == nil && id > 0:
		return id, true, nil
	case err == nil || errors.Is(err, strconv.ErrRange):
		return 0, false, fmt.Errorf("invalid service account ID %q", ref)
	}
	return 0, false, nil
}

// ResolveID returns the ID of the service account an <id|username> argument
// names. Only a username costs a request.
func ResolveID(ctx context.Context, client *gitlab.Client, scope Scope, ref string) (int64, error) {
	id, isID, err := ParseID(ref)
	if err != nil || isID {
		return id, err
	}

	_, account, err := FindByUsername(ctx, client, scope, ref)
	if err != nil {
		return 0, err
	}
	return account.ID, nil
}

// FindByUsername returns the service account with the given username, both as
// the raw API object for JSON output and as an Account for text output. The
// list endpoints accept no username filter, so the accounts are matched page by
// page, stopping at the first match.
func FindByUsername(ctx context.Context, client *gitlab.Client, scope Scope, username string) (any, Account, error) {
	var (
		raw     any
		account Account
		found   bool
		err     error
	)
	if scope.Group != "" {
		var item *gitlab.GroupServiceAccount
		item, account, found, err = find(gitlab.Scan2(groupPages(ctx, client, scope.Group)), FromGroup, username)
		raw = item
	} else {
		var item *gitlab.ProjectServiceAccount
		item, account, found, err = find(gitlab.Scan2(projectPages(ctx, client, scope.Project)), FromProject, username)
		raw = item
	}
	if err != nil {
		return nil, Account{}, err
	}
	if !found {
		return nil, Account{}, fmt.Errorf("no service account with username %q in %s", username, scope)
	}
	return raw, account, nil
}

func find[T any](accounts iter.Seq2[T, error], toAccount func(T) Account, username string) (T, Account, bool, error) {
	var zero T
	for item, err := range accounts {
		if err != nil {
			return zero, Account{}, false, err
		}
		// GitLab treats usernames case-insensitively.
		if a := toAccount(item); strings.EqualFold(a.Username, username) {
			return item, a, true, nil
		}
	}
	return zero, Account{}, false, nil
}
