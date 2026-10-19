//go:build unix && !linux

package dashboard

import "syscall"

func setParentDeathSignal(*syscall.SysProcAttr) {}
