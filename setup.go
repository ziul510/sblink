package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const setupBanner = `
           ░██        ░██ ░██           ░██
           ░██        ░██               ░██
 ░███████  ░████████  ░██ ░██░████████  ░██    ░██
░██        ░██    ░██ ░██ ░██░██    ░██ ░██   ░██
 ░███████  ░██    ░██ ░██ ░██░██    ░██ ░███████
       ░██ ░███   ░██ ░██ ░██░██    ░██ ░██   ░██
 ░███████  ░██░█████  ░██ ░██░██    ░██ ░██    ░██

`

func setup(ctx context.Context, base, executable string, config *document, log *Logger, input io.Reader, out *output) error {
	width, tty := setupTerminal(out.writer)
	ui := &setupUI{out: out, width: width, tty: tty}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	ask := func(label, current, hint string, choice bool) (string, error) {
		for {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			ui.prompt(label, current, hint, choice)
			if out.err != nil {
				return "", out.err
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if !scanner.Scan() {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if scanner.Err() != nil {
					return "", scanner.Err()
				}
				return "", fmt.Errorf("Setup input ended at '%s'", label)
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			value := strings.TrimSpace(scanner.Text())
			ui.printf("\n")
			if value == "" {
				value = current
			}
			if strings.TrimSpace(value) == "" {
				ui.printf("%s\n", ui.color("red", label+" is required"))
				continue
			}
			if choice {
				value = strings.ToLower(value)
				if value != "y" && value != "n" {
					ui.printf("%s\n", ui.color("red", "Please select one of the available options"))
					continue
				}
			}
			return value, nil
		}
	}
	changes := newDocument()
	ui.screen(1)
	community, err := ask("Community folder", config.text("community_folder_path"), `e.g.  %AppData%\Microsoft Flight Simulator\Packages\Community`, false)
	if err != nil {
		return err
	}
	changes.set("community_folder_path", community)
	countText, err := ask("How many addons libraries would you like to use?", strconv.Itoa(config.integer("addon_library_count", 1)), "Enter a number (e.g. 1, 2, 3)", false)
	if err != nil {
		return err
	}
	count, err := strconv.Atoi(countText)
	if err != nil {
		count = 1
	}
	count = max(1, count)
	changes.set("addon_library_count", count)
	paths := []struct{ label, path string }{{"Community folder", community}}
	for i := 1; i <= count; i++ {
		label := fmt.Sprintf("Addon library #%d", i)
		path, err := ask(label, config.text(libraryKey(i)), `e.g.  D:\MSFS Addons`, false)
		if err != nil {
			return err
		}
		changes.set(libraryKey(i), path)
		paths = append(paths, struct{ label, path string }{label, path})
	}
	for _, p := range paths {
		info, err := os.Stat(p.path)
		if p.path == "" {
			ui.printf("  X  %s is empty\n", p.label)
		} else if err == nil && info.IsDir() {
			ui.printf("  OK  %s: %s\n", p.label, p.path)
		} else {
			ui.printf("  !  %s path not found - saved anyway, check before running\n", p.label)
		}
	}
	ui.screen(2)
	user, err := ask("SimBrief Pilot ID", config.text("simbrief_user_id"), "Numeric only - e.g. 123456", false)
	if err != nil {
		return err
	}
	changes.set("simbrief_user_id", user)
	ui.screen(3)
	interval, err := ask("Poll interval (seconds)", strconv.Itoa(config.integer("poll_interval_seconds", defaultPollSeconds)), "", false)
	if err != nil {
		return err
	}
	seconds, err := strconv.Atoi(interval)
	if err != nil {
		ui.printf("  !  Invalid number - keeping previous value\n")
	} else {
		changes.set("poll_interval_seconds", max(minimumPollSeconds, seconds))
		if seconds < minimumPollSeconds {
			ui.printf("  !  Value below minimum - set to 10 s\n")
		}
	}
	ui.screen(4)
	hiddenDefault := config.text("run_hidden")
	if hiddenDefault != "n" {
		hiddenDefault = "y"
	}
	hidden, err := ask("Run in background (no window)?", hiddenDefault, "Should sblink run silently in the background, with no console window?", true)
	if err != nil {
		return err
	}
	changes.set("run_hidden", hidden)
	startup, shortcut := "n", "n"
	if hasWindowsIntegration() {
		enabled, err := startupEnabled()
		if err != nil {
			return fmt.Errorf("read startup: %w", err)
		}
		current := "n"
		if enabled {
			current = "y"
		}
		startup, err = ask("Start with Windows?", current, "Should sblink start automatically when Windows boots?", true)
		if err != nil {
			return err
		}
		shortcut, err = ask("Create Start Menu shortcut?", "y", "Optionally create a Start Menu shortcut to launch sblink on demand", true)
		if err != nil {
			return err
		}
	} else {
		ui.printf("\n  !  Windows startup options are unavailable on this OS\n")
	}
	ui.screen(5)
	manual, err := ask("Enable manual mapping?", "n", "", true)
	if err != nil {
		return err
	}
	// Commit complete answers together so interrupted setup never leaves half a config
	if err := updateConfig(ctx, base, changes, nil); err != nil {
		return fmt.Errorf("Failed to save config: %w", err)
	}
	if hasWindowsIntegration() {
		if err := configureStartup(executable, startup == "y"); err != nil {
			return fmt.Errorf("Failed to configure startup: %w", err)
		}
		if startup == "y" {
			ui.printf("\n  OK  Startup enabled\n")
		} else {
			ui.printf("\n  OK  Startup disabled\n")
		}
		if err := configureShortcut(executable, shortcut == "y"); err != nil {
			return fmt.Errorf("Start Menu shortcut update failed: %w", err)
		}
		if shortcut == "y" {
			ui.printf("\n  OK  Start Menu shortcut created\n")
		} else {
			ui.printf("\n  OK  Start Menu shortcut removed\n")
		}
	}
	if manual == "y" {
		_, created, err := loadMappings(ctx, base)
		if err != nil {
			return err
		}
		if created {
			ui.printf("\n  OK  Created %s with an example\n", ui.color("bold", "mappings.json"))
		} else {
			ui.printf("\n  OK  %s already exists\n", ui.color("bold", "mappings.json"))
		}
		ui.printf("  %s %s\n", ui.color("dim", "Location:"), ui.color("cyan", filepath.Join(base, "mappings.json")))
	}
	if err := portableShortcuts(executable); err != nil {
		return fmt.Errorf("create Run and Setup shortcuts: %w", err)
	}
	ui.screen(6)
	start, err := ask("Start the daemon now?", "y", "", true)
	if err != nil {
		return err
	}
	if start == "y" {
		lock, err := lockFile(filepath.Join(base, ".sblink-run.lock"), false)
		if lockBusy(err) {
			ui.printf("  OK  Daemon is already running; settings apply on the next poll\n  Restart sblink to apply a change to window visibility\n")
			return out.err
		}
		if err != nil {
			return err
		}
		if err := lock.Close(); err != nil {
			return err
		}
		if err := spawn(executable, "run"); err != nil {
			return err
		}
		ui.printf("  OK  Daemon started in background\n")
	}
	return errors.Join(out.err, log.Err())
}
