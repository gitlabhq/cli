//go:build !integration

package cmdtest

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewTestFactoryWithoutExecutorFailsWithHint(t *testing.T) {
	t.Parallel()
	executor := NewTestFactory(nil).Executor()
	var out bytes.Buffer

	_, err := executor.LookPath("git")
	require.ErrorContains(t, err, "cmdtest.WithExecutor")
	require.ErrorContains(t, executor.Exec(t.Context(), "git", nil, nil), "cmdtest.WithExecutor")
	_, err = executor.ExecWithCombinedOutput(t.Context(), "git", nil, nil)
	require.ErrorContains(t, err, "cmdtest.WithExecutor")
	require.ErrorContains(t, executor.ExecWithIO(t.Context(), "git", nil, nil, nil, &out, &out), "cmdtest.WithExecutor")
}
