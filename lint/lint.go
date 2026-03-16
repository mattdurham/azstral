// Package lint runs golangci-lint and annotates graph nodes with findings.
package lint

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/matt/azstral/graph"
)

// Issue represents a single golangci-lint finding.
type Issue struct {
	Linter   string
	Text     string
	Severity string
	File     string
	Line     int
	Col      int
}

// Result summarises a lint run.
type Result struct {
	Issues  []Issue
	ByNode  map[string][]Issue // nodeID → issues
	Total   int
	Linters []string // linters that ran
}

// golangciOutput is the JSON structure emitted by golangci-lint --out-format=json.
type golangciOutput struct {
	Issues []struct {
		FromLinter string `json:"FromLinter"`
		Text       string `json:"Text"`
		Severity   string `json:"Severity"`
		Pos        struct {
			Filename string `json:"Filename"`
			Line     int    `json:"Line"`
			Column   int    `json:"Column"`
		} `json:"Pos"`
	} `json:"Issues"`
}

// defaultLinters is the curated set used when no config file is found.
var defaultLinters = []string{
	"errcheck", "gosimple", "govet", "ineffassign", "staticcheck",
	"unused", "gosec", "gocritic", "revive", "nilerr", "bodyclose",
	"durationcheck", "gocognit", "exhaustive", "unparam", "prealloc",
}

// configFiles lists the files golangci-lint recognises as config.
var configFiles = []string{
	".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json",
}

// Run executes golangci-lint in dir for the given package pattern,
// parses the JSON output, and annotates graph nodes with lint_count
// and lint_issues metadata.
func Run(g *graph.Graph, dir, pkg string) (*Result, error) {
	if pkg == "" {
		pkg = "./..."
	}
	if dir == "" {
		dir = "."
	}

	args := buildArgs(dir, pkg)
	cmd := exec.Command("golangci-lint", args...)
	cmd.Dir = dir
	out, _ := cmd.Output() // exit code 1 when issues found — expected

	res := &Result{ByNode: make(map[string][]Issue)}
	if len(out) == 0 {
		return res, nil
	}

	var parsed golangciOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return res, fmt.Errorf("parse golangci-lint output: %w", err)
	}

	linterSet := make(map[string]bool)
	for _, raw := range parsed.Issues {
		sev := raw.Severity
		if sev == "" {
			sev = "warning"
		}
		res.Issues = append(res.Issues, Issue{
			Linter: raw.FromLinter, Text: raw.Text, Severity: sev,
			File: raw.Pos.Filename, Line: raw.Pos.Line, Col: raw.Pos.Column,
		})
		linterSet[raw.FromLinter] = true
	}
	res.Total = len(res.Issues)
	for l := range linterSet {
		res.Linters = append(res.Linters, l)
	}
	sort.Strings(res.Linters)

	// Build line-range index over function nodes.
	idx := buildFileIndex(g)

	// Assign issues to nodes.
	for _, iss := range res.Issues {
		nodeID := findNode(idx, dir, iss.File, iss.Line)
		if nodeID != "" {
			res.ByNode[nodeID] = append(res.ByNode[nodeID], iss)
		}
	}

	annotate(g, res)
	return res, nil
}

func buildArgs(dir, pkg string) []string {
	args := []string{"run", "--out-format=json"}
	for _, cf := range configFiles {
		if _, err := os.Stat(filepath.Join(dir, cf)); err == nil {
			return append(args, pkg) // use config file automatically
		}
	}
	args = append(args, "--enable="+strings.Join(defaultLinters, ","))
	return append(args, pkg)
}

type funcSpan struct{ start, end int; id string }

func buildFileIndex(g *graph.Graph) map[string][]funcSpan {
	idx := make(map[string][]funcSpan)
	for _, n := range g.NodesByKind(graph.KindFunction) {
		if n.File == "" || n.Line == 0 {
			continue
		}
		fp := strings.TrimPrefix(n.File, "file:")
		idx[fp] = append(idx[fp], funcSpan{n.Line, n.EndLine, n.ID})
	}
	return idx
}

func findNode(idx map[string][]funcSpan, dir, file string, line int) string {
	abs, _ := filepath.Abs(filepath.Join(dir, file))
	for _, path := range []string{abs, file, filepath.Join(dir, file)} {
		if spans, ok := idx[path]; ok {
			if id := tightest(spans, line); id != "" {
				return id
			}
		}
	}
	// Basename fallback.
	base := filepath.Base(file)
	for path, spans := range idx {
		if filepath.Base(path) == base {
			if id := tightest(spans, line); id != "" {
				return id
			}
		}
	}
	return ""
}

func tightest(spans []funcSpan, line int) string {
	best, bestSize := "", int(^uint(0)>>1)
	for _, s := range spans {
		end := s.end
		if end == 0 {
			end = s.start + 1000
		}
		if line >= s.start && line <= end {
			if size := end - s.start; size < bestSize {
				bestSize = size
				best = s.id
			}
		}
	}
	return best
}

func annotate(g *graph.Graph, res *Result) {
	for nodeID, issues := range res.ByNode {
		var parts []string
		for _, iss := range issues {
			parts = append(parts, iss.Linter+": "+iss.Text)
		}
		_ = g.UpdateNode(nodeID, graph.NodePatch{
			Metadata: map[string]string{
				"lint_count":  strconv.Itoa(len(issues)),
				"lint_issues": strings.Join(parts, "; "),
			},
		})
	}
}
