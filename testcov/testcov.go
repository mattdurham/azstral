// Package testcov runs Go tests with coverage and annotates the graph.
package testcov

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/matt/azstral/graph"
)

// Result summarises a test run.
type Result struct {
	Passed   int
	Failed   int
	Skipped  int
	Coverage float64            // overall statement coverage %
	ByFunc   map[string]float64 // "file.go:FuncName" → coverage %
	Failures []TestFailure
}

// TestFailure captures a single failing test.
type TestFailure struct {
	Package string
	Test    string
	Output  string
}

// Run executes `go test` with coverage on the given package pattern,
// annotates matching graph nodes with coverage and test status metadata,
// and returns a Result summary.
//
// pkg is a package pattern (e.g. "./...", "./internal/executor").
// run is an optional -run regex (empty = run all tests).
// dir is the working directory for the test run.
func Run(g *graph.Graph, dir, pkg, run string) (*Result, error) {
	if pkg == "" {
		pkg = "./..."
	}
	if dir == "" {
		dir = "."
	}

	coverFile := filepath.Join(os.TempDir(), "azstral_coverage.out")
	defer os.Remove(coverFile)

	args := []string{"test", "-json", "-coverprofile=" + coverFile, "-covermode=set"}
	if run != "" {
		args = append(args, "-run", run)
	}
	args = append(args, pkg)

	cmd := exec.Command("go", args...)
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		// Test failures return exit code 1 — capture output anyway.
		if exitErr, ok := err.(*exec.ExitError); ok {
			out = append(out, exitErr.Stderr...)
		}
	}

	res := &Result{ByFunc: make(map[string]float64)}

	// Parse JSON test output.
	parseTestJSON(out, res)

	// Parse per-function coverage.
	if _, err := os.Stat(coverFile); err == nil {
		if err2 := parseCoverage(coverFile, res); err2 != nil {
			return res, fmt.Errorf("parse coverage: %w", err2)
		}
	}

	// Annotate graph nodes.
	annotateGraph(g, res, dir)

	return res, nil
}

// testEvent is one line of `go test -json` output.
type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func parseTestJSON(data []byte, res *Result) {
	failOutput := make(map[string][]string) // "pkg/Test" → output lines
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev testEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		key := ev.Package + "/" + ev.Test
		switch ev.Action {
		case "pass":
			if ev.Test != "" {
				res.Passed++
			}
		case "fail":
			if ev.Test != "" {
				res.Failed++
				res.Failures = append(res.Failures, TestFailure{
					Package: ev.Package,
					Test:    ev.Test,
					Output:  strings.Join(failOutput[key], ""),
				})
			}
		case "skip":
			res.Skipped++
		case "output":
			if ev.Test != "" {
				failOutput[key] = append(failOutput[key], ev.Output)
			}
		}
	}
}

// parseCoverage reads `go tool cover -func` output and populates res.ByFunc.
// Line format: "file.go:line:    FuncName    X.X%"
func parseCoverage(coverFile string, res *Result) error {
	cmd := exec.Command("go", "tool", "cover", "-func="+coverFile)
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pctStr := strings.TrimSuffix(fields[len(fields)-1], "%")
		pct, err := strconv.ParseFloat(pctStr, 64)
		if err != nil {
			continue
		}

		// "total:" line — overall coverage.
		if strings.HasPrefix(fields[0], "total:") {
			res.Coverage = pct
			continue
		}

		// "path/file.go:line:   FuncName   X%"
		// Key: "basename:FuncName"
		loc := fields[0] // "path/file.go:42:"
		funcName := fields[len(fields)-2]
		base := filepath.Base(strings.TrimSuffix(loc, fmt.Sprintf(":%s", strings.Split(loc, ":")[1]+":")))
		if parts := strings.SplitN(loc, ":", 3); len(parts) >= 1 {
			base = filepath.Base(parts[0])
		}
		key := base + ":" + funcName
		res.ByFunc[key] = pct
		// Also store by full path.
		if parts := strings.SplitN(loc, ":", 3); len(parts) >= 1 {
			res.ByFunc[parts[0]+":"+funcName] = pct
		}
	}
	return nil
}

// annotateGraph updates graph nodes with coverage and test_status metadata.
func annotateGraph(g *graph.Graph, res *Result, _ string) {
	// Build a set of failing test names for quick lookup.
	failedTests := make(map[string]string) // test name → output
	for _, f := range res.Failures {
		failedTests[f.Test] = f.Output
	}

	for _, n := range g.NodesByKind(graph.KindFunction) {
		if n.Metadata["external"] == "true" {
			continue
		}

		// Match test functions to pass/fail status.
		if isTestFunc(n.Name) {
			status := "pass"
			failures := ""
			if out, failed := failedTests[n.Name]; failed {
				status = "fail"
				failures = strings.TrimSpace(out)
				if len(failures) > 500 {
					failures = failures[:500] + "…"
				}
			}
			_ = g.UpdateNode(n.ID, graph.NodePatch{
				Metadata: map[string]string{
					"test_status":   status,
					"test_failures": failures,
				},
			})
			continue
		}

		// Match non-test functions to coverage data.
		cov, found := lookupCoverage(n, res)
		if found {
			_ = g.UpdateNode(n.ID, graph.NodePatch{
				Metadata: map[string]string{
					"coverage":    fmt.Sprintf("%.1f", cov),
					"test_status": coverageStatus(cov),
				},
			})
		}
	}
}

// lookupCoverage tries to find coverage data for a function node.
func lookupCoverage(n *graph.Node, res *Result) (float64, bool) {
	// Derive the file basename from the node's File field.
	filePath := strings.TrimPrefix(n.File, "file:")

	// Try receiver-qualified name: "(*Type).Name"
	names := []string{n.Name}
	if recv := n.Metadata["receiver"]; recv != "" {
		// Extract type name from receiver like "g *Graph" → "(*Graph).Name"
		parts := strings.Fields(recv)
		if len(parts) >= 2 {
			typ := strings.TrimLeft(parts[len(parts)-1], "*")
			names = append(names, "(*"+typ+")."+n.Name)
			names = append(names, typ+"."+n.Name)
		}
	}

	for _, name := range names {
		// Try full path + name.
		if cov, ok := res.ByFunc[filePath+":"+name]; ok {
			return cov, true
		}
		// Try basename + name.
		base := filepath.Base(filePath)
		if cov, ok := res.ByFunc[base+":"+name]; ok {
			return cov, true
		}
	}
	return 0, false
}

func isTestFunc(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Example")
}

func coverageStatus(pct float64) string {
	if pct == 0 {
		return "untested"
	}
	return "covered"
}
