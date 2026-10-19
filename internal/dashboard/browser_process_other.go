//go:build !unix

package dashboard

import (
	"os"
	"os/exec"
)

func prepareBrowserCommand(*exec.Cmd) {}

func killBrowserProcessGroup(*os.Process) {}
