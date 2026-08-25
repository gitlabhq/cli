//go:build !integration

package config

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockHoldEnv, when set, makes TestFileLock_CrossProcess run as the child: it
// acquires the lock for that directory, announces it, and holds it until its
// stdin closes. This lets the parent exercise real cross-process contention by
// re-executing the test binary.
const lockHoldEnv = "GLAB_TEST_LOCK_HOLD_DIR"

func TestFileLock_CrossProcess(t *testing.T) {
	if dir := os.Getenv(lockHoldEnv); dir != "" {
		lock, err := acquireLock(dir)
		if err != nil || lock == nil {
			fmt.Println("error")
			os.Exit(3)
		}
		fmt.Println("locked")
		// Hold the lock until the parent closes our stdin, then release.
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		lock.release()
		os.Exit(0)
	}

	dir := t.TempDir()

	child := exec.Command(os.Args[0], "-test.run", "^TestFileLock_CrossProcess$")
	child.Env = append(os.Environ(), lockHoldEnv+"="+dir)
	stdin, err := child.StdinPipe()
	require.NoError(t, err)
	stdout, err := child.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, child.Start())
	// Kill before Wait: if an assertion below fails before stdin is closed, the
	// child is still blocked reading stdin for an EOF that never arrives, so a
	// bare Wait() in cleanup would hang the whole test run instead of failing.
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	// Wait until the child reports it holds the lock.
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, line, "locked")

	// The parent must not be able to take the lock the child holds.
	restore := shortenLockTimeout(t, 150*time.Millisecond)

	start := time.Now()
	lock, err := acquireLock(dir)
	require.ErrorIs(t, err, errLockTimeout)
	require.Nil(t, lock)
	require.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond)

	// Restore the default timeout before probing so each acquireLock below returns
	// as soon as the child releases the lock rather than blocking for 150 ms.
	restore()

	// Releasing the child (closing its stdin) frees the lock so we can take it.
	require.NoError(t, stdin.Close())
	require.Eventually(t, func() bool {
		l, err := acquireLock(dir)
		if err != nil || l == nil {
			return false
		}
		l.release()
		return true
	}, 2*time.Second, 20*time.Millisecond)
}

func TestAcquireLock_ReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()

	l1, err := acquireLock(dir)
	require.NoError(t, err)
	require.NotNil(t, l1)
	l1.release()

	l2, err := acquireLock(dir)
	require.NoError(t, err)
	require.NotNil(t, l2)
	l2.release()
}

func TestAcquireLock_EmptyDirIsNoop(t *testing.T) {
	lock, err := acquireLock("")
	require.NoError(t, err)
	require.Nil(t, lock)
	// release on a nil lock must not panic.
	lock.release()
}

func TestWithLock_RunsUnlockedWhenLockUnavailable(t *testing.T) {
	dir := t.TempDir()

	held, err := acquireLock(dir)
	require.NoError(t, err)
	defer held.release()

	shortenLockTimeout(t, 100*time.Millisecond) // restored via t.Cleanup

	ran := false
	err = withLock(dir, func() error {
		ran = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, ran, "withLock should run fn even when the lock is unavailable")
}

// shortenLockTimeout temporarily shrinks the global lockTimeout and returns a
// function to restore it early. Restoration is also registered with t.Cleanup,
// so the global is always reset even if the caller never invokes the returned
// function (for example, when an assertion fails first).
func shortenLockTimeout(t *testing.T, d time.Duration) func() {
	t.Helper()
	old := lockTimeout
	lockTimeout = d
	restore := func() { lockTimeout = old }
	t.Cleanup(restore)
	return restore
}
