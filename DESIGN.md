# Azstral

Azstral is a go application that uses gocallgraph and go ast and any other tools to present Go code as a connected graph for AI use via an MCP server. The idea is that instead of writing text AI can add new nodes to to the graph and reduce token use. It should be LSP like in some ways in that rename and uses are integral. 

## Beginning

Generate a hello world application from scratch. 

## Needs

Need to represent comments and what type they are attached to, files and at least in the beginning whatever is needed for a hello world application. It should be deeply embedded in SPECS/TESTS/BENCHMARKS/NOTES development, where those three markdown structures drive the development. SPECS tells what it needs to do, NOTES is why and BENCHMARKS/TESTS describe the tests. 

Comments should include things like NOTE-001 identifier that it can link back, these items should also be handled like an AST where you can ask for what code a SPEC covers, these are likely embedded as comments in teh code but should be parsed by the SYSTEM.

## Example

SPECS

SPEC-1
The application should print "Hello World" to the console.

TESTS

TEST-1
Ensure that "Hello World" is written to the terminal.
