//go:build !windows

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func setupTerminal(io.Writer) (int, bool) { return 80, false }

func pathKey(path string) string { return filepath.Clean(path) }

func diskText(data []byte) []byte             { return data }
func resolvePath(path string) (string, error) { return filepath.EvalSymlinks(path) }
func replaceFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
func isLink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
func makeJunction(link, target string) error                    { return errors.New("mklink is unavailable on this OS") }
func msfsRunning(ctx context.Context) (bool, error)             { return false, ctx.Err() }
func ensureConsole(bool, bool) error                            { return nil }
func defaultTerminal(string, []string, bool) (bool, int, error) { return false, 0, nil }
func logOwner() (func() bool, func() error, error) {
	return func() bool { return true }, func() error { return nil }, nil
}
func watchTerminal(context.Context, context.CancelFunc) (func() error, error) {
	return func() error { return nil }, nil
}
func consoleTitle(string) error { return nil }
func consoleControl(context.CancelFunc) (func() error, error) {
	return func() error { return nil }, nil
}
func spawn(executable string, args ...string) error {
	cmd := exec.Command(executable, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
func startupEnabled() (bool, error) { return false, nil }
func configureStartup(string, bool) error {
	return errors.New("Windows startup options are unavailable on this OS")
}
func configureShortcut(string, bool) error {
	return errors.New("Windows startup options are unavailable on this OS")
}
func hasWindowsIntegration() bool    { return false }
func portableShortcuts(string) error { return nil }
func runTray(ctx context.Context, executable string, actions chan<- string, log *Logger, shutdown func()) error {
	<-ctx.Done()
	return nil
}
