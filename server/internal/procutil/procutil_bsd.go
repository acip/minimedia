//go:build !windows && !linux

package procutil

import "syscall"

// dieWithServer has no equivalent here (no parent-death signal on macOS/BSD).
func dieWithServer(*syscall.SysProcAttr) {}
