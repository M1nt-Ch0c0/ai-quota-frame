package dashboard

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const browserJobHelperEnv = "AI_QUOTA_FRAME_JOB_HELPER"

// TestMain lets the test binary stand in for Chrome: "leader" starts a
// long-lived "helper" child, prints its PID and then waits.
func TestMain(m *testing.M) {
	switch os.Getenv(browserJobHelperEnv) {
	case "leader":
		self, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		helper := exec.Command(self)
		helper.Env = append(os.Environ(), browserJobHelperEnv+"=helper")
		if err := helper.Start(); err != nil {
			os.Exit(2)
		}
		os.Stdout.WriteString(strconv.Itoa(helper.Process.Pid) + "\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	case "helper":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestKillBrowserProcessGroupReapsOrphanedHelpers(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), browserJobHelperEnv+"=leader")
	prepareBrowserCommand(cmd)
	if browserJob == 0 {
		t.Fatal("browser job was not created")
	}
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
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parse helper pid %q: %v", line, err)
	}
	helper, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatalf("open helper: %v", err)
	}
	defer windows.CloseHandle(helper)
	t.Cleanup(func() { _ = windows.TerminateProcess(helper, 1) })

	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if event, _ := windows.WaitForSingleObject(helper, 0); event != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("helper should outlive a leader-only kill")
	}

	killBrowserProcessGroup(cmd.Process)
	if event, _ := windows.WaitForSingleObject(helper, 5000); event != windows.WAIT_OBJECT_0 {
		t.Fatalf("helper %d still running after job cleanup", pid)
	}
}
