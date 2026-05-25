//go:build windows && !arm64

package sandbox

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// postStart assigns the child process to a Job Object with
// KILL_ON_JOB_CLOSE so orphaned children are cleaned up.
// Returns a cleanup function that closes the job handle. The caller MUST
// defer the cleanup after cmd.Wait() returns, not before — closing the
// handle triggers KILL_ON_JOB_CLOSE which terminates the child.
func postStart(cmd *exec.Cmd, cfg Config) (cleanup func(), err error) {
	jobHandle, _, lastErr := procCreateJobObjectW.Call(0, 0)
	if jobHandle == 0 {
		return nil, fmt.Errorf("sandbox: CreateJobObject: %w", lastErr)
	}

	info := struct {
		BasicLimitInformation windows.JOBOBJECT_BASIC_LIMIT_INFORMATION
		IoInfo                windows.IO_COUNTERS
		ProcessMemoryLimit    uintptr
		JobMemoryLimit        uintptr
		PeakProcessMemoryUsed uintptr
		PeakJobMemoryUsed     uintptr
	}{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: jobObjectLimitKillOnJobClose,
		},
	}

	ret, _, lastErr := procSetInformationJobObj.Call(
		jobHandle,
		uintptr(windows.JobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ret == 0 {
		windows.CloseHandle(windows.Handle(jobHandle))
		return nil, fmt.Errorf("sandbox: SetInformationJobObject: %w", lastErr)
	}

	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		windows.CloseHandle(windows.Handle(jobHandle))
		return nil, fmt.Errorf("sandbox: OpenProcess(%d): %w", cmd.Process.Pid, err)
	}
	defer windows.CloseHandle(procHandle)

	ret, _, lastErr = procAssignProcessToJob.Call(
		jobHandle,
		uintptr(procHandle),
	)
	if ret == 0 {
		windows.CloseHandle(windows.Handle(jobHandle))
		return nil, fmt.Errorf("sandbox: AssignProcessToJobObject: %w", lastErr)
	}

	cleanup = func() {
		windows.CloseHandle(windows.Handle(jobHandle))
	}
	return cleanup, nil
}
