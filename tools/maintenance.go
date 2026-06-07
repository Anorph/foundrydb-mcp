package tools

import (
	"context"
	"fmt"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterMaintenanceTools registers maintenance window and advisory tools.
// Reading the window and listing operations are read-only; setting the
// window is mutating tier.
// cfg is the base API configuration used for direct HTTP calls to endpoints not yet in the SDK.
func RegisterMaintenanceTools(s *server.MCPServer, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("get_maintenance_window",
		mcp.WithDescription("Get the configured maintenance window for a service: day, start time, duration, timezone, and which update types apply automatically inside the window."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
	), handleGetMaintenanceWindow(cfg))

	s.AddTool(mcp.NewTool("set_maintenance_window",
		mcp.WithDescription("Create or replace the maintenance window for a service. Automatic updates (agent, OS patches, minor upgrades) only run inside this window when their auto-apply flag is enabled."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithNumber("day_of_week",
			mcp.Required(),
			mcp.Description("Day of week: 0=Sunday through 6=Saturday"),
		),
		mcp.WithString("start_time",
			mcp.Required(),
			mcp.Description("Window start time in HH:MM (24h) format, e.g. 03:00"),
		),
		mcp.WithNumber("duration_minutes",
			mcp.Required(),
			mcp.Description("Window length in minutes (30-480)"),
		),
		mcp.WithString("timezone",
			mcp.Description("IANA timezone, e.g. Europe/Stockholm. Default: UTC."),
		),
		mcp.WithBoolean("auto_apply_agent_updates",
			mcp.Description("Automatically apply agent updates inside the window (default: false)"),
		),
		mcp.WithBoolean("auto_apply_os_patches",
			mcp.Description("Automatically apply OS patches inside the window (default: false)"),
		),
		mcp.WithBoolean("auto_apply_minor_upgrades",
			mcp.Description("Automatically apply minor database upgrades inside the window (default: false)"),
		),
		mcp.WithNumber("notify_before_minutes",
			mcp.Description("Minutes of advance notice before maintenance starts (default: 0)"),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleSetMaintenanceWindow(cfg))

	s.AddTool(mcp.NewTool("list_pending_advisories",
		mcp.WithDescription("List maintenance operations for a service (patches, rolling updates, version upgrades) with their status and schedule. Pending and scheduled entries are the platform's current advisories for this service. Filter with status, e.g. status=pending or status=scheduled."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("status",
			mcp.Description("Filter by operation status, e.g. pending, scheduled, in_progress, completed, failed. Omit for all."),
		),
	), handleListPendingAdvisories(cfg))
}

func handleGetMaintenanceWindow(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		serviceID, _ := req.GetArguments()["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/managed-services/"+serviceID+"/maintenance-window")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleSetMaintenanceWindow(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}

		dayOfWeek, dayOK := args["day_of_week"].(float64)
		startTime, _ := args["start_time"].(string)
		durationMinutes, durOK := args["duration_minutes"].(float64)
		if !dayOK || startTime == "" || !durOK {
			return mcp.NewToolResultError("day_of_week, start_time and duration_minutes are required"), nil
		}

		timezone, _ := args["timezone"].(string)
		if timezone == "" {
			timezone = "UTC"
		}

		body := map[string]interface{}{
			"day_of_week":      int(dayOfWeek),
			"start_time":       startTime,
			"duration_minutes": int(durationMinutes),
			"timezone":         timezone,
		}
		if v, ok := args["auto_apply_agent_updates"].(bool); ok {
			body["auto_apply_agent_updates"] = v
		}
		if v, ok := args["auto_apply_os_patches"].(bool); ok {
			body["auto_apply_os_patches"] = v
		}
		if v, ok := args["auto_apply_minor_upgrades"].(bool); ok {
			body["auto_apply_minor_upgrades"] = v
		}
		if v, ok := args["notify_before_minutes"].(float64); ok && v > 0 {
			body["notify_before_minutes"] = int(v)
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("setting the maintenance window on %s", serviceID)); denied != nil {
			return denied, nil
		}

		result, err := apiPut(ctx, cfg, "/managed-services/"+serviceID+"/maintenance-window", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Maintenance window saved.\n\n%s", formatJSON(result))), nil
	}
}

func handleListPendingAdvisories(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		path := "/managed-services/" + serviceID + "/maintenance-operations"
		if status, ok := args["status"].(string); ok && status != "" {
			path += "?status=" + status
		}
		result, err := apiGet(ctx, cfg, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}
