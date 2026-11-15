package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const markerName = ".sblink"

func aircraftTerms(icao string) []string {
	icao = strings.ToUpper(icao)
	if utf8.RuneCountInString(icao) < 3 {
		return nil
	}
	terms := []string{icao}
	add := func(s string) {
		for _, old := range terms {
			if old == s {
				return
			}
		}
		terms = append(terms, s)
	}
	digits := func(s string) bool {
		if len(s) < 2 {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	strip := func(s string) {
		if (s[0] == 'A' || s[0] == 'B') && digits(s[1:min(4, len(s))]) {
			add(s[1:])
		}
	}
	strip(icao)
	family := map[string]string{"A20": "A320", "A21": "A320", "A22": "A220", "A30": "A300", "A31": "A310", "A33": "A330", "A34": "A340", "A35": "A350", "A38": "A380", "B73": "B737", "B74": "B747", "B75": "B757", "B76": "B767", "B77": "B777", "B78": "B787", "E17": "E170", "E19": "E190"}[icao[:3]]
	if family == "" && strings.HasPrefix(icao, "A3") && len(icao) >= 4 {
		family = icao[:3] + "0"
	}
	if family != "" {
		add(family)
		strip(family)
	}
	return terms
}

func findMatches(ctx context.Context, library string, plan *Plan, mappings *document, log *Logger) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(library)
	if err != nil || !info.IsDir() {
		log.LogContext(ctx, "ERROR", "Addon library invalid: %s", library)
		return nil, fmt.Errorf("Addon library invalid: %s", library)
	}
	terms := aircraftTerms(plan.Aircraft)
	for _, term := range terms {
		mapped := mappings.text(term)
		if mapped == "" {
			continue
		}
		log.LogContext(ctx, "INFO", "Manual mapping found for %s: %s", term, mapped)
		found, err := findMappedFolder(ctx, library, mapped)
		if err != nil {
			return nil, err
		}
		if found != "" {
			log.LogContext(ctx, "MATCH", "Found mapped folder: %s", filepath.Base(found))
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return []string{found}, nil
		}
		log.LogContext(ctx, "WARNING", "Mapped folder '%s' not found in library, falling back to search", mapped)
		break
	}
	type criterion struct {
		name  string
		terms []string
		depth int
	}
	criteria := []criterion{{"origin", []string{plan.Origin}, 6}, {"destination", []string{plan.Destination}, 6}, {"aircraft", terms, 6}, {"airline", []string{plan.Airline}, 6}}
	type node struct {
		path  string
		depth int
	}
	queue := []node{{library, 0}}
	visited := make(map[string]bool)
	matches := make(map[string]bool)
	log.LogContext(ctx, "INFO", "Searching in %s (BFS)", library)
	for head := 0; head < len(queue); head++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		folder := queue[head]
		real, err := resolvePath(folder.path)
		if err != nil {
			return nil, fmt.Errorf("Could not resolve %s: %w", folder.path, err)
		}
		real = pathKey(real)
		if visited[real] {
			continue
		}
		visited[real] = true
		children, err := os.ReadDir(folder.path)
		if err != nil {
			return nil, fmt.Errorf("Could not search %s: %w", folder.path, err)
		}
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			path := filepath.Join(folder.path, child.Name())
			info, err := os.Stat(path)
			if err != nil {
				return nil, fmt.Errorf("Could not inspect %s: %w", path, err)
			}
			if !info.IsDir() {
				continue
			}
			name := strings.ToUpper(child.Name())
			for i := range criteria {
				category := &criteria[i]
				if folder.depth > category.depth {
					continue
				}
				for _, value := range category.terms {
					if utf8.RuneCountInString(value) < 3 {
						continue
					}
					if strings.Contains(name, strings.ToUpper(value)) {
						matches[path] = true
						category.depth = folder.depth
						log.LogContext(ctx, "MATCH", "%s matched %s '%s' at depth %d", child.Name(), category.name, value, folder.depth)
						break
					}
				}
			}
			if folder.depth < 5 {
				queue = append(queue, node{path, folder.depth + 1})
			}
		}
	}
	result := make([]string, 0, len(matches))
	for path := range matches {
		result = append(result, path)
	}
	sort.Strings(result)
	log.LogContext(ctx, "INFO", "Found %d unique matching addons", len(result))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func findMappedFolder(ctx context.Context, library, pattern string) (string, error) {
	pattern = filepath.ToSlash(pathKey(pattern))
	parts := strings.Split("**/"+pattern, "/")
	for i, part := range parts {
		parts[i] = mappingComponent(part)
		if _, err := path.Match(parts[i], ""); err != nil {
			return "", fmt.Errorf("invalid mapping %q: %w", pattern, filepath.ErrBadPattern)
		}
	}
	ancestors := make(map[string]bool)
	var search func(string) (string, error)
	search = func(folder string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		real, err := resolvePath(folder)
		if err != nil {
			return "", fmt.Errorf("Could not resolve %s: %w", folder, err)
		}
		key := pathKey(real)
		if ancestors[key] {
			return "", nil
		}
		ancestors[key] = true
		defer delete(ancestors, key)
		children, err := os.ReadDir(folder)
		if err != nil {
			return "", fmt.Errorf("Could not search %s: %w", folder, err)
		}
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			path := filepath.Join(folder, child.Name())
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("Could not inspect %s: %w", path, err)
			}
			if !info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(library, path)
			if err != nil {
				return "", err
			}
			if matchesMapping(parts, filepath.ToSlash(pathKey(rel))) {
				return path, nil
			}
			if found, err := search(path); found != "" || err != nil {
				return found, err
			}
		}
		return "", nil
	}
	return search(library)
}

func mappingComponent(pattern string) string {
	var out strings.Builder
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '[' {
			if pattern[i] == '\\' {
				out.WriteByte('\\')
			}
			out.WriteByte(pattern[i])
			continue
		}
		end := i + 1
		if end < len(pattern) && pattern[end] == '!' {
			end++
		}
		if end < len(pattern) && pattern[end] == ']' {
			end++
		}
		for end < len(pattern) && pattern[end] != ']' {
			end++
		}
		if end == len(pattern) {
			out.WriteString(`\[`)
			continue
		}
		class := []rune(pattern[i+1 : end])
		var body strings.Builder
		write := func(c rune) {
			if strings.ContainsRune(`\[]^-`, c) {
				body.WriteByte('\\')
			}
			body.WriteRune(c)
		}
		start := 0
		if len(class) > 0 && class[0] == '!' {
			body.WriteByte('!')
			start = 1
		}
		for j := start; j < len(class); j++ {
			if j+2 < len(class) && class[j+1] == '-' {
				if class[j] <= class[j+2] {
					write(class[j])
					body.WriteByte('-')
					write(class[j+2])
				}
				j += 2
			} else {
				write(class[j])
			}
		}
		// Python applies negation after removing reversed ranges
		text := body.String()
		if text == "" {
			out.WriteString("[^\x00-\U0010ffff]")
		} else if text == "!" {
			out.WriteByte('?')
		} else {
			out.WriteByte('[')
			if text[0] == '!' {
				out.WriteByte('^')
				text = text[1:]
			}
			if text[0] == '-' {
				out.WriteByte('\\')
			}
			out.WriteString(text)
			out.WriteByte(']')
		}
		i = end
	}
	return out.String()
}

func matchesMapping(pattern []string, relative string) bool {
	names := strings.Split(relative, "/")
	matched := make([]bool, len(names)+1)
	matched[0] = true
	for _, part := range pattern {
		next := make([]bool, len(matched))
		if part == "**" {
			next[0] = matched[0]
			for i := range names {
				next[i+1] = matched[i+1] || next[i]
			}
		} else {
			for i, name := range names {
				ok, _ := path.Match(part, name)
				next[i+1] = matched[i] && ok
			}
		}
		matched = next
	}
	return matched[len(names)]
}

func hasMarker(path string) bool {
	marker := filepath.Join(path, markerName)
	info, err := os.Lstat(marker)
	return err == nil && info.Mode().IsRegular() && !isLink(marker)
}

func createJunction(link, target string, log *Logger) error {
	link, err := filepath.Abs(link)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("Target path does not exist: %s", target)
	}
	if _, err := os.Lstat(link); err == nil {
		if !isLink(link) {
			return fmt.Errorf("Real item already exists: %s", filepath.Base(link))
		}
		if !hasMarker(link) {
			return fmt.Errorf("Refusing to replace non-sblink junction: %s", filepath.Base(link))
		}
		old, err := resolvePath(link)
		if err != nil {
			return err
		}
		if pathKey(old) == pathKey(target) {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("Cannot remove existing junction/symlink %s: %w", filepath.Base(link), err)
		}
		if err := removeMarker(old); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		return fmt.Errorf("Could not ensure parent directory exists for %s: %w", link, err)
	}
	if err := makeJunction(link, target); err != nil {
		return fmt.Errorf("mklink failed for %s: %w", filepath.Base(link), err)
	}
	if !isLink(link) {
		return fmt.Errorf("mklink reported success but junction not created at %s", filepath.Base(link))
	}
	if err := atomicWrite(filepath.Join(target, markerName), []byte("sblink")); err != nil {
		return errors.Join(fmt.Errorf("write sblink marker: %w", err), os.Remove(link))
	}
	log.Log("LINK", "Created junction: %s -> %s", filepath.Base(link), target)
	return nil
}

func removeMarker(target string) error {
	err := os.Remove(filepath.Join(target, markerName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func clearJunctions(ctx context.Context, community string, libraries []string, log *Logger) error {
	info, err := os.Stat(community)
	if err != nil {
		return fmt.Errorf("Community folder does not exist: %s", community)
	}
	if !info.IsDir() {
		return fmt.Errorf("Community path is not a directory: %s", community)
	}
	hasLibrary := false
	for _, library := range libraries {
		if library != "" {
			hasLibrary = true
		}
	}
	if !hasLibrary {
		return errors.New("No addon library paths are set")
	}
	items, err := os.ReadDir(community)
	if err != nil {
		return err
	}
	type owned struct{ link, target string }
	var links []owned
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(community, item.Name())
		if !isLink(path) {
			continue
		}
		if !hasMarker(path) {
			log.LogContext(ctx, "INFO", "Skipping non-sblink junction: %s", item.Name())
			continue
		}
		target, err := resolvePath(path)
		if err != nil {
			return err
		}
		links = append(links, owned{path, target})
	}
	removed := 0
	var failures error
	failedTargets := make(map[string]bool)
	for _, link := range links {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		if err := os.Remove(link.link); err != nil {
			log.LogContext(ctx, "ERROR", "Failed to remove junction %s: %v", filepath.Base(link.link), err)
			failures = errors.Join(failures, err)
			failedTargets[pathKey(link.target)] = true
		} else {
			removed++
			log.LogContext(ctx, "UNLINK", "Removed junction: %s", filepath.Base(link.link))
		}
	}
	for _, link := range links {
		if !failedTargets[pathKey(link.target)] {
			failures = errors.Join(failures, removeMarker(link.target))
		}
	}
	log.LogContext(ctx, "INFO", "Cleared %d junctions", removed)
	return failures
}

func uniqueAddons(ctx context.Context, paths []string, log *Logger) []string {
	seen := make(map[string]string)
	var result []string
	for _, path := range paths {
		key := pathKey(filepath.Base(path))
		if first, ok := seen[key]; ok {
			log.LogContext(ctx, "WARNING", "Duplicate addon name '%s'; using %s instead of %s", filepath.Base(path), first, path)
			continue
		}
		seen[key] = path
		result = append(result, path)
	}
	return result
}
