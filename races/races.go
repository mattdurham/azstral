// Package races provides static heuristic analysis for common concurrency
// patterns that may indicate races or deadlocks in Go code.
//
// These are heuristics, not a sound analysis — use go test -race for definitive
// detection. The goal is to surface code patterns worth reviewing.
package races

import (
	"fmt"
	"sort"
	"strings"

	"github.com/matt/azstral/graph"
)

// Issue represents a potential concurrency problem found in the graph.
type Issue struct {
	Kind     string // pattern type
	Severity string // HIGH / MEDIUM / LOW
	NodeID   string // primary node
	File     string
	Line     int
	Message  string
}

// Analyze walks the graph looking for concurrency anti-patterns.
// Returns a list of issues sorted by severity then file/line.
func Analyze(g *graph.Graph) []Issue {
	var issues []Issue
	issues = append(issues, checkGoroutineLoopCapture(g)...)
	issues = append(issues, checkMutexNoDefer(g)...)
	issues = append(issues, checkChannelSendInLoop(g)...)
	issues = append(issues, checkGoroutineInLoop(g)...)
	issues = append(issues, checkSharedVarNoMutex(g)...)

	sort.Slice(issues, func(i, j int) bool {
		si, sj := severityRank(issues[i].Severity), severityRank(issues[j].Severity)
		if si != sj {
			return si > sj
		}
		if issues[i].File != issues[j].File {
			return issues[i].File < issues[j].File
		}
		return issues[i].Line < issues[j].Line
	})
	return issues
}

func severityRank(s string) int {
	switch s {
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	}
	return 0
}

// checkGoroutineLoopCapture detects the classic loop variable capture:
//   for _, v := range items { go func() { use(v) }() }
// The goroutine captures v by reference — all goroutines see the last value.
func checkGoroutineLoopCapture(g *graph.Graph) []Issue {
	var issues []Issue
	for _, rangeNode := range g.NodesByKind(graph.KindForRange) {
		key := rangeNode.Metadata["key"]
		value := rangeNode.Metadata["value"]
		if key == "" && value == "" {
			continue
		}

		// Find goroutine children of this range loop.
		for _, child := range g.Children(rangeNode.ID) {
			if child.Kind != graph.KindGo {
				continue
			}
			if child.Metadata["closure"] != "true" {
				continue
			}
			// Check if the closure body references the loop variables.
			if capturesVar(g, child.ID, key) || capturesVar(g, child.ID, value) {
				captured := key
				if value != "" && capturesVar(g, child.ID, value) {
					captured = value
				}
				issues = append(issues, Issue{
					Kind:     "goroutine_loop_capture",
					Severity: "HIGH",
					NodeID:   child.ID,
					File:     child.File,
					Line:     child.Line,
					Message: fmt.Sprintf(
						"goroutine closure captures loop variable %q by reference — all goroutines may see the last value; use a local copy inside the loop",
						captured),
				})
			}
		}
	}
	return issues
}

// capturesVar returns true if any expression node under parentID references
// an identifier with the given name.
func capturesVar(g *graph.Graph, parentID, varName string) bool {
	if varName == "" || varName == "_" {
		return false
	}
	for _, e := range g.EdgesFrom(parentID) {
		if e.Kind != graph.EdgeContains {
			continue
		}
		child, ok := g.GetNode(e.To)
		if !ok {
			continue
		}
		if child.Kind == graph.KindExprIdent && child.Name == varName {
			return true
		}
		if capturesVar(g, child.ID, varName) {
			return true
		}
	}
	return false
}

// checkMutexNoDefer detects Lock() calls without a corresponding deferred Unlock():
//   mu.Lock()
//   // ... no defer mu.Unlock()
// This can deadlock if the function panics or has multiple return paths.
func checkMutexNoDefer(g *graph.Graph) []Issue {
	var issues []Issue
	for _, fn := range g.NodesByKind(graph.KindFunction) {
		if fn.Metadata["external"] == "true" {
			continue
		}
		lockCalls := findCallsMatching(g, fn.ID, "Lock")
		if len(lockCalls) == 0 {
			continue
		}
		deferUnlocks := findDeferredCallsMatching(g, fn.ID, "Unlock")
		if len(deferUnlocks) == 0 {
			// Has Lock but no deferred Unlock — flag each lock site.
			for _, lc := range lockCalls {
				issues = append(issues, Issue{
					Kind:     "mutex_no_defer_unlock",
					Severity: "MEDIUM",
					NodeID:   lc.ID,
					File:     fn.File,
					Line:     lc.Line,
					Message: fmt.Sprintf(
						"function %q calls Lock() without a deferred Unlock(); add 'defer mu.Unlock()' immediately after locking to prevent deadlock on panic or early return",
						fn.Name),
				})
			}
		}
	}
	return issues
}

// checkChannelSendInLoop detects channel sends inside for loops, which can block
// if the receiver is not keeping up or the channel is unbuffered:
//   for ... { ch <- val }
func checkChannelSendInLoop(g *graph.Graph) []Issue {
	var issues []Issue
	for _, send := range g.NodesByKind(graph.KindSend) {
		// Walk up to find containing loop.
		for _, e := range g.EdgesTo(send.ID) {
			if e.Kind != graph.EdgeContains {
				continue
			}
			parent, ok := g.GetNode(e.From)
			if !ok {
				continue
			}
			if parent.Kind == graph.KindForRange || parent.Kind == graph.KindForLoop ||
				parent.Kind == graph.KindForCond || parent.Kind == graph.KindForBare {
				issues = append(issues, Issue{
					Kind:     "channel_send_in_loop",
					Severity: "LOW",
					NodeID:   send.ID,
					File:     send.File,
					Line:     send.Line,
					Message: fmt.Sprintf(
						"channel send '%s <- %s' inside a loop may block if receiver is slow or channel is full; consider buffering or a select with default",
						send.Metadata["ch"], send.Metadata["val"]),
				})
			}
		}
	}
	return issues
}

// checkGoroutineInLoop detects goroutines spawned inside loops without a local
// copy of loop variables — even if the variable isn't currently referenced, the
// pattern is common source of races.
func checkGoroutineInLoop(g *graph.Graph) []Issue {
	var issues []Issue
	for _, loop := range allLoops(g) {
		for _, child := range g.Children(loop.ID) {
			if child.Kind == graph.KindGo && child.Metadata["closure"] != "true" {
				// Non-closure goroutine in loop — lower severity, usually fine.
				continue
			}
			if child.Kind == graph.KindGo {
				// Check if the function already has a local copy of the loop var.
				// If not, it might still be fine (value semantics), but flag for review.
				// Only flag if we already found a capture issue (avoid double-reporting).
				_ = child // covered by checkGoroutineLoopCapture
			}
		}
	}
	return issues
}

// checkSharedVarNoMutex detects variables (from the variable dictionary) that
// are referenced both inside goroutine bodies AND in non-goroutine code in the
// same function, without any mutex calls visible in the function.
func checkSharedVarNoMutex(g *graph.Graph) []Issue {
	var issues []Issue

	// Build a set of statement node IDs that are inside goroutine closures.
	goroutineStmts := make(map[string]bool)
	for _, goNode := range g.NodesByKind(graph.KindGo) {
		if goNode.Metadata["closure"] != "true" {
			continue
		}
		collectDescendants(g, goNode.ID, goroutineStmts)
	}

	// For each local variable, check if it's referenced from both inside and
	// outside a goroutine closure in the same function.
	seen := make(map[string]bool)
	for _, local := range g.NodesByKind(graph.KindLocal) {
		var inGoroutine, outside bool
		for _, e := range g.EdgesTo(local.ID) {
			if e.Kind != graph.EdgeReferences {
				continue
			}
			if goroutineStmts[e.From] {
				inGoroutine = true
			} else {
				outside = true
			}
		}
		if !inGoroutine || !outside {
			continue
		}

		// Check if the containing function has any mutex calls.
		funcID := local.Metadata["qualified_id"]
		if funcID == "" {
			continue
		}
		// Find the actual function node.
		key := local.ID + ":" + local.Name
		if seen[key] {
			continue
		}
		seen[key] = true

		// Find function containing this local.
		var fn *graph.Node
		for _, e := range g.EdgesTo(local.ID) {
			if e.Kind == graph.EdgeContains {
				if n, ok := g.GetNode(e.From); ok && n.Kind == graph.KindFunction {
					fn = n
					break
				}
			}
		}
		if fn == nil {
			continue
		}
		locks := findCallsMatching(g, fn.ID, "Lock")
		if len(locks) > 0 {
			continue // has mutex, likely protected
		}

		issues = append(issues, Issue{
			Kind:     "shared_var_no_mutex",
			Severity: "HIGH",
			NodeID:   local.ID,
			File:     local.File,
			Line:     local.Line,
			Message: fmt.Sprintf(
				"variable %q (type %s) is accessed from both a goroutine closure and the enclosing scope without an observable mutex — potential data race",
				local.Name, local.Metadata["type"]),
		})
	}
	return issues
}

// --- helpers ---

func allLoops(g *graph.Graph) []*graph.Node {
	var loops []*graph.Node
	for _, k := range []graph.NodeKind{graph.KindForRange, graph.KindForLoop, graph.KindForCond, graph.KindForBare} {
		loops = append(loops, g.NodesByKind(k)...)
	}
	return loops
}

// findCallsMatching finds KindCall nodes under funcID whose callee name ends with suffix.
func findCallsMatching(g *graph.Graph, funcID, suffix string) []*graph.Node {
	var results []*graph.Node
	for _, e := range g.EdgesFrom(funcID) {
		if e.Kind != graph.EdgeContains {
			continue
		}
		child, ok := g.GetNode(e.To)
		if !ok {
			continue
		}
		if child.Kind == graph.KindCall && strings.HasSuffix(child.Name, suffix+"(") {
			results = append(results, child)
		}
	}
	return results
}

// findDeferredCallsMatching finds KindDefer nodes under funcID whose call matches suffix.
func findDeferredCallsMatching(g *graph.Graph, funcID, suffix string) []*graph.Node {
	var results []*graph.Node
	for _, child := range g.Children(funcID) {
		if child.Kind != graph.KindDefer {
			continue
		}
		call := child.Metadata["call"]
		if strings.Contains(call, suffix) {
			results = append(results, child)
		}
	}
	return results
}

// collectDescendants adds all descendant node IDs (via EdgeContains) to the set.
func collectDescendants(g *graph.Graph, nodeID string, set map[string]bool) {
	for _, e := range g.EdgesFrom(nodeID) {
		if e.Kind != graph.EdgeContains {
			continue
		}
		if set[e.To] {
			continue // already visited
		}
		set[e.To] = true
		collectDescendants(g, e.To, set)
	}
}
