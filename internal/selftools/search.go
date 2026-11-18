package selftools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxSearchResults = 200
	maxSearchLine    = 300
)

type findArgs struct{ Pattern, Path string }
type searchArgs struct{ Pattern, Path, Glob string }

// globRegexp compiles a slash-separated glob. `*` and `?` stay within one
// path segment, `**` crosses segments, and a pattern without a slash matches
// the file name at any depth.
func globRegexp(glob string) (*regexp.Regexp, error) {
	if glob == "" {
		return nil, fmt.Errorf("pattern required")
	}
	if !strings.Contains(glob, "/") {
		glob = "**/" + glob
	}
	var out strings.Builder
	out.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			if strings.HasPrefix(glob[i:], "**/") {
				out.WriteString("(?:.*/)?")
				i += 2
			} else if strings.HasPrefix(glob[i:], "**") {
				out.WriteString(".*")
				i++
			} else {
				out.WriteString("[^/]*")
			}
		case '?':
			out.WriteString("[^/]")
		default:
			out.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	out.WriteString("$")
	return regexp.Compile(out.String())
}

// walk visits regular files under dir, skipping protected names and symbolic
// links. Paths passed to visit are slash-separated and relative to dir.
func (w workspace) walk(ctx context.Context, dir string, visit func(root *os.Root, base, rel string) (stop bool)) error {
	if dir == "" {
		dir = "."
	}
	root, base, err := w.resolve(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // Read-only handle.
	return fs.WalkDir(root.FS(), filepath.ToSlash(base), func(name string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if name == filepath.ToSlash(base) {
				return err
			}
			return nil // An unreadable entry does not end the search.
		}
		if name != "." && protected(entry.Name()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, filepath.ToSlash(base)), "/")
		if base == "." {
			rel = name
		}
		if rel == "" { // dir named a single file
			rel = path.Base(name)
		}
		if visit(root, name, rel) {
			return fs.SkipAll
		}
		return nil
	})
}

func (w workspace) find(ctx context.Context, args findArgs) (string, error) {
	match, err := globRegexp(args.Pattern)
	if err != nil {
		return "", err
	}
	var found []string
	truncated := false
	err = w.walk(ctx, args.Path, func(_ *os.Root, _, rel string) bool {
		if !match.MatchString(rel) {
			return false
		}
		if len(found) == maxSearchResults {
			truncated = true
			return true
		}
		found = append(found, rel)
		return false
	})
	if err != nil {
		return "", err
	}
	return listing(found, truncated, "No files match."), nil
}

func (w workspace) search(ctx context.Context, args searchArgs) (string, error) {
	if args.Pattern == "" {
		return "", fmt.Errorf("pattern required")
	}
	pattern, err := regexp.Compile(args.Pattern)
	if err != nil {
		return "", fmt.Errorf("pattern is not a valid regular expression: %w", err)
	}
	var only *regexp.Regexp
	if args.Glob != "" {
		if only, err = globRegexp(args.Glob); err != nil {
			return "", err
		}
	}
	var found []string
	truncated := false
	err = w.walk(ctx, args.Path, func(root *os.Root, name, rel string) bool {
		if only != nil && !only.MatchString(rel) {
			return false
		}
		text, err := readRegular(root, filepath.FromSlash(name))
		if err != nil || strings.IndexByte(text[:min(len(text), 8000)], 0) >= 0 {
			return false // Unreadable, oversized and binary files are skipped.
		}
		lines := bufio.NewScanner(bytes.NewReader([]byte(text)))
		lines.Buffer(make([]byte, 0, 64<<10), maxWorkspaceFile+1)
		for number := 1; lines.Scan(); number++ {
			line := lines.Text()
			if !pattern.MatchString(line) {
				continue
			}
			if len(found) == maxSearchResults {
				truncated = true
				return true
			}
			if len(line) > maxSearchLine {
				line = strings.ToValidUTF8(line[:maxSearchLine], "") + "…"
			}
			found = append(found, fmt.Sprintf("%s:%d: %s", path.Clean(rel), number, line))
		}
		return false
	})
	if err != nil {
		return "", err
	}
	return listing(found, truncated, "No matches."), nil
}

func listing(found []string, truncated bool, none string) string {
	if len(found) == 0 {
		return none
	}
	text := strings.Join(found, "\n")
	if truncated {
		text += fmt.Sprintf("\n[stopped at %d results; narrow the pattern or path]", maxSearchResults)
	}
	return text
}
