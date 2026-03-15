# Claude Code Instructions for Azstral

## Self-hosting rule

**Use azstral to work on azstral.** When modifying this codebase, use the azstral MCP server tools rather than reading/writing files directly. The only exception is when implementing a feature that azstral does not yet support.

### Workflow

1. **Understand** — `parse_tree` loads the whole project (resets graph); `encode_ccgf` or `query_nodes` for structural overview; `get_node`/`list_nodes`/`list_edges` to navigate.
2. **Modify** — `update_node` to change function bodies (syncs to disk immediately); `add_node` to create new symbols (file nodes create the file, function nodes append to it); `add_edge` to connect symbols.
3. **Preview** — `render` to preview what a file looks like (read-only).
4. **Verify** — run `go test` and `go build` via shell.

Mutations write directly to disk — no `write_file` needed.

### What azstral can do today

| Tool | Purpose |
|---|---|
| `parse_tree`, `parse_file`, `parse_dir` | Load Go source into graph (parse_tree resets) |
| `get_graph`, `get_node`, `list_nodes`, `list_edges` | Query graph structure |
| `encode_ccgf`, `query_nodes`, `query_edges` | Structural overview and CEL queries |
| `add_node`, `update_node`, `add_edge` | Mutate graph and sync to disk |
| `add_nodes`, `add_edges` | Batch mutations |
| `delete_edge`, `delete_edges` | Remove edges |
| `reset_graph` | Clear graph and re-parse from root |
| `render` | Preview a file as Go source (read-only) |
| `create_spec`, `get_spec`, `list_specs`, `link_spec`, `find_spec` | Spec CRUD |
| `find_deadcode` | Find unreferenced symbols |
| `ccgf_grammar`, `query_help` | Format and query language docs |

### What requires falling back to direct tools

- Running tests (`go test`)
- Building (`go build`)
- Git operations
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
