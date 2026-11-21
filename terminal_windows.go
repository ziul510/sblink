//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

func setupTerminal(writer io.Writer) (int, bool) {
	f, ok := writer.(*os.File)
	if !ok || !consoleMode(f) {
		return 80, false
	}
	var mode uint32
	if err := syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode); err != nil {
		return 80, false
	}
	if r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode").Call(f.Fd(), uintptr(mode|4)); r == 0 {
		return 80, false
	}
	var info struct {
		Size, Cursor [2]int16
		Attributes   uint16
		Window       [4]int16
		Maximum      [2]int16
	}
	r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleScreenBufferInfo").Call(f.Fd(), uintptr(unsafe.Pointer(&info)))
	if r != 0 {
		return max(20, int(info.Window[2]-info.Window[0]+1)), true
	}
	return 80, true
}

func consoleMode(file *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(file.Fd()), &mode) == nil
}

func redirectedConsole() bool {
	return redirectedStream(os.Stdin) || redirectedStream(os.Stdout)
}

func redirectedStream(file *os.File) bool {
	_, err := file.Stat()
	return err == nil && !consoleMode(file)
}

func defaultTerminal(executable string, args []string, fresh bool) (bool, int, error) {
	if !fresh && (consoleMode(os.Stdout) || redirectedConsole()) {
		return false, 0, nil
	}
	processor := os.Getenv("ComSpec")
	if processor == "" {
		processor = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	for _, arg := range args {
		if strings.ContainsAny(arg, "\"&|<>^%! \t\r\n") {
			return true, 1, fmt.Errorf("invalid console argument: %q", arg)
		}
	}
	// A console process uses Windows default terminal delegation
	script := `start /b /wait "" "%SBLINK_CONSOLE_EXE%" ` + strings.Join(args, " ") + ` --terminal`
	cmd := exec.Command(processor)
	cmd.Env = append(os.Environ(), "SBLINK_CONSOLE_EXE="+executable, fmt.Sprintf("SBLINK_CONSOLE_OWNER=%d", os.Getpid()))
	cmd.Dir = filepath.Dir(executable)
	return startTerminalProcess(processor, syscall.EscapeArg(processor)+" /d /v:off /s /c "+script, cmd.Environ(), cmd.Dir)
}

func startTerminalProcess(processor, line string, env []string, directory string) (handled bool, code int, err error) {
	app, err := syscall.UTF16PtrFromString(processor)
	if err != nil {
		return true, 1, err
	}
	command, err := syscall.UTF16PtrFromString(line)
	if err != nil {
		return true, 1, err
	}
	dir, err := syscall.UTF16PtrFromString(directory)
	if err != nil {
		return true, 1, err
	}
	block := utf16.Encode([]rune(strings.Join(env, "\x00") + "\x00\x00"))
	startup := syscall.StartupInfo{Cb: uint32(unsafe.Sizeof(syscall.StartupInfo{})), Flags: syscall.STARTF_USESHOWWINDOW, ShowWindow: syscall.SW_SHOWNORMAL}
	var process syscall.ProcessInformation
	if err := syscall.CreateProcess(app, command, nil, nil, false, 0x10|0x400, &block[0], dir, &startup, &process); err != nil {
		return true, 1, err
	}
	defer func() {
		err = errors.Join(err, syscall.CloseHandle(process.Process), syscall.CloseHandle(process.Thread))
	}()
	if _, err := syscall.WaitForSingleObject(process.Process, syscall.INFINITE); err != nil {
		return true, 1, err
	}
	var status uint32
	if err := syscall.GetExitCodeProcess(process.Process, &status); err != nil {
		return true, 1, err
	}
	return true, int(status), nil
}

func logOwner() (func() bool, func() error, error) {
	return processOwner("SBLINK_LOG_OWNER")
}

func processOwner(variable string) (func() bool, func() error, error) {
	value := os.Getenv(variable)
	if value == "" {
		return func() bool { return true }, func() error { return nil }, nil
	}
	pid, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return nil, nil, err
	}
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return func() bool { return false }, func() error { return nil }, nil
	}
	return func() bool {
		state, err := syscall.WaitForSingleObject(handle, 0)
		return err == nil && state == syscall.WAIT_TIMEOUT
	}, func() error { return syscall.CloseHandle(handle) }, nil
}

func watchTerminal(ctx context.Context, cancel context.CancelFunc) (func() error, error) {
	alive, closeOwner, err := processOwner("SBLINK_CONSOLE_OWNER")
	if err != nil {
		return nil, err
	}
	watch, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	failures := make(chan error, 1)
	go func() {
		defer close(done)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil || !alive() {
				cancel()
				if err := cancelConsoleInput(); err != nil {
					select {
					case failures <- err:
					default:
					}
				}
			}
			select {
			case <-watch.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() error {
		stop()
		<-done
		var err error
		select {
		case err = <-failures:
		default:
		}
		return errors.Join(err, closeOwner())
	}, nil
}
