// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build windows

package gotesting

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/DataDog/dd-trace-go/v2/internal/locking"
)

// ProcessRetryContainmentSupported reports whether this platform can contain
// ordinary retry-child descendants.
func ProcessRetryContainmentSupported() bool { return true }

var processRetryWindowsJobs = struct {
	mu   locking.Mutex
	jobs map[*exec.Cmd]windows.Handle
}{jobs: make(map[*exec.Cmd]windows.Handle)}

func processRetryChildStartsSuspended() bool { return true }

func prepareProcessRetryControlTransport(cmd *exec.Cmd) (*processRetryControlTransport, error) {
	if cmd == nil {
		return nil, errProcessRetryProcessNotStarted
	}
	parentToChildRead, parentToChildWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	childToParentRead, childToParentWrite, err := os.Pipe()
	if err != nil {
		_ = parentToChildRead.Close()
		_ = parentToChildWrite.Close()
		return nil, err
	}
	readHandle := syscall.Handle(parentToChildRead.Fd())
	writeHandle := syscall.Handle(childToParentWrite.Fd())
	if err := syscall.SetHandleInformation(readHandle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
		_ = parentToChildRead.Close()
		_ = parentToChildWrite.Close()
		_ = childToParentRead.Close()
		_ = childToParentWrite.Close()
		return nil, err
	}
	if err := syscall.SetHandleInformation(writeHandle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
		_ = parentToChildRead.Close()
		_ = parentToChildWrite.Close()
		_ = childToParentRead.Close()
		_ = childToParentWrite.Close()
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(
		cmd.SysProcAttr.AdditionalInheritedHandles,
		readHandle,
		writeHandle,
	)
	return &processRetryControlTransport{
		read:       childToParentRead,
		write:      parentToChildWrite,
		childRead:  parentToChildRead,
		childWrite: childToParentWrite,
		config: processRetryControlConfig{
			Transport:     processRetryControlTransportWinHandles,
			ReadEndpoint:  uint64(readHandle),
			WriteEndpoint: uint64(writeHandle),
		},
	}, nil
}

func openProcessRetryChildControlTransport(cfg processRetryControlConfig) (*os.File, *os.File, error) {
	if cfg.Transport != processRetryControlTransportWinHandles {
		return nil, nil, errProcessRetryControlInvalid
	}
	readHandle := syscall.Handle(cfg.ReadEndpoint)
	writeHandle := syscall.Handle(cfg.WriteEndpoint)
	if err := syscall.SetHandleInformation(readHandle, syscall.HANDLE_FLAG_INHERIT, 0); err != nil {
		return nil, nil, err
	}
	if err := syscall.SetHandleInformation(writeHandle, syscall.HANDLE_FLAG_INHERIT, 0); err != nil {
		return nil, nil, err
	}
	read := os.NewFile(uintptr(readHandle), "dd-process-retry-control-read")
	write := os.NewFile(uintptr(writeHandle), "dd-process-retry-control-write")
	if read == nil || write == nil {
		_ = closeProcessRetryControlFile(read)
		_ = closeProcessRetryControlFile(write)
		return nil, nil, errProcessRetryControlInvalid
	}
	return read, write, nil
}

func setProcessGroupForCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errProcessRetryProcessNotStarted
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	processRetryWindowsJobs.mu.Lock()
	processRetryWindowsJobs.jobs[cmd] = job
	processRetryWindowsJobs.mu.Unlock()
	return nil
}

func resumeProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if cmd.Process.Pid <= 0 {
		return errProcessRetryProcessNotStarted
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	resumed := false
	for {
		if entry.OwnerProcessID == uint32(cmd.Process.Pid) {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			closeErr := windows.CloseHandle(thread)
			if resumeErr != nil {
				return resumeErr
			}
			if closeErr != nil {
				return closeErr
			}
			resumed = true
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return err
		}
	}
	if !resumed {
		return errors.New("process retry child thread not found")
	}
	return nil
}

func attachProcessTree(cmd *exec.Cmd) error {
	processRetryWindowsJobs.mu.Lock()
	defer processRetryWindowsJobs.mu.Unlock()
	job, ok := processRetryWindowsJobs.jobs[cmd]
	if !ok || cmd == nil {
		return errProcessRetryProcessNotStarted
	}
	if cmd.Process == nil {
		return nil
	}
	if cmd.Process.Pid <= 0 {
		return errProcessRetryProcessNotStarted
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return windows.AssignProcessToJobObject(job, process)
}

func releaseProcessTree(cmd *exec.Cmd) error {
	processRetryWindowsJobs.mu.Lock()
	defer processRetryWindowsJobs.mu.Unlock()
	job, ok := processRetryWindowsJobs.jobs[cmd]
	if ok {
		delete(processRetryWindowsJobs.jobs, cmd)
	}
	if !ok {
		return nil
	}
	return windows.CloseHandle(job)
}

func terminateProcessTree(cmd *exec.Cmd) error {
	return terminateProcessRetryWindowsJob(cmd)
}

func killProcessTree(cmd *exec.Cmd) error {
	return terminateProcessRetryWindowsJob(cmd)
}

func terminateProcessRetryWindowsJob(cmd *exec.Cmd) error {
	processRetryWindowsJobs.mu.Lock()
	defer processRetryWindowsJobs.mu.Unlock()
	job, ok := processRetryWindowsJobs.jobs[cmd]
	if !ok {
		return killDirectChild(cmd)
	}
	return windows.TerminateJobObject(job, 1)
}
