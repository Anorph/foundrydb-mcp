package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// splitAndTrim splits a comma-separated string into non-empty trimmed parts.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// RegisterDataPipelineTools registers tools for data pipelines: CDC data flows
// wired between two managed services (Phase 1: PostgreSQL CDC into Kafka).
func RegisterDataPipelineTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("list_data_pipelines",
		mcp.WithDescription("List data pipelines (CDC flows between services) in an organization. Use list_organizations to find org IDs."),
		mcp.WithString("organization_id",
			mcp.Required(),
			mcp.Description("Organization UUID that owns the pipelines."),
		),
	), handleListDataPipelines(c))

	s.AddTool(mcp.NewTool("get_data_pipeline_status",
		mcp.WithDescription("Get the status of a data pipeline: lifecycle state, Debezium connector state, per-task states, and source replication lag."),
		mcp.WithString("pipeline_id",
			mcp.Required(),
			mcp.Description("Data pipeline UUID."),
		),
	), handleGetDataPipelineStatus(c))

	s.AddTool(mcp.NewTool("create_data_pipeline",
		mcp.WithDescription("Create a data pipeline that streams change-data-capture events from a PostgreSQL source service into Kafka topics on a sink service. The sink Kafka service must have the kafka-connect addon enabled. Provisioning is asynchronous (SDN peering, Debezium plugin install, publication, connector); poll get_data_pipeline_status until Running."),
		mcp.WithString("organization_id",
			mcp.Required(),
			mcp.Description("Organization UUID that owns both services and the pipeline."),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Pipeline name (letters, digits, hyphens, underscores), unique within the organization."),
		),
		mcp.WithString("source_service_id",
			mcp.Required(),
			mcp.Description("PostgreSQL source service UUID (must be Running)."),
		),
		mcp.WithString("sink_service_id",
			mcp.Required(),
			mcp.Description("Kafka sink service UUID (must be Running with the kafka-connect addon; must differ from source)."),
		),
		mcp.WithString("database_name",
			mcp.Description("Source logical database to capture. Defaults to defaultdb."),
		),
		mcp.WithString("tables",
			mcp.Description("Comma-separated tables to capture (schema.table or table). Omit to capture all tables."),
		),
		mcp.WithString("topic_prefix",
			mcp.Description("Kafka topic prefix; topics are <prefix>.<schema>.<table>. Defaults to the pipeline name."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleCreateDataPipeline(c))

	s.AddTool(mcp.NewTool("delete_data_pipeline",
		mcp.WithDescription("Delete a data pipeline. Schedules asynchronous teardown: removes the Debezium connector, drops the replication slot and publication on the source, and removes the pipeline's database access entry."),
		mcp.WithString("organization_id",
			mcp.Required(),
			mcp.Description("Organization UUID that owns the pipeline."),
		),
		mcp.WithString("pipeline_id",
			mcp.Required(),
			mcp.Description("Data pipeline UUID."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleDeleteDataPipeline(c))
}

func handleListDataPipelines(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("organization_id is required"), nil
		}
		pipelines, err := c.ListDataPipelines(ctx, orgID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(pipelines) == 0 {
			return mcp.NewToolResultText("No data pipelines found in this organization."), nil
		}
		return mcp.NewToolResultText(formatJSON(pipelines)), nil
	}
}

func handleGetDataPipelineStatus(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		pipelineID, _ := args["pipeline_id"].(string)
		if pipelineID == "" {
			return mcp.NewToolResultError("pipeline_id is required"), nil
		}
		status, err := c.GetDataPipelineStatus(ctx, pipelineID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if status == nil {
			return mcp.NewToolResultText("Pipeline not found."), nil
		}
		return mcp.NewToolResultText(formatJSON(status)), nil
	}
}

func handleCreateDataPipeline(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		name, _ := args["name"].(string)
		sourceID, _ := args["source_service_id"].(string)
		sinkID, _ := args["sink_service_id"].(string)

		if orgID == "" || name == "" || sourceID == "" || sinkID == "" {
			return mcp.NewToolResultError("organization_id, name, source_service_id and sink_service_id are required"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("creating data pipeline %q (PostgreSQL %s -> Kafka %s)", name, sourceID, sinkID)); denied != nil {
			return denied, nil
		}

		createReq := foundrydb.CreateDataPipelineRequest{
			Name:            name,
			PipelineType:    foundrydb.DataPipelineTypeCDCPGToKafka,
			SourceServiceID: sourceID,
			SinkServiceID:   sinkID,
		}
		if db, ok := args["database_name"].(string); ok && db != "" {
			createReq.Config.DatabaseName = db
		}
		if tp, ok := args["topic_prefix"].(string); ok && tp != "" {
			createReq.Config.TopicPrefix = tp
		}
		if tbls, ok := args["tables"].(string); ok && tbls != "" {
			createReq.Config.Tables = splitAndTrim(tbls)
		}

		pipeline, err := c.CreateDataPipeline(ctx, orgID, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(pipeline)), nil
	}
}

func handleDeleteDataPipeline(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		pipelineID, _ := args["pipeline_id"].(string)
		if orgID == "" || pipelineID == "" {
			return mcp.NewToolResultError("organization_id and pipeline_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting data pipeline %s", pipelineID)); denied != nil {
			return denied, nil
		}
		if err := c.DeleteDataPipeline(ctx, orgID, pipelineID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Data pipeline %s deletion scheduled.", pipelineID)), nil
	}
}
