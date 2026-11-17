//go:build windows

package watch

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"github.com/acip/minimedia/server/internal/fsutil"
)

const notifyFilter = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
	windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE

// ioState lives on the heap for the whole watch: the kernel writes into the
// buffer and OVERLAPPED after ReadDirectoryChangesW returns, so they must
// never move (goroutine stacks can).
type ioState struct {
	ov  windows.Overlapped
	buf [64 * 1024]byte // 64 KB is the maximum over SMB; must be DWORD-aligned
}

func watchTree(ctx context.Context, root string, fn Func, ready func()) error {
	path, err := windows.UTF16PtrFromString(fsutil.LongPath(root))
	if err != nil {
		return err
	}
	// Share everything, so the user can still rename and delete inside the tree.
	h, err := windows.CreateFile(path, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return fmt.Errorf("open folder: %w", err)
	}
	defer windows.CloseHandle(h)

	ioDone, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ioDone)
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(stop)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			windows.SetEvent(stop)
		case <-finished:
		}
	}()

	st := new(ioState)
	first := true
	for {
		st.ov = windows.Overlapped{HEvent: ioDone}
		windows.ResetEvent(ioDone)
		if err := windows.ReadDirectoryChanges(h, &st.buf[0], uint32(len(st.buf)), true, notifyFilter, nil, &st.ov, 0); err != nil {
			return fmt.Errorf("ReadDirectoryChangesW: %w", err)
		}
		if first {
			first = false
			ready()
		}
		ev, err := windows.WaitForMultipleObjects([]windows.Handle{ioDone, stop}, false, windows.INFINITE)
		if err != nil {
			return err
		}
		if ev == windows.WAIT_OBJECT_0+1 {
			// Cancel the pending read and wait for it to finish before st can be freed.
			windows.CancelIoEx(h, &st.ov)
			var n uint32
			_ = windows.GetOverlappedResult(h, &st.ov, &n, true)
			return nil
		}
		var n uint32
		if err := windows.GetOverlappedResult(h, &st.ov, &n, false); err != nil {
			if errors.Is(err, windows.ERROR_NOTIFY_ENUM_DIR) {
				fn("") // too many changes for the buffer
				continue
			}
			return err
		}
		if n == 0 {
			fn("") // buffer overflow: changes were lost
			continue
		}
		for _, rel := range parseNotify(st.buf[:n]) {
			fn(rel)
		}
	}
}

// parseNotify decodes a chain of FILE_NOTIFY_INFORMATION records into the
// relative paths they name (with backslashes, as Windows reports them).
func parseNotify(b []byte) []string {
	var out []string
	off := 0
	for off+12 <= len(b) {
		next := int(binary.LittleEndian.Uint32(b[off:]))
		nameLen := int(binary.LittleEndian.Uint32(b[off+8:]))
		start := off + 12
		if nameLen < 0 || start+nameLen > len(b) {
			break
		}
		u := make([]uint16, nameLen/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(b[start+2*i:])
		}
		out = append(out, string(utf16.Decode(u)))
		if next == 0 {
			break
		}
		off += next
	}
	return out
}
