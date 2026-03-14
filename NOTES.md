# Notes

## NOTE-001
The graph representation is the core abstraction. By modeling Go code as nodes and edges, we enable AI systems to navigate and modify code structurally rather than textually. This reduces token usage because the AI only needs to reference node IDs rather than full source text.

## NOTE-002
SPEC/NOTE/TEST identifiers in comments create a bidirectional link between documentation and code. When you ask "what code implements SPEC-004?" the system can answer by traversing Covers edges from the spec node to the code nodes.

## NOTE-003
The HTTP server is the initial interface. Future work will add MCP server support for direct AI tool integration.

## NOTE-004
The hello world program serves as both a test fixture and a proof of concept. The azstral system should be able to fully represent it as a graph.
