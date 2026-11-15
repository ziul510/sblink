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

func restoreStartup(t *testing.T) {
	t.Helper()
	lock, err := lockFile(filepath.Join(os.TempDir(), ".sblink-integration-startup.lock"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	})
	path, err := syscall.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Run`)
	if err != nil {
		t.Fatal(err)
	}
	var key syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, path, 0, syscall.KEY_READ|syscall.KEY_SET_VALUE, &key); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString("sblink")
	if err != nil {
		t.Fatal(err)
	}
	var kind, size uint32
	err = syscall.RegQueryValueEx(key, name, nil, &kind, nil, &size)
	existed := !errors.Is(err, syscall.ERROR_FILE_NOT_FOUND)
	if existed && err != nil {
		t.Fatal(err)
	}
	data := make([]byte, max(1, size))
	if existed {
		if err := syscall.RegQueryValueEx(key, name, nil, &kind, &data[0], &size); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		advapi := syscall.NewLazyDLL("advapi32.dll")
		if existed {
			r, _, _ := advapi.NewProc("RegSetValueExW").Call(uintptr(key), uintptr(unsafe.Pointer(name)), 0, uintptr(kind), uintptr(unsafe.Pointer(&data[0])), uintptr(size))
			if r != 0 {
				t.Errorf("restore startup: %v", syscall.Errno(r))
			}
		} else {
			r, _, _ := advapi.NewProc("RegDeleteValueW").Call(uintptr(key), uintptr(unsafe.Pointer(name)))
			if r != 0 && r != 2 {
				t.Errorf("restore absent startup: %v", syscall.Errno(r))
			}
		}
		if err := syscall.RegCloseKey(key); err != nil {
			t.Error(err)
		}
	})
}

func verifyShortcut(t *testing.T, path, executable, arguments string) {
	t.Helper()
	quote := func(s string) string { return `'` + strings.ReplaceAll(s, "'", "''") + `'` }
	script := `$s=(New-Object -ComObject WScript.Shell).CreateShortcut(` + quote(path) + `);if($s.TargetPath -ne ` + quote(executable) + ` -or $s.Arguments -ne ` + quote(arguments) + `){throw 'shortcut target mismatch'}`
	if err := powershell(script); err != nil {
		t.Fatal(err)
	}
}

func verifyStartup(t *testing.T, executable string) {
	t.Helper()
	value := strings.ReplaceAll(startupCommand(executable), "'", "''")
	if err := powershell(`$value=(Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name sblink).sblink;if($value -ne '` + value + `'){throw 'startup target mismatch'}`); err != nil {
		t.Fatal(err)
	}
}

func TestJunctionOwnership(t *testing.T) {
	base := t.TempDir()
	community := makeDir(t, filepath.Join(base, "community"))
	library := makeDir(t, filepath.Join(base, "library"))
	target := makeDir(t, filepath.Join(library, "á addon & (100%)"))
	foreignTarget := makeDir(t, filepath.Join(library, "foreign"))
	real := makeDir(t, filepath.Join(community, "real"))
	writeFile(t, filepath.Join(real, markerName), "sblink")
	foreign := filepath.Join(community, "foreign")
	if err := makeJunction(foreign, foreignTarget); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(community, "owned")
	log := newLogger(base, nil)
	if err := createJunction(link, target, log); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolvePath(link)
	if err != nil || pathKey(resolved) != pathKey(target) || !isLink(link) {
		t.Fatalf("OS junction verification: %s %v", resolved, err)
	}
	p, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(p, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 16384)
	var size uint32
	err = syscall.DeviceIoControl(handle, 0x900a8, nil, 0, &buffer[0], uint32(len(buffer)), &size, nil)
	err = errors.Join(err, syscall.CloseHandle(handle))
	if err != nil {
		t.Fatal(err)
	}
	if buffer[0] != 3 || buffer[3] != 0xa0 {
		t.Fatalf("wrong reparse tag: %x", buffer[:4])
	}
	data, err := os.ReadFile(filepath.Join(target, markerName))
	if err != nil || string(data) != "sblink" {
		t.Fatal("marker missing from target", err)
	}
	writeFile(t, filepath.Join(target, "keep.txt"), "keep")
	if err := createJunction(foreign, target, log); err == nil {
		t.Fatal("foreign junction replaced")
	}
	if err := createJunction(real, target, log); err == nil {
		t.Fatal("real folder replaced")
	}
	// Two owned links to one target must both survive marker checks
	if err := makeJunction(filepath.Join(community, "owned2"), target); err != nil {
		t.Fatal(err)
	}
	if err := clearJunctions(context.Background(), community, []string{library}, log); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"owned", "owned2"} {
		if _, err := os.Lstat(filepath.Join(community, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned link survived %s: %v", name, err)
		}
	}
	for _, path := range []string{real, foreign, filepath.Join(target, "keep.txt")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("foreign or target removed %s: %v", path, err)
		}
	}
	if hasMarker(target) {
		t.Fatal("marker remained after cleanup")
	}
}

func TestPartialCycleRetries(t *testing.T) {
	d := testDaemon(t)
	one := makeDir(t, filepath.Join(d.config.text("addon_library_path"), "KMIA-one"))
	two := makeDir(t, filepath.Join(d.config.text("addon_library_path"), "KLAX-two"))
	conflict := makeDir(t, filepath.Join(d.config.text("community_folder_path"), filepath.Base(two)))
	if err := d.poll(context.Background(), false); err == nil {
		t.Fatal("partial failure ignored")
	}
	if d.memory != nil || d.config.identity("last_processed_plan_id") != nil {
		t.Fatal("partial cycle marked done")
	}
	if !isLink(filepath.Join(d.config.text("community_folder_path"), filepath.Base(one))) {
		t.Fatal("first addon was not linked")
	}
	if err := os.Remove(conflict); err != nil {
		t.Fatal(err)
	}
	if err := d.poll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if d.memory == nil || d.config.text("last_processed_plan_id") != "id" {
		t.Fatal("retry did not finish")
	}
	if err := d.unlink(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateLibraryNamesFinish(t *testing.T) {
	d := testDaemon(t)
	makeDir(t, filepath.Join(d.config.text("addon_library_path"), "KMIA-same"))
	second := makeDir(t, filepath.Join(d.base, "second"))
	makeDir(t, filepath.Join(second, "KMIA-same"))
	d.config.set("addon_library_count", 2)
	d.config.set("addon_library_path_2", second)
	if err := saveDocument(filepath.Join(d.base, configName), d.config); err != nil {
		t.Fatal(err)
	}
	if err := d.poll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if d.memory == nil {
		t.Fatal("duplicate names caused an endless partial cycle")
	}
	if err := d.unlink(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMSFSNativeProcessDetection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "FlightSimulator2024.exe")
	data, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "ping.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, path, "-n", "30", "127.0.0.1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		if err := cmd.Wait(); err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				t.Error(err)
			}
		}
	}()
	running, err := msfsRunning(context.Background())
	if err != nil || !running {
		t.Fatalf("MSFS process not detected: %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := msfsRunning(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("process scan ignored cancellation")
	}
}

func testTrayExecutable(t *testing.T, executable string) {
	t.Helper()
	base := filepath.Dir(executable)
	for _, hidden := range []string{"y", "n"} {
		t.Run("hidden="+hidden, func(t *testing.T) {
			config, err := loadConfig(base)
			if err != nil {
				t.Fatal(err)
			}
			config.set("run_hidden", hidden)
			config.set("simbrief_user_id", "invalid!")
			config.set("community_folder_path", makeDir(t, filepath.Join(base, "tray-community")))
			config.set("addon_library_path", makeDir(t, filepath.Join(base, "tray-library")))
			config.set("addon_library_count", 1)
			writeFile(t, filepath.Join(base, "sblink.log"), "")
			if err := saveDocument(filepath.Join(base, configName), config); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "run", "--daemon")
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x8, HideWindow: hidden == "y"}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("daemon did not terminate")
				}
			})
			user := syscall.NewLazyDLL("user32.dll")
			window, daemonPID := findTray(t, cmd.Process.Pid)
			probeConsole(t, daemonPID, hidden == "n")
			exercisePopup(t, user, window)
			post := func(id uintptr) {
				r, _, err := user.NewProc("PostMessageW").Call(window, 0x111, id, 0)
				if r == 0 {
					t.Fatal(err)
				}
			}
			post(2)
			children := waitChildren(t, daemonPID, 1)
			for _, pid := range children {
				probeConsole(t, int(pid), true)
				killChild(t, int(pid))
			}
			post(1)
			children = waitChildren(t, daemonPID, 1)
			for _, pid := range children {
				probeConsole(t, int(pid), true)
				interruptConsole(t, int(pid))
			}
			post(3)
			post(4)
			deadline := time.Now().Add(5 * time.Second)
			manualActions := false
			for time.Now().Before(deadline) {
				data, err := os.ReadFile(filepath.Join(base, "sblink.log"))
				if err == nil && strings.Contains(string(data), "Manual unlink triggered") && strings.Contains(string(data), "Manual link triggered for user invalid!") {
					manualActions = true
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if !manualActions {
				t.Fatal("manual tray actions did not complete")
			}
			if hidden == "n" {
				interruptConsole(t, daemonPID)
			} else {
				post(5)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
				done <- nil
			case <-time.After(5 * time.Second):
				t.Fatal("Quit did not stop daemon")
			}
			data, err := os.ReadFile(filepath.Join(base, "sblink.log"))
			if err != nil || !strings.Contains(string(data), "Stopping daemon") {
				t.Fatalf("unclean shutdown: %v", err)
			}
		})
	}
	t.Run("background argument dispatch", func(t *testing.T) {
		for _, args := range [][]string{{"run"}, nil} {
			config, err := loadConfig(base)
			if err != nil {
				t.Fatal(err)
			}
			config.set("run_hidden", "y")
			if err := saveDocument(filepath.Join(base, configName), config); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(ctx, executable, args...)
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x8, HideWindow: true}
			if err := cmd.Run(); err != nil {
				cancel()
				t.Fatal(err)
			}
			cancel()
			children := waitChildren(t, cmd.Process.Pid, 1)
			for _, pid := range children {
				user := syscall.NewLazyDLL("user32.dll")
				name, err := syscall.UTF16PtrFromString(fmt.Sprintf("sblink.tray.%d", pid))
				if err != nil {
					t.Fatal(err)
				}
				var window uintptr
				deadline := time.Now().Add(5 * time.Second)
				for window == 0 && time.Now().Before(deadline) {
					window, _, _ = user.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(name)), 0)
					time.Sleep(25 * time.Millisecond)
				}
				if window == 0 {
					t.Fatalf("%v did not launch background tray", args)
				}
				probeConsole(t, int(pid), false)
				process, err := os.FindProcess(int(pid))
				if err != nil {
					t.Fatal(err)
				}
				message, wparam := uintptr(0x111), uintptr(5)
				if args != nil {
					message, wparam = 0x16, 1
				}
				r, _, err := user.NewProc("PostMessageW").Call(window, message, wparam, 0)
				if r == 0 {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { _, err := process.Wait(); done <- err }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("background daemon did not quit")
				}
			}
		}
	})
}

func TestMissingAppDataFailsSafely(t *testing.T) {
	t.Setenv("APPDATA", "")
	if err := configureShortcut(filepath.Join(t.TempDir(), "sblink.exe"), true); err == nil {
		t.Fatal("missing APPDATA produced a relative shortcut")
	}
}

func exercisePopup(t *testing.T, user *syscall.LazyDLL, window uintptr) {
	t.Helper()
	thread, _, _ := user.NewProc("GetWindowThreadProcessId").Call(window, 0)
	type guiThreadInfo struct {
		Size, Flags                                        uint32
		Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
		Rect                                               [4]int32
	}
	r, _, err := user.NewProc("PostMessageW").Call(window, 0x8001, 0, 0x205)
	if r == 0 {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	opened := false
	for time.Now().Before(deadline) {
		info := guiThreadInfo{Size: uint32(unsafe.Sizeof(guiThreadInfo{}))}
		r, _, err := user.NewProc("GetGUIThreadInfo").Call(thread, uintptr(unsafe.Pointer(&info)))
		if r == 0 {
			t.Fatal(err)
		}
		if info.MenuOwner == window && info.Flags&4 != 0 {
			opened = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	r, _, err = user.NewProc("PostMessageW").Call(window, 0x1f, 0, 0)
	if r == 0 {
		t.Fatal(err)
	}
	if !opened {
		t.Fatal("native popup menu did not open")
	}
}

func probeConsole(t *testing.T, pid int, want bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		actual := pid
		if want {
			actual = consolePID(t, pid)
		}
		result := readConsoleState(t, actual)
		if result.Attached == want && (!want || result.Visible || result.Class == "PseudoConsoleWindow") {
			if want {
				t.Logf("console host class: %s", result.Class)
			}
			return
		}
		if !want || time.Now().After(deadline) {
			t.Fatalf("console %d: %+v want visible=%v", actual, result, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

type consoleState struct {
	Attached, Visible bool
	Class             string
}

func readConsoleState(t *testing.T, pid int) consoleState {
	t.Helper()
	script := fmt.Sprintf(`Add-Type -TypeDefinition 'using System;using System.Text;using System.Runtime.InteropServices;public class Probe{[DllImport("kernel32.dll")]public static extern bool FreeConsole();[DllImport("kernel32.dll")]public static extern bool AttachConsole(uint pid);[DllImport("kernel32.dll")]public static extern IntPtr GetConsoleWindow();[DllImport("user32.dll")]public static extern bool IsWindowVisible(IntPtr h);[DllImport("user32.dll",CharSet=CharSet.Unicode)]public static extern int GetClassName(IntPtr h,StringBuilder name,int count);}';[Probe]::FreeConsole()|Out-Null;$a=[Probe]::AttachConsole(%d);$h=[Probe]::GetConsoleWindow();$v=[Probe]::IsWindowVisible($h);$c=New-Object System.Text.StringBuilder 256;[Probe]::GetClassName($h,$c,256)|Out-Null;[Probe]::FreeConsole()|Out-Null;@{attached=$a;visible=$v;class=$c.ToString()}|ConvertTo-Json -Compress`, pid)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var result consoleState
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("probe %s: %v", data, err)
	}
	return result
}

func interruptConsole(t *testing.T, pid int) {
	t.Helper()
	pid = consolePID(t, pid)
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`Add-Type -TypeDefinition 'using System;using System.Runtime.InteropServices;public class Breaker{public delegate bool Handler(uint kind);public static Handler Ignore = delegate(uint kind){return true;};[DllImport("kernel32.dll")]public static extern bool FreeConsole();[DllImport("kernel32.dll")]public static extern bool AttachConsole(uint pid);[DllImport("kernel32.dll")]public static extern bool SetConsoleCtrlHandler(Handler handler,bool add);[DllImport("kernel32.dll")]public static extern bool GenerateConsoleCtrlEvent(uint kind,uint group);}';[Breaker]::FreeConsole()|Out-Null;if(![Breaker]::AttachConsole(%d)){throw 'attach failed'};[Breaker]::SetConsoleCtrlHandler([Breaker]::Ignore,$true)|Out-Null;if(![Breaker]::GenerateConsoleCtrlEvent(1,0)){throw 'signal failed'};Start-Sleep -Milliseconds 150;[Breaker]::FreeConsole()|Out-Null`, pid)
	if err := powershell(script); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		state, err := process.Wait()
		if err == nil && !state.Success() {
			err = fmt.Errorf("console process exit: %s", state)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("console interrupt did not stop process")
	}
}

func waitChildren(t *testing.T, parent, count int) []uint32 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		handle, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
		if err != nil {
			t.Fatal(err)
		}
		var found []uint32
		entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
		err = syscall.Process32First(handle, &entry)
		for err == nil {
			if entry.ParentProcessID == uint32(parent) && strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), "sblink.exe") {
				found = append(found, entry.ProcessID)
			}
			err = syscall.Process32Next(handle, &entry)
		}
		if !errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
			t.Error(err)
		}
		if err := syscall.CloseHandle(handle); err != nil {
			t.Fatal(err)
		}
		if len(found) >= count {
			for _, pid := range found {
				process, err := os.FindProcess(int(pid))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
						t.Logf("child already closed: %v", err)
					}
					if err := process.Release(); err != nil {
						t.Error(err)
					}
				})
			}
			time.Sleep(300 * time.Millisecond)
			return found
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("tray action did not launch a child")
	return nil
}

func killChild(t *testing.T, pid int) {
	t.Helper()
	pid = consolePID(t, pid)
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.Errno(87)) {
		t.Logf("child already closed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := process.Release(); err != nil {
		t.Error(err)
	}
}
