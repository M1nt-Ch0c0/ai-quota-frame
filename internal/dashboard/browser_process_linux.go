package dashboard

import "syscall"

// setParentDeathSignal keeps chromedp's Linux default, which a custom command
// modifier would otherwise drop: Chrome dies if this process dies.
func setParentDeathSignal(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGKILL
}
