//go:build !unix && !windows && !js

package dashboard

import (
	"os"
	"os/exec"
)

func prepareBrowserCommand(*exec.Cmd) {}

func killBrowserProcessGroup(*os.Process) {}
