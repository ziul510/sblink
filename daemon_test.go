package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func testDaemon(t *testing.T) *Daemon {
	t.Helper()
	base := t.TempDir()
	config, _ := loadConfig(base)
	config.set("community_folder_path", makeDir(t, filepath.Join(base, "community")))
	config.set("addon_library_path", makeDir(t, filepath.Join(base, "library")))
	config.set("simbrief_user_id", "123")
	if err := saveDocument(filepath.Join(base, configName), config); err != nil {
		t.Fatal(err)
	}
	return &Daemon{base: base, config: config, log: newLogger(base, nil), running: func(context.Context) (bool, error) { return false, nil }, fetch: func(context.Context, string) (any, error) {
		return map[string]any{"OFPID": "id", "generated": "time", "orig": "KMIA", "dest": "KLAX", "aircraft": "A321", "airline": "DAL"}, nil
	}}
}

func TestPauseResumeAndMark(t *testing.T) {
	d := testDaemon(t)
	running := true
	d.running = func(context.Context) (bool, error) { return running, nil }
	fetched := 0
	fetch := d.fetch
	d.fetch = func(ctx context.Context, user string) (any, error) { fetched++; return fetch(ctx, user) }
	if err := d.poll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if fetched != 0 || d.memory != nil {
		t.Fatal("polling did not pause")
	}
	running = false
	if err := d.poll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if fetched != 1 || d.memory == nil || d.msfsWasRunning {
		t.Fatal("did not resume and mark no-match plan")
	}
	config, err := loadConfig(d.base)
	if err != nil || config.text("last_processed_plan_id") != "id" {
		t.Fatalf("plan not persisted %v", err)
	}
	before := d.memory
	if err := d.poll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if d.memory != before {
		t.Fatal("same plan reprocessed")
	}
}

func TestSaveFailureDoesNotMark(t *testing.T) {
	d := testDaemon(t)
	writeFile(t, filepath.Join(d.base, configName), "{")
	if err := d.markProcessed(context.Background(), &Plan{ID: ptr("new")}); err == nil {
		t.Fatal("save failure ignored")
	}
	if d.memory != nil || d.config.identity("last_processed_plan_id") != nil {
		t.Fatal("failed save recorded in memory")
	}
}

func TestDaemonCancellation(t *testing.T) {
	d := testDaemon(t)
	var polls atomic.Int32
	d.fetch = func(ctx context.Context, user string) (any, error) { polls.Add(1); <-ctx.Done(); return nil, ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); d.run(ctx, make(chan string)) }()
	deadline := time.After(time.Second)
	for polls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("poll never started")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("daemon leaked")
	}
	if err := d.log.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestPollErrors(t *testing.T) {
	d := testDaemon(t)
	d.fetch = func(context.Context, string) (any, error) { return nil, errors.New("offline") }
	if err := d.poll(context.Background(), true); err == nil || d.memory != nil {
		t.Fatal("fetch failure ignored")
	}
	d.fetch = func(context.Context, string) (any, error) { return []any{}, nil }
	if err := d.poll(context.Background(), true); err == nil {
		t.Fatal("bad payload ignored")
	}
}
