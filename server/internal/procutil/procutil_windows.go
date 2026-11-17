//go:build windows

package procutil

import (
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	createNoWindow           = 0x08000000
	belowNormalPriorityClass = 0x00004000
)

// Prepare stops ffmpeg/ffprobe from opening console windows, and makes
// cancelling the command's context kill its whole process tree: package
// managers (Chocolatey, Scoop) install ffmpeg as a shim that runs the real
// ffmpeg as a child, and killing only the shim would leave that running.
// KillChildrenWithServer covers the server itself exiting or crashing.
func Prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	cmd.SysProcAttr.HideWindow = true
	if cmd.Cancel != nil { // only set for exec.CommandContext
		cmd.Cancel = func() error {
			killDescendants(uint32(cmd.Process.Pid))
			return cmd.Process.Kill()
		}
		// Don't wait forever for output pipes a stray process keeps open.
		cmd.WaitDelay = 5 * time.Second
	}
}

// StartLow starts the child at below-normal priority, for background work
// (trickplay) that must not slow down playback or the desktop.
func StartLow(cmd *exec.Cmd) error {
	Prepare(cmd)
	cmd.SysProcAttr.CreationFlags |= belowNormalPriorityClass
	return cmd.Start()
}

// killDescendants terminates every process descending from pid (children
// first found, then theirs), leaving pid itself to the caller.
func killDescendants(pid uint32) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	children := map[uint32][]uint32{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID != e.ParentProcessID {
			children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
		}
	}
	queue := append([]uint32(nil), children[pid]...)
	seen := map[uint32]bool{pid: true}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		queue = append(queue, children[p]...)
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, p); err == nil {
			_ = windows.TerminateProcess(h, 1)
			windows.CloseHandle(h)
		}
	}
}

// job is the Job Object holding the server and every process it starts. It
// stays open for the life of the process; Windows closes it on exit.
var job windows.Handle

// KillChildrenWithServer puts the server in a Job Object that kills every
// process in it when its last handle closes, i.e. when the server exits or
// crashes (PLAN.md 7). Children, and their children (ffmpeg shims), join the
// job automatically when they start.
func KillChildrenWithServer() error {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return err
	}
	if err := windows.AssignProcessToJobObject(h, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(h)
		return err
	}
	job = h
	return nil
}
