# Azstral

Azstral is an MCP server that represents Go code as a directed graph. It enables AI systems to navigate and modify code structurally rather than textually, reducing token usage by working with node IDs instead of full source.

## Architecture

```
Go source → parser → graph → codegen → Go source
                       ↕
                   MCP server
                       ↕
                   AI client
```

| Package | Purpose |
|---|---|
| `graph/` | Core node/edge data structure |
| `parser/` | Go AST → graph |
| `codegen/` | Graph → Go source |
| `ccgf/` | Compact Code Graph Format encoder |
| `store/` | Spec storage (SQLite + markdown) |
| `specs/` | SPEC/NOTE/TEST identifier extraction |
| `server/` | MCP server with all tools |

## Usage

```bash
# Run the MCP server
go run ./cmd/azstral

# Generate CCGF for a codebase
go run ./cmd/ccgf /path/to/project
go run ./cmd/ccgf -attrs -scope "type:type:Arena" /path/to/project
```

### MCP Tools

**Parse**: `parse_file`, `parse_dir` — load Go source into the graph.

**Query**: `get_graph`, `get_node`, `list_nodes`, `list_edges` — navigate the graph.

**Mutate**: `add_node`, `update_node`, `add_edge` — modify code structurally.

**Codegen**: `render`, `write_file` — generate Go source from the graph.

**CCGF**: `encode_ccgf` — compact structural overview for LLM consumption.

**Specs**: `create_spec`, `get_spec`, `list_specs`, `link_spec`, `find_spec` — manage requirements.

## CCGF

Compact Code Graph Format is a line-based representation of program structure optimized for AI consumption:

```
# ccgf1 scope=program vendor=surface mod=azstral
s s0 p main
s s1 p fmt V
s s2 t main.Header
s s3 f main.ParseFile
s s4 f main.readHeader
s s5 f fmt.Println V

d s0 s2
d s0 s3
d s0 s4
m s0 s1
c s3 s4
c s3 s5
r s3 s2

a s3 doc ParseFile parses a file.\nSPEC-001: Parse Go source.
a s3 specs SPEC-001
```

**Scoping**: whole program, single file (`-scope "file:<id>"`), or single type (`-scope "type:<id>"`).

**Vendor**: `surface` (default — only vendor APIs your code calls, marked `V` and `ro`) or `include` (full vendor tree).

## Self-hosting

Azstral is developed using azstral. When working on this codebase, use the MCP server tools to understand, modify, and generate code rather than reading/writing files directly. Fall back to direct tools only for operations azstral doesn't support yet (tests, builds, git, non-Go files, new features).

## Specs

Requirements are tracked in markdown files at each directory level:
- `SPECS.md` — system requirements
- `NOTES.md` — design decisions
- `TESTS.md` — test requirements
- `BENCHMARKS.md` — performance targets

Code references specs via `// SPEC-001:` comments. The markdown store resolves specs hierarchically — the nearest ancestor directory match wins.
