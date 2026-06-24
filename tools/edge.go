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
// services: custom domains (add, list, verify, delete), edge status (GET), edge
// analytics (GET), the full edge settings surface (GET and the wholesale-replace
// update covering cache rules, rate limit, WAF mode + custom WAF rules, IP
// allow/deny lists, redirects, header rules, CORS, maintenance, compression,
// request body limit, allowed methods, Basic Auth, blocked paths, HSTS,
// request-id injection, canary routing, origin health checks, the
// additional-origin pool, and the additive ordered rules engine), cache purge,
// and access-log drains (list, create, delete, test). Admin node management
// (POST/GET/DELETE /admin/edge/nodes) is not exposed here as there is no
// established admin-tool pattern in this server.
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

	s.AddTool(mcp.NewTool("get_app_edge_analytics",
		mcp.WithDescription("Return an account-scoped, server-aggregated edge analytics summary for a hosted app service over a time window: request total and breakdown by HTTP status class (2xx/3xx/4xx/5xx), error rate, cache hit/miss with hit ratio, latency percentiles (p50/p95/p99 ms), rate-limited and WAF detection counts, top requested paths, and a threat summary listing observed paths matching credential-scanner shapes. The summary is folded across the app's PoPs with a per-PoP breakdown. A window with no traffic returns zeros, not an error."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithNumber("window_minutes",
			mcp.Description("Aggregation window in minutes (default 60, clamped to a maximum of 1440)."),
		),
	), handleGetAppEdgeAnalytics(c))

	s.AddTool(mcp.NewTool("get_app_edge_settings",
		mcp.WithDescription("Return the customer-tunable edge settings currently stored for a hosted app service, plus the desired-state config version the fleet converges on. Covers cache rules, rate limit, WAF mode and custom WAF rules, IP allow/deny lists, redirects, header rules, CORS, maintenance mode, compression, request body limit, allowed methods, Basic Auth (enabled flag and usernames only, never password hashes), blocked paths, HSTS, request-id injection, canary routing, origin health checks, the additional-origin pool, and the additive ordered rules engine (rules)."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleGetAppEdgeSettings(c))

	s.AddTool(mcp.NewTool("update_app_edge_settings",
		mcp.WithDescription("Replace the customer-tunable edge settings for a hosted app service. Domains and origin are platform-managed and cannot be set here. Each field replaces the corresponding stored value wholesale; an empty array or null clears that setting. The fleet converges on the new settings asynchronously; check get_app_edge_status for convergence progress. Complex fields are passed as JSON strings whose shape matches the model: see each argument description."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be edge-configured)."),
		),
		mcp.WithString("cache_rules",
			mcp.Description("JSON array of cache rules, each with path_prefix (string, must start with /) and ttl_seconds (1-86400). A rule may additionally carry the cache-depth fields stale_while_revalidate_seconds (int), stale_if_error_seconds (int), request_collapsing (bool), and cache_key ({vary_query_params?: [..], vary_headers?: [..], vary_cookies?: [..]}). Example: [{\"path_prefix\":\"/static\",\"ttl_seconds\":3600,\"stale_while_revalidate_seconds\":60}]. An empty array clears all cache rules. (Cache-depth fields require an updated foundrydb-sdk-go; until that is published they are ignored by this MCP server.)"),
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
			mcp.Description("WAF mode: \"off\" (disabled), \"detect\" (inspect and log without blocking), or \"block\" (inspect, log, and reject matches with 403). Omit to leave unchanged."),
		),
		mcp.WithString("custom_waf_rules",
			mcp.Description("JSON array of structured custom WAF rules. Each rule: {name?, description?, uri_pattern? (RE2 regex on the URI), method? (exact HTTP method), header? {name, value_pattern}, source_ip_cidr? (CIDR or IP), action (\"block\" or \"log\")}. At least one match field is required; multiple are ANDed. Empty array clears all custom rules."),
		),
		mcp.WithString("ip_allow_list",
			mcp.Description("JSON array of CIDRs or bare IPs; only matching clients are admitted (everyone else gets 403). Empty array clears the gate."),
		),
		mcp.WithString("ip_deny_list",
			mcp.Description("JSON array of CIDRs or bare IPs; matching clients get 403. Empty array clears the gate."),
		),
		mcp.WithString("redirects",
			mcp.Description("JSON array of redirect rules: {from_path (absolute path, exact match), to_url (absolute http(s) URL or absolute path), status_code? (301|302|307|308, default 302)}. Empty array clears all redirects."),
		),
		mcp.WithString("header_rules",
			mcp.Description("JSON object of edge header manipulation: {request_set?: {name:value}, request_remove?: [name], response_set?: {name:value}, response_remove?: [name]}. request_* apply toward the origin, response_* toward the client. null/empty clears all header ops."),
		),
		mcp.WithString("cors",
			mcp.Description("JSON CORS policy: {allowed_origins?: [\"*\"|origin], allowed_methods?: [..], allowed_headers?: [..], expose_headers?: [..], allow_credentials?: bool, max_age_seconds?: int}. null/empty clears CORS."),
		),
		mcp.WithString("maintenance",
			mcp.Description("JSON maintenance setting: {enabled: bool, status_code? (503|200|307|451), body? (HTML/text), bypass_ips?: [CIDR]}. enabled=false clears the maintenance page."),
		),
		mcp.WithString("compression",
			mcp.Description("JSON gzip compression setting: {enabled: bool, extra_content_types?: [media-type]}. enabled=false clears compression."),
		),
		mcp.WithNumber("max_request_body_bytes",
			mcp.Description("Per-app request body size limit in bytes (up to 1 GiB). 0 clears the limit."),
		),
		mcp.WithString("allowed_methods",
			mcp.Description("JSON array of allowed HTTP methods (allow-list). Empty array clears the allow-list (every method allowed again)."),
		),
		mcp.WithString("basic_auth",
			mcp.Description("JSON Basic Auth setting: {enabled: bool, accounts?: [{username, password?}]}. Passwords are plaintext, hashed server-side and never stored or echoed; omit a password to keep an existing account's hash. enabled=false clears the challenge."),
		),
		mcp.WithString("blocked_paths",
			mcp.Description("JSON array of blocked path prefixes; matching requests are rejected at the edge. Empty array clears all blocks."),
		),
		mcp.WithString("hsts",
			mcp.Description("JSON HSTS setting: {enabled: bool, max_age_seconds?: int, include_subdomains?: bool, preload?: bool}. preload requires include_subdomains and a max-age of at least one year. enabled=false clears the header."),
		),
		mcp.WithString("request_id",
			mcp.Description("JSON request-id injection setting: {enabled: bool, header_name?: string (default X-Request-ID)}. enabled=false clears the injection."),
		),
		mcp.WithString("canary",
			mcp.Description("JSON sticky canary routing setting: {enabled: bool, match_cookie?: string, match_header?: string (exactly one of cookie/header), match_value?: string, variant_header_name?: string (default X-Variant), variant_header_value?: string (default canary)}. enabled=false clears canary routing."),
		),
		mcp.WithString("health_check",
			mcp.Description("JSON origin health-check setting: {active?: {enabled: bool, path?, interval_seconds?, timeout_seconds?, expect_status?}, passive?: {max_fails?, fail_duration_seconds?, unhealthy_status?: [int]}}. null clears all health checking."),
		),
		mcp.WithString("origin_pool",
			mcp.Description("JSON additional-origin pool: {additional_origins?: [{host, port, sni?, weight?, backup?}], lb_policy? (round_robin|weighted|least_conn|first), try_duration_seconds?, retries?, retry_statuses?: [int]}. null/empty (no additional origins) clears the pool."),
		),
		mcp.WithBoolean("canary_rollout_enabled",
			mcp.Description("Opt the app into staged per-node/per-PoP config rollouts: a new config version is dispatched to a canary subset (one node, or one PoP) first and held for a manual promote (get_app_edge_rollout, promote_app_edge_rollout) with auto-abort on a canary 5xx spike, instead of being dispatched fleet-wide immediately. false keeps immediate fleet-wide dispatch."),
		),
		mcp.WithString("rules",
			mcp.Description("JSON array of the additive, ordered, composable rules engine. Each rule: {name?, priority? (lower runs first), match: {path_prefix?, path_regex? (RE2), methods?: [string], header?: {name, value? | regex?}}, action: {type: one of redirect|set_header|rewrite|block|origin_override|continue, plus type-specific fields}}. redirect: {redirect_to, redirect_status?}. set_header: {set_request_headers?, remove_request_headers?, set_response_headers?, remove_response_headers?} (a protected header is rejected). rewrite: {rewrite (absolute path)}. block: {block_status? (default 403)}. origin_override: {origin_override: {host, port, sni?}}. Rules render at one precedence point: after the platform short-circuits and the fixed redirects/CORS/method-filter/auth, before WAF/rate-limit/cache/origin. block/redirect/origin_override are terminal; set_header/rewrite/continue fall through. Empty array clears all rules."),
		),
		mcp.WithString("jwt_auth",
			mcp.Description("JSON JWT validation setting: {enabled: bool, paths?: [prefix], jwks_url?: string, public_keys?: [PEM], issuer?: string, audiences?: [string], required_claims?: [{name, value}], forward_claims_header?: string}. enabled=false clears JWT auth. (Requires an updated foundrydb-sdk-go publishing EdgeJWTAuth; until that is published this argument is documented but ignored by this MCP server.)"),
		),
		mcp.WithString("signed_urls",
			mcp.Description("JSON signed-URL setting: {enabled: bool, paths?: [prefix], secret_name?: string (reference name only, never the secret value), ttl_seconds?: int, signature_param?: string (default sig), expires_param?: string (default exp)}. enabled=false clears signed-URL enforcement. (Requires an updated foundrydb-sdk-go publishing EdgeSignedURLs; until that is published this argument is documented but ignored by this MCP server.)"),
		),
		mcp.WithString("api_key_auth",
			mcp.Description("JSON inbound API-key auth setting: {enabled: bool, paths?: [prefix], key_location? (\"header\"|\"query\", default header), key_name?: string (default X-API-Key), keys?: [{name, key? (PLAINTEXT, write-only, hashed server-side, never echoed), rate_tier? (a rate-limit object {requests_per_second, burst, key})}]}. enabled=false clears API-key auth; the response echoes a non-secret view (no key material). (Requires an updated foundrydb-sdk-go publishing EdgeAPIKeyAuthRequest; until that is published this argument is documented but ignored by this MCP server.)"),
		),
		mcp.WithNumber("waf_paranoia_level",
			mcp.Description("WAF Core Rule Set paranoia level (1-4; 0 means the platform default PL1). Higher levels add stricter rules at the cost of more false positives. (Requires an updated foundrydb-sdk-go; until that is published this argument is documented but ignored by this MCP server.)"),
		),
		mcp.WithString("waf_rule_exclusions",
			mcp.Description("JSON array of WAF rule exclusions, each {rule_id?: int, target?: string} (at least one set), used to suppress a specific CRS rule or a target within it to tame false positives. Empty array clears all exclusions. (Requires an updated foundrydb-sdk-go; until that is published this argument is documented but ignored by this MCP server.)"),
		),
		mcp.WithString("ddos_profile",
			mcp.Description("JSON L7 DDoS protection profile: {enabled: bool, per_ip_requests_per_second?: int, per_ip_burst?: int, per_ip_conn_cap?: int}. enabled=false clears the profile. (Requires an updated foundrydb-sdk-go publishing EdgeDDoSProfile; until that is published this argument is documented but ignored by this MCP server.)"),
		),
		mcp.WithString("bot_management",
			mcp.Description("JSON bot-management setting: {enabled: bool, action? (\"log\"|\"block\"|\"challenge\", default log), known_bad_bots?: bool, rate_based_heuristic?: bool}. enabled=false clears bot management. (Requires an updated foundrydb-sdk-go publishing EdgeBotManagement; until that is published this argument is documented but ignored by this MCP server.)"),
		),
		mcp.WithString("ato_protection",
			mcp.Description("JSON account-takeover (credential-stuffing) protection: {enabled: bool, auth_paths?: [prefix], failure_status_codes?: [int] (default [401,403]), per_ip_threshold_per_min?: int, per_username_threshold_per_min?: int, username_field?: string, action? (\"alert\"|\"ratelimit\"|\"lock\", default alert)}. enabled=false clears ATO protection. (Requires an updated foundrydb-sdk-go publishing EdgeATOProtection; until that is published this argument is documented but ignored by this MCP server.)"),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the settings update."),
		),
	), handleUpdateAppEdgeSettings(c))

	s.AddTool(mcp.NewTool("purge_app_edge_cache",
		mcp.WithDescription("Flush a hosted app service's edge cache across its serving PoP nodes, either entirely or for a set of absolute paths. Set exactly one of purge_all=true or paths. The purge rolls across nodes one at a time in the background (the PoP keeps serving throughout), so the response reports the plan (planned node count and ids) rather than the completed result."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithBoolean("purge_all",
			mcp.Description("Set true to drop every cached entry for the app on the fleet. Mutually exclusive with paths."),
		),
		mcp.WithString("paths",
			mcp.Description("JSON array of absolute URL paths (each beginning with /) whose cached entries are invalidated. Ignored when purge_all is true. Example: [\"/static/app.js\",\"/index.html\"]."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the cache purge."),
		),
	), handlePurgeAppEdgeCache(c))

	s.AddTool(mcp.NewTool("list_app_edge_config_versions",
		mcp.WithDescription("List the append-only version history of a hosted app service's edge configuration, newest first. Each entry has the version number, content hash, source (reconcile, settings, or rollback), the user who initiated it (when attributable), the created-at timestamp, whether it is the currently active version, and (for a rollback) the version it restored. The live edge configuration is the single source of truth for what is active; this history is the immutable audit trail and the source rollback_app_edge_config restores from. Use it to find a version to roll back to."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleListAppEdgeConfigVersions(c))

	s.AddTool(mcp.NewTool("rollback_app_edge_config",
		mcp.WithDescription("Roll a hosted app service's edge configuration back to a prior version. Supply exactly one of to_version (an explicit version number, from list_app_edge_config_versions) or to=\"previous\" (the version immediately before the active one). The rollback NEVER mutates the history: it restores the target version's customer-settable subset onto the live configuration as a NEW forward version, keeping the current platform-derived domains and origin. The edge fleet converges on the new version asynchronously; poll get_app_edge_status for progress. Returns the new active version."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be edge-configured)."),
		),
		mcp.WithNumber("to_version",
			mcp.Description("The explicit version number to roll back to. Mutually exclusive with to."),
		),
		mcp.WithString("to",
			mcp.Description("Set to \"previous\" to roll back to the version immediately before the active one. Mutually exclusive with to_version."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the rollback."),
		),
	), handleRollbackAppEdgeConfig(c))

	s.AddTool(mcp.NewTool("get_app_edge_rollout",
		mcp.WithDescription("Get a hosted app service's current staged edge config rollout (the active one, or the most recent terminal one). A rollout stages a new config version to a canary subset (one node, or one PoP) first, then promotes it to the rest of the fleet or aborts (the rest is never given the version). Returns active=false with no rollout when the app has never had one. Rollouts are opened automatically when the app's edge settings enable canary_rollout_enabled and a new config version is produced; phase is one of canary (held on the subset), promoting (fanning out), promoted (whole fleet converged), or aborted."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleGetAppEdgeRollout(c))

	s.AddTool(mcp.NewTool("promote_app_edge_rollout",
		mcp.WithDescription("Promote a holding canary rollout for a hosted app service so the platform fans the canary config version out to the rest of the edge fleet. Only an active rollout in the canary phase can be promoted (a promoting or terminal rollout is rejected). The fleet converges asynchronously; poll get_app_edge_rollout and get_app_edge_status for progress."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must have an active canary rollout)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the promotion."),
		),
	), handlePromoteAppEdgeRollout(c))

	s.AddTool(mcp.NewTool("abort_app_edge_rollout",
		mcp.WithDescription("Abort an active staged edge config rollout for a hosted app service. The rest of the fleet was never given the target version, so it keeps serving the prior version; the canary subset can be recovered with rollback_app_edge_config. Only an active rollout (canary or promoting) can be aborted."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must have an active rollout)."),
		),
		mcp.WithString("reason",
			mcp.Description("Optional operator note recorded as the rollout's abort reason."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the abort."),
		),
	), handleAbortAppEdgeRollout(c))

	s.AddTool(mcp.NewTool("list_edge_log_drains",
		mcp.WithDescription("List the edge access-log drains for a hosted app service. Each drain streams the app's per-request edge access logs to a customer destination (S3-compatible bucket, generic HTTP webhook, or a supported observability backend). Destination secrets are never returned; only summary fields and the redaction policy are."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleListEdgeLogDrains(c))

	s.AddTool(mcp.NewTool("create_edge_log_drain",
		mcp.WithDescription("Create an edge access-log drain for a hosted app service. The destination configuration is validated at create time and missing credentials are rejected. A GDPR redaction policy is applied to every line before it leaves the platform; if omitted it defaults to truncated client IP, stripped query string, and a safe header allow-list. Authorization and Cookie headers are always dropped."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Human-readable drain name."),
		),
		mcp.WithString("destination_type",
			mcp.Required(),
			mcp.Description("Destination type: s3, webhook, datadog, loki, elasticsearch, otlp, cloudwatch, betterstack, or prometheus_remote_write."),
		),
		mcp.WithString("configuration",
			mcp.Required(),
			mcp.Description("JSON object of destination-specific config. s3: {endpoint?, region, bucket, prefix?, access_key_id, secret_access_key}. webhook: {url, auth_header_name?, auth_header_value?}."),
		),
		mcp.WithString("redaction_policy",
			mcp.Description("Optional JSON redaction policy: {ip_mode: full|truncated|hashed|omitted, strip_query_string: bool, header_allow_list: [..]}. Defaults applied when omitted."),
		),
		mcp.WithNumber("export_interval_seconds",
			mcp.Description("Export cadence in seconds (minimum 30, default 60)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm drain creation."),
		),
	), handleCreateEdgeLogDrain(c))

	s.AddTool(mcp.NewTool("delete_edge_log_drain",
		mcp.WithDescription("Delete an edge access-log drain. Stops all future exports for it; no orphan delivery remains."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithString("drain_id",
			mcp.Required(),
			mcp.Description("Drain UUID."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm deletion."),
		),
	), handleDeleteEdgeLogDrain(c))

	s.AddTool(mcp.NewTool("test_edge_log_drain",
		mcp.WithDescription("Verify connectivity and credentials to an edge log drain's destination without sending real log data. Returns ok=true or ok=false with an error message."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithString("drain_id",
			mcp.Required(),
			mcp.Description("Drain UUID."),
		),
	), handleTestEdgeLogDrain(c))
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

func handleGetAppEdgeAnalytics(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		windowMinutes := 0
		if w, ok := args["window_minutes"].(float64); ok && w > 0 {
			windowMinutes = int(w)
		}
		analytics, err := c.GetAppEdgeAnalytics(ctx, id, windowMinutes)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(analytics)), nil
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

		// Structured fields are passed as JSON strings whose shape matches the
		// model. Each non-empty argument is decoded into the corresponding request
		// field; an invalid JSON value is rejected rather than silently dropped.
		if raw, ok := args["custom_waf_rules"].(string); ok && raw != "" {
			if err := jsonUnmarshalArg(raw, &settingsReq.CustomWAFRules); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid custom_waf_rules JSON: %v", err)), nil
			}
		}
		if raw, ok := args["ip_allow_list"].(string); ok && raw != "" {
			if err := jsonUnmarshalArg(raw, &settingsReq.IPAllowList); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid ip_allow_list JSON: %v", err)), nil
			}
		}
		if raw, ok := args["ip_deny_list"].(string); ok && raw != "" {
			if err := jsonUnmarshalArg(raw, &settingsReq.IPDenyList); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid ip_deny_list JSON: %v", err)), nil
			}
		}
		if raw, ok := args["redirects"].(string); ok && raw != "" {
			if err := jsonUnmarshalArg(raw, &settingsReq.Redirects); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid redirects JSON: %v", err)), nil
			}
		}
		if raw, ok := args["header_rules"].(string); ok && raw != "" {
			var hr foundrydb.EdgeHeaderRules
			if err := jsonUnmarshalArg(raw, &hr); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid header_rules JSON: %v", err)), nil
			}
			settingsReq.HeaderRules = &hr
		}
		if raw, ok := args["cors"].(string); ok && raw != "" {
			var cors foundrydb.EdgeCORS
			if err := jsonUnmarshalArg(raw, &cors); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid cors JSON: %v", err)), nil
			}
			settingsReq.CORS = &cors
		}
		if raw, ok := args["maintenance"].(string); ok && raw != "" {
			var m foundrydb.EdgeMaintenance
			if err := jsonUnmarshalArg(raw, &m); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid maintenance JSON: %v", err)), nil
			}
			settingsReq.Maintenance = &m
		}
		if raw, ok := args["compression"].(string); ok && raw != "" {
			var comp foundrydb.EdgeCompression
			if err := jsonUnmarshalArg(raw, &comp); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid compression JSON: %v", err)), nil
			}
			settingsReq.Compression = &comp
		}
		if v, ok := args["max_request_body_bytes"].(float64); ok && v > 0 {
			settingsReq.MaxRequestBodyBytes = int64(v)
		}
		if raw, ok := args["allowed_methods"].(string); ok && raw != "" {
			if err := jsonUnmarshalArg(raw, &settingsReq.AllowedMethods); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid allowed_methods JSON: %v", err)), nil
			}
		}
		if raw, ok := args["basic_auth"].(string); ok && raw != "" {
			var ba foundrydb.EdgeBasicAuthRequest
			if err := jsonUnmarshalArg(raw, &ba); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid basic_auth JSON: %v", err)), nil
			}
			settingsReq.BasicAuth = &ba
		}
		if raw, ok := args["blocked_paths"].(string); ok && raw != "" {
			if err := jsonUnmarshalArg(raw, &settingsReq.BlockedPaths); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid blocked_paths JSON: %v", err)), nil
			}
		}
		if raw, ok := args["hsts"].(string); ok && raw != "" {
			var h foundrydb.EdgeHSTS
			if err := jsonUnmarshalArg(raw, &h); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid hsts JSON: %v", err)), nil
			}
			settingsReq.HSTS = &h
		}
		if raw, ok := args["request_id"].(string); ok && raw != "" {
			var rid foundrydb.EdgeRequestID
			if err := jsonUnmarshalArg(raw, &rid); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid request_id JSON: %v", err)), nil
			}
			settingsReq.RequestID = &rid
		}
		if raw, ok := args["canary"].(string); ok && raw != "" {
			var canary foundrydb.EdgeCanary
			if err := jsonUnmarshalArg(raw, &canary); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid canary JSON: %v", err)), nil
			}
			settingsReq.Canary = &canary
		}
		if raw, ok := args["health_check"].(string); ok && raw != "" {
			var hc foundrydb.EdgeOriginHealthCheck
			if err := jsonUnmarshalArg(raw, &hc); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid health_check JSON: %v", err)), nil
			}
			settingsReq.HealthCheck = &hc
		}
		if raw, ok := args["origin_pool"].(string); ok && raw != "" {
			var pool foundrydb.EdgeOriginPool
			if err := jsonUnmarshalArg(raw, &pool); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid origin_pool JSON: %v", err)), nil
			}
			settingsReq.OriginPool = &pool
		}
		if v, ok := args["canary_rollout_enabled"].(bool); ok {
			settingsReq.CanaryRolloutEnabled = v
		}
		if raw, ok := args["rules"].(string); ok && raw != "" {
			var rules []foundrydb.EdgeRule
			if err := jsonUnmarshalArg(raw, &rules); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid rules JSON: %v", err)), nil
			}
			settingsReq.Rules = rules
		}

		// The net-new access/auth, security-hardening, and cache-depth fields
		// (jwt_auth, signed_urls, api_key_auth, waf_paranoia_level,
		// waf_rule_exclusions, ddos_profile, bot_management, ato_protection, and
		// the per-rule cache-depth fields) are documented in this tool's argument
		// schema but cannot be wired here yet: the pinned foundrydb-sdk-go does not
		// expose the corresponding request types. Once an updated SDK is published
		// with those types, decode each argument into settingsReq the same way as
		// the structured fields above. Until then they are accepted and ignored
		// rather than fabricating types that would not compile.

		settings, err := c.UpdateAppEdgeSettings(ctx, id, settingsReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(settings)), nil
	}
}

func handleGetAppEdgeSettings(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		settings, err := c.GetAppEdgeSettings(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(settings)), nil
	}
}

func handlePurgeAppEdgeCache(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		purgeReq := foundrydb.EdgeCachePurgeRequest{}
		if all, ok := args["purge_all"].(bool); ok {
			purgeReq.All = all
		}
		if raw, ok := args["paths"].(string); ok && raw != "" {
			if err := jsonUnmarshalArg(raw, &purgeReq.Paths); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid paths JSON: %v", err)), nil
			}
		}
		if purgeReq.All && len(purgeReq.Paths) > 0 {
			return mcp.NewToolResultError("specify either purge_all or paths, not both"), nil
		}
		if !purgeReq.All && len(purgeReq.Paths) == 0 {
			return mcp.NewToolResultError("specify purge_all=true or at least one path to purge"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("purging the edge cache for app service %s", id)); denied != nil {
			return denied, nil
		}
		result, err := c.PurgeAppEdgeCache(ctx, id, purgeReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleListEdgeLogDrains(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		drains, err := c.ListEdgeLogDrains(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(drains) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No edge log drains found for app service %s.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(drains)), nil
	}
}

func handleCreateEdgeLogDrain(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		name, _ := args["name"].(string)
		destType, _ := args["destination_type"].(string)
		rawCfg, _ := args["configuration"].(string)
		if id == "" || name == "" || destType == "" || rawCfg == "" {
			return mcp.NewToolResultError("app_service_id, name, destination_type, and configuration are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("creating an edge log drain for app service %s", id)); denied != nil {
			return denied, nil
		}

		var cfg map[string]any
		if err := jsonUnmarshalArg(rawCfg, &cfg); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid configuration JSON: %v", err)), nil
		}

		createReq := foundrydb.CreateEdgeLogDrainRequest{
			Name:            name,
			DestinationType: destType,
			Configuration:   cfg,
		}
		if raw, ok := args["redaction_policy"].(string); ok && raw != "" {
			var policy foundrydb.EdgeRedactionPolicy
			if err := jsonUnmarshalArg(raw, &policy); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid redaction_policy JSON: %v", err)), nil
			}
			createReq.RedactionPolicy = &policy
		}
		if interval, ok := args["export_interval_seconds"].(float64); ok && interval > 0 {
			createReq.ExportIntervalSeconds = int(interval)
		}

		drain, err := c.CreateEdgeLogDrain(ctx, id, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(drain)), nil
	}
}

func handleDeleteEdgeLogDrain(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		drainID, _ := args["drain_id"].(string)
		if id == "" || drainID == "" {
			return mcp.NewToolResultError("app_service_id and drain_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting edge log drain %s", drainID)); denied != nil {
			return denied, nil
		}
		if err := c.DeleteEdgeLogDrain(ctx, id, drainID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Edge log drain %s deleted.", drainID)), nil
	}
}

func handleTestEdgeLogDrain(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		drainID, _ := args["drain_id"].(string)
		if id == "" || drainID == "" {
			return mcp.NewToolResultError("app_service_id and drain_id are required"), nil
		}
		result, err := c.TestEdgeLogDrain(ctx, id, drainID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

// jsonUnmarshalArg unmarshals a JSON string argument into v.
func jsonUnmarshalArg(raw string, v interface{}) error {
	return json.Unmarshal([]byte(raw), v)
}

func handleListAppEdgeConfigVersions(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		out, err := c.ListAppEdgeConfigVersions(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(out)), nil
	}
}

func handleRollbackAppEdgeConfig(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		rollbackReq := foundrydb.EdgeRollbackRequest{}
		if v, ok := args["to_version"].(float64); ok && v > 0 {
			rollbackReq.ToVersion = int64(v)
		}
		if to, ok := args["to"].(string); ok && to != "" {
			rollbackReq.To = to
		}
		if rollbackReq.ToVersion <= 0 && rollbackReq.To == "" {
			return mcp.NewToolResultError("specify either to_version (a positive version) or to=\"previous\""), nil
		}
		if rollbackReq.ToVersion > 0 && rollbackReq.To != "" {
			return mcp.NewToolResultError("specify either to_version or to, not both"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("rolling back the edge configuration for app service %s", id)); denied != nil {
			return denied, nil
		}
		out, err := c.RollbackAppEdgeConfig(ctx, id, rollbackReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(out)), nil
	}
}

func handleGetAppEdgeRollout(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		out, err := c.GetAppEdgeRollout(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(out)), nil
	}
}

func handlePromoteAppEdgeRollout(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("promoting the canary rollout for app service %s", id)); denied != nil {
			return denied, nil
		}
		if err := c.PromoteAppEdgeRollout(ctx, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Canary rollout for app service %s promoted; the platform is fanning the version out to the rest of the fleet.", id)), nil
	}
}

func handleAbortAppEdgeRollout(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("aborting the edge rollout for app service %s", id)); denied != nil {
			return denied, nil
		}
		abortReq := foundrydb.EdgeRolloutAbortRequest{}
		if reason, ok := args["reason"].(string); ok {
			abortReq.Reason = reason
		}
		if err := c.AbortAppEdgeRollout(ctx, id, abortReq); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Edge rollout for app service %s aborted; the rest of the fleet keeps serving the prior version.", id)), nil
	}
}
