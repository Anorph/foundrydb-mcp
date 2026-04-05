package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterServiceTools registers all service lifecycle tools on the MCP server.
// cfg is the base configuration used to create org-scoped clients on demand.
func RegisterServiceTools(s *server.MCPServer, c *foundrydb.Client, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("list_organizations",
		mcp.WithDescription("List all organizations the authenticated user belongs to. Use the returned org ID with create_service to provision services in a specific organization."),
	), handleListOrganizations(c))

	s.AddTool(mcp.NewTool("list_services",
		mcp.WithDescription("List all managed database services with their status, type, zone, and plan"),
	), handleListServices(c))

	s.AddTool(mcp.NewTool("get_service",
		mcp.WithDescription("Get full details of a specific database service. Provide either 'id' (UUID) or 'name'."),
		mcp.WithString("id",
			mcp.Description("Service UUID. Use either id or name, not both."),
		),
		mcp.WithString("name",
			mcp.Description("Service name. Use either id or name, not both."),
		),
	), handleGetService(c))

	s.AddTool(mcp.NewTool("create_service",
		mcp.WithDescription("Provision a new managed database service. The service will start provisioning immediately and reach Running status in 5-15 minutes."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Service name (lowercase letters, numbers, hyphens)"),
		),
		mcp.WithString("database_type",
			mcp.Required(),
			mcp.Description("Database engine: postgresql (versions: 14, 15, 16, 17), mysql (versions: 8.0, 8.4), mongodb (versions: 6.0, 7.0, 8.0), valkey (versions: 7.2, 8.0, 8.1), kafka (versions: 3.7, 3.8, 3.9), opensearch: 2), mssql (version: 4.8 - Babelfish/SQL Server compatible)"),
		),
		mcp.WithString("version",
			mcp.Description("Database version. postgresql: 17, mysql: 8.4, mongodb: 8.0, valkey: 8.1, kafka: 3.9, opensearch: 2, mssql: 4.8. Omit to use the default."),
		),
		mcp.WithString("plan_name",
			mcp.Required(),
			mcp.Description("Compute plan: tier-1 (1 CPU, 2GB) through tier-15. Use tier-2 for development, tier-4 for production."),
		),
		mcp.WithString("zone",
			mcp.Description("UpCloud zone. Default: se-sto1 (Stockholm). Options: se-sto1, fi-hel1, nl-ams1, de-fra1, us-nyc1."),
		),
		mcp.WithNumber("storage_size_gb",
			mcp.Description("Data disk size in GB (10-16384). Required for compute-only plans. Recommended: 50 for dev, 100+ for production."),
		),
		mcp.WithString("storage_tier",
			mcp.Description("Storage performance tier: 'standard' (HDD) or 'maxiops' (NVMe SSD). Default: maxiops."),
		),
		mcp.WithString("organization_id",
			mcp.Description("UUID of the organization to create the service in. If omitted, uses the personal org. Use list_organizations to find org IDs."),
		),
		mcp.WithNumber("node_count",
			mcp.Description("Number of nodes in the cluster (e.g. 1 for single-node, 3 for HA). Omit to use the default for the database type."),
		),
		mcp.WithString("replication_mode",
			mcp.Description("Replication mode for multi-node clusters (e.g. 'async', 'sync'). Omit to use the default."),
		),
		mcp.WithString("preset",
			mcp.Description("Service preset for AI agent workloads. Options: agent-postgresql-structured, agent-postgresql-rag, agent-mongodb-conversation, agent-valkey-session, agent-kafka-events. When set, fills in default database_type, version, plan, storage, and TTL. You can override any default."),
		),
		mcp.WithNumber("ttl_hours",
			mcp.Description("Auto-delete service after N hours (1-720). Useful for ephemeral agent databases."),
		),
		mcp.WithBoolean("is_ephemeral",
			mcp.Description("Mark service as ephemeral. Ephemeral services are intended for temporary agent workloads."),
		),
		mcp.WithString("agent_framework",
			mcp.Description("AI framework that created this service: langchain, crewai, autogen, claude."),
		),
		mcp.WithString("agent_purpose",
			mcp.Description("Purpose of this database: conversation_history, session_cache, structured_data, rag, event_stream."),
		),
	), handleCreateService(cfg))

	s.AddTool(mcp.NewTool("list_presets",
		mcp.WithDescription("List available service presets for AI agent workloads. Each preset bundles database type, version, plan, storage, config template, and TTL into a single name for one-call service creation."),
	), handleListPresets(c))

	s.AddTool(mcp.NewTool("delete_service",
		mcp.WithDescription("Permanently delete a managed database service and all its data. This action cannot be undone."),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("Service UUID to delete"),
		),
	), handleDeleteService(c))

	s.AddTool(mcp.NewTool("get_service_nodes",
		mcp.WithDescription("List all nodes (primary and replicas) for a managed database service, including their role, status, and VM details."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Service UUID"),
		),
	), handleGetServiceNodes(c))
}

func handleListOrganizations(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		orgs, err := c.ListOrganizations(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(orgs) == 0 {
			return mcp.NewToolResultText("No organizations found."), nil
		}
		return mcp.NewToolResultText(formatJSON(orgs)), nil
	}
}

func handleListServices(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		services, err := c.ListServices(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(services) == 0 {
			return mcp.NewToolResultText("No services found."), nil
		}
		return mcp.NewToolResultText(formatJSON(services)), nil
	}
}

func handleGetService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["id"].(string)
		name, _ := args["name"].(string)

		if id == "" && name == "" {
			return mcp.NewToolResultError("provide either 'id' or 'name'"), nil
		}

		var svc *foundrydb.Service
		var err error

		if id != "" {
			svc, err = c.GetService(ctx, id)
		} else {
			svc, err = getServiceByName(ctx, c, name)
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if svc == nil {
			return mcp.NewToolResultError("service not found"), nil
		}
		return mcp.NewToolResultText(formatJSON(svc)), nil
	}
}

// getServiceByName finds a service by its name by listing all services.
func getServiceByName(ctx context.Context, c *foundrydb.Client, name string) (*foundrydb.Service, error) {
	services, err := c.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	for i := range services {
		if services[i].Name == name {
			return &services[i], nil
		}
	}
	return nil, fmt.Errorf("service with name %q not found", name)
}

// handleCreateService uses cfg to allow creating org-scoped clients when organization_id is provided.
func handleCreateService(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)
		dbType, _ := args["database_type"].(string)
		planName, _ := args["plan_name"].(string)

		if name == "" || dbType == "" || planName == "" {
			return mcp.NewToolResultError("name, database_type and plan_name are required"), nil
		}

		createReq := foundrydb.CreateServiceRequest{
			Name:         name,
			DatabaseType: foundrydb.DatabaseType(dbType),
			PlanName:     planName,
		}

		if v, ok := args["version"].(string); ok && v != "" {
			createReq.Version = v
		}
		if z, ok := args["zone"].(string); ok && z != "" {
			createReq.Zone = z
		}
		if gb, ok := args["storage_size_gb"].(float64); ok && gb > 0 {
			n := int(gb)
			createReq.StorageSizeGB = &n
		}
		if tier, ok := args["storage_tier"].(string); ok && tier != "" {
			createReq.StorageTier = tier
		}
		if nc, ok := args["node_count"].(float64); ok && nc > 0 {
			n := int(nc)
			createReq.NodeCount = &n
		}
		if rm, ok := args["replication_mode"].(string); ok && rm != "" {
			createReq.ReplicationMode = foundrydb.ReplicationMode(rm)
		}
		if preset, ok := args["preset"].(string); ok && preset != "" {
			createReq.Preset = preset
		}
		if ttl, ok := args["ttl_hours"].(float64); ok && ttl > 0 {
			n := int(ttl)
			createReq.TTLHours = &n
		}
		if eph, ok := args["is_ephemeral"].(bool); ok {
			createReq.IsEphemeral = &eph
		}
		if fw, ok := args["agent_framework"].(string); ok && fw != "" {
			createReq.AgentFramework = fw
		}
		if purpose, ok := args["agent_purpose"].(string); ok && purpose != "" {
			createReq.AgentPurpose = purpose
		}

		// When organization_id is provided, scope the client to that org.
		clientCfg := cfg
		if orgID, ok := args["organization_id"].(string); ok && orgID != "" {
			clientCfg.OrgID = orgID
		}
		apiClient := foundrydb.New(clientCfg)

		svc, err := apiClient.CreateService(ctx, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			"Service created successfully.\nID: %s\nStatus: %s\nThe service will reach Running status in 5-15 minutes.\n\nFull details:\n%s",
			svc.ID, svc.Status, formatJSON(svc),
		)), nil
	}
}

func handleListPresets(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		data, err := c.ListPresets(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleDeleteService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := req.GetArguments()["id"].(string)
		if id == "" {
			return mcp.NewToolResultError("id is required"), nil
		}
		if err := c.DeleteService(ctx, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Service %s deletion initiated. The service and all its data will be permanently removed.", id)), nil
	}
}

func handleGetServiceNodes(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		serviceID, _ := req.GetArguments()["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		// Node data is embedded in the main service detail response.
		svc, err := c.GetService(ctx, serviceID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if svc == nil {
			return mcp.NewToolResultError("service not found"), nil
		}
		return mcp.NewToolResultText(formatJSON(svc)), nil
	}
}

// formatJSON pretty-prints a value as JSON for tool result output.
func formatJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
