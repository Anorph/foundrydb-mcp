package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/anorph/foundrydb-mcp/client"
)

// RegisterBackupTools registers backup management tools.
func RegisterBackupTools(s *server.MCPServer, c *client.Client) {
	s.AddTool(mcp.NewTool("list_backups",
		mcp.WithDescription("List all backups for a managed service, including scheduled and on-demand backups"),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
	), handleListBackups(c))

	s.AddTool(mcp.NewTool("trigger_backup",
		mcp.WithDescription("Trigger an on-demand backup for a managed service immediately"),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("backup_method",
			mcp.Description("Backup method (optional, uses service default if omitted): pg_basebackup, mysqldump, mongodump, rdb, kafka_full"),
		),
		mcp.WithNumber("retention_days",
			mcp.Description("How many days to retain this backup (1-365). Uses service default if omitted."),
		),
	), handleTriggerBackup(c))
}

func handleListBackups(c *client.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		serviceID, _ := req.GetArguments()["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		backups, err := c.ListBackups(ctx, serviceID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(backups) == 0 {
			return mcp.NewToolResultText("No backups found for this service."), nil
		}
		return mcp.NewToolResultText(formatJSON(backups)), nil
	}
}

func handleTriggerBackup(c *client.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}

		backupReq := client.CreateBackupRequest{}
		if method, ok := args["backup_method"].(string); ok && method != "" {
			backupReq.BackupMethod = &method
		}
		if days, ok := args["retention_days"].(float64); ok && days > 0 {
			n := int(days)
			backupReq.RetentionDays = &n
		}

		result, err := c.TriggerBackup(ctx, serviceID, backupReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		id, _ := result["id"].(string)
		return mcp.NewToolResultText(fmt.Sprintf(
			"Backup triggered successfully.\nBackup ID: %s\n\nDetails:\n%s",
			id, formatJSON(result),
		)), nil
	}
}
