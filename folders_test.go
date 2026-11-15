package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func makeDir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBFS(t *testing.T) {
	library := t.TempDir()
	shallow := makeDir(t, filepath.Join(library, "KMIA-airport"))
	otherShallow := makeDir(t, filepath.Join(library, "KMIA-second"))
	makeDir(t, filepath.Join(shallow, "KMIA-deeper"))
	aircraft := makeDir(t, filepath.Join(library, "aircraft", "B737"))
	livery := makeDir(t, filepath.Join(aircraft, "liveries", "DAL-special"))
	deep := library
	for i := 0; i < 5; i++ {
		deep = makeDir(t, filepath.Join(deep, "level"))
	}
	dest := makeDir(t, filepath.Join(deep, "KLAX-depth-5"))
	makeDir(t, filepath.Join(dest, "KLAX-depth-6"))
	makeDir(t, filepath.Join(library, "AB-short"))
	plan := &Plan{Origin: "KMIA", Destination: "KLAX", Aircraft: "B738", Airline: "DAL"}
	got, err := findMatches(context.Background(), library, plan, newDocument(), testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{shallow, otherShallow, aircraft, livery, dest}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	got, err = findMatches(context.Background(), library, &Plan{Origin: "AB", Aircraft: "A"}, newDocument(), testLogger(t))
	if err != nil || len(got) != 0 {
		t.Fatalf("short terms: %v %v", got, err)
	}
	makeDir(t, filepath.Join(library, "32-too-short"))
	got, err = findMatches(context.Background(), library, &Plan{Aircraft: "A32"}, newDocument(), testLogger(t))
	if err != nil || len(got) != 0 {
		t.Fatalf("short stripped candidate: %v %v", got, err)
	}
}

func TestManualMapping(t *testing.T) {
	library := t.TempDir()
	mapped := makeDir(t, filepath.Join(library, "deep", "special-aircraft"))
	airport := makeDir(t, filepath.Join(library, "KMIA-airport"))
	mappings := newDocument()
	mappings.set("B737", "special-aircraft")
	if hasWindowsIntegration() {
		mappings.set("B737", "SPECIAL-AIRCRAFT")
	}
	plan := &Plan{Origin: "KMIA", Aircraft: "B738"}
	got, err := findMatches(context.Background(), library, plan, mappings, testLogger(t))
	if err != nil || !reflect.DeepEqual(got, []string{mapped}) {
		t.Fatalf("mapping: %v %v", got, err)
	}
	mappings.set("B737", "not-present")
	got, err = findMatches(context.Background(), library, plan, mappings, testLogger(t))
	if err != nil || !reflect.DeepEqual(got, []string{airport}) {
		t.Fatalf("fallback: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := findMatches(ctx, library, plan, mappings, testLogger(t)); err == nil {
		t.Fatal("search ignored cancellation")
	}
}

func TestUniqueAddons(t *testing.T) {
	got := uniqueAddons(context.Background(), []string{filepath.Join("one", "same"), filepath.Join("two", "same")}, testLogger(t))
	if len(got) != 1 {
		t.Fatal(got)
	}
}

func TestLiveLibraries(t *testing.T) {
	if os.Getenv("SBLINK_LIVE") != "1" {
		t.Skip("set SBLINK_LIVE=1 for read-only real-library verification")
	}
	api := &SimBrief{client: &http.Client{}, endpoint: "https://www.simbrief.com/api/xml.fetcher.php"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	data, err := api.fetch(ctx, "1072997")
	if err != nil {
		t.Fatal(err)
	}
	plan := extractPlan(ctx, data, testLogger(t))
	if plan == nil {
		t.Fatal("no plan")
	}
	t.Logf("live plan: %s -> %s | %s | %s", plan.Origin, plan.Destination, plan.Aircraft, plan.Airline)
	for _, library := range []string{`D:\addonlinker`, `E:\addonlinker`} {
		matches, err := findMatches(ctx, library, plan, newDocument(), testLogger(t))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d matches", library, len(matches))
		for _, match := range matches {
			t.Log(strings.TrimPrefix(match, library))
		}
	}
	community := `C:\Users\ziul\AppData\Local\Packages\Microsoft.Limitless_8wekyb3d8bbwe\LocalCache\Packages\Community`
	info, err := os.Stat(community)
	if err != nil || !info.IsDir() {
		t.Fatalf("community: %v", err)
	}
}
