// Package procutil prepares child processes (ffmpeg/ffprobe) consistently.
package procutil

import (
	"context"
	"os/exec"
)

// Command builds an exec.Cmd bound to ctx with the platform settings applied
// (no console window on Windows, own process group on Unix).
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	Prepare(cmd)
	return cmd
}
