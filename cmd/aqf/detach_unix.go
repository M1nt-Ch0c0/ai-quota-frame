//go:build !windows

package main

import "syscall"

// A new session keeps the server alive after the starting terminal closes.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
