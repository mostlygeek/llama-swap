//go:build windows

package process

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SetupTreeCleanup assigns the current process to a Windows Job Object
// configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. Upstream processes
// spawned afterwards are associated with the same job, so when llama-swap exits
// for any reason — graceful shutdown, a forced second Ctrl+C, or a crash — the
// OS terminates the whole job and reaps every child instead of leaving orphans
// behind. It is the parent-side complement to the per-process teardown in
// runtime_windows.go.
//
// The job handle is intentionally leaked for the lifetime of the process: the
// kill-on-close behaviour fires when the last handle is released, which the OS
// does when the process exits.
func SetupTreeCleanup() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("CreateJobObject: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("SetInformationJobObject: %w", err)
	}

	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}

	return nil
}
