package procutil

import "syscall"

// dieWithServer has the kernel kill the child if the server exits without
// stopping it (killed, or crashed).
func dieWithServer(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
