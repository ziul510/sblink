//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

type windowClass struct {
	Size, Style                        uint32
	Procedure                          uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type notificationIcon struct {
	Size               uint32
	Window             uintptr
	ID, Flags, Message uint32
	Icon               uintptr
	Tip                [128]uint16
	State, StateMask   uint32
	Info               [256]uint16
	Version            uint32
	InfoTitle          [64]uint16
	InfoFlags          uint32
	GUID               [16]byte
	BalloonIcon        uintptr
}
type point struct{ X, Y int32 }
type windowMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}

func hasWindowsIntegration() bool { return true }

func runTray(ctx context.Context, executable string, actions chan<- string, log *Logger, shutdown func()) (err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, cancel := context.WithCancel(ctx)
	var viewers sync.WaitGroup
	defer func() { cancel(); viewers.Wait() }()
	user := syscall.NewLazyDLL("user32.dll")
	shell := syscall.NewLazyDLL("shell32.dll")
	kernel := syscall.NewLazyDLL("kernel32.dll")
	className, err := syscall.UTF16PtrFromString(fmt.Sprintf("sblink.tray.%d", os.Getpid()))
	if err != nil {
		return err
	}
	instance, _, callErr := kernel.NewProc("GetModuleHandleW").Call(0)
	if instance == 0 {
		return callErr
	}
	icon, err := trayIcon(user, instance)
	if err != nil {
		return err
	}
	defer func() {
		r, _, e := user.NewProc("DestroyIcon").Call(icon)
		if r == 0 {
			err = errors.Join(err, e)
		}
	}()
	var window uintptr
	notification := notificationIcon{Size: uint32(unsafe.Sizeof(notificationIcon{})), ID: 1, Flags: 1 | 2 | 4, Message: 0x8001, Icon: icon}
	copy(notification.Tip[:], syscall.StringToUTF16("sblink"))
	taskbarText, err := syscall.UTF16PtrFromString("TaskbarCreated")
	if err != nil {
		return err
	}
	taskbarMessage, _, callErr := user.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(taskbarText)))
	if taskbarMessage == 0 {
		return callErr
	}
	show := func() {
		cmd := exec.CommandContext(ctx, executable, "logs")
		cmd.Env = append(os.Environ(), fmt.Sprintf("SBLINK_LOG_OWNER=%d", os.Getpid()))
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x8}
		if e := cmd.Start(); e != nil {
			log.LogContext(ctx, "ERROR", "Show console: %v", e)
			return
		}
		viewers.Add(1)
		go func() {
			defer viewers.Done()
			if e := cmd.Wait(); e != nil && ctx.Err() == nil {
				log.LogContext(ctx, "INFO", "Log console closed: %v", e)
			}
		}()
	}
	dispatch := func(id uintptr) {
		switch id {
		case 1:
			if e := spawn(executable, "setup", "--new-console"); e != nil {
				log.LogContext(ctx, "ERROR", "Run setup: %v", e)
			}
		case 2:
			show()
		case 3, 4:
			action := "link"
			if id == 4 {
				action = "unlink"
			}
			select {
			case actions <- action:
			default:
				log.LogContext(ctx, "WARNING", "A manual action is already queued")
			}
		case 5:
			user.NewProc("PostQuitMessage").Call(0)
		}
	}
	menu := func() (menuErr error) {
		handle, _, e := user.NewProc("CreatePopupMenu").Call()
		if handle == 0 {
			return e
		}
		defer func() {
			r, _, e := user.NewProc("DestroyMenu").Call(handle)
			if r == 0 {
				menuErr = errors.Join(menuErr, e)
			}
		}()
		for _, item := range []struct {
			id    uintptr
			label string
		}{{1, "Run setup"}, {2, "Show"}, {0, ""}, {3, "Link Addons"}, {4, "Unlink Addons"}, {0, ""}, {5, "Quit"}} {
			label, e := syscall.UTF16PtrFromString(item.label)
			if e != nil {
				return e
			}
			flags := uintptr(0)
			if item.id == 0 {
				flags = 0x800
			}
			r, _, e := user.NewProc("AppendMenuW").Call(handle, flags, item.id, uintptr(unsafe.Pointer(label)))
			if r == 0 {
				return e
			}
		}
		var cursor point
		r, _, e := user.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
		if r == 0 {
			return e
		}
		if r, _, e := user.NewProc("SetForegroundWindow").Call(window); r == 0 {
			log.LogContext(ctx, "WARNING", "Could not focus tray menu: %v", e)
		}
		id, _, _ := user.NewProc("TrackPopupMenu").Call(handle, 0x100|0x2, uintptr(cursor.X), uintptr(cursor.Y), 0, window, 0)
		if id != 0 {
			dispatch(id)
		}
		if r, _, e := user.NewProc("PostMessageW").Call(window, 0, 0, 0); r == 0 {
			return e
		}
		return nil
	}
	procedure := syscall.NewCallback(func(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
		switch {
		case message == 0x8001:
			if lparam == 0x205 || lparam == 0x7b {
				if e := menu(); e != nil {
					log.LogContext(ctx, "ERROR", "Tray menu: %v", e)
				}
			}
			if lparam == 0x203 {
				show()
			}
			return 0
		case message == 0x111:
			dispatch(wparam & 0xffff)
			return 0
		case message == 0x113:
			if ctx.Err() != nil {
				user.NewProc("PostQuitMessage").Call(0)
			}
			return 0
		case message == 0x10:
			user.NewProc("PostQuitMessage").Call(0)
			return 0
		case message == 0x11:
			return 1
		case message == 0x16:
			if wparam != 0 {
				shutdown()
				user.NewProc("PostQuitMessage").Call(0)
			}
			return 0
		case message == uint32(taskbarMessage):
			r, _, e := shell.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&notification)))
			if r == 0 {
				log.LogContext(ctx, "ERROR", "Restore tray icon: %v", e)
			}
			return 0
		}
		r, _, _ := user.NewProc("DefWindowProcW").Call(hwnd, uintptr(message), wparam, lparam)
		return r
	})
	class := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Procedure: procedure, Instance: instance, Name: className, Icon: icon}
	atom, _, callErr := user.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return callErr
	}
	defer func() {
		r, _, e := user.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(className)), instance)
		if r == 0 {
			err = errors.Join(err, e)
		}
	}()
	window, _, callErr = user.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if window == 0 {
		return callErr
	}
	defer func() {
		r, _, e := user.NewProc("DestroyWindow").Call(window)
		if r == 0 {
			err = errors.Join(err, e)
		}
	}()
	notification.Window = window
	r, _, callErr := shell.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&notification)))
	if r == 0 {
		return fmt.Errorf("add tray icon: %w", callErr)
	}
	defer func() {
		r, _, e := shell.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&notification)))
		if r == 0 {
			err = errors.Join(err, e)
		}
	}()
	r, _, callErr = user.NewProc("SetTimer").Call(window, 1, 250, 0)
	if r == 0 {
		return callErr
	}
	defer func() {
		r, _, e := user.NewProc("KillTimer").Call(window, 1)
		if r == 0 {
			err = errors.Join(err, e)
		}
	}()
	var message windowMessage
	for {
		r, _, e := user.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(r) == -1 {
			return e
		}
		if r == 0 {
			return nil
		}
		user.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
		user.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
	}
}

func trayIcon(user *syscall.LazyDLL, instance uintptr) (uintptr, error) {
	const size = 64
	mask := make([]byte, size*size/8)
	pixels := make([]byte, size*size*4)
	inside := func(x, y, left, right float64) bool {
		dx := max(left+9-x, 0, x-(right-9))
		dy := max(28-y, 0, y-36)
		return dx*dx+dy*dy <= 81
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := 4 * (y*size + x)
			pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = 80, 32, 12, 255
			for _, bounds := range [][2]float64{{5, 35}, {29, 59}} {
				if inside(float64(x), float64(y), bounds[0], bounds[1]) && !(x >= int(bounds[0])+5 && x <= int(bounds[1])-5 && y >= 24 && y <= 40) {
					pixels[i], pixels[i+1], pixels[i+2] = 255, 215, 95
				}
			}
			if x >= 27 && x <= 37 && y >= 25 && y <= 39 {
				pixels[i], pixels[i+1], pixels[i+2] = 80, 32, 12
			}
		}
	}
	icon, _, err := user.NewProc("CreateIcon").Call(instance, size, size, 1, 32, uintptr(unsafe.Pointer(&mask[0])), uintptr(unsafe.Pointer(&pixels[0])))
	if icon == 0 {
		return 0, err
	}
	return icon, nil
}
