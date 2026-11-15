package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLogLockDoesNotBlockShutdown(t *testing.T) {
	d := testDaemon(t)
	lock, err := lockFile(filepath.Join(d.base, "sblink.log.lock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	started := make(chan struct{})
	d.log.console = eventWriter{func(line string) {
		if strings.Contains(line, "Daemon started") {
			close(started)
		}
	}}
	d.running = func(ctx context.Context) (bool, error) { return false, ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); d.run(ctx, make(chan string)) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("daemon did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("log lock blocked shutdown")
	}
	if !errors.Is(d.log.Err(), context.DeadlineExceeded) {
		t.Fatalf("log contention was not reported: %v", d.log.Err())
	}
}

func TestCanceledValidationStopsLogging(t *testing.T) {
	d := testDaemon(t)
	d.config.set("addon_library_count", 6)
	for i := 1; i <= 6; i++ {
		d.config.set(libraryKey(i), filepath.Join(d.base, fmt.Sprintf("missing%d", i)))
	}
	if err := saveDocument(filepath.Join(d.base, configName), d.config); err != nil {
		t.Fatal(err)
	}
	lock, err := lockFile(filepath.Join(d.base, "sblink.log.lock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	warnings := 0
	d.log.console = eventWriter{func(line string) {
		if strings.Contains(line, "Path does not exist") {
			warnings++
			cancel()
		}
	}}
	done := make(chan struct{})
	go func() { defer close(done); d.run(ctx, make(chan string)) }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("canceled validation blocked shutdown")
	}
	if warnings != 1 {
		t.Fatalf("validation continued after cancellation: %d warnings", warnings)
	}
}

func TestCanceledLogContextKeepsCleanExit(t *testing.T) {
	log := testLogger(t)
	lock, err := lockFile(log.path+".lock", false)
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
	start := time.Now()
	log.LogContext(ctx, "INFO", "canceled")
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("log ignored caller deadline")
	}
	if log.Err() != nil {
		t.Fatalf("caller cancellation became a logging failure: %v", log.Err())
	}
}

func TestResetLockWaitCancels(t *testing.T) {
	for _, name := range []string{".sblink-config.lock", ".sblink-mappings.lock", "sblink.log.lock"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, configName)
			writeFile(t, path, "keep")
			lock, err := lockFile(filepath.Join(base, name), false)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := lock.Close(); err != nil {
					t.Error(err)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if err := reset(ctx, base, &output{writer: &bytes.Buffer{}}); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("reset lock wait: %v", err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
				t.Fatalf("canceled reset changed config: %q %v", data, err)
			}
		})
	}
}

func TestNestedManualMapping(t *testing.T) {
	library := t.TempDir()
	want := makeDir(t, filepath.Join(library, "deep", "vendor", "special"))
	makeDir(t, filepath.Join(library, "KMIA-airport"))
	for _, pattern := range []string{filepath.Join("vendor", "special"), filepath.Join("ven*", "spec?al"), filepath.Join("vendor", "**", "special")} {
		mappings := newDocument()
		mappings.set("A321", pattern)
		got, err := findMatches(context.Background(), library, &Plan{Aircraft: "A321", Origin: "KMIA"}, mappings, testLogger(t))
		if err != nil || len(got) != 1 || got[0] != want {
			t.Fatalf("mapping %q: %v %v", pattern, got, err)
		}
	}
}

func TestMappingPatterns(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"vendor/special", "deep/vendor/special", true},
		{"vendor/*", "deep/vendor/special", true},
		{"vendor/*", "deep/vendor/nested/special", false},
		{"vendor/**/special", "deep/vendor/special", true},
		{"vendor/**/special", "deep/vendor/a/b/special", true},
		{"vendor/**/special", "deep/vendor/a/other", false},
		{"ven?or/[s]pecial", "deep/vendor/special", true},
		{"special", "special/nested", false},
	} {
		if got := matchesMapping(strings.Split("**/"+tc.pattern, "/"), tc.path); got != tc.want {
			t.Errorf("%q against %q: got %v want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"[!s]pecial", "xpecial", true},
		{"[!s]pecial", "special", false},
		{"[^s]pecial", "special", true},
		{"[^s]pecial", "xpecial", false},
		{"[^]", "^", true},
		{"[-s]", "-", true},
		{"[s-]", "-", true},
		{"[-s]", "^", false},
		{"[]s]", "]", true},
		{"[!]]", "]", false},
		{"[!]]", "x", true},
		{"[[]", "[", true},
		{"[!]", "[!]", true},
		{"abc[", "abc[", true},
		{"[a-b-c]", "c", true},
		{"[a-b-c]", "-", true},
		{"[a-b-c-d]", "d", true},
		{"[a--b]", "b", true},
		{"[a--b]", "a", false},
		{"[z-a]", "z", false},
		{"[!z-a]", "z", true},
		{"[b-a!]", "x", true},
		{"[b-a!-a]", "-", false},
		{"[b-a!-a]", "a", false},
		{"[b-a!-a]", "x", true},
		{"[xb-a-z]", "x", true},
		{"[xb-a-z]", "y", false},
		{"[xb-a-z]", "-", true},
		{"[xb-a!-z]", "m", true},
		{"[é-ÿ]", "ñ", true},
	} {
		pattern := []string{"**", mappingComponent(tc.pattern)}
		if got := matchesMapping(pattern, tc.name); got != tc.want {
			t.Errorf("Python pattern %q against %q: got %v want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func FuzzMappingComponent(f *testing.F) {
	for _, pattern := range []string{"", "[!s]pecial", "[^s]", "[a-b-c]", "[a--b]", "[b-a!-a]", "[xb-a-z]", "[]s]", "[é-ÿ]", "abc["} {
		f.Add(pattern)
	}
	f.Fuzz(func(t *testing.T, pattern string) {
		if !utf8.ValidString(pattern) {
			t.Skip()
		}
		if _, err := path.Match(mappingComponent(pattern), ""); err != nil {
			t.Fatalf("pattern %q produced invalid translation: %v", pattern, err)
		}
	})
}

func TestPythonMappingClasses(t *testing.T) {
	library := t.TempDir()
	makeDir(t, filepath.Join(library, "special"))
	makeDir(t, filepath.Join(library, "xpecial"))
	for _, tc := range []struct{ pattern, want string }{{"[!s]pecial", "xpecial"}, {"[^s]pecial", "special"}, {"[r-s-t]pecial", "special"}, {"[a--s]pecial", "special"}} {
		mappings := newDocument()
		mappings.set("A321", tc.pattern)
		got, err := findMatches(context.Background(), library, &Plan{Aircraft: "A321"}, mappings, testLogger(t))
		if err != nil || len(got) != 1 || filepath.Base(got[0]) != tc.want {
			t.Fatalf("mapping %q: %v %v", tc.pattern, got, err)
		}
	}
}
