//go:build darwin || linux

package power

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"
)

// On macOS and Linux sleep is held off by a helper process that the OS
// tracks: caffeinate on macOS, systemd-inhibit on Linux. It runs while the TV
// streams and is stopped afterwards. Its standard input is a pipe from this
// process, so if the server dies without cleaning up, the pipe closes and
// the helper exits on its own instead of keeping the computer awake forever.
// All calls come from Keeper.Run's goroutine.
var (
	helper     *exec.Cmd
	helperIn   io.Closer
	helperDone chan error
)

func setAwake(awake bool) error {
	if !awake {
		stopHelper()
		return nil
	}
	if helper != nil {
		select {
		case <-helperDone: // it exited on its own: start a new one
			helper = nil
		default:
			return nil
		}
	}
	cmd := exec.Command(inhibitCommand[0], inhibitCommand[1:]...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// A helper that can't hold the lock (missing, or not allowed) exits at once.
	select {
	case err := <-done:
		in.Close()
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return errors.New(inhibitCommand[0] + ": " + msg)
	case <-time.After(300 * time.Millisecond):
	}
	helper, helperIn, helperDone = cmd, in, done
	return nil
}

func stopHelper() {
	if helper == nil {
		return
	}
	helperIn.Close()
	_ = helper.Process.Kill()
	<-helperDone
	helper = nil
}
