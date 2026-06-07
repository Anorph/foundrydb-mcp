package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterMonitoringTools registers metrics and log retrieval tools.
// cfg is the base API configuration used for direct HTTP calls to endpoints not yet in the SDK.
func RegisterMonitoringTools(s *server.MCPServer, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("get_metrics",
		mcp.WithDescription("Get current CPU, memory, storage, and connection metrics for a running service"),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
	), handleGetMetrics(cfg))

	s.AddTool(mcp.NewTool("get_logs",
		mcp.WithDescription("Retrieve recent database logs from a running service. Returns the last N log lines."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithNumber("lines",
			mcp.Description("Number of log lines to retrieve (default: 100, max: 1000)"),
		),
	), handleGetLogs(cfg))

	s.AddTool(mcp.NewTool("get_task_summary",
		mcp.WithDescription("Get per-node provisioning and operation task progress for a service. Use this to monitor a service that is provisioning, scaling, or restoring: it shows each task's status and any failure details immediately."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
	), handleGetTaskSummary(cfg))
}

func handleGetTaskSummary(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		serviceID, _ := req.GetArguments()["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/managed-services/"+serviceID+"/task-summary")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetMetrics(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		serviceID, _ := req.GetArguments()["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/managed-services/"+serviceID+"/metrics/current")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetLogs(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}

		lines := 100
		if l, ok := args["lines"].(float64); ok && l > 0 {
			lines = int(l)
			if lines > 1000 {
				lines = 1000
			}
		}

		result, err := apiPost(ctx, cfg,
			fmt.Sprintf("/managed-services/%s/logs?lines=%d", serviceID, lines),
			map[string]interface{}{},
		)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to request logs: %s", err.Error())), nil
		}

		taskID, ok := result["task_id"].(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("unexpected response: %v", result)), nil
		}

		// Poll for the async log fetch result with a 60-second timeout (12 attempts * 5 seconds).
		for attempt := 0; attempt < 12; attempt++ {
			time.Sleep(5 * time.Second)

			pollResult, err := apiGet(ctx, cfg,
				fmt.Sprintf("/managed-services/%s/logs?task_id=%s", serviceID, taskID),
			)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to poll log result: %s", err.Error())), nil
			}

			status, _ := pollResult["status"].(string)
			switch status {
			case "completed":
				return mcp.NewToolResultText(formatJSON(pollResult)), nil
			case "failed":
				msg, _ := pollResult["error_message"].(string)
				return mcp.NewToolResultError(fmt.Sprintf("log fetch failed: %s", msg)), nil
			}
			// Still pending, continue polling
		}

		return mcp.NewToolResultError("timed out waiting for logs after 60 seconds"), nil
	}
}
