package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ptr(s string) *string            { return &s }
func testLogger(t *testing.T) *Logger { t.Helper(); return newLogger(t.TempDir(), nil) }

func TestAircraftTerms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{"", nil}, {"AB", nil}, {"b738", []string{"B738", "738", "B737", "737"}},
		{"A321", []string{"A321", "321", "A320", "320"}}, {"A21N", []string{"A21N", "A320", "320"}},
		{"A20N", []string{"A20N", "A320", "320"}}, {"A339", []string{"A339", "339", "A330", "330"}},
		{"A359", []string{"A359", "359", "A350", "350"}}, {"E195", []string{"E195", "E190"}},
		{"C172", []string{"C172"}}, {"A32", []string{"A32", "32"}}, {"A32X", []string{"A32X", "A320", "320"}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := aircraftTerms(tc.input); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestNewPlanMatrix(t *testing.T) {
	base := &Plan{ID: ptr("id"), Timestamp: ptr("time"), Origin: "KMIA", Destination: "KLAX", Aircraft: "A321", Airline: "DAL"}
	for _, tc := range []struct {
		name                                     string
		currentID, currentTime, lastID, lastTime *string
		memory                                   bool
		changed                                  string
		want                                     bool
	}{
		{"same", ptr("id"), ptr("time"), ptr("id"), ptr("time"), false, "", false},
		{"timestamp", ptr("id"), ptr("new"), ptr("id"), ptr("time"), false, "", true},
		{"new id", ptr("new"), ptr("time"), ptr("id"), ptr("time"), false, "", true},
		{"id vs empty", ptr("id"), nil, ptr(""), nil, true, "", true},
		{"id vs null", ptr("id"), nil, nil, nil, true, "", false},
		{"timestamp vs empty", nil, ptr("time"), nil, ptr(""), true, "", true},
		{"timestamp vs null", nil, ptr("time"), nil, nil, true, "", false},
		{"null ids same timestamp", nil, ptr("time"), nil, ptr("time"), false, "", false},
		{"null ids new timestamp", nil, ptr("new"), nil, ptr("time"), false, "", true},
		{"null first", nil, nil, nil, nil, false, "", true},
		{"empty first", ptr(""), ptr(""), ptr(""), ptr(""), false, "", true},
		{"memory same", nil, nil, nil, nil, true, "", false},
		{"memory origin", nil, nil, nil, nil, true, "origin", true},
		{"memory destination", nil, nil, nil, nil, true, "destination", true},
		{"memory aircraft", nil, nil, nil, nil, true, "aircraft", true},
		{"memory airline", nil, nil, nil, nil, true, "airline", true},
		{"memory ignores id", ptr("new"), nil, nil, nil, true, "", false},
		{"same id missing timestamp", ptr("id"), nil, ptr("id"), ptr("time"), true, "origin", false},
		{"same id null last timestamp", ptr("id"), ptr("new"), ptr("id"), nil, true, "origin", false},
		{"same id empty last timestamp", ptr("id"), ptr("new"), ptr("id"), ptr(""), true, "", true},
		{"empty current id timestamp", ptr(""), ptr("new"), ptr("id"), ptr("time"), true, "", true},
		{"null current id memory", nil, nil, ptr("id"), ptr("time"), true, "origin", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := *base
			plan.ID = tc.currentID
			plan.Timestamp = tc.currentTime
			switch tc.changed {
			case "origin":
				plan.Origin = "KJFK"
			case "destination":
				plan.Destination = "KJFK"
			case "aircraft":
				plan.Aircraft = "B738"
			case "airline":
				plan.Airline = "AAL"
			}
			var memory *Plan
			if tc.memory {
				memory = base
			}
			if got := isNewPlan(&plan, tc.lastID, tc.lastTime, memory); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	if isNewPlan(nil, nil, nil, nil) {
		t.Fatal("nil plan is not new")
	}
}

func TestExtractRecordedPayloads(t *testing.T) {
	log := testLogger(t)
	for _, tc := range []struct {
		file string
		want *Plan
	}{
		{"well-formed.json", &Plan{ID: ptr("42"), Timestamp: ptr("1700000000"), Origin: "KMIA", Destination: "KLAX", Aircraft: "A321", Airline: "DAL"}},
		{"alternate.json", &Plan{ID: ptr("99"), Timestamp: ptr("123"), Origin: "EGLL", Destination: "LFPG", Aircraft: "B738", Airline: "BAW"}},
		{"wrong-types.json", &Plan{Origin: "123", Destination: "", Aircraft: "", Airline: "TRUE"}},
		{"missing.json", &Plan{}},
		{"empty.json", nil},
		{"null.json", nil},
	} {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			var payload any
			decoder := json.NewDecoder(strings.NewReader(string(data)))
			decoder.UseNumber()
			if err := decoder.Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if got := extractPlan(context.Background(), payload, log); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
	whitespace := extractPlan(context.Background(), map[string]any{"OFPID": " ", "generated": " "}, log)
	if whitespace.ID == nil || *whitespace.ID != "" || whitespace.Timestamp == nil || *whitespace.Timestamp != "" {
		t.Fatal("whitespace must become empty string not null")
	}
	data, err := os.ReadFile(filepath.Join("testdata", "simbrief-recorded.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded any
	if err := json.Unmarshal(data, &recorded); err != nil {
		t.Fatal(err)
	}
	actual := extractPlan(context.Background(), recorded, log)
	if actual == nil || actual.ID != nil || !equalIdentity(actual.Timestamp, ptr("1794757476")) || actual.Origin != "KMIA" || actual.Destination != "KLAX" || actual.Aircraft != "A321" || actual.Airline != "DAL" {
		t.Fatalf("recorded live payload: %+v", actual)
	}
}

func TestSimBriefHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		fails      bool
	}{
		{"valid", `{"ofp_id":123}`, 200, false}, {"bad json", `{`, 200, true}, {"trailing", `{} {}`, 200, true}, {"http", `no`, 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("userid") != "1072997" || r.URL.Query().Get("json") != "1" {
					t.Error("wrong query")
				}
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			api := &SimBrief{client: server.Client(), endpoint: server.URL}
			_, err := api.fetch(context.Background(), " 1072997 ")
			if (err != nil) != tc.fails {
				t.Fatalf("err %v", err)
			}
		})
	}
	api := &SimBrief{client: &http.Client{}, endpoint: "http://127.0.0.1:1", retryDelay: time.Millisecond}
	for _, user := range []string{"", " ", "x/y", "a&b"} {
		if _, err := api.fetch(context.Background(), user); err == nil {
			t.Fatal("accepted invalid user", user)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := api.fetch(ctx, "123"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	api.endpoint = server.URL
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := api.fetch(ctx, "123"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
}
