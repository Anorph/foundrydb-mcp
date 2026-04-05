package tools

import (
	"context"
	"fmt"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterBackupTools registers backup management tools.
func RegisterBackupTools(s *server.MCPServer, c *foundrydb.Client) {
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
		mcp.WithString("backup_type",
			mcp.Description("Backup type (optional, uses service default if omitted): full, incremental, pitr"),
		),
	), handleTriggerBackup(c))
}

func handleListBackups(c *foundrydb.Client) server.ToolHandlerFunc {
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

func handleTriggerBackup(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}

		backupReq := foundrydb.CreateBackupRequest{}
		if bt, ok := args["backup_type"].(string); ok && bt != "" {
			backupReq.BackupType = foundrydb.BackupType(bt)
		}

		backup, err := c.TriggerBackup(ctx, serviceID, backupReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			"Backup triggered successfully.\nBackup ID: %s\n\nDetails:\n%s",
			backup.ID, formatJSON(backup),
		)), nil
	}
}
