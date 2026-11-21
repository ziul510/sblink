//go:build unix || linux || darwin || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"errors"
	"os"
	"syscall"
)

func lockFile(path string, wait bool) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	flags := syscall.LOCK_EX
	if !wait {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), flags); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

func lockBusy(err error) bool { return errors.Is(err, syscall.EWOULDBLOCK) }
