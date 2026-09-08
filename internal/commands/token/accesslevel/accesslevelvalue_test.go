//go:build !integration

package accesslevel

import (
	"strings"
	"testing"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func TestAccessLevel_Set(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    gitlab.AccessLevelValue
		wantErr bool
	}{
		{"no", "no", gitlab.NoPermissions, false},
		{"minimal", "minimal", gitlab.MinimalAccessPermissions, false},
		{"guest", "guest", gitlab.GuestPermissions, false},
		{"reporter", "reporter", gitlab.ReporterPermissions, false},
		{"developer", "developer", gitlab.DeveloperPermissions, false},
		{"maintainer", "maintainer", gitlab.MaintainerPermissions, false},
		{"owner", "owner", gitlab.OwnerPermissions, false},
		{"admin", "admin", gitlab.AdminPermissions, false},
		{"maintainer with caps", "Maintainer", gitlab.MaintainerPermissions, false},
		{"unknown", "blabla", gitlab.NoPermissions, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AccessLevel{}
			if err := a.Set(tt.arg); (err != nil) != tt.wantErr {
				t.Errorf("Set() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				if a.Value != tt.want {
					t.Errorf("Set() got = %v, want %v", *a, tt.want)
				}

				if tt.wantErr && err != nil {
					return
				}

				if a.String() != strings.ToLower(tt.arg) {
					t.Errorf("String() got = %v, want %v", a.String(), tt.arg)
				}

				if a.Type() != "AccessLevel" {
					t.Errorf("Type() got = %v, want %v", a.Type(), "AccessLevel")
				}
			}
		})
	}
}

func TestAccessLevel_String(t *testing.T) {
	type fields struct {
		Value gitlab.AccessLevelValue
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name:   "invalid gitlab access level value",
			fields: fields{Value: gitlab.AccessLevelValue(999)},
			want:   "999",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AccessLevel{
				Value: tt.fields.Value,
			}
			if got := a.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}
