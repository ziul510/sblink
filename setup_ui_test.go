package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupPanelsMatchPython(t *testing.T) {
	for _, width := range []int{80, 120, 190} {
		for step := 1; step <= 6; step++ {
			t.Run(fmt.Sprintf("%d/%d", width, step), func(t *testing.T) {
				var buffer bytes.Buffer
				ui := setupUI{out: &output{writer: &buffer}, width: width}
				ui.screen(step)
				want, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("setup-%d-%d.txt", width, step)))
				if err != nil {
					t.Fatal(err)
				}
				if buffer.String() != string(want) {
					gotLines, wantLines := strings.Split(buffer.String(), "\n"), strings.Split(string(want), "\n")
					for i := 0; i < min(len(gotLines), len(wantLines)); i++ {
						if gotLines[i] != wantLines[i] {
							t.Fatalf("row %d differs\ngot  %q\nwant %q", i, gotLines[i], wantLines[i])
						}
					}
					t.Fatalf("line count got %d want %d", len(gotLines), len(wantLines))
				}
			})
		}
	}
}

func TestSetupColorsAndClear(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buffer bytes.Buffer
	ui := setupUI{out: &output{writer: &buffer}, width: 100, tty: true}
	ui.screen(1)
	ui.prompt("Community folder", "", "example", false)
	for _, sequence := range []string{"\x1b[2J\x1b[H", "\x1b[1;38;2;95;215;255m", "\x1b[38;2;64;128;192m", "\x1b[38;2;176;127;255m", "\x1b[1;38;2;95;223;135m", "\x1b[2m"} {
		if !strings.Contains(buffer.String(), sequence) {
			t.Fatalf("missing terminal style %q", sequence)
		}
	}
}

func TestSetupNarrowPanels(t *testing.T) {
	for step := 1; step <= 6; step++ {
		var buffer bytes.Buffer
		ui := setupUI{out: &output{writer: &buffer}, width: 40}
		ui.screen(step)
		for _, line := range strings.Split(buffer.String(), "\n") {
			if line != "" && cellWidth(line) != 40 {
				t.Fatalf("step %d has wrong width: %q (%d)", step, line, cellWidth(line))
			}
		}
	}
}
