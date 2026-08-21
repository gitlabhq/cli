//go:build !integration

package glrepo

import (
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
)

func eq(t *testing.T, got any, expected any) {
	t.Helper()
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("expected: %v, got: %v", expected, got)
	}
}

func TestFindByName(t *testing.T) {
	list := Remotes{
		&Remote{Remote: &git.Remote{Name: "mona"}, Repo: New("monalisa", "myfork", glinstance.DefaultHostname)},
		&Remote{Remote: &git.Remote{Name: "origin"}, Repo: New("monalisa", "octo-cat", glinstance.DefaultHostname)},
		&Remote{Remote: &git.Remote{Name: "upstream"}, Repo: New("hubot", "tools", glinstance.DefaultHostname)},
	}

	r, err := list.FindByName("upstream", "origin")
	eq(t, err, nil)
	eq(t, r.Name, "upstream")

	r, err = list.FindByName("nonexist", "*")
	eq(t, err, nil)
	eq(t, r.Name, "mona")

	_, err = list.FindByName("nonexist")
	eq(t, err, errors.New(`no GitLab remotes found`))
}

func TestTranslateRemotes(t *testing.T) {
	publicURL, _ := url.Parse("https://gitlab.com/monalisa/hello")
	originURL, _ := url.Parse("http://example.com/repo")
	upstreamURL, _ := url.Parse("https://gitlab.com/profclems/glab")

	gitRemotes := git.RemoteSet{
		&git.Remote{
			Name:     "origin",
			FetchURL: originURL,
		},
		&git.Remote{
			Name:     "public",
			FetchURL: publicURL,
		},
		&git.Remote{
			Name:    "upstream",
			PushURL: upstreamURL,
		},
	}

	identityURL := func(u *url.URL) *url.URL {
		return u
	}
	result := TranslateRemotes(gitRemotes, identityURL, glinstance.DefaultHostname, config.NewBlankConfig())

	if len(result) != 2 {
		t.Errorf("got %d results", len(result))
	}
	if result[0].Name != "public" {
		t.Errorf("got %q", result[0].Name)
	}
	if result[0].RepoName() != "hello" {
		t.Errorf("got %q", result[0].RepoName())
	}
	if result[1].Name != "upstream" {
		t.Errorf("got %q", result[1].Name)
	}
	if result[1].RepoName() != "glab" {
		t.Errorf("got %q", result[1].Name)
	}
}

func Test_remoteNameSortingScore(t *testing.T) {
	testCases := []struct {
		name   string
		input  string
		output int
	}{
		{
			name:   "upstream",
			input:  "upstream",
			output: 3,
		},
		{
			name:   "gitlab",
			input:  "gitlab",
			output: 2,
		},
		{
			name:   "origin",
			input:  "origin",
			output: 1,
		},
		{
			name:   "else",
			input:  "anyOtherName",
			output: 0,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := remoteNameSortScore(tC.input)
			assert.Equal(t, tC.output, got)
		})
	}
}

func Test_FindByRepo(t *testing.T) {
	r := Remotes{
		&Remote{
			Remote: &git.Remote{
				Name: "origin",
			},
			Repo: NewWithHost("profclems", "glab", "gitlab.com"),
		},
	}

	t.Run("success", func(t *testing.T) {
		got, err := r.FindByRepo("profclems", "glab")
		require.NoError(t, err)

		assert.Equal(t, r[0].FullName(), got.FullName())
	})

	t.Run("fail/owner", func(t *testing.T) {
		got, err := r.FindByRepo("maxice8", "glab")
		assert.Nil(t, got)
		assert.EqualError(t, err, "no matching remote found")
	})

	t.Run("fail/project", func(t *testing.T) {
		got, err := r.FindByRepo("profclems", "balg")
		assert.Nil(t, got)
		assert.EqualError(t, err, "no matching remote found")
	})

	t.Run("fail/owner and project", func(t *testing.T) {
		got, err := r.FindByRepo("maxice8", "balg")
		assert.Nil(t, got)
		assert.EqualError(t, err, "no matching remote found")
	})
}

func Test_RepoFuncs(t *testing.T) {
	testCases := []struct {
		name          string
		input         []string
		wantHostname  string
		wantOwner     string
		wantGroup     string
		wantNamespace string
		wantName      string
		wantFullname  string
	}{
		{
			name:          "Simple",
			input:         []string{"profclems", "glab", "gitlab.com"},
			wantHostname:  "gitlab.com",
			wantNamespace: "profclems",
			wantOwner:     "profclems",
			wantName:      "glab",
			wantFullname:  "profclems/glab",
		},
		{
			name:          "group",
			input:         []string{"company/profclems", "glab", "gitlab.com"},
			wantHostname:  "gitlab.com",
			wantNamespace: "profclems",
			wantOwner:     "company/profclems",
			wantGroup:     "company",
			wantName:      "glab",
			wantFullname:  "company/profclems/glab",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := Remote{
				Repo: NewWithHost(tC.input[0], tC.input[1], tC.input[2]),
			}
			if tC.wantHostname != "" {
				assert.Equal(t, tC.wantHostname, got.RepoHost())
			}
			if tC.wantOwner != "" {
				assert.Equal(t, tC.wantOwner, got.RepoOwner())
			}
			if tC.wantGroup != "" {
				assert.Equal(t, tC.wantGroup, got.RepoGroup())
			}
			if tC.wantNamespace != "" {
				assert.Equal(t, tC.wantNamespace, got.RepoNamespace())
			}
			if tC.wantName != "" {
				assert.Equal(t, tC.wantName, got.RepoName())
			}
			if tC.wantFullname != "" {
				assert.Equal(t, tC.wantFullname, got.FullName())
			}
		})
	}
}

func Test_Swap(t *testing.T) {
	r := Remotes{
		&Remote{
			Remote: &git.Remote{
				Name: "origin",
			},
			Repo: NewWithHost("maxice8", "glab", "gitlab.com"),
		},
		&Remote{
			Remote: &git.Remote{
				Name: "upstream",
			},
			Repo: NewWithHost("profclems", "glab", "gitlab.com"),
		},
	}

	assert.Equal(t, "origin", r[0].Remote.Name)
	assert.Equal(t, "upstream", r[1].Remote.Name)

	assert.Equal(t, "maxice8/glab", r[0].Repo.FullName())
	assert.Equal(t, "profclems/glab", r[1].Repo.FullName())

	r.Swap(0, 1)

	assert.Equal(t, "upstream", r[0].Remote.Name)
	assert.Equal(t, "origin", r[1].Remote.Name)

	assert.Equal(t, "profclems/glab", r[0].Repo.FullName())
	assert.Equal(t, "maxice8/glab", r[1].Repo.FullName())
}

func Test_Less(t *testing.T) {
	r := Remotes{
		&Remote{
			Remote: &git.Remote{
				Name: "else",
			},
			Repo: NewWithHost("somebody", "glab", "gitlab.com"),
		},
		&Remote{
			Remote: &git.Remote{
				Name: "origin",
			},
			Repo: NewWithHost("maxice8", "glab", "gitlab.com"),
		},
		&Remote{
			Remote: &git.Remote{
				Name: "gitlab",
			},
			Repo: NewWithHost("profclems", "glab", "gitlab.com"),
		},
		&Remote{
			Remote: &git.Remote{
				Name: "upstream",
			},
			Repo: NewWithHost("profclems", "glab", "gitlab.com"),
		},
	}

	assert.True(t, r.Less(3, 2))
	assert.True(t, r.Less(3, 1))
	assert.True(t, r.Less(3, 0))
	assert.True(t, r.Less(2, 1))
	assert.True(t, r.Less(2, 0))
	assert.True(t, r.Less(1, 0))

	assert.False(t, r.Less(0, 1))
	assert.False(t, r.Less(0, 2))
	assert.False(t, r.Less(0, 3))
	assert.False(t, r.Less(1, 2))
	assert.False(t, r.Less(1, 3))
	assert.False(t, r.Less(2, 3))
}

func TestTranslateRemotes_SplitHostSubfolder(t *testing.T) {
	// This test verifies the split-host + subfolder bug (Issue #8197)
	// where FromURL() fails to strip the subfolder because the URL hostname
	// doesn't match the config key hostname in split-host setups

	t.Run("split-host with subfolder - correct config pattern", func(t *testing.T) {
		// Setup: Config keyed under API hostname with ssh_host and subfolder
		cfg := config.NewFromString(`---
hosts:
  api.example.com:
    token: TEST_TOKEN
    ssh_host: git.example.com
    subfolder: gitlab
`)

		// Git remote URL includes the subfolder in the path
		remoteURL, _ := url.Parse("https://api.example.com/gitlab/owner/repo.git")

		gitRemotes := git.RemoteSet{
			&git.Remote{
				Name:     "origin",
				FetchURL: remoteURL,
			},
		}

		identityURL := func(u *url.URL) *url.URL {
			return u
		}

		result := TranslateRemotes(gitRemotes, identityURL, "gitlab.com", cfg)

		// Verify results
		assert.Len(t, result, 1, "should translate one remote")
		assert.Equal(t, "origin", result[0].Name, "remote name should be origin")

		// The key assertion: FullName should be "owner/repo" (subfolder stripped)
		// NOT "gitlab/owner/repo" (with subfolder included)
		assert.Equal(t, "owner/repo", result[0].FullName(), "subfolder should be stripped from project path")
		assert.Equal(t, "api.example.com", result[0].RepoHost(), "should use URL hostname as RepoHost")
	})
}

// TranslateRemotes is the path `mr create` takes to resolve the head repo from
// git remotes, which is why the ported-host lookup bug surfaces there while
// `-R`-driven commands are unaffected.
func TestTranslateRemotes_SubfolderOnPortedHost(t *testing.T) {
	t.Run("host entry keyed with port", func(t *testing.T) {
		cfg := config.NewFromString(`---
hosts:
  example.com:8080:
    token: TEST_TOKEN
    api_protocol: http
    subfolder: gitlab
`)

		remoteURL, _ := url.Parse("http://example.com:8080/gitlab/owner/repo.git")

		gitRemotes := git.RemoteSet{
			&git.Remote{
				Name:     "origin",
				FetchURL: remoteURL,
			},
		}

		identityURL := func(u *url.URL) *url.URL { return u }

		result := TranslateRemotes(gitRemotes, identityURL, "gitlab.com", cfg)

		assert.Len(t, result, 1, "should translate one remote")
		assert.Equal(t, "owner/repo", result[0].FullName(), "subfolder should be stripped from project path")
		assert.Equal(t, "example.com:8080", result[0].RepoHost(), "RepoHost should retain the port")

		// RepoHost is the key the API client resolves its own settings under, so
		// it has to address the same host entry FromURL just read the subfolder
		// from. When the two disagree, requests are built with the wrong project
		// path, the default protocol, and no token.
		token, _ := cfg.Get(result[0].RepoHost(), "token")
		protocol, _ := cfg.Get(result[0].RepoHost(), "api_protocol")
		assert.Equal(t, "TEST_TOKEN", token, "RepoHost must address the configured host entry")
		assert.Equal(t, "http", protocol, "RepoHost must address the configured host entry")
	})
}

func TestRemotes_UniqueHosts(t *testing.T) {
	tests := []struct {
		name string
		r    Remotes
		want string
	}{
		{
			name: "multiple remotes with duplicates",
			r: Remotes{
				&Remote{
					Remote: &git.Remote{
						Name: "origin",
					},
					Repo: NewWithHost("owner", "repo", "gitlab.com"),
				},
				&Remote{
					Remote: &git.Remote{
						Name: "fork",
					},
					Repo: NewWithHost("forkowner", "repo", "gitlab.com"),
				},
				&Remote{
					Remote: &git.Remote{
						Name: "upstream",
					},
					Repo: NewWithHost("owner", "repo", "mygitlab-xyz.com"),
				},
			},
			want: "gitlab.com, mygitlab-xyz.com",
		},
		{
			name: "single remote",
			r: Remotes{
				&Remote{
					Remote: &git.Remote{
						Name: "origin",
					},
					Repo: NewWithHost("owner", "repo", "gitlab.com"),
				},
			},
			want: "gitlab.com",
		},
		{
			name: "no remotes",
			r:    Remotes{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.r.UniqueHosts())
		})
	}
}
