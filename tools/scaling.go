package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterScalingTools registers scaling tools (mutating tier).
// cfg is the base API configuration used for direct HTTP calls to endpoints not yet in the SDK.
func RegisterScalingTools(s *server.MCPServer, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("scale_service",
		mcp.WithDescription("Scale a managed database service vertically: change the compute plan or expand the data disk (storage can only grow, never shrink). Provide at least one of plan_name, storage_size_gb. For replicas use add_replica and remove_replica. The operation runs asynchronously; monitor it with get_task_summary and get_service."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("plan_name",
			mcp.Description("Target compute plan: tier-1 (1 CPU, 2GB) through tier-15. Changing the plan resizes every node in the cluster."),
		),
		mcp.WithNumber("storage_size_gb",
			mcp.Description("New data disk size in GB. Must be greater than or equal to the current size; shrink requests are rejected by the API."),
		),
		mcp.WithString("replication_mode",
			mcp.Description("Replication mode for multi-node clusters: async, sync, semi-sync."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleScaleService(cfg))

	s.AddTool(mcp.NewTool("add_replica",
		mcp.WithDescription("Add a read replica node to a managed database service (horizontal scale-out). The replica provisions asynchronously; monitor with get_service_nodes and get_task_summary."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("zone",
			mcp.Description("UpCloud zone for the replica. Omit to use the service's zone."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleAddReplica(cfg))

	s.AddTool(mcp.NewTool("remove_replica",
		mcp.WithDescription("Remove a specific replica node from a managed database service (horizontal scale-in). The primary cannot be removed. Use get_service_nodes to find the node UUID."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
		mcp.WithString("node_id",
			mcp.Required(),
			mcp.Description("UUID of the replica node to remove (from get_service_nodes)"),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleRemoveReplica(cfg))
}

func handleScaleService(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}

		patch := map[string]interface{}{}
		var changes []string
		if plan, ok := args["plan_name"].(string); ok && plan != "" {
			patch["plan_name"] = plan
			changes = append(changes, fmt.Sprintf("plan to %s", plan))
		}
		if gb, ok := args["storage_size_gb"].(float64); ok && gb > 0 {
			patch["storage_size_gb"] = int(gb)
			changes = append(changes, fmt.Sprintf("storage to %d GB", int(gb)))
		}
		if rm, ok := args["replication_mode"].(string); ok && rm != "" {
			patch["replication_mode"] = rm
			changes = append(changes, fmt.Sprintf("replication mode to %s", rm))
		}
		if len(patch) == 0 {
			return mcp.NewToolResultError("provide at least one of plan_name, storage_size_gb, replication_mode"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("scaling %s (%s)", serviceID, strings.Join(changes, ", "))); denied != nil {
			return denied, nil
		}

		result, err := apiPatch(ctx, cfg, "/managed-services/"+serviceID, patch)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			"Scaling initiated: %s.\nThe operation runs asynchronously; use get_task_summary and get_service to monitor progress.\n\nService details:\n%s",
			strings.Join(changes, ", "), formatJSON(result),
		)), nil
	}
}

func handleAddReplica(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("adding a replica to %s", serviceID)); denied != nil {
			return denied, nil
		}

		body := map[string]interface{}{}
		if zone, ok := args["zone"].(string); ok && zone != "" {
			body["zone"] = zone
		}

		result, err := apiPost(ctx, cfg, "/managed-services/"+serviceID+"/nodes", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Replica addition initiated.\nMonitor with get_service_nodes and get_task_summary.\n\n%s",
			formatJSON(result),
		)), nil
	}
}

func handleRemoveReplica(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		nodeID, _ := args["node_id"].(string)
		if serviceID == "" || nodeID == "" {
			return mcp.NewToolResultError("service_id and node_id are required"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("removing replica %s from %s", nodeID, serviceID)); denied != nil {
			return denied, nil
		}

		result, err := apiRequest(ctx, cfg, http.MethodDelete,
			"/managed-services/"+serviceID+"/nodes/"+nodeID, nil)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Replica removal initiated.\nMonitor with get_service_nodes and get_task_summary.\n\n%s",
			formatJSON(result),
		)), nil
	}
}
