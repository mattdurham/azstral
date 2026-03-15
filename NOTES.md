# Notes

## NOTE-001
The graph representation is the core abstraction. By modeling Go code as nodes and edges, we enable AI systems to navigate and modify code structurally rather than textually. This reduces token usage because the AI only needs to reference node IDs rather than full source text.

## NOTE-002
SPEC/NOTE/TEST identifiers in comments create a bidirectional link between documentation and code. When you ask "what code implements SPEC-004?" the system can answer by traversing Covers edges from the spec node to the code nodes.

## NOTE-003
The HTTP server is the initial interface. Future work will add MCP server support for direct AI tool integration.

## NOTE-004
The hello world program serves as both a test fixture and a proof of concept. The azstral system should be able to fully represent it as a graph.

## NOTE-005
2026-03-15. Expression nodes (KindExpr*) are purely additive and invisible to
codegen. The `isStmtKind` filter in `codegen/stmtgen.go` lists the statement
kinds explicitly; KindExpr* are absent from that list, so codegen skips them
and continues to reconstruct code from the `src` metadata string on each
statement node. This means round-trip fidelity is maintained with no changes
to the codegen package. The design decision to make expression nodes codegen-
invisible was intentional: expression nodes serve structural query purposes
(e.g. find all `== ""` comparisons), not code generation.

addExpr() skips *ast.CallExpr because KindCall nodes are created earlier by
addCallNode (which runs in addFunction before walkStatements). Creating a
second call node at the same position would collide on the ID or create
ambiguity. CallExpr children of binary/unary expressions are therefore not
expanded further by addExpr — the KindCall node is already in the graph.

*ast.ParenExpr is transparent: addExpr recurses through it without creating a
node because parentheses carry no semantic information in the expression tree.

Expression node IDs use the format `expr:<kindcode>:<fileID>:<line>:<col>` to
avoid collisions between a BinaryExpr and its LHS child, which share the same
start position.
