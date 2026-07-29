//go:build !integration

package stackutils

import (
	"errors"
	"os/user"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBranchPrefixFromCurrentUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		currentUser func() (*user.User, error)
		want        string
	}{
		{
			name: "uses current username",
			currentUser: func() (*user.User, error) {
				return &user.User{Username: "testuser"}, nil
			},
			want: "testuser",
		},
		{
			name: "removes Windows domain",
			currentUser: func() (*user.User, error) {
				return &user.User{Username: `DOMAIN\windowsuser`}, nil
			},
			want: "windowsuser",
		},
		{
			name: "falls back when username is empty",
			currentUser: func() (*user.User, error) {
				return &user.User{}, nil
			},
			want: "glab-stack",
		},
		{
			name: "falls back when lookup fails",
			currentUser: func() (*user.User, error) {
				return nil, errors.New("lookup failed")
			},
			want: "glab-stack",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, branchPrefixFromCurrentUser(tt.currentUser))
		})
	}
}
