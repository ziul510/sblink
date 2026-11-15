package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type setupUI struct {
	out   *output
	width int
	tty   bool
}

func (u *setupUI) color(style, text string) string {
	if !u.tty {
		return text
	}
	code := map[string]string{"banner": "1;38;2;95;215;255", "border": "38;2;64;128;192", "step": "38;2;176;127;255", "title": "1;38;2;176;127;255", "prompt": "1;38;2;95;223;135", "dim": "2", "green": "32", "success": "1;32", "yellow": "33", "red": "31", "cyan": "36", "command": "1;36", "bold": "1", "choices": "1;35"}[style]
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (u *setupUI) printf(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	for _, item := range []struct{ token, style string }{{"OK", "green"}, {"!", "yellow"}, {"X", "red"}} {
		text = strings.ReplaceAll(text, "  "+item.token+"  ", "  "+u.color(item.style, item.token)+"  ")
	}
	u.out.printf("%s", text)
}

func cellWidth(text string) int {
	width := 0
	for len(text) > 0 {
		if strings.HasPrefix(text, "\x1b[") {
			if end := strings.IndexByte(text, 'm'); end >= 0 {
				text = text[end+1:]
				continue
			}
		}
		r, n := utf8.DecodeRuneInString(text)
		text = text[n:]
		if unicode.Is(unicode.Mn, r) || r == 0x200d {
			continue
		}
		if r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff || r >= 0xfe10 && r <= 0xfe6f || r >= 0xff00 && r <= 0xff60 || r >= 0x1f300) {
			width += 2
		} else {
			width++
		}
	}
	return width
}

func wrapConsole(text string, width int) []string {
	var result []string
	for _, line := range strings.Split(text, "\n") {
		if cellWidth(line) <= width {
			result = append(result, line)
			continue
		}
		var current strings.Builder
		used := 0
		for index, word := range strings.Split(line, " ") {
			space := 0
			if index > 0 {
				space = 1
			}
			if used+space+cellWidth(word) > width && used > 0 {
				result = append(result, current.String())
				current.Reset()
				used = 0
				space = 0
			}
			if space > 0 {
				current.WriteByte(' ')
				used++
			}
			for len(word) > 0 {
				if strings.HasPrefix(word, "\x1b[") {
					if end := strings.IndexByte(word, 'm'); end >= 0 {
						current.WriteString(word[:end+1])
						word = word[end+1:]
						continue
					}
				}
				r, n := utf8.DecodeRuneInString(word)
				part := string(r)
				size := cellWidth(part)
				if used+size > width {
					result = append(result, current.String())
					current.Reset()
					used = 0
				}
				current.WriteString(part)
				used += size
				word = word[n:]
			}
		}
		result = append(result, current.String())
	}
	return result
}

func (u *setupUI) rule(left, right, title, style string) {
	inner := u.width - 2
	if title == "" {
		u.printf("%s\n", u.color(style, left+strings.Repeat("─", inner)+right))
		return
	}
	label := " " + title + " "
	length := cellWidth(label)
	if length > inner {
		label = strings.Join(wrapConsole(label, inner)[:1], "")
		length = cellWidth(label)
	}
	before := (inner - length) / 2
	u.printf("%s%s%s\n", u.color(style, left+strings.Repeat("─", before)), label, u.color(style, strings.Repeat("─", inner-length-before)+right))
}

func (u *setupUI) panel(title, body, style string, padding int) {
	u.rule("╭", "╮", title, style)
	for i := 0; i < padding; i++ {
		u.printf("%s%s%s\n", u.color(style, "│"), strings.Repeat(" ", u.width-2), u.color(style, "│"))
	}
	for _, line := range wrapConsole(body, u.width-6) {
		u.printf("%s  %s%s  %s\n", u.color(style, "│"), line, strings.Repeat(" ", max(0, u.width-6-cellWidth(line))), u.color(style, "│"))
	}
	for i := 0; i < padding; i++ {
		u.printf("%s%s%s\n", u.color(style, "│"), strings.Repeat(" ", u.width-2), u.color(style, "│"))
	}
	u.rule("╰", "╯", "", style)
}

func (u *setupUI) header(step, body string) {
	if width, tty := setupTerminal(u.out.writer); tty {
		u.width = width
	}
	if u.tty {
		u.printf("\x1b[2J\x1b[H")
	}
	lines := strings.Split(setupBanner, "\n")
	maximum := 0
	for _, line := range lines {
		maximum = max(maximum, cellWidth(line))
	}
	u.rule("╭", "╮", "", "border")
	for _, line := range lines {
		left := max(0, (u.width-2-maximum)/2)
		for _, part := range wrapConsole(line, u.width-2-left) {
			u.printf("%s%s%s%s%s\n", u.color("border", "│"), strings.Repeat(" ", left), u.color("banner", part), strings.Repeat(" ", max(0, u.width-2-left-cellWidth(part))), u.color("border", "│"))
		}
	}
	u.rule("╰", "╯", u.color("banner", "SimBrief Linker"), "border")
	u.printf("\n")
	if step == "Setup Complete" {
		u.panel(u.color("success", "── Setup Complete ──"), body, "green", 1)
	} else {
		u.panel(u.color("title", "── "+step+" ──"), body, "step", 0)
	}
	u.printf("\n")
}

func (u *setupUI) prompt(label, current, hint string, choice bool) {
	if hint != "" {
		u.printf("  %s\n", u.color("dim", hint))
	}
	if current != "" {
		u.printf("  %s %s\n", u.color("dim", "Current:"), u.color("yellow", current))
	}
	prompt := "  " + u.color("prompt", label)
	if current != "" {
		prompt += "  " + u.color("dim", "(Enter to keep)")
	}
	if choice {
		prompt += " " + u.color("choices", "[y/n]")
	}
	if current != "" {
		prompt += " " + u.color("command", "("+current+")")
	}
	u.printf("%s: ", prompt)
}

func (u *setupUI) screen(step int) {
	switch step {
	case 1:
		u.header("1 / 5  MSFS Paths", "  sblink needs to know where your MSFS community folder is,\n  and the root of your addon library (where your liveries / addons live)\n\n"+u.color("dim", "  Both paths must already exist on disk"))
	case 2:
		u.header("2 / 5  SimBrief Account", "  Your numeric SimBrief Pilot ID.  Find it at "+u.color("bold", "simbrief.com")+" -> Account -> Briefing")
	case 3:
		u.header("3 / 5  Polling Interval", "  How often sblink polls SimBrief for a new flight plan.  "+u.color("dim", "Minimum: 10 s"))
	case 4:
		u.header("4 / 5  Startup & Background", "  Configure background behavior and startup options.\n\n"+u.color("dim", "  These settings can be changed later by running setup again"))
	case 5:
		u.header("5 / 5  Manual Folder Mapping", "  Would you like to manually map specific aircraft to folders?\n"+u.color("dim", "  This creates mappings.json where you can specify overrides"))
	case 6:
		u.header("Setup Complete", "  "+u.color("green", "OK")+"  Configuration saved\n\n  "+u.color("dim", "To start the daemon:")+"        "+u.color("command", "sblink.exe run")+"\n  "+u.color("dim", "To check your settings:")+"     "+u.color("command", "sblink.exe status")+"\n  "+u.color("dim", "To trigger a manual sync:")+"   "+u.color("command", "sblink.exe manual"))
	}
}
