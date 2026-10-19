//go:build unix

package dashboard

import (
	"bufio"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestKillBrowserProcessGroupReapsOrphanedHelpers(t *testing.T) {
	// The shell stands in for Chrome: it forks a long-lived helper, then the
	// leader is hard-killed the way chromedp kills Chrome.
	cmd := exec.Command("/bin/sh", "-c", "sleep 60 & echo $!; exec sleep 60")
	prepareBrowserCommand(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read helper pid: %v", err)
	}
	helper, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parse helper pid %q: %v", line, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(helper, syscall.SIGKILL) })

	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if err := syscall.Kill(helper, 0); err != nil {
		t.Fatalf("helper should outlive a leader-only kill, got %v", err)
	}

	killBrowserProcessGroup(cmd.Process)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(helper, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("helper %d still running after process group kill", helper)
}
