package store

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MarkdownStore reads and writes specs from markdown files in the filesystem.
//
// Each directory may contain SPECS.md, NOTES.md, TESTS.md, BENCHMARKS.md.
// Specs are headed by ## SPEC-001 or ## SPEC-001: Title.
//
// Resolution: when looking up a spec from a source file, the store walks
// up the directory tree from the file's directory to the root.
// The nearest match wins. This means the same ID (e.g. SPEC-001) can
// appear in different directories with different meanings.
type MarkdownStore struct {
	root    string
	entries map[string][]*mdEntry // id → entries (may span multiple dirs)
}

type mdEntry struct {
	spec *Spec
	dir  string // directory containing the spec file
}

var specHeadingRe = regexp.MustCompile(`^##\s+((?:SPEC|NOTE|TEST|BENCH)-\d+)(?:\s*:\s*(.*))?$`)

// OpenMarkdown scans root and all subdirectories for spec markdown files.
func OpenMarkdown(root string) (*MarkdownStore, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	ms := &MarkdownStore{
		root:    root,
		entries: make(map[string][]*mdEntry),
	}
	if err := ms.scan(); err != nil {
		return nil, err
	}
	return ms, nil
}

// scan walks the root directory tree looking for spec files.
func (ms *MarkdownStore) scan() error {
	return filepath.WalkDir(ms.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if d.IsDir() {
			// Skip .git and vendor directories.
			base := d.Name()
			if base == ".git" || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !isSpecFile(name) {
			return nil
		}
		return ms.parseFile(path)
	})
}

func isSpecFile(name string) bool {
	switch strings.ToUpper(name) {
	case "SPECS.MD", "NOTES.MD", "TESTS.MD", "BENCHMARKS.MD":
		return true
	}
	return false
}

func kindFromFilename(name string) string {
	switch strings.ToUpper(name) {
	case "SPECS.MD":
		return "SPEC"
	case "NOTES.MD":
		return "NOTE"
	case "TESTS.MD":
		return "TEST"
	case "BENCHMARKS.MD":
		return "BENCH"
	}
	return ""
}

func filenameForKind(kind string) string {
	switch strings.ToUpper(kind) {
	case "SPEC":
		return "SPECS.md"
	case "NOTE":
		return "NOTES.md"
	case "TEST":
		return "TESTS.md"
	case "BENCH":
		return "BENCHMARKS.md"
	}
	return "SPECS.md"
}

func kindFromID(id string) string {
	if idx := strings.Index(id, "-"); idx > 0 {
		return strings.ToUpper(id[:idx])
	}
	return "SPEC"
}

// parseFile reads a single markdown file and extracts all spec entries.
func (ms *MarkdownStore) parseFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dir := filepath.Dir(path)
	fileKind := kindFromFilename(filepath.Base(path))

	scanner := bufio.NewScanner(f)
	var current *Spec
	var bodyLines []string

	flush := func() {
		if current == nil {
			return
		}
		body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
		if current.Title == "" && body != "" {
			// First line of body is the title.
			lines := strings.SplitN(body, "\n", 2)
			current.Title = strings.TrimSpace(lines[0])
			if len(lines) > 1 {
				current.Body = strings.TrimSpace(lines[1])
			}
		} else {
			current.Body = body
		}
		ms.entries[current.ID] = append(ms.entries[current.ID], &mdEntry{
			spec: current,
			dir:  dir,
		})
		current = nil
		bodyLines = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if m := specHeadingRe.FindStringSubmatch(line); m != nil {
			flush()
			current = &Spec{
				ID:   strings.ToUpper(m[1]),
				Kind: fileKind,
			}
			if current.Kind == "" {
				current.Kind = kindFromID(current.ID)
			}
			if m[2] != "" {
				current.Title = strings.TrimSpace(m[2])
			}
			// Compute namespace from relative dir path.
			rel, _ := filepath.Rel(ms.root, dir)
			if rel != "." {
				current.Namespace = rel
			}
			continue
		}
		if current != nil {
			bodyLines = append(bodyLines, line)
		}
	}
	flush()

	return scanner.Err()
}

// Refresh rescans the filesystem for spec changes.
func (ms *MarkdownStore) Refresh() error {
	ms.entries = make(map[string][]*mdEntry)
	return ms.scan()
}

// ResolveSpec finds a spec by walking up from fromPath to the root.
// The nearest matching directory wins.
func (ms *MarkdownStore) ResolveSpec(id, fromPath string) (*Spec, error) {
	id = strings.ToUpper(id)
	entries := ms.entries[id]
	if len(entries) == 0 {
		return nil, fmt.Errorf("spec %s not found", id)
	}

	fromPath, _ = filepath.Abs(fromPath)
	dir := fromPath
	info, err := os.Stat(dir)
	if err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}

	// Walk up from dir to root, looking for a match.
	for {
		for _, e := range entries {
			if e.dir == dir {
				return e.spec, nil
			}
		}
		if dir == ms.root || dir == "/" || dir == "." {
			break
		}
		dir = filepath.Dir(dir)
	}

	// No directory match found — return the root-level entry if any.
	for _, e := range entries {
		rel, _ := filepath.Rel(ms.root, e.dir)
		if rel == "." {
			return e.spec, nil
		}
	}

	// Just return the first entry.
	return entries[0].spec, nil
}

// GetSpec returns a spec by ID, preferring the root-level definition.
func (ms *MarkdownStore) GetSpec(id string) (*Spec, error) {
	return ms.ResolveSpec(id, ms.root)
}

// ListSpecs returns specs filtered by kind and/or directory scope.
// Empty kind returns all kinds. Empty dir returns all directories.
func (ms *MarkdownStore) ListSpecs(kind, dir string) ([]*Spec, error) {
	kind = strings.ToUpper(kind)
	var result []*Spec
	seen := make(map[string]bool)

	for _, entries := range ms.entries {
		for _, e := range entries {
			if kind != "" && e.spec.Kind != kind {
				continue
			}
			if dir != "" && e.dir != dir {
				continue
			}
			key := e.spec.ID + "|" + e.dir
			if !seen[key] {
				seen[key] = true
				result = append(result, e.spec)
			}
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result, nil
}

// CreateSpec appends a new spec entry to the appropriate markdown file in dir.
// If the file doesn't exist, it is created with a heading.
func (ms *MarkdownStore) CreateSpec(spec *Spec) error {
	dir := ms.root
	if spec.Namespace != "" {
		dir = filepath.Join(ms.root, spec.Namespace)
	}
	spec.ID = strings.ToUpper(spec.ID)
	if spec.Kind == "" {
		spec.Kind = kindFromID(spec.ID)
	}

	filename := filenameForKind(spec.Kind)
	path := filepath.Join(dir, filename)

	// Ensure directory exists.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// Check if file exists; if not, create with heading.
	var prefix string
	if _, err := os.Stat(path); os.IsNotExist(err) {
		word := strings.ToLower(spec.Kind)
		prefix = "# " + strings.ToUpper(word[:1]) + word[1:] + "s\n\n"
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var b strings.Builder
	b.WriteString(prefix)
	if spec.Title != "" {
		fmt.Fprintf(&b, "## %s: %s\n", spec.ID, spec.Title)
	} else {
		fmt.Fprintf(&b, "## %s\n", spec.ID)
	}
	if spec.Body != "" {
		b.WriteString(spec.Body)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if _, err := f.WriteString(b.String()); err != nil {
		return err
	}

	// Update in-memory cache.
	now := time.Now().UTC()
	spec.CreatedAt = now
	spec.UpdatedAt = now
	rel, _ := filepath.Rel(ms.root, dir)
	if rel == "." {
		spec.Namespace = ""
	} else {
		spec.Namespace = rel
	}
	ms.entries[spec.ID] = append(ms.entries[spec.ID], &mdEntry{
		spec: spec,
		dir:  dir,
	})

	return nil
}

// Close is a no-op for the markdown store (satisfies a common interface).
func (ms *MarkdownStore) Close() error { return nil }

// GetLinks returns node IDs linked to a spec. In the markdown model, links
// are derived from the graph (spec nodes connected via EdgeCovers), so this
// always returns nil. Use the graph's EdgesFrom on spec nodes instead.
func (ms *MarkdownStore) GetLinks(specID string) ([]string, error) {
	return nil, nil
}

// LinkSpec is a no-op for the markdown store — links live in the graph.
func (ms *MarkdownStore) LinkSpec(specID, nodeID string) error { return nil }

// UnlinkSpec is a no-op for the markdown store.
func (ms *MarkdownStore) UnlinkSpec(specID, nodeID string) error { return nil }

// GetSpecsForNode is not supported directly — use graph edges to find spec IDs
// then resolve each via GetSpec/ResolveSpec.
func (ms *MarkdownStore) GetSpecsForNode(nodeID string) ([]*Spec, error) {
	return nil, nil
}

// DeleteSpec removes a spec entry from the markdown file. Not yet implemented.
func (ms *MarkdownStore) DeleteSpec(id string) error {
	return fmt.Errorf("DeleteSpec not yet implemented for markdown store")
}

// UpdateSpec updates a spec entry in the markdown file. Not yet implemented.
func (ms *MarkdownStore) UpdateSpec(id, title, body string) error {
	return fmt.Errorf("UpdateSpec not yet implemented for markdown store")
}

// Root returns the root directory of the markdown store.
func (ms *MarkdownStore) Root() string { return ms.root }
