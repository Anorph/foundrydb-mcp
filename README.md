# FoundryDB MCP Server

Model Context Protocol (MCP) server for [FoundryDB](https://foundrydb.com) — the managed database platform supporting PostgreSQL, MySQL, MongoDB, Valkey, and Kafka.

This server lets AI coding assistants (Claude Code, Cursor, GitHub Copilot, Windsurf, Cline) manage your FoundryDB database services via natural language.

## Tools

| Tool | Description |
|------|-------------|
| `list_services` | List all managed database services |
| `get_service` | Get details of a service by ID or name |
| `create_service` | Provision a new database service |
| `delete_service` | Delete a service permanently |
| `list_users` | List database users for a service |
| `reveal_password` | Reveal a user's database password |
| `get_connection_string` | Get connection string in any format (url, psql, mysql, mongosh, redis-cli, env) |
| `list_backups` | List backups for a service |
| `trigger_backup` | Trigger an on-demand backup |
| `get_metrics` | Get current CPU, memory, storage, and connection metrics |
| `get_logs` | Retrieve recent database logs |

## Installation

### Option 1: Download pre-built binary

Download the latest binary from [GitHub Releases](https://github.com/anorph/foundrydb-mcp/releases).

```bash
# macOS (Apple Silicon)
curl -L https://github.com/anorph/foundrydb-mcp/releases/latest/download/foundrydb-mcp-darwin-arm64 -o foundrydb-mcp
chmod +x foundrydb-mcp

# macOS (Intel)
curl -L https://github.com/anorph/foundrydb-mcp/releases/latest/download/foundrydb-mcp-darwin-amd64 -o foundrydb-mcp
chmod +x foundrydb-mcp

# Linux (amd64)
curl -L https://github.com/anorph/foundrydb-mcp/releases/latest/download/foundrydb-mcp-linux-amd64 -o foundrydb-mcp
chmod +x foundrydb-mcp
```

### Option 2: Build from source

```bash
git clone https://github.com/anorph/foundrydb-mcp.git
cd foundrydb-mcp
CGO_ENABLED=0 go build -o foundrydb-mcp .
```

## Configuration

The server is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `FOUNDRYDB_API_URL` | `http://localhost:10090` | FoundryDB API base URL |
| `FOUNDRYDB_USERNAME` | `admin` | API username |
| `FOUNDRYDB_PASSWORD` | `admin` | API password |

For the FoundryDB hosted platform, use:
- `FOUNDRYDB_API_URL=https://api.foundrydb.com`

## Setup

### Claude Code

Add to your project's `.mcp.json` (or `~/.claude/mcp.json` for global access):

```json
{
  "mcpServers": {
    "foundrydb": {
      "command": "/path/to/foundrydb-mcp",
      "env": {
        "FOUNDRYDB_API_URL": "https://api.foundrydb.com",
        "FOUNDRYDB_USERNAME": "your-username",
        "FOUNDRYDB_PASSWORD": "your-password"
      }
    }
  }
}
```

### Cursor

Add to `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "foundrydb": {
      "command": "/path/to/foundrydb-mcp",
      "env": {
        "FOUNDRYDB_API_URL": "https://api.foundrydb.com",
        "FOUNDRYDB_USERNAME": "your-username",
        "FOUNDRYDB_PASSWORD": "your-password"
      }
    }
  }
}
```

### Windsurf

Add to `~/.codeium/windsurf/mcp_config.json`:

```json
{
  "mcpServers": {
    "foundrydb": {
      "command": "/path/to/foundrydb-mcp",
      "env": {
        "FOUNDRYDB_API_URL": "https://api.foundrydb.com",
        "FOUNDRYDB_USERNAME": "your-username",
        "FOUNDRYDB_PASSWORD": "your-password"
      }
    }
  }
}
```

## Usage Examples

Once configured, you can manage your databases with natural language:

> "List all my database services"

> "Create a PostgreSQL 17 service called 'prod-db' with 100GB storage in Stockholm"

> "Get the connection string for my prod-db service for the app_user"

> "Trigger a backup for my MySQL service"

> "Show me the current metrics for my Kafka cluster"

> "What are the last 100 log lines from my MongoDB service?"

## License

Apache 2.0
