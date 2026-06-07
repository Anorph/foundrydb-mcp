# FoundryDB MCP Server

Model Context Protocol (MCP) server for [FoundryDB](https://foundrydb.com), the managed database platform supporting PostgreSQL, MySQL, MongoDB, Valkey, Kafka, OpenSearch, and SQL Server (Babelfish).

This server lets AI assistants (Claude Code, Claude Desktop, Cursor, Windsurf, Cline) operate your FoundryDB database fleet via natural language: provision, back up, restore to a point in time, tune, scale, and decommission. Every operation goes through the platform's authenticated, scoped, and audit-logged REST API. There are no raw SQL or shell tools: agents propose, the platform brokers.

## Safety model

Every tool belongs to exactly one tier, enforced before any API call is made:

| Tier | Guard |
|------|-------|
| Read-only | none |
| Mutating | requires an explicit `confirm: true` parameter, so the assistant must surface the action to you first |
| Destructive (`delete_service`, `restore_service`) | requires the exact service NAME as typed confirmation; any mismatch aborts with zero side effects |

Mutations are brokered, never free-text: for example `apply_index_recommendation` passes only a recommendation ID, and the platform composes and executes the `CREATE INDEX CONCURRENTLY` statement through an audited task. The model never authors SQL.

## Tools (24)

| Tool | Tier | Description |
|------|------|-------------|
| `list_organizations` | read-only | Organizations you belong to |
| `list_services` | read-only | All services with status, type, zone, plan |
| `get_service` | read-only | Service detail by ID or name |
| `get_service_nodes` | read-only | Nodes (primary and replicas) |
| `list_presets` | read-only | AI agent workload presets |
| `create_service` | mutating | Provision a service (presets, TTL, ephemeral supported) |
| `delete_service` | destructive | Permanent deletion, typed name confirmation |
| `scale_service` | mutating | Change compute plan or expand storage |
| `add_replica` | mutating | Add a read replica |
| `remove_replica` | mutating | Remove a replica by node ID |
| `list_backups` | read-only | Backups with status and type |
| `trigger_backup` | mutating | On-demand backup |
| `get_pitr_range` | read-only | Restorable time window |
| `restore_service` | destructive | Full or point-in-time restore, typed name confirmation |
| `list_restore_jobs` | read-only | Restore progress |
| `get_query_stats` | read-only | Top-N queries by total time, calls, or mean time |
| `list_index_recommendations` | read-only | Index advisor output |
| `apply_index_recommendation` | mutating | Brokered CREATE INDEX via audited task |
| `list_users` | read-only | Database users |
| `reveal_password` | read-only | Credentials for a user |
| `get_connection_string` | read-only | Connection string (url, env, psql, mysql, mongosh, redis-cli) |
| `get_metrics` | read-only | Current CPU, memory, storage, connections |
| `get_logs` | read-only | Recent database log lines |
| `get_task_summary` | read-only | Per-node provisioning and operation progress |
| `get_maintenance_window` | read-only | Configured maintenance window |
| `set_maintenance_window` | mutating | Create or replace the window |
| `list_pending_advisories` | read-only | Maintenance operations with status |

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

The server is configured via environment variables. Either a token or a username/password pair is required; the server exits at startup with neither.

| Variable | Description |
|----------|-------------|
| `FOUNDRYDB_API_URL` | API base URL (use `https://api.foundrydb.com` for the hosted platform) |
| `FOUNDRYDB_TOKEN` | Scoped API token (preferred) |
| `FOUNDRYDB_USERNAME` / `FOUNDRYDB_PASSWORD` | Basic auth fallback (not scope-restricted) |

### Token scopes

Prefer `FOUNDRYDB_TOKEN` with a scoped API token. Scopes are `<family>:<level>` (families include `services` and `backups`; levels `read` < `write` < `admin`, mapped from HTTP methods: GET=read, POST/PUT/PATCH=write, DELETE=admin). The scope bounds what the assistant can do regardless of what it tries; out-of-scope calls return a 403 naming the missing scope, and every call lands in your organization's audit log.

| Intent | Scopes |
|--------|--------|
| Observe only | `services:read`, `backups:read` |
| Operate | `services:write`, `backups:write` |
| Full control (delete, restore) | `services:admin`, `backups:admin` |

Notes: backup, restore, and PITR routes are the `backups` family, separate from `services`. `get_query_stats`, `get_logs`, `reveal_password`, and `get_connection_string` POST under the hood and need `services:write`. `list_organizations` requires a full-access token.

## Setup

### Claude Code

```bash
claude mcp add foundrydb \
  -e FOUNDRYDB_API_URL=https://api.foundrydb.com \
  -e FOUNDRYDB_TOKEN=your-token \
  -- /path/to/foundrydb-mcp
```

Or add to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "foundrydb": {
      "command": "/path/to/foundrydb-mcp",
      "env": {
        "FOUNDRYDB_API_URL": "https://api.foundrydb.com",
        "FOUNDRYDB_TOKEN": "your-token"
      }
    }
  }
}
```

### Claude Desktop

Add the same block to `claude_desktop_config.json` under `mcpServers`.

### Cursor

Add the same block to `~/.cursor/mcp.json`.

### Windsurf

Add the same block to `~/.codeium/windsurf/mcp_config.json`.

## Usage Examples

> "List all my database services"

> "Create a PostgreSQL 17 service called 'prod-db' with 100GB storage in Stockholm"

> "Take a backup of prod-db, then show me the point-in-time recovery window"

> "What are the slowest queries on prod-db? Any index recommendations?"

> "Apply that user_id index recommendation"

> "Expand prod-db storage to 150 GB"

> "Restore prod-db to 14:30 UTC yesterday" (requires typing the service name to confirm)

Mutating requests prompt you for confirmation before anything happens; destructive requests additionally require the exact service name.

## License

Apache 2.0
