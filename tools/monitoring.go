package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/anorph/foundrydb-mcp/client"
)

// RegisterMonitoringTools registers metrics and log retrieval tools.
func RegisterMonitoringTools(s *server.MCPServer, c *client.Client) {
	s.AddTool(mcp.NewTool("get_metrics",
		mcp.WithDescription("Get current CPU, memory, storage, and connection metrics for a running service"),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
	), handleGetMetrics(c))

	s.AddTool(mcp.NewTool("get_logs",
		mcp.WithDescription("Retrieve recent database logs from a running service. Returns the last N log lines."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithNumber("lines",
			mcp.Description("Number of log lines to retrieve (default: 100, max: 1000)"),
		),
	), handleGetLogs(c))
}

func handleGetMetrics(c *client.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		serviceID, _ := req.GetArguments()["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		result, err := c.GetMetrics(ctx, serviceID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetLogs(c *client.Client) server.ToolHandlerFunc {
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

		taskID, err := c.RequestLogs(ctx, serviceID, lines)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to request logs: %s", err.Error())), nil
		}

		// Poll for the async log fetch result with a 60-second timeout (12 attempts * 5 seconds).
		for attempt := 0; attempt < 12; attempt++ {
			time.Sleep(5 * time.Second)

			result, err := c.GetLogsResult(ctx, serviceID, taskID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to poll log result: %s", err.Error())), nil
			}

			status, _ := result["status"].(string)
			switch status {
			case "completed":
				return mcp.NewToolResultText(formatJSON(result)), nil
			case "failed":
				msg, _ := result["error_message"].(string)
				return mcp.NewToolResultError(fmt.Sprintf("log fetch failed: %s", msg)), nil
			}
			// Still pending, continue polling
		}

		return mcp.NewToolResultError("timed out waiting for logs after 60 seconds"), nil
	}
}
