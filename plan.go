package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	simBriefURL    = "https://www.simbrief.com/api/xml.fetcher.php"
	responseLimit  = 32 << 20
	requestTimeout = 30 * time.Second
)

type Plan struct {
	ID          *string `json:"ofp_id"`
	Timestamp   *string `json:"timestamp"`
	Origin      string  `json:"orig"`
	Destination string  `json:"dest"`
	Aircraft    string  `json:"aircraft"`
	Airline     string  `json:"airline"`
}

func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		if x {
			return "True"
		}
		return "False"
	default:
		return ""
	}
}

func truth(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case string:
		return x != ""
	case bool:
		return x
	case json.Number:
		n, err := x.Float64()
		return err == nil && n != 0
	case float64:
		return x != 0
	case int:
		return x != 0
	case map[string]any:
		return len(x) != 0
	case []any:
		return len(x) != 0
	default:
		return false
	}
}

func extractPlan(ctx context.Context, data any, log *Logger) *Plan {
	d, ok := data.(map[string]any)
	if !ok || len(d) == 0 {
		return nil
	}
	section := func(key string) map[string]any {
		value := d[key]
		m, ok := value.(map[string]any)
		if value != nil && !ok {
			log.LogContext(ctx, "WARNING", "Unexpected '%s' structure in SimBrief response", key)
		}
		return m
	}
	params, origin, destination := section("params"), section("origin"), section("destination")
	aircraft, general := section("aircraft"), section("general")
	identity := func(values ...any) *string {
		for _, v := range values {
			if text := scalar(v); truth(v) && text != "" {
				text = strings.TrimSpace(text)
				return &text
			}
		}
		return nil
	}
	code := func(values ...any) string {
		for _, value := range values {
			if truth(value) && scalar(value) != "" {
				return strings.ToUpper(strings.TrimSpace(scalar(value)))
			}
		}
		return ""
	}
	return &Plan{
		ID:        identity(params["OFPID"], d["OFPID"], d["ofp_id"], params["ofp_id"]),
		Timestamp: identity(params["TIME_GENERATED"], d["time_generated"], params["time_generated"], d["generated"]),
		Origin:    code(origin["icao_code"], d["orig"]), Destination: code(destination["icao_code"], d["dest"]),
		Aircraft: code(aircraft["icaocode"], d["aircraft"]), Airline: code(general["icao_airline"], d["airline"]),
	}
}

func nonempty(s *string) bool { return s != nil && *s != "" }
func equalIdentity(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func isNewPlan(plan *Plan, lastID, lastTime *string, memory *Plan) bool {
	if plan == nil {
		return false
	}
	if nonempty(plan.ID) && lastID != nil {
		if !equalIdentity(plan.ID, lastID) {
			return true
		}
		if nonempty(plan.Timestamp) && lastTime != nil {
			return !equalIdentity(plan.Timestamp, lastTime)
		}
		return false
	}
	if nonempty(plan.Timestamp) && lastTime != nil {
		return !equalIdentity(plan.Timestamp, lastTime)
	}
	if memory != nil {
		return plan.Origin != memory.Origin || plan.Destination != memory.Destination || plan.Aircraft != memory.Aircraft || plan.Airline != memory.Airline
	}
	return true
}

type SimBrief struct {
	client     *http.Client
	endpoint   string
	retryDelay time.Duration
}

func (s *SimBrief) fetch(ctx context.Context, user string) (any, error) {
	if user == "" {
		return nil, errors.New("Invalid SimBrief user ID: must be a non-empty string")
	}
	user = strings.TrimSpace(user)
	valid := user != ""
	for _, c := range user {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			valid = false
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("Invalid SimBrief user ID: %s", user)
	}
	endpoint, err := url.Parse(s.endpoint)
	if err != nil {
		return nil, fmt.Errorf("SimBrief URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("userid", user)
	query.Set("json", "1")
	endpoint.RawQuery = query.Encode()
	for attempt := 0; ; attempt++ {
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			cancel()
			return nil, err
		}
		response, err := s.client.Do(request)
		if err == nil {
			var data any
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				err = fmt.Errorf("HTTP error: %s", response.Status)
			} else {
				bounded := &io.LimitedReader{R: response.Body, N: responseLimit + 1}
				dec := json.NewDecoder(bounded)
				dec.UseNumber()
				if e := dec.Decode(&data); e != nil {
					err = fmt.Errorf("Failed to parse JSON: %w", e)
				}
				if err == nil {
					var extra any
					if e := dec.Decode(&extra); !errors.Is(e, io.EOF) {
						err = errors.New("Failed to parse JSON: trailing data")
					}
				}
				if bounded.N == 0 {
					err = errors.New("Failed to parse JSON: response exceeds size limit")
				}
			}
			err = errors.Join(err, response.Body.Close())
			cancel()
			return data, err
		}
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt == 2 {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("Request timed out: %w", err)
			}
			return nil, fmt.Errorf("Connection failed: %w", err)
		}
		if err := sleepContext(ctx, s.retryDelay*time.Duration(1<<attempt)); err != nil {
			return nil, err
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
