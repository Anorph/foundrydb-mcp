package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterAIActionTools registers the AI Action Center surface: the
// prioritized actions feed, the copilot planner (preview only), and the
// tier-gated executor.
//
// list_ai_actions and plan_ai_copilot are read-only. plan_ai_copilot never
// executes anything; it returns a previewable plan whose step tiers are set
// server-side. execute_ai_action is mutating and sits at the confirm tier:
// confirm=true is required. Destructive (delete, restore) actions are not
// executable through this surface in v1; for those, follow the feed item's
// deep link to the native flow.
func RegisterAIActionTools(s *server.MCPServer, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("list_ai_actions",
		mcp.WithDescription("List the prioritized AI actions feed across the caller's services: index recommendations and security advisories unified into one list, sorted by severity then recency. Each item carries an action type, a safety tier, the underlying recommendation/advisory id, and a deep link. Read-only."),
		mcp.WithString("service_id",
			mcp.Description("Optional managed service UUID to filter the feed to a single service."),
		),
		mcp.WithString("kind",
			mcp.Description("Optional source kind filter: index or advisory."),
		),
		mcp.WithString("severity",
			mcp.Description("Optional minimum severity: info, warning, or critical."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum items to return (default 50, capped at 200)."),
		),
	), handleListAIActions(cfg))

	s.AddTool(mcp.NewTool("plan_ai_copilot",
		mcp.WithDescription("Turn a natural-language intent into a previewable plan of tool calls for a service. PREVIEW ONLY: this executes nothing. Each step carries a server-set safety tier and a one-line preview. To carry out a step, call execute_ai_action with the step's tool as action_type and its args. Read-only."),
		mcp.WithString("intent",
			mcp.Required(),
			mcp.Description("The natural-language ask, e.g. 'scale prod-pg to a bigger plan'. Must be non-empty."),
		),
		mcp.WithString("service_id",
			mcp.Description("Optional managed service UUID for context; when set it must be a service the caller can see. Lets the planner fill tool arguments without naming the service."),
		),
	), handlePlanAICopilot(cfg))

	s.AddTool(mcp.NewTool("execute_ai_action",
		mcp.WithDescription("Execute ONE confirm-tier AI action by delegating to its existing brokered, audited handler. Mutating: confirm=true is required. Supported action_type values (all confirm-tier): apply_index_recommendation (args.recommendation_id), dismiss_advisory (args.advisory_match_id + args.reason), scale_service (args.target_plan_name OR args.cpu_cores+args.memory_mb, optional args.storage_mb), add_replica (args.node_name, args.zone, optional sizing). Destructive actions (delete, restore) are NOT executable here in v1; follow the feed item's deep link to the native flow instead. Unknown or destructive action types are rejected by the server."),
		mcp.WithString("action_type",
			mcp.Required(),
			mcp.Description("The action to execute: apply_index_recommendation, dismiss_advisory, scale_service, or add_replica."),
		),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Target managed service UUID. Must be a service the caller can see."),
		),
		mcp.WithString("args",
			mcp.Description(`Action-specific parameters as a JSON object, e.g. {"recommendation_id":"<uuid>"} or {"target_plan_name":"tier-3"}.`),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleExecuteAIAction(cfg))

	s.AddTool(mcp.NewTool("list_ai_action_executions",
		mcp.WithDescription("List the outcome-loop history of AI action executions visible to the caller, newest first. Each record carries the execution id (use it to roll back), the service, the action_type, the target recommendation/advisory id, the outcome status (executed or failed) and http_status, and the rollback state (revert_status: requested, done, failed, or not_reversible) when one was attempted. Records hold identifiers and outcome status only, never secrets. Read-only."),
		mcp.WithString("service_id",
			mcp.Description("Optional managed service UUID to filter the history to a single service. Must be a service the caller can see."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum records to return (default 50, capped at 200)."),
		),
	), handleListAIActionExecutions(cfg))

	s.AddTool(mcp.NewTool("rollback_ai_action",
		mcp.WithDescription("Reverse a reversible AI action execution by its execution id. Mutating: confirm=true is required. Reversibility is decided by the recorded action_type: apply_index_recommendation drops the created index via a brokered agent task (revert_status requested), and dismiss_advisory reactivates the advisory (revert_status done). It only reverses reversible actions: scale_service and add_replica are NOT reversible here and are refused with 422, because scaling down or removing a replica is a separate, deliberate operation rather than a rollback. The server returns 404 when the execution is not found or its service is not visible to the caller."),
		mcp.WithString("execution_id",
			mcp.Required(),
			mcp.Description("The execution record UUID to reverse, from list_ai_action_executions."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleRollbackAIAction(cfg))
}

func handleListAIActions(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path := "/ai/actions"
		sep := "?"
		if v, ok := args["service_id"].(string); ok && v != "" {
			path += sep + "service_id=" + v
			sep = "&"
		}
		if v, ok := args["kind"].(string); ok && v != "" {
			path += sep + "kind=" + v
			sep = "&"
		}
		if v, ok := args["severity"].(string); ok && v != "" {
			path += sep + "severity=" + v
			sep = "&"
		}
		if v, ok := args["limit"].(float64); ok && v > 0 {
			path += sep + "limit=" + itoa(int(v))
		}
		result, err := apiGet(ctx, cfg, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result == nil {
			return mcp.NewToolResultText("No AI actions in the feed for the given filters."), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handlePlanAICopilot(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		intent, _ := args["intent"].(string)
		if intent == "" {
			return mcp.NewToolResultError("intent is required and must be non-empty"), nil
		}
		body := map[string]interface{}{"intent": intent}
		if v, ok := args["service_id"].(string); ok && v != "" {
			body["service_id"] = v
		}
		result, err := apiPost(ctx, cfg, "/ai/copilot/plan", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleExecuteAIAction(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		actionType, _ := args["action_type"].(string)
		serviceID, _ := args["service_id"].(string)
		if actionType == "" || serviceID == "" {
			return mcp.NewToolResultError("action_type and service_id are required"), nil
		}

		body := map[string]interface{}{
			"action_type": actionType,
			"service_id":  serviceID,
		}
		if v, ok := args["args"].(string); ok && v != "" {
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(v), &parsed); err != nil {
				return mcp.NewToolResultError(`args must be a JSON object, e.g. {"recommendation_id":"<uuid>"}`), nil
			}
			body["args"] = parsed
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("executing AI action %q on service %s", actionType, serviceID)); denied != nil {
			return denied, nil
		}
		body["confirm"] = true

		result, err := apiPost(ctx, cfg, "/ai/actions/execute", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleListAIActionExecutions(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path := "/ai/actions/executions"
		sep := "?"
		if v, ok := args["service_id"].(string); ok && v != "" {
			path += sep + "service_id=" + v
			sep = "&"
		}
		if v, ok := args["limit"].(float64); ok && v > 0 {
			path += sep + "limit=" + itoa(int(v))
		}
		result, err := apiGet(ctx, cfg, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result == nil {
			return mcp.NewToolResultText("No AI action executions in the history for the given filters."), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleRollbackAIAction(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		executionID, _ := args["execution_id"].(string)
		if executionID == "" {
			return mcp.NewToolResultError("execution_id is required"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("rolling back AI action execution %s (only reversible actions can be undone; scale_service and add_replica are refused)", executionID)); denied != nil {
			return denied, nil
		}

		result, err := apiPost(ctx, cfg, "/ai/actions/executions/"+executionID+"/rollback", nil)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}
