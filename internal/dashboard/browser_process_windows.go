package dashboard

import (
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no process groups, and a hard-killed Chrome leaves its helpers
// running. This process joins a job that kills every member when its last
// handle closes; Chrome and its helpers inherit the job, so they cannot
// outlive the service even when a watchdog force-stops it.
var (
	browserJobOnce sync.Once
	browserJob     windows.Handle
)

func prepareBrowserCommand(*exec.Cmd) {
	browserJobOnce.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(job)
			return
		}
		if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
			windows.CloseHandle(job)
			return
		}
		browserJob = job
	})
}

// killBrowserProcessGroup terminates every other member of the job. Renders
// are serialized, so after closeBrowser those are only leftover Chrome
// processes.
func killBrowserProcessGroup(*os.Process) {
	if browserJob == 0 {
		return
	}
	var list struct {
		assigned uint32
		listed   uint32
		ids      [1024]uintptr
	}
	if err := windows.QueryInformationJobObject(browserJob, windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&list)), uint32(unsafe.Sizeof(list)), nil); err != nil {
		return
	}
	self := uint32(os.Getpid())
	for _, id := range list.ids[:list.listed] {
		if uint32(id) == self {
			continue
		}
		process, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(id))
		if err != nil {
			continue
		}
		_ = windows.TerminateProcess(process, 1)
		windows.CloseHandle(process)
	}
}
