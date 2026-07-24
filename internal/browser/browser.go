package browser

import (
	"errors"
	"os"
	"os/exec"
	"runtime"

	"github.com/google/shlex"
)

// Command produces an exec.Cmd respecting runtime.GOOS and $BROWSER environment variable
func Command(url, launcher string) (*exec.Cmd, error) {
	if launcher != "" {
		return FromLauncher(launcher, url)
	}
	return ForOS(runtime.GOOS, url), nil
}

// ForOS produces an exec.Cmd to open the web browser for different OS
func ForOS(goos, url string) *exec.Cmd {
	cmd := exec.Command("xdg-open", url)
	switch goos {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}

	cmd.Stderr = os.Stderr

	return cmd
}

// FromLauncher parses the launcher string based on shell splitting rules
func FromLauncher(launcher, url string) (*exec.Cmd, error) {
	args, err := shlex.Split(launcher)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("browser launcher cannot be blank")
	}

	args = append(args, url)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stderr = os.Stderr
	return cmd, nil
}
