package bench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/matt/azstral/graph"
)

// ProfileType selects which pprof profile to collect.
type ProfileType string

const (
	ProfileCPU    ProfileType = "cpu"
	ProfileMem    ProfileType = "mem"
	ProfileBlock  ProfileType = "block"
	ProfileMutex  ProfileType = "mutex"
)

// ProfileResult contains the path to the saved profile and a top-N summary.
type ProfileResult struct {
	Path     string         // absolute path to the .pprof file
	Type     ProfileType
	TopFuncs []ProfileEntry // top functions from go tool pprof -top
}

// ProfileEntry is one line from pprof -top output.
type ProfileEntry struct {
	Flat    float64
	FlatPct float64
	Sum     float64
	Cum     float64
	CumPct  float64
	Name    string
}

// RunProfile runs a benchmark with CPU or memory profiling, saves the
// profile to outputDir (or os.TempDir if empty), annotates graph nodes
// with flat% from the profile, and returns the profile path + top-N summary.
func RunProfile(g *graph.Graph, dir, pkg, benchPattern string, profType ProfileType, outputDir string, topN int) (*ProfileResult, error) {
	if pkg == "" {
		pkg = "./..."
	}
	if benchPattern == "" {
		benchPattern = "."
	}
	if dir == "" {
		dir = "."
	}
	if outputDir == "" {
		outputDir = os.TempDir()
	}
	if topN <= 0 {
		topN = 20
	}

	profileFlag := "-cpuprofile"
	profileExt := "cpu.pprof"
	if profType == ProfileMem {
		profileFlag = "-memprofile"
		profileExt = "mem.pprof"
	} else if profType == ProfileBlock {
		profileFlag = "-blockprofile"
		profileExt = "block.pprof"
	} else if profType == ProfileMutex {
		profileFlag = "-mutexprofile"
		profileExt = "mutex.pprof"
	}

	profilePath := filepath.Join(outputDir, "azstral_bench_"+profileExt)

	args := []string{
		"test",
		"-bench=" + benchPattern,
		"-benchmem",
		"-run=^$",
		"-count=1",
		profileFlag + "=" + profilePath,
		pkg,
	}

	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go test -bench: %w\n%s", err, out)
	}

	if _, err := os.Stat(profilePath); err != nil {
		return nil, fmt.Errorf("profile not written to %s: %w", profilePath, err)
	}

	result := &ProfileResult{
		Path: profilePath,
		Type: profType,
	}

	// Run go tool pprof -top to get function-level breakdown.
	topOut, err := exec.Command("go", "tool", "pprof", "-top",
		fmt.Sprintf("-nodecount=%d", topN), profilePath).CombinedOutput()
	if err == nil {
		result.TopFuncs = parsePprofTop(string(topOut))
		annotatePprof(g, result.TopFuncs)
	}

	return result, nil
}

// pprofTopRe matches lines from go tool pprof -top:
//   1.23s  45.6% 78.9%   2.34s  90.1%  runtime.mallogc
var pprofTopRe = regexp.MustCompile(`^\s*([\d.]+\S*)\s+([\d.]+)%\s+([\d.]+)%\s+([\d.]+\S*)\s+([\d.]+)%\s+(\S+)`)

func parsePprofTop(output string) []ProfileEntry {
	var entries []ProfileEntry
	for _, line := range strings.Split(output, "\n") {
		m := pprofTopRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		e := ProfileEntry{Name: m[6]}
		e.Flat, _ = strconv.ParseFloat(strings.TrimRight(m[1], "smkMGB"), 64)
		e.FlatPct, _ = strconv.ParseFloat(m[2], 64)
		e.Sum, _ = strconv.ParseFloat(m[3], 64)
		e.Cum, _ = strconv.ParseFloat(strings.TrimRight(m[4], "smkMGB"), 64)
		e.CumPct, _ = strconv.ParseFloat(m[5], 64)
		entries = append(entries, e)
	}
	return entries
}

// annotatePprof stores pprof_flat_pct and pprof_cum_pct on matching nodes.
func annotatePprof(g *graph.Graph, entries []ProfileEntry) {
	byName := make(map[string]*graph.Node)
	for _, n := range g.NodesByKind(graph.KindFunction) {
		byName[n.Name] = n
		// Also index by short qualified name: "pkg.FuncName"
		if idx := strings.LastIndex(n.Name, "."); idx >= 0 {
			byName[n.Name[idx+1:]] = n
		}
	}

	for _, e := range entries {
		// pprof names can be "pkg.FuncName", "(*Type).Method", etc.
		name := e.Name
		if idx := strings.LastIndex(name, "."); idx >= 0 {
			name = name[idx+1:]
		}
		name = strings.TrimLeft(name, "*")
		n, ok := byName[name]
		if !ok {
			continue
		}
		_ = g.UpdateNode(n.ID, graph.NodePatch{
			Metadata: map[string]string{
				"pprof_flat_pct": fmt.Sprintf("%.2f", e.FlatPct),
				"pprof_cum_pct":  fmt.Sprintf("%.2f", e.CumPct),
			},
		})
	}
}
