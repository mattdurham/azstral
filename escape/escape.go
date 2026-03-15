// Package escape runs Go's escape analysis and annotates graph nodes
// with per-function heap and stack allocation counts.
//
// Uses: go build -gcflags="-m" ./...
// Output lines like:
//   ./file.go:84:6: &Node{} escapes to heap
//   ./file.go:91:6: x does not escape
package escape

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/matt/azstral/graph"
)

// Result summarises an escape analysis run.
type Result struct {
	HeapByFunc  map[string]int // nodeID → heap escape count
	StackByFunc map[string]int // nodeID → stack (does not escape) count
	Total       int
	HeapTotal   int
}

// funcRange is a line-range span for a function node.
type funcRange struct {
	start, end int
	id         string
}

// escapeLineRe matches lines like:
//   ./graph.go:84:6: &Node{} escapes to heap
//   ./graph.go:84:6: x does not escape
//   ./graph.go:84:6: (*Graph).AddNode x does not escape
var escapeLineRe = regexp.MustCompile(`^([^:]+):(\d+):\d+: .+$`)

// Run executes escape analysis on the given package pattern, annotates
// graph nodes, and returns a summary.
func Run(g *graph.Graph, dir, pkg string) (*Result, error) {
	if pkg == "" {
		pkg = "./..."
	}
	if dir == "" {
		dir = "."
	}

	cmd := exec.Command("go", "build", "-gcflags=-m", pkg)
	cmd.Dir = dir
	// Escape analysis is written to stderr.
	out, _ := cmd.CombinedOutput()

	res := &Result{
		HeapByFunc:  make(map[string]int),
		StackByFunc: make(map[string]int),
	}

	// Build a line-range index: file → list of (startLine, endLine, nodeID).
	fileIndex := make(map[string][]funcRange)
	for _, n := range g.NodesByKind(graph.KindFunction) {
		if n.File == "" || n.Line == 0 {
			continue
		}
		filePath := strings.TrimPrefix(n.File, "file:")
		fileIndex[filePath] = append(fileIndex[filePath], funcRange{
			start: n.Line,
			end:   n.EndLine,
			id:    n.ID,
		})
	}

	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		m := escapeLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		relFile := strings.TrimPrefix(m[1], "./")
		lineNum, _ := strconv.Atoi(m[2])
		isHeap := strings.Contains(line, "escapes to heap") ||
			strings.Contains(line, "moved to heap")

		res.Total++
		if isHeap {
			res.HeapTotal++
		}

		// Find which function this line belongs to.
		nodeID := findFunc(fileIndex, dir, relFile, lineNum)
		if nodeID == "" {
			continue
		}
		if isHeap {
			res.HeapByFunc[nodeID]++
		} else if strings.Contains(line, "does not escape") {
			res.StackByFunc[nodeID]++
		}
	}

	// Annotate graph nodes.
	annotate(g, res)
	return res, nil
}

// findFunc returns the node ID of the function containing the given line.
func findFunc(fileIndex map[string][]funcRange, dir, relFile string, line int) string {
	// Try relative path first, then try joining with dir.
	candidates := []string{
		relFile,
		filepath.Join(dir, relFile),
	}
	// Also try resolving against each file in the index.
	for _, path := range candidates {
		abs, _ := filepath.Abs(path)
		if ranges, ok := fileIndex[abs]; ok {
			return bestMatch(ranges, line)
		}
		if ranges, ok := fileIndex[path]; ok {
			return bestMatch(ranges, line)
		}
	}
	// Fallback: match by basename.
	base := filepath.Base(relFile)
	for path, ranges := range fileIndex {
		if filepath.Base(path) == base {
			if id := bestMatch(ranges, line); id != "" {
				return id
			}
		}
	}
	return ""
}

// bestMatch finds the tightest function range containing the line.
func bestMatch(ranges []funcRange, line int) string {
	best := ""
	bestSize := int(^uint(0) >> 1)
	for _, r := range ranges {
		end := r.end
		if end == 0 {
			end = r.start + 1000 // unknown end — use large window
		}
		if line >= r.start && line <= end {
			size := end - r.start
			if size < bestSize {
				bestSize = size
				best = r.id
			}
		}
	}
	return best
}

// annotate stores heap_allocs and stack_allocs metadata on function nodes.
func annotate(g *graph.Graph, res *Result) {
	// Collect all unique node IDs that appeared in either map.
	seen := make(map[string]bool)
	for id := range res.HeapByFunc {
		seen[id] = true
	}
	for id := range res.StackByFunc {
		seen[id] = true
	}

	for id := range seen {
		heap := res.HeapByFunc[id]
		stack := res.StackByFunc[id]
		_ = g.UpdateNode(id, graph.NodePatch{
			Metadata: map[string]string{
				"heap_allocs":  fmt.Sprintf("%d", heap),
				"stack_allocs": fmt.Sprintf("%d", stack),
			},
		})
	}
}
