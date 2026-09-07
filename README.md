# Styx

Minimal AI agent harness for CI/CD environments. CLI-based, no TUI, robust by design.

## Quick Start

```bash
go build -o styx ./cmd/styx
./styx --config styx.yaml --verbose
```

## CLI Flags

| Flag | Description |
|------|-------------|
| `-config`, `-c` | Path to styx.yaml config file (required) |
| `-verbose`, `-v` | Enable verbose output (shows assistant text) |
| `-json` | Output events as JSON lines (for CI parsing) |

## Configuration (styx.yaml)

| Field | Required | Description |
|-------|----------|-------------|
| `prompt` | Yes | Task instruction for the agent |
| `mode` | No | `read` or `read_write` (default: `read`) |
| `api_endpoint` | Yes | OpenAI-compatible API URL. Supports `${ENV_VAR}` expansion |
| `api_key` | Yes | API key. Supports `${ENV_VAR}` expansion |
| `model` | Yes | Model name |
| `max_context` | No | Max context window in tokens (default: 128000) |
| `max_working_time` | No | Max duration (default: `30m`) |

## Tools

### Task Management

| Tool | Description |
|------|-------------|
| `todos_add` | Add a todo item |
| `todos_update` | Update todo status (pending/done) |
| `todos_list` | List all todos |
| `todos_clear` | Clear all todos |

### File Operations

| Tool | Description |
|------|-------------|
| `read_file` | Read a file (>50KB truncated) |
| `write_file` | Write a file (read_write mode only) |
| `edit_file` | Surgical text replacement (read_write mode only) |
| `insert_at_line` | Insert text at a specific line (read_write mode only) |

### Search & Discovery

| Tool | Description |
|------|-------------|
| `grep` | Search file contents with regex. Returns matches with line numbers |
| `find_files` | Find files matching glob patterns |
| `git_diff` | Get structured git diff output |

### Execution

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands |
| `spawn_agent` | Spawn a sub-agent (sub-agents cannot spawn further) |

## Streaming Output

Styx streams agent progress in real-time. Use `-verbose` to see assistant text as it's generated:

```bash
./styx --config styx.yaml --verbose
```

For CI/CD integration, use `-json` for machine-parseable output:

```bash
./styx --config styx.yaml --json
```

JSON output format:
```json
{"type":"tool_call","tool":"grep","args":"{\"pattern\":\"TODO\"}"}
{"type":"tool_result","tool":"grep","result":"src/main.go:42: // TODO: fix this"}
{"type":"summary","status":"success","duration_ms":45230,"result":"Found 3 TODOs..."}
```

## Edit Tools

The `edit_file` tool enables surgical edits without rewriting entire files:

```json
{
  "path": "src/main.go",
  "old_text": "func oldName()",
  "new_text": "func newName()"
}
```

This is more context-efficient than `write_file` and less error-prone.

## Search Tools

Dedicated search tools return clean, structured results instead of raw bash output:

- `grep` - Regex search with file/line context
- `find_files` - Glob-based file discovery  
- `git_diff` - Structured diff output (working, staged, or commit-based)

These save context tokens compared to running `bash` -> `grep -r`.

## Environment Variables

Any config value can reference environment variables:

```yaml
api_key: ${OPENAI_API_KEY}
```

Defaults can be specified: `${VAR:-default_value}`

## Behavior

- Git changes must be made on feature branches, never directly to main/master
- When context reaches 90% capacity, it auto-compacts with a summary
- Agent fails fast on timeout
- Use `-v` to stream assistant text in real-time
- Use `-json` for machine-parseable event output

## License

Non-Commercial License — Copyright (c) 2026 PUT Request

You may use Styx as a pipeline, tool, or component in closed-source commercial workflows. **You may not sell Styx, offer it as a SaaS service, or redistribute it as a standalone product.** For commercial licensing inquiries, contact: PUT@fmhy
