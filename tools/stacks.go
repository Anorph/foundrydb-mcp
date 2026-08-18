package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterStackTools registers MCP tools for the vertical-starter stack engine:
// first-party catalog browsing, cost preview, launch, status, teardown, retry,
// in-place upgrades, and customer-authored marketplace template authoring.
// Each stack composes existing platform primitives (a PostgreSQL+pgvector
// database, a files bucket, an EU-routed inference key, and a hosted app) into
// one unit, wired and metered together.
func RegisterStackTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("stacks_list_templates",
		mcp.WithDescription("List the first-party stack templates available in the catalog. Each entry includes a name, description, version, and a fresh cost preview. Use this to discover available templates before calling stacks_preview or stacks_launch."),
	), handleStacksListTemplates(c))

	s.AddTool(mcp.NewTool("stacks_preview",
		mcp.WithDescription("Preview the estimated monthly cost for a stack template before launching. Returns a breakdown by resource (database, files, inference, app). A line item marked is_ceiling is a maximum charge (such as an inference budget), not a fixed recurring cost. Pass the returned monthly_total as accepted_monthly_cost when calling stacks_launch. Provide either template_name (first-party catalog) or template_id (marketplace template)."),
		mcp.WithString("template_name",
			mcp.Description("First-party stack template name (e.g. rag-chatbot). Mutually exclusive with template_id."),
		),
		mcp.WithString("template_id",
			mcp.Description("UUID of a customer-authored marketplace template to preview. Mutually exclusive with template_name."),
		),
	), handleStacksPreview(c))

	s.AddTool(mcp.NewTool("stacks_launch",
		mcp.WithDescription("Launch a stack from a catalog or marketplace template. Composes platform primitives (database, files, inference, app) into one unit. Provisioning is asynchronous; poll stacks_get until status is Running. Requires: (1) accepted_monthly_cost matching the estimate from stacks_preview (re-launch is rejected with conflict if the cost drifted); (2) the organization must have an enabled inference provider when the template uses inference. Provide either template_name (first-party catalog) or template_id (marketplace template). Use stacks_list_templates + stacks_preview first."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Customer-given name for this stack instance."),
		),
		mcp.WithString("template_name",
			mcp.Description("First-party catalog template name to launch (e.g. rag-chatbot). Mutually exclusive with template_id."),
		),
		mcp.WithString("template_id",
			mcp.Description("UUID of a customer-authored marketplace template to launch. The template must be visible to the caller. Mutually exclusive with template_name."),
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

	// Customer-authored marketplace template authoring.
	s.AddTool(mcp.NewTool("stacks_create_template",
		mcp.WithDescription("Create a customer-authored stack template. The template starts in draft status and is visible only to the owning organization. Set visibility to org_shared or public and call stacks_publish_template to share it. The descriptor field defines the resources the stack composes."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Unique template identifier within the organization (slug-style, used as the template name, e.g. my-rag-pipeline)."),
		),
		mcp.WithString("display_name",
			mcp.Description("Human-readable name shown in the UI and marketplace catalog."),
		),
		mcp.WithString("description",
			mcp.Description("Short description of what this template provisions."),
		),
		mcp.WithString("version",
			mcp.Description("Semantic version of this descriptor, e.g. 1.0.0. Defaults to 1.0.0."),
		),
		mcp.WithString("visibility",
			mcp.Description("Who can see and launch this template: private (owning org only, default), org_shared (all org members, published immediately), or public (any organization, requires platform admin approval)."),
		),
		mcp.WithString("descriptor_json",
			mcp.Required(),
			mcp.Description("JSON-encoded StackDescriptor defining the resources this template composes. Must include a non-empty resources array with valid kinds (database, files, inference, app)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm creation of the template."),
		),
	), handleStacksCreateTemplate(c))

	s.AddTool(mcp.NewTool("stacks_list_my_templates",
		mcp.WithDescription("List all customer-authored templates owned by the caller's organization. Returns all templates regardless of visibility or publication status. Use stacks_list_marketplace for templates published by other organizations."),
	), handleStacksListMyTemplates(c))

	s.AddTool(mcp.NewTool("stacks_list_marketplace",
		mcp.WithDescription("List all customer-authored templates that are publicly published in the marketplace. Any organization may launch these templates. Use stacks_list_my_templates to see your own organization's templates."),
	), handleStacksListMarketplace(c))

	s.AddTool(mcp.NewTool("stacks_publish_template",
		mcp.WithDescription("Publish a customer-authored template. For org_shared visibility: publishes immediately; all organization members can launch it. For public visibility: submits to the platform admin moderation queue (submitted status); becomes publicly launchable only once a platform admin approves it. The template must have visibility set to org_shared or public before publishing."),
		mcp.WithString("template_id",
			mcp.Required(),
			mcp.Description("UUID of the template to publish."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm publication of the template."),
		),
	), handleStacksPublishTemplate(c))

	// In-place stack upgrade tools.
	s.AddTool(mcp.NewTool("stacks_upgrade_preview",
		mcp.WithDescription("Preview an in-place upgrade of a running stack to the current version of its template. Returns a classified upgrade plan: each resource is marked unchanged (no action), in_place (safe non-destructive change: app redeploy, plan resize, inference remint), or blocked (requires a fresh stack: stateful recreation, engine change, resource add/remove, port change). When blocked is true the upgrade cannot proceed; launch a fresh stack to adopt those changes. Calling this endpoint does not change any state."),
		mcp.WithString("stack_id",
			mcp.Required(),
			mcp.Description("UUID of the running stack to preview an upgrade for."),
		),
	), handleStacksUpgradePreview(c))

	s.AddTool(mcp.NewTool("stacks_upgrade",
		mcp.WithDescription("Apply a previewed in-place upgrade of a running stack to the current version of its template. Requires accepted_monthly_cost from stacks_upgrade_preview (enforced as a cost gate). Returns the StackMigration record when the upgrade is accepted (the reconciler applies changes asynchronously). Returns up_to_date when the stack is already on the latest version. Returns an error when the plan is blocked (launch a fresh stack instead) or when an upgrade is already in progress."),
		mcp.WithString("stack_id",
			mcp.Required(),
			mcp.Description("UUID of the running stack to upgrade."),
		),
		mcp.WithNumber("accepted_monthly_cost",
			mcp.Required(),
			mcp.Description("The new monthly cost the customer accepted after calling stacks_upgrade_preview. Must match the computed estimate within $0.01; a drift rejects the upgrade."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the upgrade (applies in-place changes to running resources)."),
		),
	), handleStacksUpgrade(c))
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
		templateID, _ := args["template_id"].(string)
		if templateName == "" && templateID == "" {
			return mcp.NewToolResultError("provide either template_name (first-party catalog) or template_id (marketplace template)"), nil
		}
		previewReq := foundrydb.StackPreviewRequest{TemplateName: templateName, TemplateID: templateID}
		preview, err := c.PreviewStackCost(ctx, previewReq)
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
		templateID, _ := args["template_id"].(string)
		if name == "" {
			return mcp.NewToolResultError("name is required"), nil
		}
		if templateName == "" && templateID == "" {
			return mcp.NewToolResultError("provide either template_name (first-party catalog) or template_id (marketplace template)"), nil
		}
		acceptedCostRaw, hasAccepted := args["accepted_monthly_cost"].(float64)
		if !hasAccepted {
			return mcp.NewToolResultError("accepted_monthly_cost is required; call stacks_preview first to obtain the current estimate"), nil
		}
		templateRef := templateName
		if templateRef == "" {
			templateRef = templateID
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("launching stack %q from template %q (estimated $%.2f/month)", name, templateRef, acceptedCostRaw)); denied != nil {
			return denied, nil
		}
		launchReq := foundrydb.StackLaunchRequest{
			Name:                name,
			TemplateName:        templateName,
			TemplateID:          templateID,
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

func handleStacksCreateTemplate(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)
		descriptorJSON, _ := args["descriptor_json"].(string)
		if name == "" {
			return mcp.NewToolResultError("name is required"), nil
		}
		if descriptorJSON == "" {
			return mcp.NewToolResultError("descriptor_json is required"), nil
		}
		var descriptor foundrydb.StackDescriptor
		if err := json.Unmarshal([]byte(descriptorJSON), &descriptor); err != nil {
			return mcp.NewToolResultError("descriptor_json is not valid JSON: " + err.Error()), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("creating stack template %q", name)); denied != nil {
			return denied, nil
		}
		createReq := foundrydb.CustomTemplateRequest{
			Name:       name,
			Descriptor: descriptor,
		}
		if v, ok := args["display_name"].(string); ok && v != "" {
			createReq.DisplayName = v
		}
		if v, ok := args["description"].(string); ok && v != "" {
			createReq.Description = v
		}
		if v, ok := args["version"].(string); ok && v != "" {
			createReq.Version = v
		}
		if v, ok := args["visibility"].(string); ok && v != "" {
			createReq.Visibility = foundrydb.StackVisibility(v)
		}
		tmpl, err := c.CreateStackTemplate(ctx, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(
			fmt.Sprintf("Template %s created (id: %s, status: %s). Call stacks_publish_template to share it.\n%s",
				tmpl.Name, tmpl.ID, tmpl.PublicationStatus, formatJSON(tmpl)),
		), nil
	}
}

func handleStacksListMyTemplates(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		templates, err := c.ListMyStackTemplates(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(templates) == 0 {
			return mcp.NewToolResultText("No templates found in your organization."), nil
		}
		return mcp.NewToolResultText(formatJSON(templates)), nil
	}
}

func handleStacksListMarketplace(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		templates, err := c.ListMarketplaceStackTemplates(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(templates) == 0 {
			return mcp.NewToolResultText("No templates published in the marketplace."), nil
		}
		return mcp.NewToolResultText(formatJSON(templates)), nil
	}
}

func handleStacksPublishTemplate(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["template_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("template_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("publishing template %s", id)); denied != nil {
			return denied, nil
		}
		tmpl, err := c.PublishStackTemplate(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		switch tmpl.PublicationStatus {
		case foundrydb.PublicationStatusPublished:
			return mcp.NewToolResultText(
				fmt.Sprintf("Template %s is now published. Organization members can launch it via template_id=%s.\n%s",
					tmpl.Name, tmpl.ID, formatJSON(tmpl)),
			), nil
		case foundrydb.PublicationStatusSubmitted:
			return mcp.NewToolResultText(
				fmt.Sprintf("Template %s submitted for platform review (status: submitted). It will become publicly launchable once a platform admin approves it.\n%s",
					tmpl.Name, formatJSON(tmpl)),
			), nil
		default:
			return mcp.NewToolResultText(formatJSON(tmpl)), nil
		}
	}
}

func handleStacksUpgradePreview(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["stack_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("stack_id is required"), nil
		}
		plan, err := c.PreviewStackUpgrade(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if plan.Blocked {
			return mcp.NewToolResultText(
				fmt.Sprintf("Upgrade from %s to %s is BLOCKED and cannot be applied in place. Launch a fresh stack to adopt these changes. Reasons: %v\n%s",
					plan.FromVersion, plan.ToVersion, plan.BlockedReasons, formatJSON(plan)),
			), nil
		}
		blocked := false
		for _, ch := range plan.Changes {
			if ch.Change == "blocked" {
				blocked = true
				break
			}
		}
		if blocked {
			return mcp.NewToolResultText(
				fmt.Sprintf("Upgrade plan has blocked changes. Launch a fresh stack to adopt them.\n%s", formatJSON(plan)),
			), nil
		}
		return mcp.NewToolResultText(
			fmt.Sprintf("Upgrade plan: %s -> %s. New monthly cost: $%.2f (delta: %+.2f). Pass %.2f as accepted_monthly_cost to stacks_upgrade.\n%s",
				plan.FromVersion, plan.ToVersion, plan.NewMonthlyCost, plan.CostDelta, plan.NewMonthlyCost, formatJSON(plan)),
		), nil
	}
}

func handleStacksUpgrade(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["stack_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("stack_id is required"), nil
		}
		acceptedCostRaw, hasAccepted := args["accepted_monthly_cost"].(float64)
		if !hasAccepted {
			return mcp.NewToolResultError("accepted_monthly_cost is required; call stacks_upgrade_preview first to obtain the new cost estimate"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("applying in-place upgrade to stack %s (new monthly cost $%.2f)", id, acceptedCostRaw)); denied != nil {
			return denied, nil
		}
		mig, err := c.ApplyStackUpgrade(ctx, id, foundrydb.StackUpgradeRequest{AcceptedMonthlyCost: &acceptedCostRaw})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if mig == nil {
			return mcp.NewToolResultText("Stack is already on the latest template version. No upgrade needed."), nil
		}
		return mcp.NewToolResultText(
			fmt.Sprintf("Upgrade accepted (migration id: %s, status: %s). The reconciler is applying changes. Poll stacks_get with stack_id=%s to monitor progress.\n%s",
				mig.ID, mig.Status, id, formatJSON(mig)),
		), nil
	}
}
