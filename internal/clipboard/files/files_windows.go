//go:build windows && !clipship_fake

package files

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const cfHDROP = 15

var (
	modUser32  = windows.NewLazySystemDLL("user32.dll")
	modShell32 = windows.NewLazySystemDLL("shell32.dll")
	modKernel  = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard    = modUser32.NewProc("OpenClipboard")
	procCloseClipboard   = modUser32.NewProc("CloseClipboard")
	procGetClipboardData = modUser32.NewProc("GetClipboardData")

	procDragQueryFileW = modShell32.NewProc("DragQueryFileW")

	procGlobalLock   = modKernel.NewProc("GlobalLock")
	procGlobalUnlock = modKernel.NewProc("GlobalUnlock")
)

// ReadFiles reads CF_HDROP from the clipboard and returns an Entry per path.
func ReadFiles() ([]Entry, error) {
	ok, _, _ := procOpenClipboard.Call(0)
	if ok == 0 {
		return nil, fmt.Errorf("OpenClipboard failed: %w", syscall.GetLastError())
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(uintptr(cfHDROP))
	if h == 0 {
		return nil, ErrNoFiles
	}

	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, fmt.Errorf("GlobalLock failed: %w", syscall.GetLastError())
	}
	defer procGlobalUnlock.Call(h)

	count, _, _ := procDragQueryFileW.Call(p, 0xFFFFFFFF, 0, 0)
	if count == 0 {
		return nil, ErrNoFiles
	}

	paths := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		length, _, _ := procDragQueryFileW.Call(p, i, 0, 0)
		if length == 0 {
			continue
		}
		buf := make([]uint16, length+1)
		n, _, _ := procDragQueryFileW.Call(
			p, i,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(len(buf)),
		)
		if n == 0 {
			continue
		}
		paths = append(paths, windows.UTF16ToString(buf[:n]))
	}
	if len(paths) == 0 {
		return nil, ErrNoFiles
	}
	return entriesFromPaths(paths), nil
}
