//go:build windows

package agenttool

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wireProcessTreeKill wires cmd so context cancellation terminates the whole
// Windows process tree. It creates a Job Object with KILL_ON_JOB_CLOSE, routes
// exec's Cancel through TerminateJobObject, and returns an assign hook to be
// called right after cmd.Start plus a cleanup hook that closes the job (which
// kills any leftover grandchildren).
func wireProcessTreeKill(cmd *exec.Cmd) (assign func(pid int), cleanup func()) {
	noop := func() {}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func(int) {}, noop
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return func(int) {}, noop
	}
	cmd.Cancel = func() error {
		return windows.TerminateJobObject(job, 1)
	}
	assign = func(pid int) {
		if pid <= 0 {
			return
		}
		h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			return
		}
		defer windows.CloseHandle(h)
		_ = windows.AssignProcessToJobObject(job, h)
	}
	cleanup = func() {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	}
	return assign, cleanup
}
