package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterUserTools registers database user and connection string tools.
func RegisterUserTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("list_users",
		mcp.WithDescription("List all database users for a managed service"),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
	), handleListUsers(c))

	s.AddTool(mcp.NewTool("reveal_password",
		mcp.WithDescription("Reveal the password for a database user"),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("username",
			mcp.Required(),
			mcp.Description("Database username"),
		),
	), handleRevealPassword(c))

	s.AddTool(mcp.NewTool("get_connection_string",
		mcp.WithDescription("Get a ready-to-use connection string for a database service and user. Returns the connection string in the requested format."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("username",
			mcp.Required(),
			mcp.Description("Database username"),
		),
		mcp.WithString("format",
			mcp.Description("Output format: url (default), env (DATABASE_URL=...), psql, mysql, mongosh, redis-cli"),
		),
	), handleGetConnectionString(c))
}

func handleListUsers(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		serviceID, _ := req.GetArguments()["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		users, err := c.ListUsers(ctx, serviceID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(users) == 0 {
			return mcp.NewToolResultText("No users found for this service."), nil
		}
		return mcp.NewToolResultText(formatJSON(users)), nil
	}
}

func handleRevealPassword(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		username, _ := args["username"].(string)
		if serviceID == "" || username == "" {
			return mcp.NewToolResultError("service_id and username are required"), nil
		}
		result, err := c.RevealPassword(ctx, serviceID, username)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetConnectionString(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		username, _ := args["username"].(string)
		format, _ := args["format"].(string)
		if format == "" {
			format = "url"
		}

		if serviceID == "" || username == "" {
			return mcp.NewToolResultError("service_id and username are required"), nil
		}

		creds, err := c.RevealPassword(ctx, serviceID, username)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		portStr := fmt.Sprintf("%d", creds.Port)

		switch strings.ToLower(format) {
		case "env":
			return mcp.NewToolResultText(fmt.Sprintf("DATABASE_URL=%s", creds.ConnectionString)), nil
		case "psql":
			return mcp.NewToolResultText(fmt.Sprintf(
				"psql \"%s\"\n\n# Or with explicit args:\nPGPASSWORD=%s psql -h %s -p %s -U %s -d %s",
				creds.ConnectionString, creds.Password, creds.Host, portStr, creds.Username, creds.Database,
			)), nil
		case "mysql":
			return mcp.NewToolResultText(fmt.Sprintf(
				"mysql -h %s -P %s -u %s -p%s %s",
				creds.Host, portStr, creds.Username, creds.Password, creds.Database,
			)), nil
		case "mongosh":
			return mcp.NewToolResultText(fmt.Sprintf(
				"mongosh \"%s\"", creds.ConnectionString,
			)), nil
		case "redis-cli":
			return mcp.NewToolResultText(fmt.Sprintf(
				"redis-cli -h %s -p %s --user %s --pass %s",
				creds.Host, portStr, creds.Username, creds.Password,
			)), nil
		default:
			return mcp.NewToolResultText(creds.ConnectionString), nil
		}
	}
}
