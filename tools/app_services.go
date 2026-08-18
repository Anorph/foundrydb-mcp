package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterAppServiceTools registers tools for app services: customer OCI
// containers hosted on the platform next to their managed databases, reachable
// over HTTPS at {name}.foundrydb.com. App-to-database traffic flows over private
// SDN networking with injected connection credentials.
func RegisterAppServiceTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("list_app_services",
		mcp.WithDescription("List the hosted application services (customer containers) visible to the authenticated user."),
	), handleListAppServices(c))

	s.AddTool(mcp.NewTool("get_app_service",
		mcp.WithDescription("Get a hosted app service: status, public URL, plan, container config, and attached databases."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleGetAppService(c))

	s.AddTool(mcp.NewTool("create_app_service",
		mcp.WithDescription("Deploy a public OCI container image as a hosted app on a dedicated VM, reachable over HTTPS at {name}.foundrydb.com. Optionally attach one managed database (same owner, Running, same peering region): the platform peers the private networks, opens the database firewall to the app subnet, and injects connection credentials (DATABASE_URL and MDB_<DBNAME>_* variables). Provisioning is asynchronous; poll get_app_service until status is Running."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("App name (3-63 chars, DNS-compatible), unique to the owner; becomes the {name} in {name}.foundrydb.com."),
		),
		mcp.WithString("image_ref",
			mcp.Required(),
			mcp.Description("Full public OCI image reference, e.g. ghcr.io/acme/web:1.4."),
		),
		mcp.WithNumber("container_port",
			mcp.Required(),
			mcp.Description("TCP port the container listens on (1-65535). Published on the VM loopback only; public HTTPS terminates at the platform ingress."),
		),
		mcp.WithString("plan_name",
			mcp.Description("Compute tier plan (e.g. tier-2). Defaults to tier-2."),
		),
		mcp.WithString("zone",
			mcp.Description("UpCloud zone (e.g. se-sto1). Defaults to se-sto1; must share a peering region with any attached database."),
		),
		mcp.WithString("attached_service_id",
			mcp.Description("Optional database service UUID to attach (same owner, Running). Connection env vars are injected automatically."),
		),
		mcp.WithString("env",
			mcp.Description("Optional container environment as comma-separated KEY=VALUE pairs (e.g. LOG_LEVEL=info,TZ=UTC). Must not collide with platform-injected MDB_* or DATABASE_URL."),
		),
		mcp.WithString("custom_domains",
			mcp.Description("Optional comma-separated custom hostnames to serve the app on (e.g. app.acme.com,www.acme.com). Point each at the app (CNAME to the primary domain or A record to the floating IP); certificates are issued automatically. Up to 5; foundrydb.com subdomains are not allowed."),
		),
		mcp.WithString("registry_username",
			mcp.Description("Optional username for a private registry the image is pulled from. Provide together with registry_password; the registry host is derived from image_ref."),
		),
		mcp.WithString("registry_password",
			mcp.Description("Optional password or access token for the private registry. Write-only; never returned by the API."),
		),
		mcp.WithNumber("storage_size_gb",
			mcp.Description("Persistent volume size in GB for the container's /data (default 10)."),
		),
		mcp.WithString("health_check_path",
			mcp.Description("Optional HTTP path probed to decide container health during blue/green redeploys and at runtime (e.g. /healthz). Falls back to a TCP connect on container_port when empty."),
		),
		mcp.WithNumber("health_check_interval_seconds",
			mcp.Description("Optional seconds between health probes."),
		),
		mcp.WithNumber("health_check_timeout_seconds",
			mcp.Description("Optional seconds a single health probe may take before counting as a failure."),
		),
		mcp.WithNumber("health_check_healthy_threshold",
			mcp.Description("Optional number of consecutive successful probes required before a new container is promoted to serve traffic."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm creation (provisions a billable VM)."),
		),
	), handleCreateAppService(c))

	s.AddTool(mcp.NewTool("redeploy_app_service",
		mcp.WithDescription("Roll a hosted app to a new image and/or environment via a zero-downtime blue/green swap (a failed redeploy leaves the old container serving). The container port cannot be changed after creation. Asynchronous; poll get_app_service until Running."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be Running)."),
		),
		mcp.WithString("image_ref",
			mcp.Required(),
			mcp.Description("New full OCI image reference to deploy."),
		),
		mcp.WithNumber("container_port",
			mcp.Required(),
			mcp.Description("Container port; must match the value set at creation."),
		),
		mcp.WithString("env",
			mcp.Description("Container environment as comma-separated KEY=VALUE pairs. Replaces the previous user environment."),
		),
		mcp.WithString("custom_domains",
			mcp.Description("Comma-separated custom hostnames to serve the app on. Replaces the previous set. Up to 5; foundrydb.com subdomains are not allowed."),
		),
		mcp.WithString("registry_username",
			mcp.Description("Username for a private registry. Provide with registry_password to change the stored credentials; omit both to keep the existing ones."),
		),
		mcp.WithString("registry_password",
			mcp.Description("Password or access token for the private registry. Write-only; omit to keep the stored credential."),
		),
		mcp.WithString("health_check_path",
			mcp.Description("HTTP path probed to decide container health during the blue/green redeploy and at runtime (e.g. /healthz). Falls back to a TCP connect on container_port when empty."),
		),
		mcp.WithNumber("health_check_interval_seconds",
			mcp.Description("Seconds between health probes."),
		),
		mcp.WithNumber("health_check_timeout_seconds",
			mcp.Description("Seconds a single health probe may take before counting as a failure."),
		),
		mcp.WithNumber("health_check_healthy_threshold",
			mcp.Description("Number of consecutive successful probes required before the new container is promoted to serve traffic."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the redeploy."),
		),
	), handleRedeployAppService(c))

	s.AddTool(mcp.NewTool("attach_app_database",
		mcp.WithDescription("Attach a managed service to a running app. The target may be a database, a files (object storage) service, or another app (east-west app-to-app). The platform rolls a zero-downtime redeploy so the injected environment is updated: a database injects connection credentials (DATABASE_URL when exactly one database is attached, plus MDB_<NAME>_* variables); a files service injects S3 credentials (the MDB_<NAME>_S3_* quintet, plus the bare S3_* quartet when exactly one files service is attached) and skips peering; an app injects MDB_<NAME>_HOST/PORT/URL for plain-HTTP calls over the private SDN (no credentials). For a files target you can optionally scope the minted S3 key with prefix and permission and declare a wiring intent. The app passes through PendingModification before returning to Running. Up to five attachments are supported. The target must be Running, same owner (databases also in the app's peering region), and not the app itself. Asynchronous; poll get_app_service until Running."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be Running)."),
		),
		mcp.WithString("attached_service_id",
			mcp.Required(),
			mcp.Description("UUID of the database, files, or app service to attach (same owner, Running; databases also in the app's peering region; an app cannot attach to itself)."),
		),
		mcp.WithString("prefix",
			mcp.Description("Files attachments only: object key prefix to scope the minted S3 key to (e.g. \"uploads/\"). Blank grants the whole bucket. Rejected for database or app targets."),
		),
		mcp.WithString("permission",
			mcp.Description("Files attachments only: access level for the minted S3 key, \"read_only\" or \"read_write\" (default). Rejected for database or app targets."),
		),
		mcp.WithString("wiring_intent",
			mcp.Description("Files attachments only: how the app uses the bucket, \"inject_creds\" (default) or \"auto_embed\". Rejected for database or app targets."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the attach (triggers a redeploy)."),
		),
	), handleAttachAppDatabase(c))

	s.AddTool(mcp.NewTool("detach_app_database",
		mcp.WithDescription("Detach a database from a running app. The platform reverts the database firewall opening, tears down the peering, and rolls a zero-downtime redeploy so the connection credentials are removed. The app passes through PendingModification before returning to Running. Asynchronous; poll get_app_service until Running."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be Running)."),
		),
		mcp.WithString("attachment_id",
			mcp.Required(),
			mcp.Description("Attachment UUID to remove (from the app's attachments list)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the detach (triggers a redeploy)."),
		),
	), handleDetachAppDatabase(c))

	s.AddTool(mcp.NewTool("scale_app_service",
		mcp.WithDescription("Change the compute tier of a hosted app service. Scaling up is a zero-downtime hot resize of the running VM (no redeploy); scaling down may require a brief restart. Asynchronous; poll get_app_service until Running."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be Running)."),
		),
		mcp.WithString("plan_name",
			mcp.Required(),
			mcp.Description("Target compute tier plan (e.g. tier-4)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the scale (changes the billable plan)."),
		),
	), handleScaleAppService(c))

	s.AddTool(mcp.NewTool("list_app_deployments",
		mcp.WithDescription("List the deploy history (revision history) of a hosted app service, newest first. Each entry is a previously rolled-out image and configuration; pass an entry's id to rollback_app_service to redeploy it."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleListAppDeployments(c))

	s.AddTool(mcp.NewTool("rollback_app_service",
		mcp.WithDescription("Roll a hosted app back to an earlier deployment (from list_app_deployments) via a zero-downtime blue/green swap. The app passes through PendingModification before returning to Running. Asynchronous; poll get_app_service until Running."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be Running)."),
		),
		mcp.WithString("deployment_id",
			mcp.Required(),
			mcp.Description("Deployment UUID to redeploy (from list_app_deployments)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the rollback (triggers a redeploy)."),
		),
	), handleRollbackAppService(c))

	s.AddTool(mcp.NewTool("restart_app_service",
		mcp.WithDescription("Restart the app's running container in place."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the restart."),
		),
	), handleRestartAppService(c))

	s.AddTool(mcp.NewTool("delete_app_service",
		mcp.WithDescription("Delete a hosted app service. The platform reverts any attached database's firewall, tears down the network peerings, and destroys the VM. This is irreversible."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm deletion."),
		),
	), handleDeleteAppService(c))

	s.AddTool(mcp.NewTool("enable_app_service_auth",
		mcp.WithDescription("Enable end-user authentication (sign-up, magic-link login, sessions) for a hosted app, backed by one of its attached PostgreSQL services. The platform provisions an identity schema in the customer database and stands up a hosted OIDC issuer. The named attachment must reference a PostgreSQL service. Customer SMTP is required for magic-link delivery; the credentials are stored in the secret store and never returned. Optionally enable social login (Sign in with Google / GitHub) via idp_providers; each provider's client_secret is stored in the secret store and never returned. The issuer domain is fixed at enable time. Asynchronous; poll get_app_service_auth until the configuration status is Active."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be Running)."),
		),
		mcp.WithString("attachment_id",
			mcp.Required(),
			mcp.Description("Attachment UUID of the PostgreSQL service to back the identity store (from the app's attachments list)."),
		),
		mcp.WithString("issuer_domain_choice",
			mcp.Required(),
			mcp.Description("Issuer domain: \"fallback\" for a platform auth-<id>.foundrydb.com subdomain, or \"custom\" to serve the issuer on a custom domain. Fixed at enable time."),
		),
		mcp.WithString("smtp_host",
			mcp.Required(),
			mcp.Description("SMTP server hostname used to send magic-link emails."),
		),
		mcp.WithNumber("smtp_port",
			mcp.Required(),
			mcp.Description("SMTP server port (1-65535)."),
		),
		mcp.WithString("smtp_username",
			mcp.Required(),
			mcp.Description("SMTP username."),
		),
		mcp.WithString("smtp_password",
			mcp.Required(),
			mcp.Description("SMTP password or app token. Write-only; stored in the secret store and never returned."),
		),
		mcp.WithString("smtp_from_address",
			mcp.Required(),
			mcp.Description("From email address for magic-link emails (must contain '@')."),
		),
		mcp.WithString("smtp_from_name",
			mcp.Description("Optional display name for the From header."),
		),
		mcp.WithBoolean("smtp_insecure_skip_verify",
			mcp.Description("Disable STARTTLS certificate verification for the SMTP leg. Defaults to false; only for test mail catchers with self-signed certificates. Never set for production SMTP."),
		),
		mcp.WithString("theme_display_name",
			mcp.Description("Optional product/brand name shown on the hosted login pages."),
		),
		mcp.WithString("theme_brand_color",
			mcp.Description("Optional accent color for the hosted login pages (e.g. #4F46E5)."),
		),
		mcp.WithString("theme_logo_url",
			mcp.Description("Optional logo URL shown on the hosted login pages."),
		),
		mcp.WithString("theme_support_url",
			mcp.Description("Optional support/help URL linked from the hosted login pages."),
		),
		mcp.WithArray("idp_providers",
			mcp.Description("Optional social-login providers (Sign in with Google / GitHub). Each entry needs provider (\"google\" or \"github\"), client_id, and client_secret from an OAuth app the customer registered at that provider; display_name is optional. The client_secret is write-only: it is stored in the secret store and never returned. Omit for magic-link login only. Register the provider's redirect/callback as https://<issuer-host>/callback/<provider>."),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"provider": map[string]any{
						"type":        "string",
						"enum":        []string{foundrydb.AuthIDPProviderGoogle, foundrydb.AuthIDPProviderGitHub},
						"description": "Social provider id: \"google\" or \"github\".",
					},
					"client_id": map[string]any{
						"type":        "string",
						"description": "OAuth client id from the provider.",
					},
					"client_secret": map[string]any{
						"type":        "string",
						"description": "OAuth client secret from the provider. Write-only; stored in the secret store and never returned.",
					},
					"display_name": map[string]any{
						"type":        "string",
						"description": "Optional label for the login button (defaults to the provider name).",
					},
				},
				"required": []string{"provider", "client_id", "client_secret"},
			}),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm enabling auth (provisions the identity schema and OIDC issuer)."),
		),
	), handleEnableAppServiceAuth(c))

	s.AddTool(mcp.NewTool("get_app_service_auth",
		mcp.WithDescription("Get the auth configuration of a hosted app service: status, issuer URL, backing database, schema version, theme, and the JWT signing key records (kid, algorithm, lifecycle status). Reports that auth is not enabled when no configuration exists."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleGetAppServiceAuth(c))

	s.AddTool(mcp.NewTool("disable_app_service_auth",
		mcp.WithDescription("Disable end-user authentication for a hosted app. The platform tears down the OIDC issuer and enablement state. The end-user identity data in the customer's database is left untouched."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm disabling auth."),
		),
	), handleDisableAppServiceAuth(c))

	s.AddTool(mcp.NewTool("rotate_app_service_auth_key",
		mcp.WithDescription("Rotate the JWT signing key for a hosted app's auth. Rotation is dual-kid: the new key is published alongside the outgoing one so tokens signed by the previous key keep validating until it retires. Returns the newly minted key record."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (auth must be enabled)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the key rotation."),
		),
	), handleRotateAppServiceAuthKey(c))

	s.AddTool(mcp.NewTool("revoke_app_service_auth_session",
		mcp.WithDescription("Revoke one end-user session of a hosted app's auth by session id. The revocation is dispatched asynchronously to the backing database's primary VM."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (auth must be enabled)."),
		),
		mcp.WithString("session_id",
			mcp.Required(),
			mcp.Description("End-user session id to revoke."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the session revocation."),
		),
	), handleRevokeAppServiceAuthSession(c))

	s.AddTool(mcp.NewTool("delete_app_service_auth_user",
		mcp.WithDescription("Erase one end-user of a hosted app's auth under the GDPR right to erasure (Art. 17), addressed by exactly one of email or user_id. This permanently deletes the user and their identity data (identities, sessions, refresh tokens, MFA enrolments, pending login/oauth tokens) and scrubs the user's audit-log rows in the backing database. Irreversible: confirm=true is required. The erasure is dispatched asynchronously to the backing database's primary VM and returns a task id."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (auth must be enabled)."),
		),
		mcp.WithString("email",
			mcp.Description("End-user email to erase. Provide exactly one of email or user_id."),
		),
		mcp.WithString("user_id",
			mcp.Description("End-user auth subject UUID to erase. Provide exactly one of email or user_id."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm this irreversible erasure."),
		),
	), handleDeleteAppServiceAuthUser(c))

	s.AddTool(mcp.NewTool("delete_app_service_auth_user_by_identifier",
		mcp.WithDescription("Erase one end-user of a hosted app's auth under the GDPR right to erasure (Art. 17), addressed by a single identifier in the URL path: an email address (contains '@') or a user UUID. This permanently deletes the user and their identity data (identities, sessions, refresh tokens, MFA enrolments, pending login/oauth tokens) and scrubs the user's audit-log rows in the backing database. Irreversible: confirm=true is required. The erasure is dispatched asynchronously to the backing database's primary VM and returns a task id."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (auth must be enabled)."),
		),
		mcp.WithString("identifier",
			mcp.Required(),
			mcp.Description("End-user email address (contains '@') or auth subject UUID to erase."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm this irreversible erasure."),
		),
	), handleDeleteAppServiceAuthUserByIdentifier(c))

	s.AddTool(mcp.NewTool("list_app_service_auth_providers",
		mcp.WithDescription("List the social-login providers (Google, GitHub) configured for a hosted app's auth. Returns each provider's id, client_id, and optional display_name. The client_secret is never returned."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (auth must be enabled)."),
		),
	), handleListAppServiceAuthProviders(c))

	s.AddTool(mcp.NewTool("upsert_app_service_auth_provider",
		mcp.WithDescription("Add or update one social-login provider (Google or GitHub) for a hosted app's auth. The same endpoint is used for both add and update: supplying credentials for an existing provider replaces them. The client_secret is write-only: it is stored in the platform secret store and never returned. When the auth configuration is Active the issuer redeploys automatically to pick up the new credentials."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (auth must be enabled)."),
		),
		mcp.WithString("provider",
			mcp.Required(),
			mcp.Description("Social provider id: \"google\" or \"github\"."),
		),
		mcp.WithString("client_id",
			mcp.Required(),
			mcp.Description("OAuth client id from an OAuth app you registered at the provider."),
		),
		mcp.WithString("client_secret",
			mcp.Required(),
			mcp.Description("OAuth client secret from the provider. Write-only: stored in the platform secret store and never returned."),
		),
		mcp.WithString("display_name",
			mcp.Description("Optional label for the login button (defaults to the provider name when omitted)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the upsert (triggers an issuer redeploy when auth is Active)."),
		),
	), handleUpsertAppServiceAuthProvider(c))

	s.AddTool(mcp.NewTool("remove_app_service_auth_provider",
		mcp.WithDescription("Remove one social-login provider (Google or GitHub) from a hosted app's auth. Returns the remaining configured providers. When the auth configuration is Active the issuer redeploys automatically; a non-Active configuration picks up the change on its next deploy."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (auth must be enabled)."),
		),
		mcp.WithString("provider",
			mcp.Required(),
			mcp.Description("Social provider id to remove: \"google\" or \"github\"."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the removal (triggers an issuer redeploy when auth is Active)."),
		),
	), handleRemoveAppServiceAuthProvider(c))

	s.AddTool(mcp.NewTool("list_attachment_catalog",
		mcp.WithDescription("List the installable companion apps (for example Metabase, Directus) that can be attached to a database service. Each entry names the attachment kind to pass to create_attachment, a default compute plan, and the database engines it supports. Static and read-only."),
	), handleListAttachmentCatalog(c))

	s.AddTool(mcp.NewTool("create_attachment",
		mcp.WithDescription("Attach a companion app from the catalog (see list_attachment_catalog) to a database service. The platform provisions a constrained app service from the catalog descriptor, links it to the database over private SDN, injects connection credentials, and drives it to Running. Supported kinds include metabase, directus, hasura, nocodb, open-webui. Returns the created app service; manage its lifecycle through the app-service tools keyed by its id. Asynchronous; poll get_app_service until Running."),
		mcp.WithString("managed_service_id",
			mcp.Required(),
			mcp.Description("Parent database service UUID to attach the companion app to (must be a database service)."),
		),
		mcp.WithString("kind",
			mcp.Required(),
			mcp.Description("Catalog companion-app kind to provision (from list_attachment_catalog, e.g. \"metabase\", \"directus\", \"hasura\", \"nocodb\", \"open-webui\")."),
		),
		mcp.WithString("plan_name",
			mcp.Description("Compute-only tier for the companion app. Defaults to the catalog descriptor's default plan."),
		),
		mcp.WithString("subdomain",
			mcp.Description("Optional subdomain override: a single DNS label (letters, digits, hyphens; max 40 chars) used as the app name and primary subdomain. Defaults to a generated name."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm provisioning the companion app (provisions a billable VM)."),
		),
	), handleCreateAttachment(c))

	s.AddTool(mcp.NewTool("list_attachments",
		mcp.WithDescription("List the companion apps (from the attachment catalog) attached to a database service. Each entry reports the attachment id, the companion app service id and kind, its status, the attachment wiring status, and its HTTPS URL. Use the app service id with get_app_service to poll provisioning or with the app-service lifecycle tools to manage it."),
		mcp.WithString("managed_service_id",
			mcp.Required(),
			mcp.Description("Parent database service UUID whose attached companion apps to list."),
		),
	), handleListAttachments(c))

	s.AddTool(mcp.NewTool("get_attachment_credentials",
		mcp.WithDescription("Reveal the generated admin login for a catalog attachment's companion app: the admin email and password (for apps such as Metabase whose admin is created by a post-deploy hook) or the reveal-flagged generated values (for apps such as Directus that bootstrap their admin from environment), plus the login URL. The credential is minted once at provisioning, scoped to this companion app, stored encrypted, and decrypted on demand. Only catalog attachments carry credentials; a raw app or one still provisioning returns not-found (poll get_app_service until Running first)."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("Companion app service UUID (the id returned by create_attachment or listed by list_attachments)."),
		),
	), handleGetAttachmentCredentials(c))
}

func handleListAppServiceAuthProviders(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		providers, err := c.ListAppServiceAuthProviders(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(providers) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No social-login providers configured for app service %s.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(providers)), nil
	}
}

func handleUpsertAppServiceAuthProvider(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		provider, _ := args["provider"].(string)
		clientID, _ := args["client_id"].(string)
		clientSecret, _ := args["client_secret"].(string)
		if id == "" || provider == "" {
			return mcp.NewToolResultError("app_service_id and provider are required"), nil
		}
		if provider != foundrydb.AuthIDPProviderGoogle && provider != foundrydb.AuthIDPProviderGitHub {
			return mcp.NewToolResultError(fmt.Sprintf("provider must be %q or %q", foundrydb.AuthIDPProviderGoogle, foundrydb.AuthIDPProviderGitHub)), nil
		}
		if clientID == "" {
			return mcp.NewToolResultError("client_id is required"), nil
		}
		if clientSecret == "" {
			return mcp.NewToolResultError("client_secret is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("upserting auth provider %q for app service %s", provider, id)); denied != nil {
			return denied, nil
		}
		upsertReq := foundrydb.UpsertAppServiceAuthProviderRequest{
			ClientID:     clientID,
			ClientSecret: clientSecret,
		}
		if v, ok := args["display_name"].(string); ok && v != "" {
			upsertReq.DisplayName = v
		}
		providers, err := c.UpsertAppServiceAuthProvider(ctx, id, provider, upsertReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(providers)), nil
	}
}

func handleRemoveAppServiceAuthProvider(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		provider, _ := args["provider"].(string)
		if id == "" || provider == "" {
			return mcp.NewToolResultError("app_service_id and provider are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("removing auth provider %q from app service %s", provider, id)); denied != nil {
			return denied, nil
		}
		providers, err := c.RemoveAppServiceAuthProvider(ctx, id, provider)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(providers)), nil
	}
}

func handleListAttachmentCatalog(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		catalog, err := c.GetAttachmentCatalog(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(catalog) == 0 {
			return mcp.NewToolResultText("No attachment kinds available."), nil
		}
		return mcp.NewToolResultText(formatJSON(catalog)), nil
	}
}

func handleCreateAttachment(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["managed_service_id"].(string)
		kind, _ := args["kind"].(string)
		if serviceID == "" || kind == "" {
			return mcp.NewToolResultError("managed_service_id and kind are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("attaching companion app %q to database service %s", kind, serviceID)); denied != nil {
			return denied, nil
		}
		createReq := foundrydb.CreateAttachmentRequest{Kind: kind}
		if plan, ok := args["plan_name"].(string); ok && plan != "" {
			createReq.PlanName = plan
		}
		if subdomain, ok := args["subdomain"].(string); ok && subdomain != "" {
			createReq.Subdomain = subdomain
		}
		app, err := c.CreateAttachment(ctx, serviceID, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(app)), nil
	}
}

func handleListAttachments(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["managed_service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("managed_service_id is required"), nil
		}
		attachments, err := c.ListAttachments(ctx, serviceID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(attachments) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No companion apps attached to database service %s.", serviceID)), nil
		}
		return mcp.NewToolResultText(formatJSON(attachments)), nil
	}
}

func handleGetAttachmentCredentials(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		creds, err := c.GetAttachmentCredentials(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(creds)), nil
	}
}

func handleEnableAppServiceAuth(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		attachmentID, _ := args["attachment_id"].(string)
		issuerChoice, _ := args["issuer_domain_choice"].(string)
		smtpHost, _ := args["smtp_host"].(string)
		smtpPort, _ := args["smtp_port"].(float64)
		smtpUsername, _ := args["smtp_username"].(string)
		smtpPassword, _ := args["smtp_password"].(string)
		smtpFromAddress, _ := args["smtp_from_address"].(string)

		if id == "" || attachmentID == "" {
			return mcp.NewToolResultError("app_service_id and attachment_id are required"), nil
		}
		if issuerChoice != foundrydb.AuthIssuerDomainFallback && issuerChoice != foundrydb.AuthIssuerDomainCustom {
			return mcp.NewToolResultError(fmt.Sprintf("issuer_domain_choice must be %q or %q", foundrydb.AuthIssuerDomainFallback, foundrydb.AuthIssuerDomainCustom)), nil
		}
		if smtpHost == "" || smtpUsername == "" || smtpPassword == "" || smtpFromAddress == "" {
			return mcp.NewToolResultError("smtp_host, smtp_username, smtp_password and smtp_from_address are required: auth requires customer-supplied SMTP for magic-link delivery"), nil
		}
		if smtpPort < 1 || smtpPort > 65535 {
			return mcp.NewToolResultError("smtp_port must be between 1 and 65535"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("enabling auth for app service %s (backing attachment %s)", id, attachmentID)); denied != nil {
			return denied, nil
		}

		enableReq := foundrydb.AuthEnableRequest{
			AttachmentID:       attachmentID,
			IssuerDomainChoice: issuerChoice,
			SMTP: foundrydb.AuthSMTPConfig{
				Host:        smtpHost,
				Port:        int(smtpPort),
				Username:    smtpUsername,
				Password:    smtpPassword,
				FromAddress: smtpFromAddress,
			},
		}
		if v, ok := args["smtp_from_name"].(string); ok && v != "" {
			enableReq.SMTP.FromName = v
		}
		if v, ok := args["smtp_insecure_skip_verify"].(bool); ok {
			enableReq.SMTP.InsecureSkipVerify = v
		}
		if v, ok := args["theme_display_name"].(string); ok && v != "" {
			enableReq.Theme.DisplayName = v
		}
		if v, ok := args["theme_brand_color"].(string); ok && v != "" {
			enableReq.Theme.BrandColor = v
		}
		if v, ok := args["theme_logo_url"].(string); ok && v != "" {
			enableReq.Theme.LogoURL = v
		}
		if v, ok := args["theme_support_url"].(string); ok && v != "" {
			enableReq.Theme.SupportURL = v
		}

		if raw, ok := args["idp_providers"].([]interface{}); ok && len(raw) > 0 {
			providers, errMsg := parseIDPProviders(raw)
			if errMsg != "" {
				return mcp.NewToolResultError(errMsg), nil
			}
			enableReq.IDPProviders = providers
		}

		cfg, err := c.EnableAppServiceAuth(ctx, id, enableReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(cfg)), nil
	}
}

func handleGetAppServiceAuth(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		auth, err := c.GetAppServiceAuth(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if auth == nil {
			return mcp.NewToolResultText(fmt.Sprintf("Auth is not enabled for app service %s.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(auth)), nil
	}
}

func handleDisableAppServiceAuth(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("disabling auth for app service %s", id)); denied != nil {
			return denied, nil
		}
		if err := c.DisableAppServiceAuth(ctx, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Auth disabled for app service %s.", id)), nil
	}
}

func handleRotateAppServiceAuthKey(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("rotating the auth signing key for app service %s", id)); denied != nil {
			return denied, nil
		}
		key, err := c.RotateAppServiceAuthKey(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(key)), nil
	}
}

func handleRevokeAppServiceAuthSession(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		sessionID, _ := args["session_id"].(string)
		if id == "" || sessionID == "" {
			return mcp.NewToolResultError("app_service_id and session_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("revoking auth session %s for app service %s", sessionID, id)); denied != nil {
			return denied, nil
		}
		if err := c.RevokeAppServiceAuthSession(ctx, id, sessionID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Auth session %s revocation requested for app service %s.", sessionID, id)), nil
	}
}

func handleDeleteAppServiceAuthUser(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		email, _ := args["email"].(string)
		userID, _ := args["user_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		if (email == "") == (userID == "") {
			return mcp.NewToolResultError("provide exactly one of email or user_id"), nil
		}
		// The confirmation message names the addressing mode, not the email, so
		// the end-user email is not echoed back through the tool surface.
		subject := "user_id " + userID
		if email != "" {
			subject = "the requested email"
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("erasing auth end-user (%s) for app service %s under the GDPR right to erasure", subject, id)); denied != nil {
			return denied, nil
		}
		taskID, err := c.DeleteAppServiceAuthUser(ctx, id, foundrydb.DeleteAppServiceAuthUserRequest{Email: email, UserID: userID})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Auth end-user erasure requested for app service %s (task %s).", id, taskID)), nil
	}
}

func handleDeleteAppServiceAuthUserByIdentifier(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		identifier, _ := args["identifier"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		if identifier == "" {
			return mcp.NewToolResultError("identifier is required (email address or user UUID)"), nil
		}
		// The confirmation message describes addressing mode without echoing the
		// identifier value, keeping the end-user email off the tool surface.
		mode := "user_id"
		if strings.Contains(identifier, "@") {
			mode = "email"
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("erasing auth end-user (%s) for app service %s under the GDPR right to erasure", mode, id)); denied != nil {
			return denied, nil
		}
		taskID, err := c.DeleteAppServiceAuthUserByIdentifier(ctx, id, identifier)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Auth end-user erasure requested for app service %s (task %s).", id, taskID)), nil
	}
}

func handleListAppServices(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		apps, err := c.ListAppServices(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(apps) == 0 {
			return mcp.NewToolResultText("No app services found."), nil
		}
		return mcp.NewToolResultText(formatJSON(apps)), nil
	}
}

func handleGetAppService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		app, err := c.GetAppService(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if app == nil {
			return mcp.NewToolResultText(fmt.Sprintf("App service %s not found.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(app)), nil
	}
}

func handleCreateAppService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)
		imageRef, _ := args["image_ref"].(string)
		port, _ := args["container_port"].(float64)

		if name == "" || imageRef == "" || port == 0 {
			return mcp.NewToolResultError("name, image_ref and container_port are required"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("creating app service %q (image %s)", name, imageRef)); denied != nil {
			return denied, nil
		}

		createReq := foundrydb.CreateAppServiceRequest{
			Name: name,
			AppConfig: foundrydb.AppContainerConfig{
				ImageRef:      imageRef,
				ContainerPort: int(port),
			},
		}
		if plan, ok := args["plan_name"].(string); ok && plan != "" {
			createReq.PlanName = plan
		} else {
			createReq.PlanName = "tier-2"
		}
		if zone, ok := args["zone"].(string); ok && zone != "" {
			createReq.Zone = zone
		}
		if att, ok := args["attached_service_id"].(string); ok && att != "" {
			createReq.AttachedServiceIDs = []string{att}
		}
		if env, ok := args["env"].(string); ok && env != "" {
			createReq.AppConfig.Env = parseEnvPairs(env)
		}
		if cd, ok := args["custom_domains"].(string); ok && cd != "" {
			createReq.AppConfig.CustomDomains = parseCSVList(cd)
		}
		if ru, ok := args["registry_username"].(string); ok && ru != "" {
			createReq.AppConfig.RegistryUsername = ru
		}
		if rp, ok := args["registry_password"].(string); ok && rp != "" {
			createReq.AppConfig.RegistryPassword = rp
		}
		if sz, ok := args["storage_size_gb"].(float64); ok && sz > 0 {
			createReq.StorageSizeGB = int(sz)
		}
		applyHealthCheckArgs(args, &createReq.AppConfig)

		app, err := c.CreateAppService(ctx, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(app)), nil
	}
}

func handleRedeployAppService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		imageRef, _ := args["image_ref"].(string)
		port, _ := args["container_port"].(float64)
		if id == "" || imageRef == "" || port == 0 {
			return mcp.NewToolResultError("app_service_id, image_ref and container_port are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("redeploying app service %s to image %s", id, imageRef)); denied != nil {
			return denied, nil
		}
		updateReq := foundrydb.UpdateAppServiceRequest{
			AppConfig: foundrydb.AppContainerConfig{
				ImageRef:      imageRef,
				ContainerPort: int(port),
			},
		}
		if env, ok := args["env"].(string); ok && env != "" {
			updateReq.AppConfig.Env = parseEnvPairs(env)
		}
		if cd, ok := args["custom_domains"].(string); ok && cd != "" {
			updateReq.AppConfig.CustomDomains = parseCSVList(cd)
		}
		if ru, ok := args["registry_username"].(string); ok && ru != "" {
			updateReq.AppConfig.RegistryUsername = ru
		}
		if rp, ok := args["registry_password"].(string); ok && rp != "" {
			updateReq.AppConfig.RegistryPassword = rp
		}
		applyHealthCheckArgs(args, &updateReq.AppConfig)
		app, err := c.UpdateAppService(ctx, id, updateReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(app)), nil
	}
}

func handleAttachAppDatabase(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		attachedID, _ := args["attached_service_id"].(string)
		if id == "" || attachedID == "" {
			return mcp.NewToolResultError("app_service_id and attached_service_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("attaching service %s to app service %s", attachedID, id)); denied != nil {
			return denied, nil
		}
		prefix, _ := args["prefix"].(string)
		permission, _ := args["permission"].(string)
		wiringIntent, _ := args["wiring_intent"].(string)
		var opts *foundrydb.AttachOptions
		if prefix != "" || permission != "" || wiringIntent != "" {
			opts = &foundrydb.AttachOptions{Prefix: prefix, Permission: permission, WiringIntent: wiringIntent}
		}
		app, err := c.AttachServiceWithOptions(ctx, id, attachedID, opts)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(app)), nil
	}
}

func handleDetachAppDatabase(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		attachmentID, _ := args["attachment_id"].(string)
		if id == "" || attachmentID == "" {
			return mcp.NewToolResultError("app_service_id and attachment_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("detaching attachment %s from app service %s", attachmentID, id)); denied != nil {
			return denied, nil
		}
		app, err := c.DetachDatabase(ctx, id, attachmentID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(app)), nil
	}
}

func handleScaleAppService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		planName, _ := args["plan_name"].(string)
		if id == "" || planName == "" {
			return mcp.NewToolResultError("app_service_id and plan_name are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("scaling app service %s to plan %s", id, planName)); denied != nil {
			return denied, nil
		}
		app, err := c.ScaleAppService(ctx, id, planName)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(app)), nil
	}
}

func handleListAppDeployments(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		deployments, err := c.ListAppDeployments(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(deployments) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No deployments found for app service %s.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(deployments)), nil
	}
}

func handleRollbackAppService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		deploymentID, _ := args["deployment_id"].(string)
		if id == "" || deploymentID == "" {
			return mcp.NewToolResultError("app_service_id and deployment_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("rolling back app service %s to deployment %s", id, deploymentID)); denied != nil {
			return denied, nil
		}
		app, err := c.RollbackAppService(ctx, id, deploymentID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(app)), nil
	}
}

func handleRestartAppService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("restarting app service %s", id)); denied != nil {
			return denied, nil
		}
		if err := c.RestartAppService(ctx, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("App service %s restart requested.", id)), nil
	}
}

func handleDeleteAppService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["app_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting app service %s", id)); denied != nil {
			return denied, nil
		}
		if err := c.DeleteAppService(ctx, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("App service %s deletion scheduled.", id)), nil
	}
}

// applyHealthCheckArgs copies the optional health_check_* tool arguments onto an
// app container config. Zero or absent values are left unset so the platform
// keeps its defaults.
func applyHealthCheckArgs(args map[string]interface{}, cfg *foundrydb.AppContainerConfig) {
	if p, ok := args["health_check_path"].(string); ok && p != "" {
		cfg.HealthCheckPath = p
	}
	if v, ok := args["health_check_interval_seconds"].(float64); ok && v > 0 {
		cfg.HealthCheckIntervalSeconds = int(v)
	}
	if v, ok := args["health_check_timeout_seconds"].(float64); ok && v > 0 {
		cfg.HealthCheckTimeoutSeconds = int(v)
	}
	if v, ok := args["health_check_healthy_threshold"].(float64); ok && v > 0 {
		cfg.HealthCheckHealthyThreshold = int(v)
	}
}

// parseIDPProviders converts the idp_providers tool argument into the SDK's
// social-provider request slice. Each entry must name a supported provider
// (google or github) and carry both a client_id and a client_secret; a missing
// secret disables the provider, so it is rejected rather than passed through.
// On any problem it returns a non-empty error message and a nil slice.
func parseIDPProviders(raw []interface{}) ([]foundrydb.AuthIDPProviderRequest, string) {
	providers := make([]foundrydb.AuthIDPProviderRequest, 0, len(raw))
	seen := map[string]struct{}{}
	for i, entry := range raw {
		obj, ok := entry.(map[string]interface{})
		if !ok {
			return nil, fmt.Sprintf("idp_providers[%d] must be an object with provider, client_id and client_secret", i)
		}
		provider, _ := obj["provider"].(string)
		clientID, _ := obj["client_id"].(string)
		clientSecret, _ := obj["client_secret"].(string)
		displayName, _ := obj["display_name"].(string)

		if provider != foundrydb.AuthIDPProviderGoogle && provider != foundrydb.AuthIDPProviderGitHub {
			return nil, fmt.Sprintf("idp_providers[%d].provider must be %q or %q", i, foundrydb.AuthIDPProviderGoogle, foundrydb.AuthIDPProviderGitHub)
		}
		if _, dup := seen[provider]; dup {
			return nil, fmt.Sprintf("idp_providers lists provider %q more than once", provider)
		}
		seen[provider] = struct{}{}
		if clientID == "" {
			return nil, fmt.Sprintf("idp_providers[%d].client_id is required for provider %q", i, provider)
		}
		if clientSecret == "" {
			return nil, fmt.Sprintf("idp_providers[%d].client_secret is required for provider %q", i, provider)
		}
		providers = append(providers, foundrydb.AuthIDPProviderRequest{
			Provider:     provider,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			DisplayName:  displayName,
		})
	}
	return providers, ""
}

// parseCSVList splits a comma-separated argument into a trimmed, non-empty
// slice (used for custom_domains). Returns nil when empty.
func parseCSVList(s string) []string {
	items := splitAndTrim(s)
	if len(items) == 0 {
		return nil
	}
	return items
}

func parseEnvPairs(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range splitAndTrim(s) {
		for i := 0; i < len(pair); i++ {
			if pair[i] == '=' {
				if k := pair[:i]; k != "" {
					out[k] = pair[i+1:]
				}
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
