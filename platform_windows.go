//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

func pathKey(path string) string { return strings.ToLower(filepath.Clean(path)) }

func diskText(data []byte) []byte { return []byte(strings.ReplaceAll(string(data), "\n", "\r\n")) }

func resolvePath(path string) (result string, err error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := syscall.CreateFile(p, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, syscall.CloseHandle(handle)) }()
	buffer := make([]uint16, 32768)
	r, _, callErr := syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW").Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	if r == 0 {
		return "", callErr
	}
	if r >= uintptr(len(buffer)) {
		return "", errors.New("resolved path is too long")
	}
	result = syscall.UTF16ToString(buffer[:r])
	if strings.HasPrefix(result, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(result, `\\?\UNC\`), nil
	}
	return strings.TrimPrefix(result, `\\?\`), nil
}

func lockFile(path string, wait bool) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	flags := uintptr(2)
	if !wait {
		flags |= 1
	}
	var overlap syscall.Overlapped
	r, _, callErr := syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx").Call(f.Fd(), flags, 0, 1, 0, uintptr(unsafe.Pointer(&overlap)))
	if r == 0 {
		return nil, errors.Join(fmt.Errorf("lock %s: %w", path, callErr), f.Close())
	}
	return f, nil
}

func lockBusy(err error) bool { return errors.Is(err, syscall.Errno(33)) }

func replaceFile(from, to string) error {
	a, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	b, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	r, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW").Call(uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(b)), 0x1|0x8)
	if r == 0 {
		return err
	}
	return nil
}

func isLink(path string) bool {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := syscall.GetFileAttributes(p)
	return err == nil && attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func makeJunction(link, target string) (err error) {
	target, err = resolvePath(target)
	if err != nil {
		return err
	}
	if strings.HasPrefix(target, `\\`) {
		return errors.New("directory junctions require a local target")
	}
	if err := os.Mkdir(link, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.Remove(link))
		}
	}()
	p, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(p, syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, syscall.CloseHandle(handle)) }()
	substitute := utf16.Encode([]rune(`\??\` + strings.TrimPrefix(target, `\\?\`)))
	printName := utf16.Encode([]rune(target))
	buffer := make([]byte, 16+2*(len(substitute)+1+len(printName)+1))
	binary.LittleEndian.PutUint32(buffer, 0xa0000003)
	binary.LittleEndian.PutUint16(buffer[4:], uint16(len(buffer)-8))
	binary.LittleEndian.PutUint16(buffer[10:], uint16(2*len(substitute)))
	binary.LittleEndian.PutUint16(buffer[12:], uint16(2*(len(substitute)+1)))
	binary.LittleEndian.PutUint16(buffer[14:], uint16(2*len(printName)))
	if len(buffer) > 16*1024 {
		return errors.New("junction target is too long")
	}
	for i, v := range substitute {
		binary.LittleEndian.PutUint16(buffer[16+2*i:], v)
	}
	for i, v := range printName {
		binary.LittleEndian.PutUint16(buffer[16+2*(len(substitute)+1+i):], v)
	}
	var returned uint32
	return syscall.DeviceIoControl(handle, 0x900a4, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
}

func msfsRunning(ctx context.Context) (running bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	handle, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, syscall.CloseHandle(handle)) }()
	entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
	err = syscall.Process32First(handle, &entry)
	for err == nil {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if strings.Contains(strings.ToLower(syscall.UTF16ToString(entry.ExeFile[:])), "flightsimulator") {
			return true, nil
		}
		err = syscall.Process32Next(handle, &entry)
	}
	if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
		err = nil
	}
	return false, err
}

func ensureConsole(fresh, bind bool) error {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	if !fresh && !bind && redirectedStream(os.Stdin) && redirectedStream(os.Stdout) {
		return nil
	}
	r, _, _ := kernel.NewProc("GetConsoleWindow").Call()
	if fresh && r != 0 {
		if r, _, err := kernel.NewProc("FreeConsole").Call(); r == 0 {
			return err
		}
		r = 0
	}
	if r == 0 {
		var attached uintptr
		if !fresh {
			attached, _, _ = kernel.NewProc("AttachConsole").Call(uintptr(uint32(0xffffffff)))
		}
		if attached == 0 {
			allocated, _, err := kernel.NewProc("AllocConsole").Call()
			if allocated == 0 {
				return fmt.Errorf("allocate console: %w", err)
			}
		}
	}
	for _, stream := range []struct {
		file **os.File
		name string
		flag int
		id   int32
	}{
		{&os.Stdin, "CONIN$", os.O_RDONLY, syscall.STD_INPUT_HANDLE},
		// Console sizing and mode queries also require read access
		{&os.Stdout, "CONOUT$", os.O_RDWR, syscall.STD_OUTPUT_HANDLE},
		{&os.Stderr, "CONOUT$", os.O_RDWR, syscall.STD_ERROR_HANDLE},
	} {
		// Preserve redirected streams for scripted commands
		if !fresh && !bind && redirectedStream(*stream.file) {
			continue
		}
		f, err := os.OpenFile(stream.name, stream.flag, 0)
		if err != nil {
			return err
		}
		if r, _, err := kernel.NewProc("SetStdHandle").Call(uintptr(uint32(stream.id)), f.Fd()); r == 0 {
			return errors.Join(err, f.Close())
		}
		*stream.file = f
	}
	// Keep console text readable in UTF8
	if r, _, err := kernel.NewProc("SetConsoleOutputCP").Call(65001); r == 0 {
		return err
	}
	if r, _, err := kernel.NewProc("SetConsoleCP").Call(65001); r == 0 {
		return err
	}
	return nil
}

func consoleTitle(title string) error {
	if r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow").Call(); r == 0 {
		return nil
	}
	p, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return err
	}
	r, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(p)))
	if r == 0 {
		return err
	}
	return nil
}

func spawn(executable string, args ...string) error {
	cmd := exec.Command(executable, args...)
	cmd.Dir = filepath.Dir(executable)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func startupCommand(executable string) string { return `"` + executable + `" run` }

func startupEnabled() (enabled bool, err error) {
	path, err := syscall.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Run`)
	if err != nil {
		return false, err
	}
	var key syscall.Handle
	err = syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, path, 0, syscall.KEY_READ, &key)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, syscall.RegCloseKey(key)) }()
	name, err := syscall.UTF16PtrFromString("sblink")
	if err != nil {
		return false, err
	}
	var size uint32
	err = syscall.RegQueryValueEx(key, name, nil, nil, nil, &size)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	return size > 2, err
}

func configureStartup(executable string, enabled bool) error {
	// PowerShell is part of Windows and the script stays inside this executable
	value := strings.ReplaceAll(startupCommand(executable), "'", "''")
	script := `$p='HKCU:\Software\Microsoft\Windows\CurrentVersion\Run';`
	if enabled {
		script += `if(!(Test-Path $p)){New-Item -Path $p -Force -ErrorAction Stop | Out-Null};Set-ItemProperty -Path $p -Name sblink -Value '` + value + `' -Type String -ErrorAction Stop`
	} else {
		script += `if(Get-ItemProperty -Path $p -Name sblink -ErrorAction SilentlyContinue){Remove-ItemProperty -Path $p -Name sblink -ErrorAction Stop}`
	}
	return powershell(script)
}

func startMenuPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Microsoft", "Windows", "Start Menu", "Programs", "sblink.lnk"), nil
}

func configureShortcut(executable string, enabled bool) error {
	path, err := startMenuPath()
	if err != nil {
		return err
	}
	if !enabled {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return writeShortcut(path, executable, "run", home)
}

func portableShortcuts(executable string) error {
	for _, mode := range []string{"Run", "Setup"} {
		if err := writeShortcut(filepath.Join(filepath.Dir(executable), "sblink "+mode+".lnk"), executable, strings.ToLower(mode), filepath.Dir(executable)); err != nil {
			return err
		}
	}
	return nil
}

func writeShortcut(path, executable, arguments, directory string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	quote := func(s string) string { return `'` + strings.ReplaceAll(s, "'", "''") + `'` }
	return powershell(`$w=New-Object -ComObject WScript.Shell -ErrorAction Stop;$s=$w.CreateShortcut(` + quote(path) + `);$s.TargetPath=` + quote(executable) + `;$s.Arguments=` + quote(arguments) + `;$s.WorkingDirectory=` + quote(directory) + `;$s.IconLocation=` + quote(executable+",0") + `;$s.Save()`)
}

func powershell(script string) error {
	words := utf16.Encode([]rune("$ErrorActionPreference='Stop';" + script))
	data := make([]byte, 2*len(words))
	for i, w := range words {
		binary.LittleEndian.PutUint16(data[2*i:], w)
	}
	return runPowerShell(data)
}
