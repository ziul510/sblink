package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	logLimit       = 1024 * 1024
	logLockTimeout = time.Second
)

type Logger struct {
	mu      sync.Mutex
	path    string
	console io.Writer
	err     error
}

func newLogger(base string, console io.Writer) *Logger {
	return &Logger{path: filepath.Join(base, "sblink.log"), console: console}
}

func (l *Logger) Log(tag, format string, args ...any) {
	l.LogContext(context.Background(), tag, format, args...)
}

func (l *Logger) LogContext(ctx context.Context, tag, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	line := fmt.Sprintf("[%s] [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), tag, fmt.Sprintf(format, args...))
	if l.console != nil {
		_, err := io.WriteString(l.console, line)
		if l.err == nil {
			l.err = err
		}
	}
	if err := l.append(ctx, diskText([]byte(line))); l.err == nil && (ctx.Err() == nil || !errors.Is(err, ctx.Err())) {
		l.err = err
	}
}

func (l *Logger) Err() error { l.mu.Lock(); defer l.mu.Unlock(); return l.err }

func (l *Logger) Console(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.console != nil {
		_, err := fmt.Fprintf(l.console, format, args...)
		if l.err == nil {
			l.err = err
		}
	}
}

func (l *Logger) append(ctx context.Context, line []byte) (err error) {
	ctx, cancel := context.WithTimeout(ctx, logLockTimeout)
	defer cancel()
	lock, err := lockFileContext(ctx, l.path+".lock")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	info, err := os.Stat(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info != nil && info.Size()+int64(len(line)) >= logLimit {
		if err := os.Remove(l.path + ".2"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(l.path+".1", l.path+".2"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(line)
	return errors.Join(err, f.Close())
}

type output struct {
	writer io.Writer
	err    error
}

func (o *output) printf(format string, args ...any) {
	if o.err == nil {
		_, o.err = fmt.Fprintf(o.writer, format, args...)
	}
}
