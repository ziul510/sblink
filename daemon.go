package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const pollTimeout = 120 * time.Second

type Daemon struct {
	base           string
	config         *document
	log            *Logger
	fetch          func(context.Context, string) (any, error)
	running        func(context.Context) (bool, error)
	memory         *Plan
	msfsWasRunning bool
	forceNext      bool
}

func (d *Daemon) paused(ctx context.Context, message string) (bool, error) {
	running, err := d.running(ctx)
	if err != nil {
		return false, err
	}
	if running && !d.msfsWasRunning {
		d.log.LogContext(ctx, "PAUSE", "%s", message)
		d.msfsWasRunning = true
	}
	if !running && d.msfsWasRunning {
		d.log.LogContext(ctx, "RESUME", "MSFS closed, resuming operation")
		d.msfsWasRunning = false
	}
	return running, nil
}

func (d *Daemon) poll(ctx context.Context, force bool) error {
	paused, err := d.paused(ctx, "MSFS detected, polling paused")
	if err != nil || paused {
		return err
	}
	user := d.config.text("simbrief_user_id")
	if force {
		d.log.LogContext(ctx, "POLL", "Manual link triggered for user %s", user)
	} else {
		d.log.LogContext(ctx, "POLL", "Querying SimBrief for user %s", user)
	}
	data, err := d.fetch(ctx, user)
	if err != nil {
		return err
	}
	plan := extractPlan(ctx, data, d.log)
	if plan == nil {
		return errors.New("Could not extract plan info")
	}
	if force || d.forceNext || isNewPlan(plan, d.config.identity("last_processed_plan_id"), d.config.identity("last_processed_timestamp"), d.memory) {
		return d.process(ctx, plan)
	}
	d.log.LogContext(ctx, "POLL", "No new plan detected")
	return nil
}

func (d *Daemon) process(ctx context.Context, plan *Plan) (err error) {
	lock, err := lockFile(filepath.Join(d.base, ".sblink-cycle.lock"), false)
	if err != nil {
		return fmt.Errorf("another link cycle is active: %w", err)
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	paused, err := d.paused(ctx, "MSFS is running, skipping link cycle")
	if err != nil || paused {
		return err
	}
	d.log.LogContext(ctx, "NEW PLAN", "%s -> %s | %s | %s", plan.Origin, plan.Destination, plan.Aircraft, plan.Airline)
	mappings, _, err := loadMappings(ctx, d.base)
	if err != nil {
		return err
	}
	var matched []string
	for _, library := range d.config.libraries() {
		found, err := findMatches(ctx, library, plan, mappings, d.log)
		if err != nil {
			return err
		}
		matched = append(matched, found...)
	}
	matched = uniqueAddons(ctx, matched, d.log)
	if len(matched) == 0 {
		d.log.LogContext(ctx, "INFO", "No matching addons found")
		return d.markProcessed(ctx, plan)
	}
	d.log.LogContext(ctx, "INFO", "Matched %d addons", len(matched))
	// MSFS may start while a large library is being scanned
	paused, err = d.paused(ctx, "MSFS is running, skipping link cycle")
	if err != nil || paused {
		return err
	}
	if err := clearJunctions(ctx, d.config.text("community_folder_path"), d.config.libraries(), d.log); err != nil {
		return fmt.Errorf("Failed to clean Community folder: %w", err)
	}
	linked := 0
	var failures error
	for _, target := range matched {
		if err := ctx.Err(); err != nil {
			return err
		}
		paused, err := d.paused(ctx, "MSFS is running, skipping link cycle")
		if err != nil || paused {
			return err
		}
		if err := createJunction(filepath.Join(d.config.text("community_folder_path"), filepath.Base(target)), target, d.log); err != nil {
			d.log.LogContext(ctx, "ERROR", "%v", err)
			failures = errors.Join(failures, err)
		} else {
			linked++
		}
	}
	d.log.LogContext(ctx, "INFO", "Successfully linked %d addons", linked)
	if linked < len(matched) {
		d.log.LogContext(ctx, "WARNING", "Partially linked addons (%d/%d); will retry next poll", linked, len(matched))
		return failures
	}
	return d.markProcessed(ctx, plan)
}

func (d *Daemon) markProcessed(ctx context.Context, plan *Plan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	changes := newDocument()
	var id, timeValue any
	if plan.ID != nil {
		id = *plan.ID
	}
	if plan.Timestamp != nil {
		timeValue = *plan.Timestamp
	}
	changes.set("last_processed_plan_id", id)
	changes.set("last_processed_timestamp", timeValue)
	if err := updateConfig(ctx, d.base, changes, d.config); err != nil {
		return fmt.Errorf("Failed to save config: %w", err)
	}
	d.config.set("last_processed_plan_id", id)
	d.config.set("last_processed_timestamp", timeValue)
	copy := *plan
	d.memory = &copy
	d.forceNext = false
	return nil
}

func (d *Daemon) unlink(ctx context.Context) (err error) {
	lock, err := lockFile(filepath.Join(d.base, ".sblink-cycle.lock"), false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	d.log.LogContext(ctx, "INFO", "Manual unlink triggered")
	return clearJunctions(ctx, d.config.text("community_folder_path"), d.config.libraries(), d.log)
}

func (d *Daemon) cycle(ctx context.Context, action string) {
	cycleCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	var err error
	if err = d.reloadConfig(cycleCtx); err != nil {
		d.log.LogContext(ctx, "ERROR", "%v", err)
		return
	}
	if action == "unlink" {
		err = d.unlink(cycleCtx)
	} else {
		err = d.poll(cycleCtx, action == "link")
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		d.log.LogContext(ctx, "ERROR", "Poll cycle failed or timed out: %v", err)
	}
}

func (d *Daemon) reloadConfig(ctx context.Context) error {
	config, err := loadConfig(d.base)
	if err != nil {
		return err
	}
	if !config.valid() || !validatePaths(ctx, config, d.log) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("Configuration incomplete. Run 'sblink.exe setup' first")
	}
	if !config.sameLinkSettings(d.config) {
		d.forceNext = true
		d.memory = nil
	}
	d.config = config
	return nil
}

func pollDelay(config *document) time.Duration {
	seconds := max(int64(minimumPollSeconds), int64(config.integer("poll_interval_seconds", defaultPollSeconds)))
	return time.Duration(min(seconds, int64((1<<63-1)/time.Second))) * time.Second
}

func (d *Daemon) run(ctx context.Context, actions <-chan string) {
	d.log.LogContext(ctx, "INFO", "Daemon started")
	defer d.log.Log("INFO", "Stopping daemon")
	d.cycle(ctx, "")
	for {
		seconds := pollDelay(d.config)
		timer := time.NewTimer(seconds)
		d.log.Console("\rWaiting %ds for next poll...   ", int64(seconds/time.Second))
		var action string
		select {
		case <-ctx.Done():
		case next, ok := <-actions:
			if !ok {
				timer.Stop()
				return
			}
			action = next
		case <-timer.C:
		}
		timer.Stop()
		d.log.Console("\r%s\r", strings.Repeat(" ", 40))
		if ctx.Err() != nil {
			return
		}
		d.cycle(ctx, action)
	}
}
