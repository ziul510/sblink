package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConfigMalformedAndDefaults(t *testing.T) {
	for _, body := range []string{`{`, `null`, `[]`, `42`, `{} {}`} {
		t.Run(body, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, configName), body)
			d, err := loadConfig(base)
			if err == nil || d.valid() || d.integer("poll_interval_seconds", 0) != 60 {
				t.Fatalf("%v %v", d, err)
			}
		})
	}
	base := t.TempDir()
	d, err := loadConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	data, err := d.encode()
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n    \"community_folder_path\": \"\",\n    \"addon_library_path\": \"\",\n    \"simbrief_user_id\": \"\",\n    \"poll_interval_seconds\": 60,\n    \"last_processed_plan_id\": null,\n    \"last_processed_timestamp\": null,\n    \"run_hidden\": \"y\"\n}"
	if string(data) != want {
		t.Fatalf("format:\n%s", data)
	}
	d.set("last_processed_plan_id", "")
	if d.identity("last_processed_plan_id") == nil {
		t.Fatal("lost empty id")
	}
	d.set("last_processed_plan_id", nil)
	if d.identity("last_processed_plan_id") != nil {
		t.Fatal("lost null id")
	}
	d.set("unicode", "á & < >")
	if err := saveDocument(filepath.Join(base, configName), d); err != nil {
		t.Fatal(err)
	}
	read, err := loadConfig(base)
	if err != nil || read.text("unicode") != "á & < >" {
		t.Fatalf("UTF-8 %v %v", read, err)
	}
}

func TestValidationStrictness(t *testing.T) {
	d := newDocument()
	defaults(d)
	d.set("community_folder_path", t.TempDir())
	d.set("simbrief_user_id", "123")
	d.set("addon_library_count", "2")
	d.set("addon_library_path", t.TempDir())
	d.set("addon_library_path_2", "")
	if !d.valid() {
		t.Fatal("config permits at least one library")
	}
	if validatePaths(context.Background(), d, testLogger(t)) {
		t.Fatal("path validation requires every library")
	}
	d.set("addon_library_path_2", t.TempDir())
	if !validatePaths(context.Background(), d, testLogger(t)) {
		t.Fatal("valid directories rejected")
	}
	d.set("addon_library_count", -1)
	if len(d.libraries()) != 1 {
		t.Fatal("negative count not normalized")
	}
	d.set("addon_library_count", []any{})
	if len(d.libraries()) != 1 {
		t.Fatal("bad count not normalized")
	}
	d.set("addon_library_path", " ")
	if d.valid() {
		t.Fatal("whitespace library accepted")
	}
	d.set("addon_library_path", 42)
	if d.valid() {
		t.Fatal("numeric path accepted")
	}
}

func TestAtomicConcurrentUpdates(t *testing.T) {
	base := t.TempDir()
	var workers sync.WaitGroup
	for _, key := range []string{"one", "two", "three", "four"} {
		workers.Add(1)
		go func(key string) {
			defer workers.Done()
			changes := newDocument()
			changes.set(key, key)
			if err := updateConfig(context.Background(), base, changes, nil); err != nil {
				t.Error(err)
			}
		}(key)
	}
	workers.Wait()
	d, err := loadConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"one", "two", "three", "four"} {
		if d.text(key) != key {
			t.Fatal("lost concurrent update", key)
		}
	}
	files, err := filepath.Glob(filepath.Join(base, "*.tmp"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files: %v %v", files, err)
	}
	writeFile(t, filepath.Join(base, configName), "{")
	if err := updateConfig(context.Background(), base, newDocument(), nil); err == nil {
		t.Fatal("silently replaced damaged config")
	}
}

func TestRotationAndConcurrentLogging(t *testing.T) {
	base := t.TempDir()
	log := newLogger(base, nil)
	writeFile(t, log.path, strings.Repeat("x", logLimit-1))
	log.Log("INFO", "first")
	writeFile(t, log.path, strings.Repeat("y", logLimit-1))
	log.Log("INFO", "second")
	writeFile(t, log.path, strings.Repeat("z", logLimit-1))
	log.Log("INFO", "third")
	if err := log.Err(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log.path + ".2")
	if err != nil || !bytes.HasPrefix(data, []byte("y")) {
		t.Fatalf("backup 2: %v", err)
	}
	if _, err := os.Stat(log.path + ".3"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("kept too many backups")
	}
	data, err = os.ReadFile(log.path)
	if err != nil || !strings.Contains(string(data), "] [INFO] third") {
		t.Fatalf("format: %s %v", data, err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			other := newLogger(base, nil)
			for i := 0; i < 10; i++ {
				other.Log("POLL", "test")
			}
			if err := other.Err(); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	data, err = os.ReadFile(log.path)
	if err != nil || strings.Count(string(data), "[POLL] test") != 40 {
		t.Fatalf("lost logs: %v", err)
	}
}

func TestInterruptedSetupDoesNotSave(t *testing.T) {
	base := t.TempDir()
	d, _ := loadConfig(base)
	out := &output{writer: &bytes.Buffer{}}
	err := setup(context.Background(), base, "unused", d, testLogger(t), strings.NewReader("some path\n"), out)
	if err == nil {
		t.Fatal("EOF accepted")
	}
	if _, err := os.Stat(filepath.Join(base, configName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial config saved")
	}
}

func TestReset(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{configName, "mappings.json", "sblink.log", "sblink.log.1", "sblink.log.2", "unrelated.json"} {
		writeFile(t, filepath.Join(base, name), name)
	}
	out := &output{writer: &bytes.Buffer{}}
	if err := reset(context.Background(), base, out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{configName, "mappings.json", "sblink.log", "sblink.log.1", "sblink.log.2"} {
		data, err := os.ReadFile(filepath.Join(base, "backup", name))
		if err != nil || string(data) != name {
			t.Fatalf("%s %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "unrelated.json")); err != nil {
		t.Fatal("unrelated file moved")
	}
}

type chunkWriter struct{ chunks chan string }

func (w chunkWriter) Write(data []byte) (int, error) { w.chunks <- string(data); return len(data), nil }

func TestLogViewerFollowsRotation(t *testing.T) {
	base := t.TempDir()
	log := newLogger(base, nil)
	log.Log("INFO", "before rotation")
	ctx, cancel := context.WithCancel(context.Background())
	chunks := make(chan string, 8)
	done := make(chan error, 1)
	go func() { done <- showLogs(ctx, base, &output{writer: chunkWriter{chunks}}) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("log viewer leaked")
		}
	}()
	waitText := func(text string) {
		deadline := time.After(3 * time.Second)
		for {
			select {
			case data := <-chunks:
				if strings.Contains(data, text) {
					return
				}
			case <-deadline:
				t.Fatalf("log viewer did not show %q", text)
			}
		}
	}
	waitText("before rotation")
	log.Log("POLL", "%s", strings.Repeat("x", logLimit-10))
	log.Log("INFO", "after rotation")
	if err := log.Err(); err != nil {
		t.Fatal(err)
	}
	waitText("after rotation")
}
