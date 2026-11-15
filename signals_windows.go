//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

func consoleControl(cancel context.CancelFunc) (func() error, error) {
	// Console allocation resets the handlers installed by Go
	finished := make(chan struct{})
	failures := make(chan error, 1)
	callback := syscall.NewCallback(func(event uint32) uintptr {
		if event != 0 && event != 1 && event != 2 && event != 5 && event != 6 {
			return 0
		}
		cancel()
		// Windows ends the process when a close handler returns
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			if err := cancelConsoleInput(); err != nil {
				select {
				case failures <- err:
				default:
				}
			}
			select {
			case <-finished:
				return 1
			case <-timer.C:
				return 1
			case <-ticker.C:
			}
		}
	})
	setHandler := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
	if r, _, err := setHandler.Call(callback, 1); r == 0 {
		return nil, err
	}
	return func() error {
		close(finished)
		var err error
		if r, _, e := setHandler.Call(callback, 0); r == 0 {
			err = e
		}
		select {
		case e := <-failures:
			err = errors.Join(err, e)
		default:
		}
		return err
	}, nil
}

func cancelConsoleInput() error {
	err := syscall.CancelIoEx(syscall.Handle(os.Stdin.Fd()), nil)
	if errors.Is(err, syscall.ERROR_NOT_FOUND) || errors.Is(err, syscall.Errno(6)) {
		return nil
	}
	return err
}
