package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuth2AccessTokenOnlyAuthSource_Header(t *testing.T) {
	tests := []struct {
		name    string
		expiry  time.Time
		wantErr bool
	}{
		{name: "no expiry recorded", expiry: time.Time{}},
		{name: "not yet expired", expiry: time.Now().Add(time.Hour)},
		{name: "expired", expiry: time.Now().Add(-time.Minute), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as := oauth2AccessTokenOnlyAuthSource{token: "access", host: "gitlab.example.com", expiry: tt.expiry}

			_, value, err := as.Header(t.Context())
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, "glab auth login --hostname gitlab.example.com")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "Bearer access", value)
		})
	}
}
