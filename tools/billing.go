package tools

import (
	"context"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterBillingTools registers read-only billing tools so an assistant can
// report cost and usage. No payment-mutating endpoints (top-up, payment
// method, credits) are exposed. All endpoints accept an optional org_id and
// default to the caller's personal organization.
func RegisterBillingTools(s *server.MCPServer, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("get_billing_usage",
		mcp.WithDescription("Get current billing usage: credit balance, the current period, per-service cost breakdown, and total hourly and monthly cost."),
		mcp.WithString("organization_id",
			mcp.Description("Organization UUID. Omit to use your personal organization."),
		),
	), handleGetBillingUsage(cfg))

	s.AddTool(mcp.NewTool("get_billing_invoices",
		mcp.WithDescription("List billing invoices with their status (draft, issued, paid, overdue, cancelled), amount, and dates."),
		mcp.WithNumber("limit",
			mcp.Description("Maximum invoices to return (default: 20)"),
		),
		mcp.WithString("organization_id",
			mcp.Description("Organization UUID. Omit to use your personal organization."),
		),
	), handleGetBillingInvoices(cfg))

	s.AddTool(mcp.NewTool("get_billing_credits",
		mcp.WithDescription("Get the credit balance, auto top-up configuration, and recent credit transactions for an organization."),
		mcp.WithString("organization_id",
			mcp.Description("Organization UUID. Omit to use your personal organization."),
		),
	), handleGetBillingCredits(cfg))

	s.AddTool(mcp.NewTool("get_billing_pricing",
		mcp.WithDescription("Get the platform price list: compute tiers, starter bundles, and per-GB storage and backup rates. Use this to estimate the cost of a service before creating it."),
	), handleGetBillingPricing(cfg))
}

// withOrgQuery appends an org_id query parameter when organization_id is set.
func withOrgQuery(path string, args map[string]interface{}) string {
	if orgID, ok := args["organization_id"].(string); ok && orgID != "" {
		return path + "?org_id=" + orgID
	}
	return path
}

func handleGetBillingUsage(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := apiGet(ctx, cfg, withOrgQuery("/billing/usage", req.GetArguments()))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetBillingInvoices(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		path := "/billing/invoices"
		sep := "?"
		if limit, ok := args["limit"].(float64); ok && limit > 0 {
			path += "?limit=" + itoa(int(limit))
			sep = "&"
		}
		if orgID, ok := args["organization_id"].(string); ok && orgID != "" {
			path += sep + "org_id=" + orgID
		}
		result, err := apiGet(ctx, cfg, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetBillingCredits(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := apiGet(ctx, cfg, withOrgQuery("/billing/credits", req.GetArguments()))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetBillingPricing(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := apiGet(ctx, cfg, "/billing/pricing")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}
