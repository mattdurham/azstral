// Package query provides CEL-based querying of the code graph.
//
// Node queries evaluate a CEL expression against every node, returning matches.
// Edge queries evaluate against every edge.
//
// Available node variables: id, kind, name, file, line, text, external,
// cyclomatic, cognitive, receiver, params, returns, parent_id,
// callee_ids, caller_ids, child_ids.
//
// Available edge variables: from, to, kind.
package query

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/matt/azstral/graph"
)

// NodeQuery evaluates a CEL expression against every node in the graph,
// returning all nodes for which the expression is true.
func NodeQuery(g *graph.Graph, expr string) ([]*graph.Node, error) {
	callerIndex := buildCallerIndex(g)

	env, err := nodeEnv()
	if err != nil {
		return nil, err
	}

	prg, err := compile(env, expr)
	if err != nil {
		return nil, err
	}

	var results []*graph.Node
	for _, n := range g.Nodes {
		act := nodeActivation(g, n, callerIndex)
		out, _, evalErr := prg.Eval(act)
		if evalErr != nil {
			continue
		}
		if b, ok := out.Value().(bool); ok && b {
			results = append(results, n)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].File != results[j].File {
			return results[i].File < results[j].File
		}
		return results[i].Line < results[j].Line
	})
	return results, nil
}

// EdgeQuery evaluates a CEL expression against every edge in the graph.
func EdgeQuery(g *graph.Graph, expr string) ([]*graph.Edge, error) {
	env, err := edgeEnv()
	if err != nil {
		return nil, err
	}

	prg, err := compile(env, expr)
	if err != nil {
		return nil, err
	}

	var results []*graph.Edge
	for _, e := range g.Edges {
		out, _, evalErr := prg.Eval(map[string]any{
			"from": e.From,
			"to":   e.To,
			"kind": string(e.Kind),
		})
		if evalErr != nil {
			continue
		}
		if b, ok := out.Value().(bool); ok && b {
			results = append(results, e)
		}
	}
	return results, nil
}

// nodeEnv builds the CEL environment for node queries.
func nodeEnv() (*cel.Env, error) {
	env, err := cel.NewEnv(
		cel.Variable("id", cel.StringType),
		cel.Variable("kind", cel.StringType),
		cel.Variable("name", cel.StringType),
		cel.Variable("file", cel.StringType),
		cel.Variable("line", cel.IntType),
		cel.Variable("text", cel.StringType),
		cel.Variable("external", cel.BoolType),
		cel.Variable("cyclomatic", cel.IntType),
		cel.Variable("cognitive", cel.IntType),
		cel.Variable("receiver", cel.StringType),
		cel.Variable("params", cel.StringType),
		cel.Variable("returns", cel.StringType),
		cel.Variable("parent_id", cel.StringType),
		cel.Variable("callee_ids", cel.ListType(cel.StringType)),
		cel.Variable("caller_ids", cel.ListType(cel.StringType)),
		cel.Variable("child_ids", cel.ListType(cel.StringType)),
		cel.Variable("coverage", cel.DoubleType),
		cel.Variable("test_status", cel.StringType),
		cel.Variable("heap_allocs", cel.IntType),
		cel.Variable("stack_allocs", cel.IntType),
		cel.Variable("bench_ns_op", cel.DoubleType),
		cel.Variable("bench_b_op", cel.DoubleType),
		cel.Variable("bench_allocs_op", cel.DoubleType),
		cel.Variable("pprof_flat_pct", cel.DoubleType),
		cel.Variable("pprof_cum_pct", cel.DoubleType),
		// metadata exposes the full key-value map; use .num(key) for numeric lookup.
		cel.Variable("metadata", cel.MapType(cel.StringType, cel.StringType)),
		// num(key) parses metadata[key] as float64, returns 0.0 if missing/non-numeric.
		cel.Function("num",
			cel.MemberOverload(
				"map_num_string",
				[]*cel.Type{cel.MapType(cel.StringType, cel.StringType), cel.StringType},
				cel.DoubleType,
				cel.BinaryBinding(func(mapVal, keyVal ref.Val) ref.Val {
					idx, ok := mapVal.(traits.Indexer)
					if !ok {
						return types.Double(0)
					}
					v := idx.Get(keyVal)
					if types.IsError(v) {
						return types.Double(0)
					}
					s, ok := v.(types.String)
					if !ok {
						return types.Double(0)
					}
					f, err := strconv.ParseFloat(string(s), 64)
					if err != nil {
						return types.Double(0)
					}
					return types.Double(f)
				}),
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("node env: %w", err)
	}
	return env, nil
}

// edgeEnv builds the CEL environment for edge queries.
func edgeEnv() (*cel.Env, error) {
	env, err := cel.NewEnv(
		cel.Variable("from", cel.StringType),
		cel.Variable("to", cel.StringType),
		cel.Variable("kind", cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("edge env: %w", err)
	}
	return env, nil
}

func compile(env *cel.Env, expr string) (cel.Program, error) {
	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compile: %w", issues.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("program: %w", err)
	}
	return prg, nil
}

// nodeActivation builds the CEL activation map for a node.
func nodeActivation(g *graph.Graph, n *graph.Node, callerIndex map[string][]string) map[string]any {
	parentID := ""
	for _, e := range g.EdgesTo(n.ID) {
		if e.Kind == graph.EdgeContains {
			parentID = e.From
			break
		}
	}

	childIDs := []string{}
	for _, e := range g.EdgesFrom(n.ID) {
		if e.Kind == graph.EdgeContains {
			childIDs = append(childIDs, e.To)
		}
	}

	// Callees: walk function → call nodes → callee edges.
	calleeIDs := []string{}
	if n.Kind == graph.KindFunction {
		for _, e := range g.EdgesFrom(n.ID) {
			if e.Kind != graph.EdgeContains {
				continue
			}
			callNode, ok := g.GetNode(e.To)
			if !ok || callNode.Kind != graph.KindCall {
				continue
			}
			for _, ce := range g.EdgesFrom(callNode.ID) {
				if ce.Kind == graph.EdgeCallee {
					calleeIDs = append(calleeIDs, ce.To)
				}
			}
		}
	}

	callerIDs := callerIndex[n.ID]
	if callerIDs == nil {
		callerIDs = []string{}
	}

	return map[string]any{
		"id":         n.ID,
		"kind":       string(n.Kind),
		"name":       n.Name,
		"file":       n.File,
		"line":       int64(n.Line),
		"text":       n.Text,
		"external":   n.Metadata["external"] == "true",
		"cyclomatic": metaInt(n, "cyclomatic"),
		"cognitive":  metaInt(n, "cognitive"),
		"receiver":   n.Metadata["receiver"],
		"params":     n.Metadata["params"],
		"returns":    n.Metadata["returns"],
		"parent_id":   parentID,
		"callee_ids":  calleeIDs,
		"caller_ids":  callerIDs,
		"child_ids":   childIDs,
		"coverage":     metaFloat64(n, "coverage"),
		"test_status":  n.Metadata["test_status"],
		"heap_allocs":     metaInt(n, "heap_allocs"),
		"stack_allocs":    metaInt(n, "stack_allocs"),
		"bench_ns_op":     metaFloat64(n, "bench_ns_op"),
		"bench_b_op":      metaFloat64(n, "bench_b_op"),
		"bench_allocs_op": metaFloat64(n, "bench_allocs_op"),
		"pprof_flat_pct":  metaFloat64(n, "pprof_flat_pct"),
		"pprof_cum_pct":   metaFloat64(n, "pprof_cum_pct"),
		"metadata":        metadataMap(n),
	}
}

// buildCallerIndex builds a reverse map: callee node ID → []caller function IDs.
func buildCallerIndex(g *graph.Graph) map[string][]string {
	index := make(map[string][]string)
	for _, n := range g.Nodes {
		if n.Kind != graph.KindFunction {
			continue
		}
		for _, e := range g.EdgesFrom(n.ID) {
			if e.Kind != graph.EdgeContains {
				continue
			}
			callNode, ok := g.GetNode(e.To)
			if !ok || callNode.Kind != graph.KindCall {
				continue
			}
			for _, ce := range g.EdgesFrom(callNode.ID) {
				if ce.Kind == graph.EdgeCallee {
					index[ce.To] = append(index[ce.To], n.ID)
				}
			}
		}
	}
	return index
}

func metaInt(n *graph.Node, key string) int64 {
	v, _ := strconv.ParseInt(n.Metadata[key], 10, 64)
	return v
}

func metaFloat64(n *graph.Node, key string) float64 {
	v, _ := strconv.ParseFloat(n.Metadata[key], 64)
	return v
}

// metadataMap returns the node's metadata as a plain map[string]string
// for the CEL `metadata` variable. Returns an empty map if nil.
func metadataMap(n *graph.Node) map[string]string {
	if n.Metadata == nil {
		return map[string]string{}
	}
	return n.Metadata
}

// Help is the query language documentation returned by the query_help tool.
// Examples is a curated set of ready-to-use queries grouped by use case.
const Examples = `# Query Examples

## Complexity

  # High cyclomatic complexity
  kind == "function" && cyclomatic > 15

  # High cognitive complexity (hardest to understand)
  kind == "function" && cognitive > 20

  # Both — highest refactoring risk
  kind == "function" && cyclomatic > 10 && cognitive > 15

  # Complex methods on a specific type
  kind == "function" && receiver.contains("Executor") && cyclomatic > 5

## Call graph

  # Functions called by many callers (hot paths)
  kind == "function" && caller_ids.size() > 5

  # Functions that call many other functions (coordinators)
  kind == "function" && callee_ids.size() > 8

  # Who calls a specific function
  kind == "function" && "func:ParseFile" in callee_ids

  # What calls a specific function
  kind == "function" && "func:main" in caller_ids

## Coverage (requires run_tests)

  # Zero coverage
  kind == "function" && test_status == "untested"

  # Low coverage
  kind == "function" && coverage < 50.0

  # Failing tests
  test_status == "fail"

  # High-risk: complex, heavily-used, no tests
  kind == "function" && cyclomatic > 10 && caller_ids.size() > 3 && test_status == "untested"

## Benchmarks (requires run_bench)

  # Slow benchmarks
  kind == "function" && bench_ns_op > 1000.0

  # Benchmarks with heap allocations
  kind == "function" && bench_allocs_op > 0.0

  # Memory-heavy benchmarks
  kind == "function" && bench_b_op > 1024.0

  # Slow AND allocating — optimisation targets
  kind == "function" && bench_ns_op > 500.0 && bench_allocs_op > 2.0

  # Custom metrics via metadata.num(key) — works for any b.ReportMetric field
  metadata.num("bench_rows_op") > 100.0
  metadata.num("p99_ns") > 5000.0
  metadata.num("bench_rows_op") > 0.0 && bench_ns_op > 200.0

## Statements (for, if, switch, defer, go, assign, send, return, branch)

  # All range loops
  kind == "for" && metadata["range"] == "true"

  # Range loops over a specific type/variable
  kind == "for" && metadata["over"].contains("spans")

  # All error checks (if err != nil pattern)
  kind == "if" && metadata["cond"].contains("err")

  # All goroutine spawns
  kind == "go"

  # All defer statements in a specific file
  kind == "defer" && file.contains("executor")

  # Short variable declarations only
  kind == "assign" && metadata["op"] == ":="

  # Channel sends
  kind == "send"

  # Goroutines spawned inside loops (go inside for)
  kind == "go" && parent_id.startsWith("for:")

  # All breaks/continues
  kind == "branch"

  # Labelled breaks (complex flow)
  kind == "branch" && metadata["label"] != ""

## Allocation hotspots — fast workflow

  # Single call: escape analysis + rank + return body text ready for editing
  # (replaces run_escape → query_nodes → get_nodes — saves ~10 round-trips)
  find_hotspots  min_allocs=3  top_n=20

## Allocations (requires run_escape)

  # Functions that cause heap allocations
  kind == "function" && heap_allocs > 0

  # Allocation-heavy functions
  kind == "function" && heap_allocs > 5

  # Hot allocation paths: heavy callers + heap allocs
  kind == "function" && heap_allocs > 3 && caller_ids.size() > 3

  # All risk factors combined
  kind == "function" && heap_allocs > 3 && cyclomatic > 10 && test_status == "untested"

## Structure

  # Exported functions (library API surface)
  kind == "function" && name.matches("^[A-Z]") && !external

  # Methods on a type
  kind == "function" && receiver.contains("Graph")

  # Functions returning error
  kind == "function" && returns.contains("error")

  # Test functions
  kind == "function" && name.startsWith("Test")

  # Functions in a specific file
  kind == "function" && file.contains("executor")

  # Types in a package
  kind == "type" && parent_id == "pkg:executor"

  # All symbols in a package
  parent_id == "pkg:parser" && !external

## Vendor

  # External/vendor symbols referenced
  kind == "function" && external == true

  # Non-vendor functions that call into a vendor package
  kind == "function" && !external && "func:fmt.Println" in callee_ids

## Edge queries

  # All callee edges into a function
  to == "func:ParseFile" && kind == "callee"

  # All symbols a package defines
  from == "pkg:graph" && kind == "contains"
`

// Help is the query language documentation.
const Help = `# CEL Graph Query Language

## Node query variables

  id          string  unique node ID (e.g. "func:ParseFile", "type:Config")
  kind        string  function | type | variable | package | file | import | comment
  name        string  symbol name
  file        string  source file path as node ID ("file:/abs/path/to/file.go")
  line        int     start line number
  text        string  function body or type definition text
  external    bool    true if from an external/vendor package
  cyclomatic  int     cyclomatic complexity (functions only)
  cognitive   int     cognitive complexity (functions only)
  receiver    string  method receiver (e.g. "r *Reader")
  params      string  parameter list text
  returns     string  return type text
  parent_id   string  ID of the containing node (package, file, etc.)
  callee_ids  list    IDs of functions this node calls
  caller_ids  list    IDs of functions that call this node
  child_ids   list    IDs of direct children
  Statement node kinds (children of functions):
    for     range/for loop      — metadata: range, key, value, over, cond
    if      if/else-if          — metadata: cond, has_else
    switch  switch/type switch  — metadata: tag, type_switch
    select  select              — (no extra metadata)
    return  return statement    — metadata: values
    defer   defer statement     — metadata: call
    go      goroutine spawn     — metadata: call
    assign  assignment/:=       — metadata: op, lhs
    send    channel send        — metadata: ch, val
    branch  break/continue/goto — metadata: tok, label

  metadata     map     full key-value metadata; use .num(key) for numeric lookup
                       e.g. metadata.num("bench_rows_op") > 20.0
  coverage     float   statement coverage % (0-100); 0 if run_tests not called
  test_status  string  "pass" | "fail" | "covered" | "untested"; "" if not run
  heap_allocs     int     allocations that escape to heap (from run_escape)
  stack_allocs    int     allocations that stay on stack (from run_escape)
  bench_ns_op     float   nanoseconds per op (from run_bench)
  bench_b_op      float   bytes allocated per op (from run_bench)
  bench_allocs_op float   heap allocations per op (from run_bench)

## Edge query variables

  from   string  source node ID
  to     string  target node ID
  kind   string  contains | calls | callee | references | annotates | covers

## CEL operators and methods

  ==  !=  <  <=  >  >=        comparison
  &&  ||  !                    boolean logic
  in                           list membership:  "func:X" in callee_ids
  .contains(s)                 substring:        name.contains("Parse")
  .startsWith(s)               prefix:           name.startsWith("Test")
  .endsWith(s)                 suffix:           file.endsWith("_test.go")
  .matches(re)                 regex:            name.matches("^[A-Z]")
  .size()                      length:           callee_ids.size() > 3

## Examples

  # High-complexity functions
  kind == "function" && cyclomatic > 15

  # Functions that call a specific target
  kind == "function" && "func:ParseFile" in callee_ids

  # Functions called by main
  kind == "function" && "func:main" in caller_ids

  # Exported, non-vendor functions with high cognitive load
  kind == "function" && name.matches("^[A-Z]") && !external && cognitive > 10

  # Methods on a specific type
  kind == "function" && receiver.contains("Arena")

  # Functions returning error
  kind == "function" && returns.contains("error")

  # Test functions
  kind == "function" && name.startsWith("Test")

  # Types in a specific package
  kind == "type" && parent_id == "pkg:parser"

  # High-fan-out functions (calls many things)
  kind == "function" && callee_ids.size() > 8

  # Hot functions (called by many callers)
  kind == "function" && caller_ids.size() > 5

  # All callee edges into a specific function (edge query)
  to == "func:ParseFile" && kind == "callee"

  # All edges from a package (edge query)
  from.startsWith("pkg:") && kind == "contains"
`
