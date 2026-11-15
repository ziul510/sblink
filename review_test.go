package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type eventWriter struct{ handle func(string) }

func (w eventWriter) Write(data []byte) (int, error) { w.handle(string(data)); return len(data), nil }

func TestMalformedAircraftDoesNotPanic(t *testing.T) {
	for _, code := range []string{"ıı", "ıa", "é", "𐐀"} {
		if terms := aircraftTerms(code); len(terms) != 0 {
			t.Fatalf("short aircraft %q accepted: %v", code, terms)
		}
	}
}

func TestFailedScanDoesNotMarkPlan(t *testing.T) {
	for _, mode := range []string{"missing library", "canceled completion"} {
		t.Run(mode, func(t *testing.T) {
			d := testDaemon(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d.log.console = eventWriter{func(line string) {
				if mode == "missing library" && strings.Contains(line, "(BFS)") {
					if err := os.Remove(d.config.text("addon_library_path")); err != nil {
						t.Error(err)
					}
				}
				if mode == "canceled completion" && strings.Contains(line, "Found 0 unique") {
					cancel()
				}
			}}
			if err := d.poll(ctx, false); err == nil {
				t.Fatal("incomplete scan succeeded")
			}
			config, err := loadConfig(d.base)
			if err != nil {
				t.Fatal(err)
			}
			if d.memory != nil || config.identity("last_processed_plan_id") != nil {
				t.Fatal("incomplete plan marked as processed")
			}
		})
	}
}

func TestDamagedMappingsStayUntouched(t *testing.T) {
	for _, body := range []string{`{`, `{"A321":42}`, `[]`, "{\"A321\":\"\xff\"}"} {
		base := t.TempDir()
		path := filepath.Join(base, "mappings.json")
		writeFile(t, path, body)
		if _, _, err := loadMappings(context.Background(), base); err == nil {
			t.Fatal("damaged mappings accepted")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != body {
			t.Fatalf("mappings overwritten: %s %v", data, err)
		}
	}
}

func FuzzAircraftTerms(f *testing.F) {
	for _, code := range []string{"", "A321", "B738", "ıı", "ıa", "é", string([]byte{255, 255, 255})} {
		f.Add(code)
	}
	f.Fuzz(func(t *testing.T, code string) { aircraftTerms(code) })
}

func TestCanceledLockWait(t *testing.T) {
	base := t.TempDir()
	lock, err := lockFile(filepath.Join(base, ".sblink-config.lock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := updateConfig(ctx, base, newDocument(), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock wait: %v", err)
	}
}

func TestDaemonReloadsSettings(t *testing.T) {
	d := testDaemon(t)
	d.cycle(context.Background(), "")
	before := d.memory
	changes := newDocument()
	changes.set("simbrief_user_id", "newuser")
	changes.set("poll_interval_seconds", minimumPollSeconds)
	if err := updateConfig(context.Background(), d.base, changes, nil); err != nil {
		t.Fatal(err)
	}
	fetch := d.fetch
	d.fetch = func(ctx context.Context, user string) (any, error) {
		if user != "newuser" {
			t.Errorf("stale user %q", user)
		}
		return fetch(ctx, user)
	}
	d.cycle(context.Background(), "")
	if d.memory == nil || d.memory == before || pollDelay(d.config) != minimumPollSeconds*time.Second {
		t.Fatal("settings were not applied")
	}
	if err := d.log.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestMatchingSettingsInvalidateSavedPlan(t *testing.T) {
	d := testDaemon(t)
	if err := d.markProcessed(context.Background(), &Plan{ID: ptr("saved"), Timestamp: ptr("time")}); err != nil {
		t.Fatal(err)
	}
	changes := newDocument()
	changes.set("poll_interval_seconds", minimumPollSeconds)
	if err := updateConfig(context.Background(), d.base, changes, nil); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(d.base)
	if err != nil {
		t.Fatal(err)
	}
	if config.text("last_processed_plan_id") != "saved" {
		t.Fatal("unrelated setting reset plan")
	}
	changes.set("addon_library_path", makeDir(t, filepath.Join(d.base, "new-library")))
	if err := updateConfig(context.Background(), d.base, changes, nil); err != nil {
		t.Fatal(err)
	}
	config, err = loadConfig(d.base)
	if err != nil {
		t.Fatal(err)
	}
	if config.identity("last_processed_plan_id") != nil || config.identity("last_processed_timestamp") != nil {
		t.Fatal("changed paths retained processed identities")
	}
	restarted := &Daemon{base: d.base, config: config, log: d.log, running: d.running, fetch: d.fetch}
	if err := restarted.poll(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if restarted.memory == nil {
		t.Fatal("restart skipped current plan")
	}
}

func TestSettingsChangedDuringCycleStayRetryable(t *testing.T) {
	d := testDaemon(t)
	d.log.console = eventWriter{func(line string) {
		if strings.Contains(line, "No matching addons found") {
			changes := newDocument()
			changes.set("simbrief_user_id", "anotheruser")
			if err := updateConfig(context.Background(), d.base, changes, nil); err != nil {
				t.Error(err)
			}
		}
	}}
	if err := d.poll(context.Background(), false); err == nil {
		t.Fatal("stale cycle saved completion")
	}
	config, err := loadConfig(d.base)
	if err != nil {
		t.Fatal(err)
	}
	if config.identity("last_processed_plan_id") != nil || d.memory != nil {
		t.Fatal("settings change was overwritten by stale cycle")
	}
}

type rejectingReader struct{ t *testing.T }

func (r rejectingReader) Read([]byte) (int, error) {
	r.t.Error("input read started after cancellation")
	return 0, io.EOF
}

func TestSetupCanceledAtPromptDoesNotRead(t *testing.T) {
	base := t.TempDir()
	config, err := loadConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var captured bytes.Buffer
	out := &output{writer: eventWriter{func(line string) {
		captured.WriteString(line)
		if strings.Contains(line, "Community folder:") {
			cancel()
		}
	}}}
	if err := setup(ctx, base, "unused", config, testLogger(t), rejectingReader{t}, out); !errors.Is(err, context.Canceled) {
		t.Fatalf("prompt cancellation: %v", err)
	}
}

func TestSetupRejectsEmptyRequiredFields(t *testing.T) {
	base := t.TempDir()
	config, err := loadConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	community := makeDir(t, filepath.Join(base, "community"))
	library := makeDir(t, filepath.Join(base, "library"))
	input := strings.Join([]string{"", "  ", community, "", "", " ", library, "", " ", "1072997"}, "\n") + "\n"
	var buffer bytes.Buffer
	err = setup(context.Background(), base, "unused", config, testLogger(t), strings.NewReader(input), &output{writer: &buffer})
	if err == nil || !strings.Contains(err.Error(), "Poll interval (seconds)") {
		t.Fatalf("unexpected stopping point: %v", err)
	}
	for _, label := range []string{"Community folder", "Addon library #1", "SimBrief Pilot ID"} {
		if count := strings.Count(buffer.String(), label+" is required"); count != 2 {
			t.Fatalf("%s rejected %d blanks instead of 2", label, count)
		}
	}
	if _, err := os.Stat(filepath.Join(base, configName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete setup saved config")
	}
}

func TestSetupKeepsExistingRequiredValues(t *testing.T) {
	base := t.TempDir()
	config, err := loadConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	config.set("community_folder_path", base)
	config.set("addon_library_path", base)
	config.set("simbrief_user_id", "1072997")
	var buffer bytes.Buffer
	err = setup(context.Background(), base, "unused", config, testLogger(t), strings.NewReader("\n\n\n\n"), &output{writer: &buffer})
	if err == nil || !strings.Contains(err.Error(), "Poll interval (seconds)") || strings.Contains(buffer.String(), " is required") {
		t.Fatalf("existing defaults were rejected: %v", err)
	}
}

func TestIdentityFallsBackFromWrongType(t *testing.T) {
	plan := extractPlan(context.Background(), map[string]any{"params": map[string]any{"OFPID": map[string]any{"bad": true}, "TIME_GENERATED": []any{1}}, "ofp_id": 123, "time_generated": 456}, testLogger(t))
	if !equalIdentity(plan.ID, ptr("123")) || !equalIdentity(plan.Timestamp, ptr("456")) {
		t.Fatalf("valid alternate identities lost: %+v", plan)
	}
}

func TestOversizedResponseRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, `{}`+strings.Repeat(" ", responseLimit)+`junk`); err != nil {
			return
		}
	}))
	defer server.Close()
	api := &SimBrief{client: server.Client(), endpoint: server.URL}
	if _, err := api.fetch(context.Background(), "123"); err == nil {
		t.Fatal("oversized response accepted")
	}
}
