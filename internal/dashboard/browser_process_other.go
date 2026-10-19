//go:build !unix && !windows

package dashboard

import (
	"os"
	"os/exec"
)

func prepareBrowserCommand(*exec.Cmd) {}

func killBrowserProcessGroup(*os.Process) {}
