package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() { os.Exit(entry(os.Args[1:])) }

func entry(args []string) (exitCode int) {
	terminalChild := len(args) > 1 && args[len(args)-1] == "--terminal"
	if terminalChild {
		args = args[:len(args)-1]
	}
	executable, err := os.Executable()
	if err != nil {
		return report(err)
	}
	base := filepath.Dir(executable)
	command := ""
	if len(args) > 0 {
		command = strings.ToLower(args[0])
	}
	config, loadErr := loadConfig(base)
	if command == "" {
		if _, err := os.Stat(filepath.Join(base, configName)); err == nil {
			command = "run"
		} else {
			command = "setup"
		}
	}
	newConsole := command == "setup" && len(args) == 2 && args[1] == "--new-console"
	if command != "run" || (len(args) == 2 && args[1] == "--daemon" && config.text("run_hidden") == "n") {
		fresh := command == "run" || command == "logs" || newConsole
		if !terminalChild && (len(args) <= 1 || newConsole || command == "run" && len(args) == 2 && args[1] == "--daemon") {
			consoleArgs := []string{command}
			if command == "run" {
				consoleArgs = append(consoleArgs, "--daemon")
			}
			if handled, code, err := defaultTerminal(executable, consoleArgs, fresh); handled {
				if err != nil {
					return report(err)
				}
				return code
			}
		}
		if err := ensureConsole(fresh && !terminalChild, terminalChild); err != nil {
			return report(err)
		}
		if err := consoleTitle("sblink " + command); err != nil {
			return report(err)
		}
	}
	console := io.Writer(os.Stdout)
	if command == "run" && config.text("run_hidden") != "n" {
		console = nil
	}
	log := newLogger(base, console)
	if loadErr != nil {
		log.Log("ERROR", "%v", loadErr)
	}
	out := &output{writer: os.Stdout}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	stopControl, err := consoleControl(cancel)
	if err != nil {
		return report(err)
	}
	defer func() {
		if err := stopControl(); err != nil {
			exitCode = report(err)
		}
	}()
	if terminalChild {
		stopWatch, err := watchTerminal(ctx, cancel)
		if err != nil {
			return report(err)
		}
		defer func() {
			if err := stopWatch(); err != nil {
				exitCode = report(err)
			}
		}()
	}
	client := &http.Client{}
	defer client.CloseIdleConnections()
	api := &SimBrief{client: client, endpoint: simBriefURL, retryDelay: time.Second}
	daemon := &Daemon{base: base, config: config, log: log, fetch: api.fetch, running: msfsRunning}
	var actionErr error
	if len(args) > 1 && !newConsole && !(command == "run" && len(args) == 2 && args[1] == "--daemon") {
		log.Log("ERROR", "Unexpected arguments")
		return 1
	}
	switch command {
	case "setup":
		actionErr = setup(ctx, base, executable, config, log, os.Stdin, out)
	case "status":
		actionErr = status(ctx, base, config, out)
	case "reset", "prepare_setup":
		actionErr = reset(ctx, base, out)
	case "logs":
		actionErr = showLogs(ctx, base, out)
	case "run", "manual", "link", "unlink":
		if !config.valid() {
			log.Log("ERROR", "Configuration incomplete. Run 'sblink.exe setup' first")
			return 1
		}
		if loadErr != nil || !validatePaths(ctx, config, log) {
			return 1
		}
		switch command {
		case "manual":
			log.Log("INFO", "Starting manual poll cycle")
			cycleCtx, stop := context.WithTimeout(ctx, pollTimeout)
			actionErr = daemon.poll(cycleCtx, false)
			stop()
			log.Log("INFO", "Manual cycle complete")
		case "link":
			cycleCtx, stop := context.WithTimeout(ctx, pollTimeout)
			actionErr = daemon.poll(cycleCtx, true)
			stop()
		case "unlink":
			actionErr = daemon.unlink(ctx)
		case "run":
			if len(args) < 2 {
				actionErr = spawn(executable, "run", "--daemon")
			} else {
				actionErr = runDaemon(ctx, executable, daemon, out)
			}
		}
	default:
		log.Log("ERROR", "Unknown command: %s", command)
		return 1
	}
	if actionErr != nil && !errors.Is(actionErr, context.Canceled) {
		log.Log("ERROR", "%v", actionErr)
		return 1
	}
	if err := errors.Join(log.Err(), out.err); err != nil {
		return report(err)
	}
	return 0
}

func report(err error) int {
	if _, e := fmt.Fprintln(os.Stderr, err); e != nil {
		return 1
	}
	return 1
}

func runDaemon(ctx context.Context, executable string, d *Daemon, out *output) (err error) {
	lock, err := lockFile(filepath.Join(d.base, ".sblink-run.lock"), false)
	if err != nil {
		return fmt.Errorf("sblink is already running: %w", err)
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	if d.config.text("run_hidden") == "n" {
		out.printf("sblink running\nCommunity: %s\nLibrary: %s\nUser ID: %s\nInterval: %d seconds\n\n", d.config.text("community_folder_path"), d.config.text("addon_library_path"), d.config.text("simbrief_user_id"), d.config.integer("poll_interval_seconds", 60))
	}
	ctx, cancel := context.WithCancel(ctx)
	actions := make(chan string, 8)
	var worker sync.WaitGroup
	worker.Add(1)
	go func() { defer worker.Done(); d.run(ctx, actions) }()
	err = runTray(ctx, executable, actions, d.log, func() { cancel(); worker.Wait() })
	cancel()
	worker.Wait()
	return err
}

func status(ctx context.Context, base string, config *document, out *output) error {
	display := func(key string) string {
		value := scalar(config.values[key])
		if !truth(config.values[key]) {
			return "-"
		}
		return value
	}
	out.printf("Configuration\nCommunity Folder: %s\nAddon Library: %s\nSimBrief User ID: %s\nPoll Interval: %d s\n\nLast Processed\nPlan ID: %s\nTimestamp: %s\n\n", display("community_folder_path"), display("addon_library_path"), display("simbrief_user_id"), config.integer("poll_interval_seconds", 60), display("last_processed_plan_id"), display("last_processed_timestamp"))
	mappings, _, err := loadMappings(ctx, base)
	if err != nil {
		return err
	}
	if len(mappings.keys) > 0 {
		out.printf("Manual Mappings\n")
		for _, key := range mappings.keys {
			out.printf("  %s -> %s\n", key, mappings.text(key))
		}
		out.printf("\n")
	}
	return out.err
}

func reset(ctx context.Context, base string, out *output) (err error) {
	lock, err := lockFile(filepath.Join(base, ".sblink-run.lock"), false)
	if err != nil {
		return fmt.Errorf("Quit sblink before resetting preferences: %w", err)
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	cycle, err := lockFile(filepath.Join(base, ".sblink-cycle.lock"), false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cycle.Close()) }()
	configLock, err := lockFileContext(ctx, filepath.Join(base, ".sblink-config.lock"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, configLock.Close()) }()
	mappingsLock, err := lockFileContext(ctx, filepath.Join(base, ".sblink-mappings.lock"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, mappingsLock.Close()) }()
	logLock, err := lockFileContext(ctx, filepath.Join(base, "sblink.log.lock"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, logLock.Close()) }()
	out.printf("==================================================\n              SBLINK PREPARE SETUP                \n==================================================\n")
	backup := filepath.Join(base, "backup")
	if err := os.MkdirAll(backup, 0700); err != nil {
		return err
	}
	items, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	moved := 0
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Reset only app files so unrelated portable files stay untouched
		name := item.Name()
		if name != configName && name != "mappings.json" && name != "sblink.log" && name != "sblink.log.1" && name != "sblink.log.2" {
			continue
		}
		if item.IsDir() || item.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to back up non-file %s", name)
		}
		if err := replaceFile(filepath.Join(base, name), filepath.Join(backup, name)); err != nil {
			return err
		}
		out.printf("Backed up: %s\n", name)
		moved++
	}
	if moved == 0 {
		out.printf("No existing configuration or log files found to backup\n")
	}
	out.printf("[+] Preferences reset\n\n==================================================\nPreparation complete! Please run sblink.exe setup to start the interactive setup\n==================================================\n")
	return out.err
}

func showLogs(ctx context.Context, base string, out *output) (err error) {
	alive, closeOwner, err := logOwner()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeOwner()) }()
	var offset int64
	var previous os.FileInfo
	for {
		if !alive() {
			return nil
		}
		data, next, info, err := readLogChunk(ctx, base, offset, previous)
		if err != nil {
			return err
		}
		offset, previous = next, info
		if len(data) > 0 {
			if n, err := out.writer.Write(data); err != nil {
				return err
			} else if n != len(data) {
				return io.ErrShortWrite
			}
		}
		if err := sleepContext(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
}

func readLogChunk(ctx context.Context, base string, offset int64, previous os.FileInfo) (data []byte, next int64, info os.FileInfo, err error) {
	lock, err := lockFileContext(ctx, filepath.Join(base, "sblink.log.lock"))
	if err != nil {
		return nil, offset, previous, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	f, err := os.Open(filepath.Join(base, "sblink.log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, offset, previous, nil
	}
	if err != nil {
		return nil, offset, previous, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err = f.Stat()
	if err != nil {
		return nil, offset, previous, err
	}
	if previous != nil && (!os.SameFile(previous, info) || info.Size() < offset) {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, previous, err
	}
	data, err = io.ReadAll(io.LimitReader(f, logLimit))
	return data, offset + int64(len(data)), info, err
}
