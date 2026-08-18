package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterPerformanceTools registers query statistics and index advisor
// tools. Query stats and recommendation listing are read-only; applying a
// recommendation is mutating tier and goes through the brokered DDL path
// (the controller composes the CREATE INDEX statement from the persisted
// recommendation and dispatches it as a typed agent task; this tool never
// sends SQL).
// cfg is the base API configuration used for direct HTTP calls to endpoints not yet in the SDK.
func RegisterPerformanceTools(s *server.MCPServer, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("get_query_stats",
		mcp.WithDescription("Get the top-N queries by execution statistics from a running database service (collected from the primary node, e.g. pg_stat_statements for PostgreSQL). Runs asynchronously and waits up to 60 seconds for the result."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Number of queries to return (default: 20, max: 100)"),
		),
		mcp.WithString("sort_by",
			mcp.Description("Sort order: total_time (default), calls, mean_time"),
		),
	), handleGetQueryStats(cfg))

	s.AddTool(mcp.NewTool("list_index_recommendations",
		mcp.WithDescription("List index recommendations produced by the index advisor for a service, including the table, columns, reason, priority, estimated impact, and the CREATE INDEX statement that apply_index_recommendation would run."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("status",
			mcp.Description("Filter: 'pending' (default, only unapplied recommendations) or 'all' (includes applied and dismissed)"),
		),
	), handleListIndexRecommendations(cfg))

	s.AddTool(mcp.NewTool("apply_index_recommendation",
		mcp.WithDescription("Apply a pending index recommendation. The platform brokers the DDL: the controller composes the CREATE INDEX statement from the stored recommendation and dispatches it to the primary node as an audited agent task. PostgreSQL only. Use list_index_recommendations first to review the recommendation."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("recommendation_id",
			mcp.Required(),
			mcp.Description("Recommendation UUID from list_index_recommendations"),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleApplyIndexRecommendation(cfg))
}

func handleGetQueryStats(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}

		limit := 20
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
			if limit > 100 {
				limit = 100
			}
		}
		sortBy, _ := args["sort_by"].(string)
		if sortBy == "" {
			sortBy = "total_time"
		}

		result, err := apiPost(ctx, cfg,
			fmt.Sprintf("/managed-services/%s/query-stats?limit=%d&sort_by=%s", serviceID, limit, sortBy),
			map[string]interface{}{},
		)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to request query stats: %s", err.Error())), nil
		}

		taskID, ok := result["task_id"].(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("unexpected response: %v", result)), nil
		}

		// Poll the agent task with a 60-second budget (12 attempts * 5 seconds),
		// matching the get_logs pattern for async agent-collected data.
		for attempt := 0; attempt < 12; attempt++ {
			time.Sleep(5 * time.Second)

			pollResult, err := apiGet(ctx, cfg,
				fmt.Sprintf("/managed-services/%s/query-stats?task_id=%s", serviceID, taskID),
			)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to poll query stats: %s", err.Error())), nil
			}

			status, _ := pollResult["status"].(string)
			switch status {
			case "COMPLETED":
				return mcp.NewToolResultText(formatJSON(pollResult)), nil
			case "FAILED", "TIMEOUT", "CANCELLED":
				msg, _ := pollResult["error_message"].(string)
				return mcp.NewToolResultError(fmt.Sprintf("query stats collection %s: %s", status, msg)), nil
			}
			// PENDING / DISPATCHED / IN_PROGRESS: keep polling
		}

		return mcp.NewToolResultError("timed out waiting for query stats after 60 seconds; retry with the same parameters"), nil
	}
}

func handleListIndexRecommendations(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		path := "/managed-services/" + serviceID + "/index-recommendations"
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

func handleApplyIndexRecommendation(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		recID, _ := args["recommendation_id"].(string)
		if serviceID == "" || recID == "" {
			return mcp.NewToolResultError("service_id and recommendation_id are required"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("applying index recommendation %s on service %s", recID, serviceID)); denied != nil {
			return denied, nil
		}

		result, err := apiPost(ctx, cfg,
			fmt.Sprintf("/managed-services/%s/index-recommendations/%s/apply", serviceID, recID),
			nil,
		)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			"Index recommendation apply dispatched through the brokered path (typed agent task on the primary node).\n\n%s",
			formatJSON(result),
		)), nil
	}
}
