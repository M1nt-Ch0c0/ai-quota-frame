//go:build unix

package dashboard

import (
	"os"
	"os/exec"
	"syscall"
)

// prepareBrowserCommand starts Chrome as the leader of its own process group
// so every helper it spawns can be killed together.
func prepareBrowserCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}
	cmd.SysProcAttr.Setpgid = true
	setParentDeathSignal(cmd.SysProcAttr)
}

// killBrowserProcessGroup kills any process still in the browser's group. It
// runs after chromedp has reaped the leader; a group ID cannot be reused while
// any member remains, so this only reaches leftover Chrome helpers.
func killBrowserProcessGroup(process *os.Process) {
	_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
}
