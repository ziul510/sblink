package main

import (
	"context"
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildExecutable(t *testing.T) string {
	t.Helper()
	name := "sblink"
	flags := "-s -w"
	if hasWindowsIntegration() {
		name += ".exe"
		flags += " -H=windowsgui"
	}
	path := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", flags, "-o", path, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, data)
	}
	return path
}

func runExecutable(t *testing.T, executable, input string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdin = strings.NewReader(input)
	// A clean PATH proves the executable does not invoke Go or Python
	cmd.Env = append(os.Environ(), "APPDATA="+filepath.Join(filepath.Dir(executable), "appdata"), "PATH="+os.Getenv("SystemRoot")+`\System32`)
	data, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("command timed out: %v\n%s", args, data)
	}
	if err == nil {
		return string(data), 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return string(data), exit.ExitCode()
	}
	t.Fatalf("launch %v: %v", args, err)
	return "", -1
}

func TestExecutableContract(t *testing.T) {
	executable := buildExecutable(t)
	base := filepath.Dir(executable)
	t.Run("no argument starts setup without config", func(t *testing.T) {
		got, code := runExecutable(t, executable, "")
		if code != 1 || !strings.Contains(got, "1 / 5  MSFS Paths") {
			t.Fatalf("code %d\n%s", code, got)
		}
		if _, err := os.Stat(filepath.Join(base, configName)); !os.IsNotExist(err) {
			t.Fatal("interrupted setup created config")
		}
	})
	t.Run("status clean directory", func(t *testing.T) {
		got, code := runExecutable(t, executable, "", "status")
		prefix := "Configuration\nCommunity Folder: -\nAddon Library: -\nSimBrief User ID: -\nPoll Interval: 60 s\n\nLast Processed\nPlan ID: -\nTimestamp: -\n\nManual Mappings\n"
		if code != 0 || !strings.HasPrefix(strings.ReplaceAll(got, "\r\n", "\n"), prefix) {
			t.Fatalf("code %d\n%s", code, got)
		}
		if _, err := os.Stat(filepath.Join(base, "mappings.json")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("manual incomplete", func(t *testing.T) {
		got, code := runExecutable(t, executable, "", "manual")
		if code != 1 || !strings.Contains(got, "[ERROR] Configuration incomplete. Run 'sblink.exe setup' first") {
			t.Fatalf("code %d\n%s", code, got)
		}
	})
	t.Run("unknown command", func(t *testing.T) {
		got, code := runExecutable(t, executable, "", "unknown")
		if code != 1 || !strings.Contains(got, "Unknown command: unknown") {
			t.Fatalf("%d %s", code, got)
		}
	})
	t.Run("setup format and clamp", func(t *testing.T) {
		restoreStartup(t)
		community := makeDir(t, filepath.Join(base, "Community"))
		library := makeDir(t, filepath.Join(base, "Library"))
		second := makeDir(t, filepath.Join(base, "Library2"))
		answers := []string{community, "2", library, second, "1072997", "3", "y"}
		if hasWindowsIntegration() {
			answers = append(answers, "y", "n")
		}
		answers = append(answers, "n", "n")
		got, code := runExecutable(t, executable, strings.Join(answers, "\n")+"\n", "setup")
		if code != 0 || !strings.Contains(got, "1 / 5  MSFS Paths") || strings.Contains(got, "Installing") {
			t.Fatalf("code %d\n%s", code, got)
		}
		want := fmt.Sprintf("{\n    \"community_folder_path\": %q,\n    \"addon_library_path\": %q,\n    \"simbrief_user_id\": \"1072997\",\n    \"poll_interval_seconds\": 10,\n    \"last_processed_plan_id\": null,\n    \"last_processed_timestamp\": null,\n    \"run_hidden\": \"y\",\n    \"addon_library_count\": 2,\n    \"addon_library_path_2\": %q\n}", community, library, second)
		if hasWindowsIntegration() {
			want = strings.ReplaceAll(want, "\n", "\r\n")
		}
		actual, err := os.ReadFile(filepath.Join(base, configName))
		if err != nil {
			t.Fatal(err)
		}
		if string(actual) != want {
			t.Fatalf("config bytes\ngot %s\nwant %s", actual, want)
		}
		if hasWindowsIntegration() {
			verifyStartup(t, executable)
			verifyShortcut(t, filepath.Join(base, "sblink Run.lnk"), executable, "run")
			verifyShortcut(t, filepath.Join(base, "sblink Setup.lnk"), executable, "setup")
		}
	})
	t.Run("setup defaults and invalid values", func(t *testing.T) {
		restoreStartup(t)
		answers := []string{"", "0", "", "", "invalid", "N"}
		if hasWindowsIntegration() {
			answers = append(answers, "n", "y")
		}
		answers = append(answers, "y", "n")
		got, code := runExecutable(t, executable, strings.Join(answers, "\n")+"\n", "setup")
		if code != 0 || !strings.Contains(got, "Invalid number - keeping previous value") {
			t.Fatalf("code %d\n%s", code, got)
		}
		config, err := loadConfig(base)
		if err != nil {
			t.Fatal(err)
		}
		if config.integer("addon_library_count", 0) != 1 || config.integer("poll_interval_seconds", 0) != 10 || config.text("run_hidden") != "n" {
			t.Fatal("defaults or normalization wrong")
		}
		if hasWindowsIntegration() {
			verifyShortcut(t, filepath.Join(base, "appdata", "Microsoft", "Windows", "Start Menu", "Programs", "sblink.lnk"), executable, "run")
		}
	})
	t.Run("single native executable", func(t *testing.T) {
		if !hasWindowsIntegration() {
			return
		}
		image, err := pe.Open(executable)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := image.Close(); err != nil {
				t.Error(err)
			}
		}()
		if image.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			t.Fatal("not x64")
		}
		header, ok := image.OptionalHeader.(*pe.OptionalHeader64)
		if !ok || header.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
			t.Fatal("hidden-safe Windows subsystem missing")
		}
		libraries, err := image.ImportedLibraries()
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range libraries {
			lower := strings.ToLower(name)
			if strings.Contains(lower, "python") || strings.Contains(lower, "vcruntime") || strings.Contains(lower, "libgcc") {
				t.Fatal("external runtime", name)
			}
		}
		t.Logf("PE imports: %v", libraries)
	})
	if hasWindowsIntegration() {
		t.Run("tray and native consoles", func(t *testing.T) { testTrayExecutable(t, executable) })
		t.Run("terminal path and lifetime", func(t *testing.T) { testTerminalLifetime(t, executable) })
	}
}
