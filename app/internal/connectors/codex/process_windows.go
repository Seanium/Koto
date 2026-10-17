package codex

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type serverProcessTree struct{ job windows.Handle }

func startServerProcessTree(cmd *exec.Cmd) (_ *serverProcessTree, err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = windows.CloseHandle(job)
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, err
	}
	// Assign before any launcher code runs, so even immediate child processes
	// belong to our job. Go closes the primary thread handle after Start; reopen
	// that suspended thread by its exact owning PID to resume it below.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, err
	}
	err = windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if err != nil {
		return nil, err
	}
	if err = resumeServerProcess(uint32(cmd.Process.Pid)); err != nil {
		return nil, err
	}
	return &serverProcessTree{job: job}, nil
}

func resumeServerProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		return err
	}
	return fmt.Errorf("cannot resume Codex process %d: %w", pid, err)
}

func (p *serverProcessTree) kill() { _ = windows.TerminateJobObject(p.job, 1) }

func (p *serverProcessTree) wait() {
	defer windows.CloseHandle(p.job)
	// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION has four LARGE_INTEGER values,
	// followed by page faults and the total/active/terminated process counts.
	var accounting struct {
		Times                                          [4]int64
		PageFaults, TotalProcesses, Active, Terminated uint32
	}
	for {
		err := windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
		if err != nil || accounting.Active == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
