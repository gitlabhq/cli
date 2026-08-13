package update

import (
	"os"
	"path/filepath"
	"strings"
)

// InstallMethod is the detected install method for the running glab binary.
type InstallMethod struct {
	Name           string
	UpgradeCommand string
}

const (
	installMethodHomebrew   = "homebrew"
	installMethodMacPorts   = "macports"
	installMethodSnap       = "snap"
	installMethodNix        = "nix"
	installMethodMise       = "mise"
	installMethodAsdf       = "asdf"
	installMethodChocolatey = "chocolatey"
	installMethodScoop      = "scoop"
	installMethodWinGet     = "winget"
	installMethodGoInstall  = "go-install"
	installMethodUnknown    = "unknown"

	homebrewUpgradeCommand   = "brew upgrade glab"
	macportsUpgradeCommand   = "sudo port selfupdate && sudo port upgrade glab"
	snapUpgradeCommand       = "sudo snap refresh glab"
	nixUpgradeCommand        = "nix profile upgrade glab"
	miseUpgradeCommand       = "mise upgrade glab"
	asdfUpgradeCommand       = "asdf install glab latest && asdf global glab latest"
	chocolateyUpgradeCommand = "choco upgrade glab"
	scoopUpgradeCommand      = "scoop update glab"
	wingetUpgradeCommand     = "winget upgrade glab.glab"
	goInstallUpgradeCommand  = "go install gitlab.com/gitlab-org/cli/cmd/glab@latest"
)

var installMethods = []struct {
	name            string
	upgrade         string
	prefixes        []string
	contains        []string
	caseInsensitive bool
}{
	{
		name:     installMethodHomebrew,
		upgrade:  homebrewUpgradeCommand,
		prefixes: []string{"/opt/homebrew/", "/usr/local/Cellar/", "/home/linuxbrew/.linuxbrew/"},
	},
	{
		name:     installMethodMacPorts,
		upgrade:  macportsUpgradeCommand,
		prefixes: []string{"/opt/local/"},
	},
	{
		name:     installMethodSnap,
		upgrade:  snapUpgradeCommand,
		prefixes: []string{"/snap/glab/", "/var/lib/snapd/snap/glab/"},
	},
	{
		name:     installMethodNix,
		upgrade:  nixUpgradeCommand,
		prefixes: []string{"/nix/store/"},
	},
	{
		name:     installMethodMise,
		upgrade:  miseUpgradeCommand,
		contains: []string{"/mise/installs/", "/mise/shims/"},
	},
	{
		name:     installMethodAsdf,
		upgrade:  asdfUpgradeCommand,
		contains: []string{"/.asdf/installs/", "/.asdf/shims/"},
	},
	{
		name:            installMethodChocolatey,
		upgrade:         chocolateyUpgradeCommand,
		contains:        []string{"/chocolatey/lib/", "/chocolatey/bin/"},
		caseInsensitive: true,
	},
	{
		name:            installMethodScoop,
		upgrade:         scoopUpgradeCommand,
		contains:        []string{"/scoop/apps/"},
		caseInsensitive: true,
	},
	{
		name:            installMethodWinGet,
		upgrade:         wingetUpgradeCommand,
		contains:        []string{"/winget/packages/"},
		caseInsensitive: true,
	},
}

// DetectInstallMethod returns the install method of the running glab binary.
func DetectInstallMethod() InstallMethod {
	exe, err := os.Executable()
	if err != nil {
		return InstallMethod{Name: installMethodUnknown}
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return detectInstallMethodFromPath(exe, os.Getenv("GOPATH"), homeDir())
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func detectInstallMethodFromPath(exePath, gopath, home string) InstallMethod {
	p := filepath.ToSlash(exePath)
	pLower := strings.ToLower(p)

	for _, m := range installMethods {
		target := p
		if m.caseInsensitive {
			target = pLower
		}
		for _, prefix := range m.prefixes {
			if strings.HasPrefix(target, prefix) {
				return InstallMethod{Name: m.name, UpgradeCommand: m.upgrade}
			}
		}
		for _, sub := range m.contains {
			if strings.Contains(target, sub) {
				return InstallMethod{Name: m.name, UpgradeCommand: m.upgrade}
			}
		}
	}

	for _, dir := range goBinDirs(gopath, home) {
		if dir != "" && strings.HasPrefix(p, filepath.ToSlash(dir)+"/") {
			return InstallMethod{Name: installMethodGoInstall, UpgradeCommand: goInstallUpgradeCommand}
		}
	}
	return InstallMethod{Name: installMethodUnknown}
}

func goBinDirs(gopath, home string) []string {
	var dirs []string
	if gopath != "" {
		for _, g := range filepath.SplitList(gopath) {
			if g != "" {
				dirs = append(dirs, filepath.Join(g, "bin"))
			}
		}
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}
	return dirs
}
