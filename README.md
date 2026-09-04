# Styx

Minimal AI agent harness for CI/CD environments. CLI-based, no TUI, robust by design.

## Quick Start

```bash
go build -o styx ./cmd/styx
./styx --run styx.yaml
```

## Configuration (styx.yaml)

| Field | Required | Description |
|-------|----------|-------------|
| `prompt` | Yes | Task instruction for the agent |
| `mode` | No | `read` or `read_write` (default: `read`) |
| `api_endpoint` | Yes | OpenAI-compatible API URL. Supports `${ENV_VAR}` expansion |
| `api_key` | Yes | API key. Supports `${ENV_VAR}` expansion |
| `model` | Yes | Model name |
| `max_context` | No | Max context window in tokens (default: 128000) |
| `verification_regex` | No | Regex pattern the agent must output to complete |
| `max_working_time` | No | Max duration (default: `30m`) |
| `save_log` | No | Save `log.md` with full run transcript |
| `send_log` | No | Send `log.md` to Discord webhook |
| `webhook_url` | No | Discord webhook URL. Supports `${ENV_VAR}` expansion |

## Tools

| Tool | Description |
|------|-------------|
| `todos_add` | Add a todo item |
| `todos_update` | Update todo status (pending/done) |
| `todos_list` | List all todos |
| `todos_clear` | Clear all todos |
| `read_file` | Read a file (>50KB truncated) |
| `write_file` | Write a file (read_write mode only) |
| `bash` | Execute shell commands |
| `spawn_agent` | Spawn a sub-agent (sub-agents cannot spawn further) |

## Environment Variables

Any config value can reference environment variables:

```yaml
api_key: ${OPENAI_API_KEY}
webhook_url: ${DISCORD_WEBHOOK_URL}
```

Defaults can be specified: `${VAR:-default_value}`

## Rules Enforced

- Git changes must be made on feature branches, never directly to main/master
- When context reaches 90% capacity, it auto-compacts with a summary
- Agent fails fast on timeout or if regex not matched
- API calls retry with exponential backoff
