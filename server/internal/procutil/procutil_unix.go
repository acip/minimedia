//go:build !windows

package procutil

import (
	"os/exec"
	"syscall"
	"time"
)

// Prepare puts the child in its own process group, and makes cancelling the
// command's context kill that whole group.
func Prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	dieWithServer(cmd.SysProcAttr)
	if cmd.Cancel != nil { // only set for exec.CommandContext
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 5 * time.Second
	}
}

// KillChildrenWithServer is a no-op outside Windows: children run in their
// own process groups, which shutdown and cancellation kill. If the server is
// killed outright, Linux kills them too (dieWithServer); on macOS a running
// ffmpeg finishes its current job and exits.
func KillChildrenWithServer() error { return nil }

// StartLow starts the child and lowers its priority (nice 10), for background
// work (trickplay) that must not slow down playback.
func StartLow(cmd *exec.Cmd) error {
	Prepare(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 10)
	return nil
}
