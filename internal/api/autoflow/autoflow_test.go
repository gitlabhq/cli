//go:build !integration

package autoflow

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/tokenexchangetest"
)

func TestTokenSource_RefreshesBeforeExpiry(t *testing.T) {
	t.Parallel()

	token := tokenexchangetest.MakeJWT(t, jwt.RegisteredClaims{
		Issuer:    "https://gitlab.example.com",
		Subject:   "gid://gitlab/User/1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	})
	var gotBody tokenexchangetest.WireRequest
	srv, count := tokenexchangetest.NewTokenExchangeServer(t, token, &gotBody)

	now := time.Now()
	src := &tokenSource{gl: tokenexchangetest.NewTestClient(t, srv.URL), now: func() time.Time { return now }}
	require.NoError(t, src.refresh(t.Context()))
	assert.Equal(t, tokenexchangetest.WireRequest{Audience: "gitlab-autoflow", ExpiresIn: 60}, gotBody)

	md, err := src.GetRequestMetadata(t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"authorization": "Bearer " + token}, md)
	assert.EqualValues(t, 1, count.Load())

	now = now.Add(tokenLifetime - refreshMargin)
	_, err = src.GetRequestMetadata(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 1, count.Load())

	now = now.Add(time.Second)
	_, err = src.GetRequestMetadata(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 2, count.Load())
}
