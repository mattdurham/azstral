# Tests

## TEST-001
Parse /cmd/hello/main.go and verify the graph contains a Package node, a File node, and a Function node for main.

## TEST-002
Parse /cmd/hello/main.go and verify SPEC and NOTE identifiers are extracted from comments and linked to the correct code nodes.

## TEST-003
Verify the HTTP API returns the full graph as JSON when GET /graph is called.

## TEST-004
Verify GET /graph/nodes returns all nodes and GET /graph/nodes/{id} returns a specific node.

## TEST-005
Verify GET /specs returns all spec identifiers with their coverage (linked code nodes).

## TEST-006
Verify /cmd/hello prints "Hello World" to stdout.
