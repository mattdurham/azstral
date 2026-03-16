package ccgf

// Grammar returns the CCGF format definition as a string.
// This is intended for LLM consumption — it describes how to parse CCGF output.
const Grammar = `# CCGF Grammar (Compact Code Graph Format v2)

## Line types

  # <header>              Header (first line). Key=value pairs.
  s <id> <type> <name> [V] [#N]  Symbol definition. V = vendored. #N = integer index.
  <edge> <from> <to>     Edge between two symbols.
  <blank line>           Separates symbols section from edges section.
  dead <type> <name> <file>:<line>  Dead code report line (find_deadcode output).

## Attribute lines (ccgf2 indented format)

Attributes follow their symbol's s-line with exactly 2 spaces of indentation.
The 2 spaces are the only indentation — there is no additional nesting.

Example (leading 2 spaces on each attribute line are the actual indentation):

s <id> <type> <name>
  doc <text>
  specs SPEC-001,TEST-006
  loc <file>:<line>
  sig func(params)returns

Attributes belong to the most recently seen s-line above them.
There is no "a" prefix — exactly 2 spaces of indentation identifies attribute lines.

## Header keys

  scope=<scope>     What was encoded: program, file:<id>, type:<id>
  vendor=<mode>     Vendor mode: surface (API only), include (all)
  mod=<path>        Go module path

## Symbol types

  p  package
  f  function
  m  method (function with receiver)
  t  struct/type
  i  interface
  v  variable
  c  constant

## Edge types

  d  defines    (package → symbol)
  c  calls      (function → function)
  u  uses       (function → type via params)
  r  returns    (function → return type)
  m  imports    (package → package)

## Attribute keys

  doc    Doc comment text (\n = newline)
  specs  Comma-separated spec IDs (SPEC-001,TEST-006)
  go:*   Go compiler directives (go:embed, go:build, go:noinline, etc.)
  loc    File location (file.go:line) — path is relative to project root
  sig    Function signature (func(params)returns)
  kind   Type kind (struct, interface)
  ro     Read-only (1 = vendored)
  cyclo  Cyclomatic complexity (decision points + 1). Omitted if ≤ 1.
  cogn   Cognitive complexity (nesting-weighted). Omitted if 0.
  text   Source body (newlines escaped as \n)
  idx    Integer node index — now on s-line as #N suffix, not a separate attribute.

## Vendor flag

  V on a symbol line means the symbol is from an external dependency.
  Vendor=surface includes only the 1-layer API your code calls.
  Vendor symbols are read-only — they describe the interface, not the source.

## Integer index (#N)

  The #N suffix on an s-line is the node's integer index in the graph.
  Use it with get_nodes(idxs=[N]) to fetch full details without typing the full ID.
  Example: s func:parser.ParseFile f ParseFile #42
  Then: get_nodes(idxs=[42])

## find_deadcode output format

  dead f unusedHelper parser/parser.go:178
  dead t OldStruct    parser/types.go:42
  dead v debugMode    parser/parser.go:15

  Columns: dead <typeCode> <name> <file>:<line>

## Example (encode_ccgf with attrs=true)

  # ccgf2 scope=program vendor=surface mod=example
  s s0 p main
  s s1 p fmt V
  s s2 t main.Header
  s s3 f main.ParseFile
    doc ParseFile parses a Go file.\nSPEC-001: Uses go/ast.
    specs SPEC-001
    loc parser/parser.go:42
    sig func(path string)error

  d s0 s2
  d s0 s3
  m s0 s1
  c s3 s1

## Example (get_nodes / query_nodes output)

  s func:parser.ParseFile f ParseFile #42
    loc parser/parser.go:45
    sig func(g *graph.Graph, path string)error
    cyclo 8
    cogn 4
    text func ParseFile(g *graph.Graph, path string) error {\n\treturn parseFileImpl(g, path)\n}

## Example (list_nodes output)

  s pkg:main p main #1
  s pkg:graph p graph #2
  s func:parser.ParseFile f ParseFile #42
  s func:parser.ParseTree f ParseTree #43
  s type:graph.Node t Node #87
`
