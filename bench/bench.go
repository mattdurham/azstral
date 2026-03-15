// Package bench runs Go benchmarks and annotates graph nodes with results.
package bench

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/matt/azstral/graph"
)

// Result holds the parsed result of a single benchmark.
type Result struct {
	Name        string
	Iterations  int
	NsPerOp     float64
	BPerOp      float64
	AllocsPerOp float64
	MBPerSec    float64
	Custom      map[string]float64 // any non-standard b.ReportMetric fields
}

// Summary summarises a benchmark run.
type Summary struct {
	Results  []*Result
	Failures []string // packages or benchmarks that failed
}

// metricKey normalises a benchmark unit string into a metadata key fragment.
// "rows/op" → "rows_op", "p99-ns" → "p99_ns", "MB/s" → "mb_s"
func metricKey(unit string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(unit) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// benchLineRe matches a benchmark result line.
// BenchmarkFoo-8    1000000    1234.5 ns/op    64 B/op    2 allocs/op    100 MB/s
var benchLineRe = regexp.MustCompile(`^(Benchmark\S+?)(?:-\d+)?\s+(\d+)\s+(.+)$`)
var metricRe = regexp.MustCompile(`([\d.]+)\s+(\S+)`)

// Run executes `go test -bench` with -benchmem and annotates graph nodes.
// pkg is a package pattern (e.g. "./..." or "./internal/executor").
// benchPattern is the -bench regex (empty = "." to run all benchmarks).
// count is the -count flag (0 = default, 1 recommended for consistency).
func Run(g *graph.Graph, dir, pkg, benchPattern string, count int) (*Summary, error) {
	if pkg == "" {
		pkg = "./..."
	}
	if benchPattern == "" {
		benchPattern = "."
	}
	if dir == "" {
		dir = "."
	}

	args := []string{"test", "-bench=" + benchPattern, "-benchmem", "-run=^$"}
	if count > 0 {
		args = append(args, fmt.Sprintf("-count=%d", count))
	}
	args = append(args, pkg)

	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	sum := &Summary{}
	parseOutput(out, sum)

	if err != nil && len(sum.Results) == 0 {
		return sum, fmt.Errorf("go test -bench failed: %w\n%s", err, out)
	}

	annotate(g, sum)
	return sum, nil
}

// parseOutput parses benchmark lines from go test output.
func parseOutput(data []byte, sum *Summary) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := sc.Text()

		if strings.HasPrefix(line, "FAIL") {
			sum.Failures = append(sum.Failures, strings.TrimSpace(line))
			continue
		}

		m := benchLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		r := &Result{
			Name: m[1],
		}
		r.Iterations, _ = strconv.Atoi(m[2])

		// Parse metric pairs: "1234.5 ns/op", "64 B/op", custom "42 rows/op", etc.
		for _, mm := range metricRe.FindAllStringSubmatch(m[3], -1) {
			val, _ := strconv.ParseFloat(mm[1], 64)
			switch mm[2] {
			case "ns/op":
				r.NsPerOp = val
			case "B/op":
				r.BPerOp = val
			case "allocs/op":
				r.AllocsPerOp = val
			case "MB/s":
				r.MBPerSec = val
			default:
				// Custom metric from b.ReportMetric(val, "unit").
				if r.Custom == nil {
					r.Custom = make(map[string]float64)
				}
				r.Custom[mm[2]] = val
			}
		}

		sum.Results = append(sum.Results, r)
	}
}

// annotate stores benchmark results on matching graph nodes.
// Matches "BenchmarkFoo" to a function node named "BenchmarkFoo".
func annotate(g *graph.Graph, sum *Summary) {
	// Index function nodes by name for fast lookup.
	byName := make(map[string]*graph.Node)
	for _, n := range g.NodesByKind(graph.KindFunction) {
		byName[n.Name] = n
	}

	for _, r := range sum.Results {
		n, ok := byName[r.Name]
		if !ok {
			continue
		}
		meta := map[string]string{
			"bench_iters":     fmt.Sprintf("%d", r.Iterations),
			"bench_ns_op":     fmt.Sprintf("%.2f", r.NsPerOp),
			"bench_b_op":      fmt.Sprintf("%.0f", r.BPerOp),
			"bench_allocs_op": fmt.Sprintf("%.0f", r.AllocsPerOp),
		}
		if r.MBPerSec > 0 {
			meta["bench_mb_s"] = fmt.Sprintf("%.2f", r.MBPerSec)
		}
		// Custom metrics from b.ReportMetric — stored as bench_<normalised_unit>.
		for unit, val := range r.Custom {
			meta["bench_"+metricKey(unit)] = fmt.Sprintf("%.4g", val)
		}
		_ = g.UpdateNode(n.ID, graph.NodePatch{Metadata: meta})
	}
}
