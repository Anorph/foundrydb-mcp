package tools

import (
	"context"
	"fmt"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterStackTools registers MCP tools for the vertical-starter stack engine:
// first-party catalog browsing, cost preview, launch, status, teardown, and
// retry. Each stack composes existing platform primitives (a PostgreSQL+pgvector
// database, a files bucket, an EU-routed inference key, and a hosted app) into
// one unit, wired and metered together.
func RegisterStackTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("stacks_list_templates",
		mcp.WithDescription("List the first-party stack templates available in the catalog. Each entry includes a name, description, version, and a fresh cost preview. Use this to discover available templates before calling stacks_preview or stacks_launch."),
	), handleStacksListTemplates(c))

	s.AddTool(mcp.NewTool("stacks_preview",
		mcp.WithDescription("Preview the estimated monthly cost for a stack template before launching. Returns a breakdown by resource (database, files, inference, app). A line item marked is_ceiling is a maximum charge (such as an inference budget), not a fixed recurring cost. Pass the returned monthly_total as accepted_monthly_cost when calling stacks_launch."),
		mcp.WithString("template_name",
			mcp.Required(),
			mcp.Description("Stack template name (e.g. rag-chatbot). Use stacks_list_templates to see available templates."),
		),
	), handleStacksPreview(c))

	s.AddTool(mcp.NewTool("stacks_launch",
		mcp.WithDescription("Launch a stack from a catalog template. Composes platform primitives (database, files, inference, app) into one unit. Provisioning is asynchronous; poll stacks_get until status is Running. Requires: (1) accepted_monthly_cost matching the estimate from stacks_preview (re-launch is rejected with conflict if the cost drifted); (2) the organization must have an enabled inference provider. Use stacks_list_templates + stacks_preview first."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Customer-given name for this stack instance."),
		),
		mcp.WithString("template_name",
			mcp.Required(),
			mcp.Description("Stack template name to launch (e.g. rag-chatbot)."),
		),
		mcp.WithNumber("accepted_monthly_cost",
			mcp.Required(),
			mcp.Description("The estimated monthly cost accepted by the customer after previewing. Must match the current estimate within $0.01; a material drift rejects the launch. Obtain this value from stacks_preview."),
		),
		mcp.WithString("organization_id",
			mcp.Description("Optional organization UUID to scope the stack and its inference key. The requesting user must be a member. When omitted, the caller's primary billing organization is used."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the launch (creates billable child resources)."),
		),
	), handleStacksLaunch(c))

	s.AddTool(mcp.NewTool("stacks_list",
		mcp.WithDescription("List the stacks owned by the authenticated user. Returns each stack's status, template, estimated monthly cost, and endpoint URL (once Running)."),
	), handleStacksList(c))

	s.AddTool(mcp.NewTool("stacks_get",
		mcp.WithDescription("Get a stack by UUID, including the status of every child resource (database, files, inference key, app). Poll this after stacks_launch until status is Running and endpoint_url is populated."),
		mcp.WithString("stack_id",
			mcp.Required(),
			mcp.Description("Stack UUID."),
		),
	), handleStacksGet(c))

	s.AddTool(mcp.NewTool("stacks_delete",
		mcp.WithDescription("Delete a stack: atomically tears down every child resource (database, files bucket, inference key, app) in reverse dependency order. No child resource is left orphaned. This is irreversible."),
		mcp.WithString("stack_id",
			mcp.Required(),
			mcp.Description("Stack UUID to delete."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm teardown (removes all child resources and is irreversible)."),
		),
	), handleStacksDelete(c))

	s.AddTool(mcp.NewTool("stacks_retry",
		mcp.WithDescription("Retry a Failed stack. A failed stack has already rolled back all child resources; retry re-provisions fresh from the beginning. Only stacks in the Failed status can be retried."),
		mcp.WithString("stack_id",
			mcp.Required(),
			mcp.Description("Stack UUID to retry (must be in Failed status)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the retry (provisions new billable child resources)."),
		),
	), handleStacksRetry(c))
}

func handleStacksListTemplates(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		templates, err := c.ListStackTemplates(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(templates) == 0 {
			return mcp.NewToolResultText("No stack templates available."), nil
		}
		return mcp.NewToolResultText(formatJSON(templates)), nil
	}
}

func handleStacksPreview(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		templateName, _ := args["template_name"].(string)
		if templateName == "" {
			return mcp.NewToolResultError("template_name is required"), nil
		}
		preview, err := c.PreviewStackCost(ctx, foundrydb.StackPreviewRequest{TemplateName: templateName})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(
			fmt.Sprintf("Pass monthly_total %.2f as accepted_monthly_cost when calling stacks_launch.\n%s",
				preview.MonthlyTotal, formatJSON(preview)),
		), nil
	}
}

func handleStacksLaunch(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)
		templateName, _ := args["template_name"].(string)
		if name == "" || templateName == "" {
			return mcp.NewToolResultError("name and template_name are required"), nil
		}
		acceptedCostRaw, hasAccepted := args["accepted_monthly_cost"].(float64)
		if !hasAccepted {
			return mcp.NewToolResultError("accepted_monthly_cost is required; call stacks_preview first to obtain the current estimate"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("launching stack %q from template %q (estimated $%.2f/month)", name, templateName, acceptedCostRaw)); denied != nil {
			return denied, nil
		}
		launchReq := foundrydb.StackLaunchRequest{
			Name:                name,
			TemplateName:        templateName,
			AcceptedMonthlyCost: &acceptedCostRaw,
		}
		if orgID, ok := args["organization_id"].(string); ok && orgID != "" {
			launchReq.OrganizationID = orgID
		}
		stack, err := c.LaunchStack(ctx, launchReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(
			fmt.Sprintf("Stack %s launched (status: %s). Poll stacks_get with stack_id=%s until status is Running.\n%s",
				stack.ID, stack.Status, stack.ID, formatJSON(stack)),
		), nil
	}
}

func handleStacksList(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		stacks, err := c.ListStacks(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(stacks) == 0 {
			return mcp.NewToolResultText("No stacks found."), nil
		}
		return mcp.NewToolResultText(formatJSON(stacks)), nil
	}
}

func handleStacksGet(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["stack_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("stack_id is required"), nil
		}
		stack, err := c.GetStack(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if stack == nil {
			return mcp.NewToolResultText(fmt.Sprintf("Stack %s not found.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(stack)), nil
	}
}

func handleStacksDelete(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["stack_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("stack_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting stack %s and all its child resources (database, files, inference key, app)", id)); denied != nil {
			return denied, nil
		}
		if err := c.DeleteStack(ctx, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Stack %s teardown initiated. Child resources will be removed atomically.", id)), nil
	}
}

func handleStacksRetry(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["stack_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("stack_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("retrying stack %s (provisions new billable child resources)", id)); denied != nil {
			return denied, nil
		}
		if err := c.RetryStack(ctx, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Stack %s retry initiated. Poll stacks_get until status is Running.", id)), nil
	}
}
