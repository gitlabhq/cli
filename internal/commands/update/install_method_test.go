//go:build !integration

package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectInstallMethodFromPath(t *testing.T) {
	t.Parallel()

	homebrew := InstallMethod{Name: installMethodHomebrew, UpgradeCommand: homebrewUpgradeCommand}
	macports := InstallMethod{Name: installMethodMacPorts, UpgradeCommand: macportsUpgradeCommand}
	snap := InstallMethod{Name: installMethodSnap, UpgradeCommand: snapUpgradeCommand}
	nix := InstallMethod{Name: installMethodNix, UpgradeCommand: nixUpgradeCommand}
	mise := InstallMethod{Name: installMethodMise, UpgradeCommand: miseUpgradeCommand}
	asdf := InstallMethod{Name: installMethodAsdf, UpgradeCommand: asdfUpgradeCommand}
	chocolatey := InstallMethod{Name: installMethodChocolatey, UpgradeCommand: chocolateyUpgradeCommand}
	scoop := InstallMethod{Name: installMethodScoop, UpgradeCommand: scoopUpgradeCommand}
	winget := InstallMethod{Name: installMethodWinGet, UpgradeCommand: wingetUpgradeCommand}
	goInstall := InstallMethod{Name: installMethodGoInstall, UpgradeCommand: goInstallUpgradeCommand}
	unknown := InstallMethod{Name: installMethodUnknown}

	tests := []struct {
		name    string
		exePath string
		gopath  string
		home    string
		want    InstallMethod
	}{
		{name: "homebrew apple silicon", exePath: "/opt/homebrew/bin/glab", home: "/Users/somebody", want: homebrew},
		{name: "homebrew intel cellar", exePath: "/usr/local/Cellar/glab/1.62.1/bin/glab", home: "/Users/somebody", want: homebrew},
		{name: "linuxbrew", exePath: "/home/linuxbrew/.linuxbrew/bin/glab", home: "/home/someone", want: homebrew},
		{name: "macports typical", exePath: "/opt/local/bin/glab", home: "/Users/somebody", want: macports},
		{name: "macports resolved through software dir", exePath: "/opt/local/var/macports/software/glab/1.62.0_0/opt/local/bin/glab", home: "/Users/somebody", want: macports},
		{name: "mise installs", exePath: "/Users/somebody/.local/share/mise/installs/glab/1.62.0/bin/glab", home: "/Users/somebody", want: mise},
		{name: "mise ubi provider install", exePath: "/Users/somebody/.local/share/mise/installs/ubi-gitlab-org-cli/1.62.0/glab", home: "/Users/somebody", want: mise},
		{name: "mise shim", exePath: "/Users/somebody/.local/share/mise/shims/glab", home: "/Users/somebody", want: mise},
		{name: "asdf install", exePath: "/Users/somebody/.asdf/installs/glab/1.62.0/bin/glab", home: "/Users/somebody", want: asdf},
		{name: "asdf shim", exePath: "/Users/somebody/.asdf/shims/glab", home: "/Users/somebody", want: asdf},
		{name: "snap standard mount", exePath: "/snap/glab/42/bin/glab", home: "/home/someone", want: snap},
		{name: "snap fedora-style mount", exePath: "/var/lib/snapd/snap/glab/42/bin/glab", home: "/home/someone", want: snap},
		{name: "nix store", exePath: "/nix/store/abcdef0123456789-glab-1.62.0/bin/glab", home: "/home/someone", want: nix},
		{name: "chocolatey lib", exePath: "C:/ProgramData/chocolatey/lib/glab/tools/glab.exe", home: "C:/Users/somebody", want: chocolatey},
		{name: "chocolatey bin shim", exePath: "C:/ProgramData/chocolatey/bin/glab.exe", home: "C:/Users/somebody", want: chocolatey},
		{name: "scoop apps", exePath: "C:/Users/somebody/scoop/apps/glab/current/glab.exe", home: "C:/Users/somebody", want: scoop},
		{name: "winget packages (mixed case)", exePath: "C:/Users/somebody/AppData/Local/Microsoft/WinGet/Packages/glab.glab_Microsoft.Winget.Source_8wekyb3d8bbwe/glab.exe", home: "C:/Users/somebody", want: winget},
		{name: "winget packages (lowercase)", exePath: "C:/Users/somebody/AppData/Local/Microsoft/winget/packages/glab.glab_Microsoft.Winget.Source_8wekyb3d8bbwe/glab.exe", home: "C:/Users/somebody", want: winget},
		{name: "go install with explicit GOPATH", exePath: "/Users/someone/sdk/go/bin/glab", gopath: "/Users/someone/sdk/go", home: "/Users/someone", want: goInstall},
		{name: "go install via $HOME/go/bin fallback", exePath: "/Users/someone/go/bin/glab", home: "/Users/someone", want: goInstall},
		{name: "unknown system path", exePath: "/usr/local/bin/glab", home: "/Users/somebody", want: unknown},
		{name: "unknown local custom path", exePath: "/Users/somebody/projects/glab/cli/bin/glab", home: "/Users/somebody", want: unknown},
		{name: "user-local path containing homebrew substring is not homebrew", exePath: "/home/user/opt/homebrew/bin/glab", home: "/home/user", want: unknown},
		{name: "user-local path containing mise substring is not mise", exePath: "/home/user/promise/installs/glab", home: "/home/user", want: unknown},
		{name: "user-local path containing asdf substring is not asdf", exePath: "/home/user/foo.asdf/installs/glab", home: "/home/user", want: unknown},
		{name: "user-local path containing chocolatey substring is not chocolatey", exePath: "C:/Users/somebody/mychocolatey/lib/glab.exe", home: "C:/Users/somebody", want: unknown},
		{name: "user-local path containing scoop substring is not scoop", exePath: "C:/Users/somebody/myscoop/apps/glab.exe", home: "C:/Users/somebody", want: unknown},
		{name: "user-local path containing winget substring is not winget", exePath: "C:/Users/somebody/mywinget/packages/glab.exe", home: "C:/Users/somebody", want: unknown},
		{name: "empty home does not match go-install", exePath: "/some/go/bin/glab", home: "", want: unknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, detectInstallMethodFromPath(tc.exePath, tc.gopath, tc.home))
		})
	}
}
