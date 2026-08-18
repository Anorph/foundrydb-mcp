package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterRecoveryTools registers PITR and restore tools. The range and job
// listings are read-only; restore_service is destructive tier because it
// overwrites the current database state.
// c resolves services for typed confirmation; cfg is used for direct HTTP
// calls to endpoints not yet in the SDK.
func RegisterRecoveryTools(s *server.MCPServer, c *foundrydb.Client, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("get_pitr_range",
		mcp.WithDescription("Get the available point-in-time recovery window for a service: earliest and latest restorable times and the number of base backups. Use this before restore_service to pick a valid target_time. Supported for postgresql, mssql, mysql, and mongodb; valkey and kafka are snapshot-only."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
	), handleGetPITRRange(cfg))

	s.AddTool(mcp.NewTool("list_restore_jobs",
		mcp.WithDescription("List restore jobs for a service with their status and progress. Use this to monitor a restore started with restore_service."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of jobs to return (default: 50)"),
		),
	), handleListRestoreJobs(cfg))

	s.AddTool(mcp.NewTool("restore_service",
		mcp.WithDescription("Restore a service from backup, either a full restore of a specific backup or point-in-time recovery to target_time. DESTRUCTIVE: the restore overwrites the service's current data with the historical state. Check get_pitr_range first for the valid window. Requires confirm to be the exact service name."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("restore_type",
			mcp.Required(),
			mcp.Description("Restore type: 'pitr' (restore to target_time) or 'full' (restore a specific backup)"),
		),
		mcp.WithString("target_time",
			mcp.Description("Target timestamp for PITR in RFC3339 format, e.g. 2026-06-07T14:30:00Z. Required when restore_type is pitr. Must be inside the window reported by get_pitr_range."),
		),
		mcp.WithString("backup_id",
			mcp.Description("Backup UUID to restore from (see list_backups). Used with restore_type full; optional for pitr."),
		),
		mcp.WithString("notes",
			mcp.Description("Free-text note recorded on the restore job, e.g. the reason for the restore."),
		),
		mcp.WithString("confirm",
			mcp.Description("Typed confirmation: must be the exact service NAME (not the UUID). The restore is rejected if this does not match."),
		),
	), handleRestoreService(c, cfg))
}

func handleGetPITRRange(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		serviceID, _ := req.GetArguments()["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/managed-services/"+serviceID+"/pitr-range")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleListRestoreJobs(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		path := "/managed-services/" + serviceID + "/restore-jobs"
		if l, ok := args["limit"].(float64); ok && l > 0 {
			path = fmt.Sprintf("%s?limit=%d", path, int(l))
		}
		result, err := apiGet(ctx, cfg, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleRestoreService(c *foundrydb.Client, cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		restoreType, _ := args["restore_type"].(string)
		if serviceID == "" || restoreType == "" {
			return mcp.NewToolResultError("service_id and restore_type are required"), nil
		}
		if restoreType != "pitr" && restoreType != "full" {
			return mcp.NewToolResultError("restore_type must be 'pitr' or 'full'"), nil
		}

		targetTime, _ := args["target_time"].(string)
		backupID, _ := args["backup_id"].(string)
		if restoreType == "pitr" && targetTime == "" {
			return mcp.NewToolResultError("target_time is required for PITR restores; use get_pitr_range to find the valid window"), nil
		}
		if targetTime != "" {
			if _, err := time.Parse(time.RFC3339, targetTime); err != nil {
				return mcp.NewToolResultError("target_time must be RFC3339, e.g. 2026-06-07T14:30:00Z"), nil
			}
		}

		svc, denied := requireTypedConfirm(ctx, c, args, serviceID,
			fmt.Sprintf("restoring %s (%s) which overwrites its current data", serviceID, restoreType))
		if denied != nil {
			return denied, nil
		}

		body := map[string]interface{}{"restore_type": restoreType}
		if targetTime != "" {
			body["target_time"] = targetTime
		}
		if backupID != "" {
			body["backup_id"] = backupID
		}
		if notes, ok := args["notes"].(string); ok && notes != "" {
			body["notes"] = notes
		}

		result, err := apiPost(ctx, cfg, "/managed-services/"+serviceID+"/restore-jobs", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			"Restore job created for service %s (%s).\nMonitor it with list_restore_jobs and get_task_summary.\n\nJob details:\n%s",
			svc.Name, restoreType, formatJSON(result),
		)), nil
	}
}
