package tools

import (
	"context"
	"fmt"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
)

// Safety tiers for MCP tools. Every tool falls into exactly one tier and the
// tier is enforced in the handler before any API call is made:
//
//   - read-only:   no confirmation required (list_*, get_*).
//   - mutating:    requires the boolean parameter confirm=true. Without it
//     the handler returns guidance and performs no side effects.
//   - destructive: requires the string parameter confirm to equal the exact
//     service name. The handler resolves the service first; a mismatch
//     aborts with no side effects. Used where the operation discards data
//     (delete, restore-over).
//
// All mutating and destructive tools route exclusively through audited REST
// endpoints; none of them touch databases or VMs directly.

// confirmFlagDescription documents the mutating-tier confirm parameter on
// tool definitions.
const confirmFlagDescription = "Must be set to true to execute. This is a mutating operation; verify the parameters before confirming."

// requireConfirmFlag enforces the mutating tier. It returns a non-nil tool
// result describing how to confirm when confirm=true is absent.
func requireConfirmFlag(args map[string]interface{}, action string) *mcp.CallToolResult {
	if v, ok := args["confirm"].(bool); ok && v {
		return nil
	}
	return mcp.NewToolResultError(fmt.Sprintf(
		"Not executed: %s is a mutating operation and requires confirm=true. Review the parameters (and confirm with the user if one is present), then re-run with confirm set to true.",
		action,
	))
}

// requireTypedConfirm enforces the destructive tier. The caller must pass the
// exact service name in the confirm parameter. It resolves the service by
// UUID and returns it on success, or a non-nil tool result on any mismatch.
func requireTypedConfirm(ctx context.Context, c *foundrydb.Client, args map[string]interface{}, serviceID, action string) (*foundrydb.Service, *mcp.CallToolResult) {
	confirm, _ := args["confirm"].(string)
	svc, err := c.GetService(ctx, serviceID)
	if err != nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("could not resolve service %s: %s", serviceID, err.Error()))
	}
	if svc == nil {
		return nil, mcp.NewToolResultError("service not found")
	}
	if confirm == "" {
		return nil, mcp.NewToolResultError(fmt.Sprintf(
			"Not executed: %s is a destructive operation. To proceed, re-run with confirm set to the exact service name %q.",
			action, svc.Name,
		))
	}
	if confirm != svc.Name {
		return nil, mcp.NewToolResultError(fmt.Sprintf(
			"Not executed: typed confirmation mismatch. confirm was %q but the service %s is named %q. Nothing was changed.",
			confirm, serviceID, svc.Name,
		))
	}
	return svc, nil
}
