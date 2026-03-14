package ccgf

// Grammar returns the CCGF format definition as a string.
// This is intended for LLM consumption — it describes how to parse CCGF output.
const Grammar = `# CCGF Grammar (Compact Code Graph Format v1)

## Line types

  # <header>        Header (first line). Key=value pairs.
  s <id> <type> <name> [V]   Symbol definition. V = vendored/read-only.
  <edge> <from> <to>         Edge between two symbols.
  a <id> <key> <value>       Attribute on a symbol.

Blank lines separate sections (symbols, edges, attributes).

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
  loc    File location (file.go:line)
  sig    Function signature (func(params)returns)
  kind   Type kind (struct, interface)
  ro     Read-only (1 = vendored)

## Vendor flag

  V on a symbol line means the symbol is from an external dependency.
  Vendor=surface includes only the 1-layer API your code calls.
  Vendor symbols are read-only — they describe the interface, not the source.

## Example

  # ccgf1 scope=program vendor=surface mod=example
  s s0 p main
  s s1 p fmt V
  s s2 t main.Header
  s s3 f main.ParseFile

  d s0 s2
  d s0 s3
  m s0 s1
  c s3 s1

  a s3 doc ParseFile parses a Go file.\nSPEC-001: Uses go/ast.
  a s3 specs SPEC-001
  a s3 loc parser.go:42
  a s3 sig func(path string)(error)
`
