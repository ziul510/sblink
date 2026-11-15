package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	configName         = "sblink_config.json"
	defaultPollSeconds = 60
	minimumPollSeconds = 10
)

// Keep insertion order to preserve the Python file format
type document struct {
	keys   []string
	values map[string]any
}

func newDocument() *document { return &document{values: make(map[string]any)} }

func (d *document) set(key string, value any) {
	if _, ok := d.values[key]; !ok {
		d.keys = append(d.keys, key)
	}
	d.values[key] = value
}

func (d *document) text(key string) string {
	s, _ := d.values[key].(string)
	return s
}

func (d *document) identity(key string) *string {
	if d.values[key] == nil {
		return nil
	}
	s := scalar(d.values[key])
	return &s
}

func (d *document) integer(key string, fallback int) int {
	s := scalar(d.values[key])
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func decodeDocument(data []byte) (*document, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("invalid UTF-8 JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, errors.New("expected a JSON object")
	}
	d := newDocument()
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var value any
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		d.set(key.(string), value)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing JSON data")
	}
	return d, nil
}

func (d *document) encode() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range d.keys {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString("\n    ")
		for j, value := range []any{key, d.values[key]} {
			var part bytes.Buffer
			encoder := json.NewEncoder(&part)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(value); err != nil {
				return nil, err
			}
			out.Write(bytes.TrimSuffix(part.Bytes(), []byte("\n")))
			if j == 0 {
				out.WriteString(": ")
			}
		}
	}
	if len(d.keys) > 0 {
		out.WriteByte('\n')
	}
	out.WriteByte('}')
	var indented bytes.Buffer
	if err := json.Indent(&indented, out.Bytes(), "", "    "); err != nil {
		return nil, err
	}
	return indented.Bytes(), nil
}

func defaults(d *document) {
	for _, item := range []struct {
		key   string
		value any
	}{
		{"community_folder_path", ""}, {"addon_library_path", ""}, {"simbrief_user_id", ""},
		{"poll_interval_seconds", defaultPollSeconds}, {"last_processed_plan_id", nil},
		{"last_processed_timestamp", nil}, {"run_hidden", "y"},
	} {
		if _, ok := d.values[item.key]; !ok {
			d.set(item.key, item.value)
		}
	}
}

func loadConfig(base string) (*document, error) {
	d := newDocument()
	data, err := os.ReadFile(filepath.Join(base, configName))
	if err == nil {
		d, err = decodeDocument(data)
	}
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if d == nil {
		d = newDocument()
	}
	defaults(d)
	if err != nil {
		err = fmt.Errorf("Failed to load config: %w", err)
	}
	return d, err
}

func saveDocument(path string, d *document) error {
	data, err := d.encode()
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return atomicWrite(path, diskText(data))
}

func atomicWrite(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".sblink-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	name := f.Name()
	defer func() {
		if e := os.Remove(name); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = errors.Join(err, e)
		}
	}()
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := replaceFile(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func updateConfig(ctx context.Context, base string, changes, expected *document) (err error) {
	lock, err := lockFileContext(ctx, filepath.Join(base, ".sblink-config.lock"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	d, err := loadConfig(base)
	if err != nil {
		return err
	}
	if expected != nil && !d.sameLinkSettings(expected) {
		return errors.New("Configuration changed during link cycle; will retry next poll")
	}
	community, user, libraries := d.text("community_folder_path"), d.text("simbrief_user_id"), d.libraries()
	for _, key := range changes.keys {
		d.set(key, changes.values[key])
	}
	if community != d.text("community_folder_path") || user != d.text("simbrief_user_id") || !slices.Equal(libraries, d.libraries()) {
		d.set("last_processed_plan_id", nil)
		d.set("last_processed_timestamp", nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return saveDocument(filepath.Join(base, configName), d)
}

func (d *document) sameLinkSettings(other *document) bool {
	return d.text("community_folder_path") == other.text("community_folder_path") && d.text("simbrief_user_id") == other.text("simbrief_user_id") && slices.Equal(d.libraries(), other.libraries())
}

func libraryKey(i int) string {
	if i == 1 {
		return "addon_library_path"
	}
	return fmt.Sprintf("addon_library_path_%d", i)
}

func (d *document) libraries() []string {
	count := max(1, d.integer("addon_library_count", 1))
	// Reject impossible counts without iterating untrusted billions of keys
	count = min(count, len(d.keys)+1)
	paths := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		paths = append(paths, d.text(libraryKey(i)))
	}
	return paths
}

func (d *document) valid() bool {
	if d.text("community_folder_path") == "" || d.text("simbrief_user_id") == "" {
		return false
	}
	for _, path := range d.libraries() {
		if strings.TrimSpace(path) != "" {
			return true
		}
	}
	return false
}

func validatePaths(ctx context.Context, d *document, log *Logger) bool {
	ok := true
	keys := []string{"community_folder_path"}
	for i := range d.libraries() {
		keys = append(keys, libraryKey(i+1))
	}
	for _, key := range keys {
		if ctx.Err() != nil {
			return false
		}
		path := d.text(key)
		if path == "" {
			log.LogContext(ctx, "ERROR", "Config field '%s' is empty. Run setup", key)
			ok = false
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			log.LogContext(ctx, "ERROR", "Path does not exist: %s", path)
			ok = false
		} else if !info.IsDir() {
			log.LogContext(ctx, "ERROR", "Path is not a directory: %s", path)
			ok = false
		}
	}
	return ok
}

func loadMappings(ctx context.Context, base string) (*document, bool, error) {
	path := filepath.Join(base, "mappings.json")
	lock, err := lockFileContext(ctx, filepath.Join(base, ".sblink-mappings.lock"))
	if err != nil {
		return nil, false, err
	}
	d, created, readErr := readMappings(path)
	return d, created, errors.Join(readErr, lock.Close())
}

func readMappings(path string) (*document, bool, error) {
	data, err := os.ReadFile(path)
	var d *document
	if err == nil {
		d, err = decodeDocument(data)
	}
	if err == nil {
		for _, key := range d.keys {
			if _, ok := d.values[key].(string); !ok {
				err = fmt.Errorf("mapping %s must be a string", key)
				break
			}
		}
	}
	if err == nil {
		return d, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("Failed to load mappings: %w", err)
	}
	d = newDocument()
	for i := 1; i <= 3; i++ {
		d.set(fmt.Sprintf("example%d-ICAOcodeHere", i), fmt.Sprintf("example%d-AircraftOrSceneryFolderNameHere", i))
	}
	return d, true, saveDocument(path, d)
}
