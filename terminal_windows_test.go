//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func processTree(t *testing.T, root int) []syscall.ProcessEntry32 {
	t.Helper()
	handle, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	var all []syscall.ProcessEntry32
	entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
	err = syscall.Process32First(handle, &entry)
	for err == nil {
		all = append(all, entry)
		err = syscall.Process32Next(handle, &entry)
	}
	if !errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
		t.Error(err)
	}
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	result := []syscall.ProcessEntry32{{ProcessID: uint32(root)}}
	seen := map[uint32]bool{uint32(root): true}
	for i := 0; i < len(result); i++ {
		for _, candidate := range all {
			if candidate.ParentProcessID == result[i].ProcessID && !seen[candidate.ProcessID] {
				seen[candidate.ProcessID] = true
				result = append(result, candidate)
			}
		}
	}
	return result
}

func TestTerminalBinding(t *testing.T) {
	if path := os.Getenv("SBLINK_BINDING_REPORT"); path != "" {
		if consoleMode(os.Stdout) {
			t.Fatal("helper stdout must start redirected")
		}
		if err := ensureConsole(false, true); err != nil {
			t.Fatal(err)
		}
		width, tty := setupTerminal(os.Stdout)
		if !tty || !consoleMode(os.Stdin) || !consoleMode(os.Stderr) {
			t.Fatal("terminal handles were not rebound")
		}
		ui := setupUI{out: &output{writer: os.Stdout}, width: width, tty: tty}
		ui.screen(1)
		var mode uint32
		if err := syscall.GetConsoleMode(syscall.Handle(os.Stdout.Fd()), &mode); err != nil {
			t.Fatal(err)
		}
		if mode&4 == 0 || !strings.Contains(ui.color("banner", "test"), "38;2;95;215;255") {
			t.Fatal("native terminal colors disabled")
		}
		data, err := json.Marshal(struct {
			Width int
			Color bool
		}{width, true})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "terminal.json")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestTerminalBinding$")
	cmd.Env = append(os.Environ(), "SBLINK_BINDING_REPORT="+path, "NO_COLOR=1")
	cmd.Stdin = strings.NewReader("")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x10}
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native binding: %v %s", err, data)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Native terminal report: %s", data)
}

func consolePID(t *testing.T, root int) int {
	t.Helper()
	result := root
	for _, entry := range processTree(t, root) {
		if strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), "sblink.exe") {
			result = int(entry.ProcessID)
		}
	}
	return result
}

func findTray(t *testing.T, root int) (uintptr, int) {
	t.Helper()
	user := syscall.NewLazyDLL("user32.dll")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, entry := range processTree(t, root) {
			name, err := syscall.UTF16PtrFromString(fmt.Sprintf("sblink.tray.%d", entry.ProcessID))
			if err != nil {
				t.Fatal(err)
			}
			window, _, _ := user.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(name)), 0)
			if window != 0 {
				return window, int(entry.ProcessID)
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("tray window missing")
	return 0, root
}

func testTerminalLifetime(t *testing.T, executable string) {
	t.Helper()
	directory := makeDir(t, filepath.Join(t.TempDir(), "portable & 100%! test"))
	path := filepath.Join(directory, "sblink.exe")
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "setup", "--new-console")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	root := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var child int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		child = consolePID(t, root)
		if child != root {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if child == root {
		cancel()
		<-done
		t.Fatal("quoted executable did not start in terminal")
	}
	time.Sleep(300 * time.Millisecond)
	probeConsole(t, child, true)
	process, err := os.FindProcess(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-done
	closed := make(chan error, 1)
	go func() { _, err := process.Wait(); closed <- err }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		killChild(t, child)
		t.Fatal("terminal input survived its launcher")
	}
}
