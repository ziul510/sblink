package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUnavailableAddonStaysRetryable(t *testing.T) {
	d := testDaemon(t)
	target := makeDir(t, filepath.Join(d.base, "offline-addon"))
	link := filepath.Join(d.config.text("addon_library_path"), "KMIA-offline")
	if err := makeJunction(link, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := d.poll(context.Background(), false); err == nil {
		t.Fatal("unavailable addon scan succeeded")
	}
	saved, err := loadConfig(d.base)
	if err != nil {
		t.Fatal(err)
	}
	if d.memory != nil || saved.identity("last_processed_plan_id") != nil {
		t.Fatal("unavailable addon marked as processed")
	}
	makeDir(t, target)
	if err := d.poll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if d.memory == nil {
		t.Fatal("recovered addon was not retried")
	}
	if err := d.unlink(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManualMappingThroughJunction(t *testing.T) {
	library := t.TempDir()
	target := t.TempDir()
	makeDir(t, filepath.Join(target, "special"))
	for _, alias := range []string{"first", "shared"} {
		if err := makeJunction(filepath.Join(library, alias), target); err != nil {
			t.Fatal(err)
		}
	}
	if err := makeJunction(filepath.Join(target, "loop"), library); err != nil {
		t.Fatal(err)
	}
	makeDir(t, filepath.Join(library, "KMIA-airport"))
	for _, pattern := range []string{"special", filepath.Join("shared", "special"), "absent"} {
		mappings := newDocument()
		mappings.set("A321", pattern)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		got, err := findMatches(ctx, library, &Plan{Aircraft: "A321", Origin: "KMIA"}, mappings, testLogger(t))
		cancel()
		if err != nil || len(got) != 1 {
			t.Fatalf("mapping %q: %v %v", pattern, got, err)
		}
		want := filepath.Join(library, "first", "special")
		if pattern == "absent" {
			want = filepath.Join(library, "KMIA-airport")
		} else if pattern != "special" {
			want = filepath.Join(library, "shared", "special")
		}
		if got[0] != want {
			t.Fatalf("mapping %q: got %s want %s", pattern, got[0], want)
		}
	}
}

func TestConsoleRedirection(t *testing.T) {
	if os.Getenv("SBLINK_REDIRECTION_HELPER") == "1" {
		if err := ensureConsole(false, true); err != nil {
			t.Fatal(err)
		}
		input, output, stderr := os.Stdin, os.Stdout, os.Stderr
		defer func() { os.Stdin, os.Stdout, os.Stderr = input, output, stderr }()
		for _, device := range []bool{false, true} {
			for _, mode := range []string{"stdin", "stdout", "both"} {
				path := os.DevNull
				if !device {
					path = filepath.Join(t.TempDir(), "stream")
				}
				file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				os.Stdin, os.Stdout, os.Stderr = input, output, stderr
				if mode != "stdout" {
					os.Stdin = file
				}
				if mode != "stdin" {
					os.Stdout = file
				}
				beforeInput, beforeOutput := os.Stdin, os.Stdout
				handled, _, err := defaultTerminal("unused", []string{"invalid argument"}, false)
				if handled || err != nil {
					t.Errorf("%s device=%v delegated: %v", mode, device, err)
				}
				if err := ensureConsole(false, false); err != nil {
					t.Error(err)
				}
				if mode != "stdout" && os.Stdin != beforeInput || mode != "stdin" && os.Stdout != beforeOutput {
					t.Errorf("%s device=%v replaced redirected handles", mode, device)
				}
				os.Stdin, os.Stdout, os.Stderr = input, output, stderr
				if err := file.Close(); err != nil {
					t.Error(err)
				}
			}
		}
		closed, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		if err := closed.Close(); err != nil {
			t.Fatal(err)
		}
		os.Stdin, os.Stdout = closed, closed
		if handled, _, err := defaultTerminal("unused", []string{"invalid argument"}, false); !handled || err == nil {
			t.Error("invalid handles suppressed console delegation")
		}
		if err := ensureConsole(false, false); err != nil {
			t.Error(err)
		}
		if !consoleMode(os.Stdin) || !consoleMode(os.Stdout) {
			t.Error("invalid handles were not rebound")
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestConsoleRedirection$")
	cmd.Env = append(os.Environ(), "SBLINK_REDIRECTION_HELPER=1")
	cmd.Stdin = strings.NewReader("")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x10, HideWindow: true}
	if data, err := cmd.CombinedOutput(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatal("console redirection helper timed out")
		}
		t.Fatalf("console redirection: %v %s", err, data)
	}
}
