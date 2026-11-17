//go:build windows

package power

import (
	"errors"

	"golang.org/x/sys/windows"
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// setAwake holds off (or allows) system sleep for the calling thread. The
// display may still turn off; the TV is the screen.
func setAwake(awake bool) error {
	flags := uintptr(esContinuous)
	if awake {
		flags |= esSystemRequired
	}
	if r, _, err := setThreadExecutionState.Call(flags); r == 0 {
		return errors.Join(errors.New("SetThreadExecutionState failed"), err)
	}
	return nil
}
