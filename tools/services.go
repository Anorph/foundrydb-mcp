package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/anorph/foundrydb-mcp/client"
)

// RegisterServiceTools registers all service lifecycle tools on the MCP server.
func RegisterServiceTools(s *server.MCPServer, c *client.Client) {
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
			mcp.Description("Database engine: postgresql, mysql, mongodb, valkey, kafka, opensearch, mssql"),
		),
		mcp.WithString("version",
			mcp.Description("Database version, e.g. '17' for PostgreSQL 17, '8.4' for MySQL 8.4. Omit to use the default."),
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
	), handleCreateService(c))

	s.AddTool(mcp.NewTool("delete_service",
		mcp.WithDescription("Permanently delete a managed database service and all its data. This action cannot be undone."),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("Service UUID to delete"),
		),
	), handleDeleteService(c))
}

func handleListServices(c *client.Client) server.ToolHandlerFunc {
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

func handleGetService(c *client.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["id"].(string)
		name, _ := args["name"].(string)

		if id == "" && name == "" {
			return mcp.NewToolResultError("provide either 'id' or 'name'"), nil
		}

		var svc map[string]interface{}
		var err error

		if id != "" {
			svc, err = c.GetService(ctx, id)
		} else {
			svc, err = c.GetServiceByName(ctx, name)
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(svc)), nil
	}
}

func handleCreateService(c *client.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)
		dbType, _ := args["database_type"].(string)
		planName, _ := args["plan_name"].(string)

		if name == "" || dbType == "" || planName == "" {
			return mcp.NewToolResultError("name, database_type and plan_name are required"), nil
		}

		createReq := client.CreateServiceRequest{
			Name:         name,
			DatabaseType: dbType,
			PlanName:     planName,
		}

		if v, ok := args["version"].(string); ok && v != "" {
			createReq.Version = &v
		}
		if z, ok := args["zone"].(string); ok && z != "" {
			createReq.Zone = &z
		}
		if gb, ok := args["storage_size_gb"].(float64); ok && gb > 0 {
			n := int(gb)
			createReq.StorageSizeGB = &n
		}
		if tier, ok := args["storage_tier"].(string); ok && tier != "" {
			createReq.StorageTier = &tier
		}

		svc, err := c.CreateService(ctx, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		id, _ := svc["id"].(string)
		status, _ := svc["status"].(string)
		return mcp.NewToolResultText(fmt.Sprintf(
			"Service created successfully.\nID: %s\nStatus: %s\nThe service will reach Running status in 5-15 minutes.\n\nFull details:\n%s",
			id, status, formatJSON(svc),
		)), nil
	}
}

func handleDeleteService(c *client.Client) server.ToolHandlerFunc {
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

// formatJSON pretty-prints a value as JSON for tool result output.
func formatJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
