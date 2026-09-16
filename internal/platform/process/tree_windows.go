//go:build windows

package process

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var kernel32 = windows.NewLazySystemDLL("kernel32.dll")
var threadFirst = kernel32.NewProc("Thread32First")
var threadNext = kernel32.NewProc("Thread32Next")
var openThread = kernel32.NewProc("OpenThread")
var resumeThread = kernel32.NewProc("ResumeThread")

type threadEntry struct {
	Size, Usage, ID, Owner uint32
	Priority, Delta        int32
	Flags                  uint32
}

func prepare(command *exec.Cmd) (func() error, func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, nil, err
	}
	// Suspend before assigning to the job: no child can escape between Start and assignment.
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED, HideWindow: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return windows.TerminateJobObject(job, 1)
	}
	install := func() error {
		process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
		if err != nil {
			return err
		}
		defer windows.CloseHandle(process)
		if err = windows.AssignProcessToJobObject(job, process); err != nil {
			return err
		}
		snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
		if err != nil {
			return err
		}
		defer windows.CloseHandle(snapshot)
		entry := threadEntry{Size: uint32(unsafe.Sizeof(threadEntry{}))}
		ok, _, _ := threadFirst.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
		for ok != 0 {
			if entry.Owner == uint32(command.Process.Pid) {
				thread, _, err := openThread.Call(0x0002, 0, uintptr(entry.ID)) // THREAD_SUSPEND_RESUME
				if thread == 0 {
					return err
				}
				result, _, resumeErr := resumeThread.Call(thread)
				windows.CloseHandle(windows.Handle(thread))
				if result == 0xffffffff {
					return resumeErr
				}
				return nil
			}
			ok, _, _ = threadNext.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
		}
		return syscall.ESRCH
	}
	return install, func() { windows.CloseHandle(job) }, nil
}
