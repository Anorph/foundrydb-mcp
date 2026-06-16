package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterEdgeTools registers MCP tools for the edge gateway surface of app
// services: custom domains (add, list, verify, delete), edge status (GET), and
// edge settings (cache rules, rate limit, WAF mode).
func RegisterEdgeTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("list_app_domains",
		mcp.WithDescription("List the custom domains attached to a hosted app service and their verification status (pending_verification, verifying, issuing_certificate, propagating, active, failed, deleting). Each domain also carries the CNAME target that customer DNS should point at."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleListAppDomains(c))

	s.AddTool(mcp.NewTool("add_app_domain",
		mcp.WithDescription("Add a custom domain to a hosted app service. The domain enters pending_verification; the platform worker checks that the domain's DNS points at the app (CNAME to the platform hostname returned in cname_target, or A record to the home PoP edge IP) and then provisions a TLS certificate via on-demand TLS. Use verify_app_domain to trigger an immediate re-check after updating DNS."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithString("domain",
			mcp.Required(),
			mcp.Description("Fully-qualified custom hostname to add (e.g. app.acme.com). Must not be a foundrydb.com subdomain; up to five domains per app."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm adding the domain."),
		),
	), handleAddAppDomain(c))

	s.AddTool(mcp.NewTool("verify_app_domain",
		mcp.WithDescription("Trigger an immediate re-verification pass for a custom domain that is in failed or pending_verification status. Use this after updating your DNS records so the platform checks them without waiting for the background polling interval."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithString("domain_id",
			mcp.Required(),
			mcp.Description("Domain UUID (from list_app_domains). Must be in failed or pending_verification status."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the re-verification request."),
		),
	), handleVerifyAppDomain(c))

	s.AddTool(mcp.NewTool("delete_app_domain",
		mcp.WithDescription("Remove a custom domain from a hosted app service. The platform withdraws the domain from the edge fleet; the edge runtime stops serving it and drops its certificate. Idempotent: succeeds even if the domain is already gone."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithString("domain_id",
			mcp.Required(),
			mcp.Description("Domain UUID to remove (from list_app_domains)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm deletion (irreversible)."),
		),
	), handleDeleteAppDomain(c))

	s.AddTool(mcp.NewTool("get_app_edge_status",
		mcp.WithDescription("Return the edge status for a hosted app service: whether the edge tier is enabled, the home PoP zone, the CNAME target for custom domains, the desired-state config version, and per-PoP convergence status (zone, applied_version, status). Use this to check that the edge fleet has fully converged after a domain add or settings change."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleGetAppEdgeStatus(c))

	s.AddTool(mcp.NewTool("update_app_edge_settings",
		mcp.WithDescription("Replace the customer-tunable edge settings for a hosted app service: cache rules (path prefix + TTL), rate limiting (requests per second, burst, key by ip or api_key), and WAF mode (off or detect). Domains and origin are platform-managed and cannot be set here. The fleet converges on the new settings asynchronously; check get_app_edge_status for convergence progress."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be edge-configured)."),
		),
		mcp.WithString("cache_rules",
			mcp.Description("JSON array of cache rules, each with path_prefix (string, must start with /) and ttl_seconds (1-86400). Example: [{\"path_prefix\":\"/static\",\"ttl_seconds\":3600}]. An empty array clears all cache rules."),
		),
		mcp.WithNumber("rate_limit_rps",
			mcp.Description("Optional rate limit: maximum requests per second per bucket (1-10000)."),
		),
		mcp.WithNumber("rate_limit_burst",
			mcp.Description("Optional rate limit: token bucket depth; must be >= rate_limit_rps and <= 20000."),
		),
		mcp.WithString("rate_limit_key",
			mcp.Description("Optional rate limit key: \"ip\" (bucket by client IP) or \"api_key\" (bucket by API key header, falls back to IP). Required when rate_limit_rps is set."),
		),
		mcp.WithString("waf_mode",
			mcp.Description("WAF mode: \"off\" (disabled) or \"detect\" (inspect and log without blocking). Omit to leave unchanged."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the settings update."),
		),
	), handleUpdateAppEdgeSettings(c))
}

func handleListAppDomains(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		domains, err := c.ListAppDomains(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(domains) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No custom domains found for app service %s.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(domains)), nil
	}
}

func handleAddAppDomain(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		domain, _ := args["domain"].(string)
		if id == "" || domain == "" {
			return mcp.NewToolResultError("app_service_id and domain are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("adding custom domain %q to app service %s", domain, id)); denied != nil {
			return denied, nil
		}
		d, err := c.CreateAppDomain(ctx, id, foundrydb.CreateEdgeDomainRequest{Domain: domain})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(d)), nil
	}
}

func handleVerifyAppDomain(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		domainID, _ := args["domain_id"].(string)
		if id == "" || domainID == "" {
			return mcp.NewToolResultError("app_service_id and domain_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("re-verifying domain %s on app service %s", domainID, id)); denied != nil {
			return denied, nil
		}
		if err := c.VerifyAppDomain(ctx, id, domainID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Verification pass queued for domain %s.", domainID)), nil
	}
}

func handleDeleteAppDomain(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		domainID, _ := args["domain_id"].(string)
		if id == "" || domainID == "" {
			return mcp.NewToolResultError("app_service_id and domain_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting domain %s from app service %s", domainID, id)); denied != nil {
			return denied, nil
		}
		if err := c.DeleteAppDomain(ctx, id, domainID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Domain %s deletion requested.", domainID)), nil
	}
}

func handleGetAppEdgeStatus(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		status, err := c.GetAppEdgeStatus(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(status)), nil
	}
}

func handleUpdateAppEdgeSettings(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("updating edge settings for app service %s", id)); denied != nil {
			return denied, nil
		}

		settingsReq := foundrydb.EdgeSettingsRequest{}

		// Parse cache rules from JSON string argument.
		if raw, ok := args["cache_rules"].(string); ok && raw != "" {
			var rules []foundrydb.EdgeCacheRule
			if err := jsonUnmarshalArg(raw, &rules); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid cache_rules JSON: %v", err)), nil
			}
			settingsReq.CacheRules = rules
		}

		// Build rate limit when any rate limit argument is provided.
		rps, hasRPS := args["rate_limit_rps"].(float64)
		burst, hasBurst := args["rate_limit_burst"].(float64)
		key, _ := args["rate_limit_key"].(string)
		if hasRPS || hasBurst || key != "" {
			if !hasRPS {
				return mcp.NewToolResultError("rate_limit_rps is required when setting a rate limit"), nil
			}
			if key == "" {
				key = "ip"
			}
			rl := &foundrydb.EdgeRateLimit{
				RequestsPerSecond: int(rps),
				Key:               foundrydb.EdgeRateLimitKey(key),
			}
			if hasBurst {
				rl.Burst = int(burst)
			} else {
				rl.Burst = int(rps) * 2
			}
			settingsReq.RateLimit = rl
		}

		if waf, ok := args["waf_mode"].(string); ok && waf != "" {
			mode := foundrydb.EdgeWAFMode(waf)
			settingsReq.WAFMode = &mode
		}

		settings, err := c.UpdateAppEdgeSettings(ctx, id, settingsReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(settings)), nil
	}
}

// jsonUnmarshalArg unmarshals a JSON string argument into v.
func jsonUnmarshalArg(raw string, v interface{}) error {
	return json.Unmarshal([]byte(raw), v)
}
