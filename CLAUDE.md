# Claude Code Instructions for Azstral

## Self-hosting rule

**Use azstral to work on azstral.** When modifying this codebase, use the azstral MCP server tools rather than reading/writing files directly. The only exception is when implementing a feature that azstral does not yet support.

### Workflow

1. **Understand** — `parse_file` or `parse_dir` to load code into the graph, then `encode_ccgf` for a structural overview, `get_node`/`list_nodes`/`list_edges` to navigate.
2. **Modify** — `update_node` to change function bodies or metadata, `add_node`/`add_edge` to create new symbols.
3. **Output** — `render` to preview source, `write_file` to write it to disk.
4. **Verify** — run `go test` and `go build` via shell (azstral doesn't run tests yet).

### What azstral can do today

| Tool | Purpose |
|---|---|
| `parse_file`, `parse_dir` | Load Go source into graph |
| `get_graph`, `get_node`, `list_nodes`, `list_edges` | Query graph structure |
| `encode_ccgf` | Compact structural overview for navigation |
| `add_node`, `update_node`, `add_edge` | Mutate the graph |
| `render`, `write_file` | Generate Go source from graph |
| `create_spec`, `get_spec`, `list_specs`, `link_spec`, `find_spec` | Spec CRUD |

### What requires falling back to direct tools

- Running tests (`go test`)
- Building (`go build`)
- Git operations
- Creating entirely new files not yet in the graph
- Modifying non-Go files (markdown, config, etc.)
- Implementing new azstral features (bootstrap problem)

When you fall back, note what capability was missing — it may be the next feature to implement.

## Spec-driven development

All work should be tied to SPEC/NOTE/TEST/BENCH identifiers in code comments. Use `// SPEC-NNN:` annotations to link code to requirements. Specs live in markdown files (SPECS.md, NOTES.md, TESTS.md, BENCHMARKS.md) at the appropriate directory level.

## Code style

- Go standard formatting
- No unnecessary abstractions
- Comments only where logic isn't self-evident
- `// NOTE-NNN:` for design decisions in code
