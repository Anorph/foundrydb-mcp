package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterAIDataTools registers the AI data surface: embedding pipelines and
// their runs, vector search over the read-only data plane, and the inference
// proxy's keys and usage.
//
// Provider-config CRUD (the org's raw OpenAI/Anthropic/Mistral/Azure API
// keys) is deliberately not exposed via MCP: passing raw provider keys
// through an agent transcript is avoidable risk, so those are UI/API only.
func RegisterAIDataTools(s *server.MCPServer, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("list_embedding_pipelines",
		mcp.WithDescription("List the embedding pipelines (managed auto-vectorization jobs) configured on a PostgreSQL service, including mode, schedule, status, and row/token counters."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Managed service UUID (PostgreSQL with pgvector)."),
		),
	), handleListEmbeddingPipelines(cfg))

	s.AddTool(mcp.NewTool("get_embedding_pipeline_runs",
		mcp.WithDescription("Get the run history of an embedding pipeline (scheduled or manual mode), newest first: status, trigger, row counters, tokens used, and per-row error samples. Pass run_id to fetch a single run."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Managed service UUID."),
		),
		mcp.WithString("pipeline_id",
			mcp.Required(),
			mcp.Description("Embedding pipeline UUID."),
		),
		mcp.WithString("run_id",
			mcp.Description("Optional run UUID to fetch one specific run instead of the list."),
		),
	), handleGetEmbeddingPipelineRuns(cfg))

	s.AddTool(mcp.NewTool("create_embedding_pipeline",
		mcp.WithDescription("Create an embedding pipeline on a PostgreSQL service with pgvector: the platform watches the source table, embeds the text columns through the customer's own provider key, and writes vectors into an indexed companion table. Setup is asynchronous; the pipeline starts in the configuring status and becomes active."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Managed service UUID (PostgreSQL with pgvector)."),
		),
		mcp.WithString("database_name",
			mcp.Required(),
			mcp.Description("Database containing the source table."),
		),
		mcp.WithString("source_table",
			mcp.Required(),
			mcp.Description("Source table to vectorize."),
		),
		mcp.WithString("source_schema",
			mcp.Description("Source schema (default public)."),
		),
		mcp.WithString("text_columns",
			mcp.Required(),
			mcp.Description("Comma-separated text columns to embed (concatenated per row), e.g. title,body."),
		),
		mcp.WithString("model_provider",
			mcp.Required(),
			mcp.Description("Embedding provider: openai, cohere, or custom."),
		),
		mcp.WithString("embedding_model",
			mcp.Required(),
			mcp.Description("Embedding model name, e.g. text-embedding-3-small."),
		),
		mcp.WithNumber("model_dimensions",
			mcp.Required(),
			mcp.Description("Vector dimensions the model produces, e.g. 1536."),
		),
		mcp.WithString("provider_api_key",
			mcp.Required(),
			mcp.Description("Provider API key used to call the embedding model. SENSITIVE: this secret passes through the conversation transcript; prefer a key with minimal scope and rotate it if the transcript is shared. Stored encrypted at rest and never returned by the API."),
		),
		mcp.WithString("provider_base_url",
			mcp.Description("Optional provider base URL (required for the custom provider)."),
		),
		mcp.WithString("target_table",
			mcp.Description("Optional companion table name for the vectors (auto-derived when omitted)."),
		),
		mcp.WithString("target_schema",
			mcp.Description("Optional companion table schema."),
		),
		mcp.WithNumber("batch_size",
			mcp.Description("Optional rows embedded per provider call."),
		),
		mcp.WithNumber("poll_interval_seconds",
			mcp.Description("Optional poll interval for continuous mode."),
		),
		mcp.WithString("mode",
			mcp.Description("Processing mode: continuous (default, rows processed as they change), scheduled (cron-driven runs), or manual (runs only when triggered)."),
		),
		mcp.WithString("schedule_cron",
			mcp.Description("5-field cron expression; required when mode is scheduled."),
		),
		mcp.WithString("source_filter",
			mcp.Description("Optional restricted read-only WHERE fragment limiting which source rows are embedded, e.g. status = 'published'. No semicolons, comments, or subqueries."),
		),
		mcp.WithNumber("max_row_retries",
			mcp.Description("Optional per-row retry budget when a provider batch fails (default 3, max 10)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleCreateEmbeddingPipeline(cfg))

	s.AddTool(mcp.NewTool("trigger_embedding_job_run",
		mcp.WithDescription("Trigger one embedding run now for a scheduled or manual pipeline. The run is queued and dispatched within one scheduler tick; poll get_embedding_pipeline_runs until it finishes. Rejected for continuous pipelines and when a run is already in flight."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Managed service UUID."),
		),
		mcp.WithString("pipeline_id",
			mcp.Required(),
			mcp.Description("Embedding pipeline UUID (must be active, mode scheduled or manual)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleTriggerEmbeddingJobRun(cfg))

	s.AddTool(mcp.NewTool("pause_embedding_pipeline",
		mcp.WithDescription("Pause an active embedding pipeline. Continuous pipelines stop processing on the database VM; scheduled pipelines stop enqueueing runs. Already-written vectors are untouched."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Managed service UUID."),
		),
		mcp.WithString("pipeline_id",
			mcp.Required(),
			mcp.Description("Embedding pipeline UUID (must be active)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handlePauseEmbeddingPipeline(cfg))

	s.AddTool(mcp.NewTool("resume_embedding_pipeline",
		mcp.WithDescription("Resume a paused embedding pipeline. Processing picks up from the existing watermark; no rows are re-embedded."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Managed service UUID."),
		),
		mcp.WithString("pipeline_id",
			mcp.Required(),
			mcp.Description("Embedding pipeline UUID (must be paused)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleResumeEmbeddingPipeline(cfg))

	s.AddTool(mcp.NewTool("delete_embedding_pipeline",
		mcp.WithDescription("Delete an embedding pipeline. Destructive: pipelines have no unique name, so confirm must equal the exact pipeline UUID. With remove_data=true the companion vector table is dropped as well; otherwise the vectors are left in place."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Managed service UUID."),
		),
		mcp.WithString("pipeline_id",
			mcp.Required(),
			mcp.Description("Embedding pipeline UUID to delete."),
		),
		mcp.WithBoolean("remove_data",
			mcp.Description("Also drop the companion vector table (irreversible). Default false."),
		),
		mcp.WithString("confirm",
			mcp.Description("Must equal the exact pipeline UUID to execute. This is the typed confirmation for a destructive operation."),
		),
	), handleDeleteEmbeddingPipeline(cfg))

	s.AddTool(mcp.NewTool("vector_search",
		mcp.WithDescription("Run a read-only pgvector similarity search on a managed PostgreSQL service. Provide either a raw vector or query_text plus pipeline_id (the platform embeds the text with the pipeline's model so dimensions match). Brokered through the read-only data plane and capped at 100 rows."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Managed service UUID (PostgreSQL with pgvector)."),
		),
		mcp.WithString("database_name",
			mcp.Required(),
			mcp.Description("Database containing the table."),
		),
		mcp.WithString("table",
			mcp.Required(),
			mcp.Description("Table holding the vector column (often an embedding pipeline's target table)."),
		),
		mcp.WithString("schema",
			mcp.Description("Schema (default public)."),
		),
		mcp.WithString("embedding_column",
			mcp.Description("Vector column name (default embedding)."),
		),
		mcp.WithString("query_text",
			mcp.Description("Natural-language query to embed server-side; requires pipeline_id. Mutually exclusive with vector."),
		),
		mcp.WithString("pipeline_id",
			mcp.Description("Embedding pipeline UUID whose model embeds query_text (guarantees dimension match with the indexed vectors)."),
		),
		mcp.WithString("vector",
			mcp.Description("Raw query vector as a JSON array of numbers, e.g. [0.1, -0.2, 0.3]. Mutually exclusive with query_text."),
		),
		mcp.WithNumber("top_k",
			mcp.Description("Number of nearest rows to return (default 10, max 100)."),
		),
		mcp.WithString("metric",
			mcp.Description("Distance metric: cosine (default), l2, or ip (inner product)."),
		),
		mcp.WithString("filters",
			mcp.Description(`Optional column filters as a JSON array, e.g. [{"column":"category","op":"eq","value":"docs"}]. Only the eq operator is supported.`),
		),
		mcp.WithString("include_columns",
			mcp.Description("Optional comma-separated columns to return alongside the distance."),
		),
	), handleVectorSearch(cfg))

	s.AddTool(mcp.NewTool("get_inference_usage",
		mcp.WithDescription("Get aggregated AI inference proxy usage for an organization: calls, tokens, and cost per model or per key. Defaults to the current month grouped by model."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
		mcp.WithString("from",
			mcp.Description("Start of the window as an RFC 3339 timestamp (default: start of the current month)."),
		),
		mcp.WithString("to",
			mcp.Description("End of the window as an RFC 3339 timestamp (default: now)."),
		),
		mcp.WithString("group_by",
			mcp.Description("Aggregation key: model (default) or key."),
		),
	), handleGetInferenceUsage(cfg))

	s.AddTool(mcp.NewTool("list_inference_keys",
		mcp.WithDescription("List an organization's inference proxy keys: name, key prefix, status, token ceiling, and current-cycle usage. Key prefixes only; secrets are never returned after creation."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
	), handleListInferenceKeys(cfg))

	s.AddTool(mcp.NewTool("create_inference_key",
		mcp.WithDescription("Mint a new inference proxy key (fdb-inf-...) for an organization. The secret is returned exactly once in this tool's result and can never be retrieved again: store it immediately. Every key has a hard monthly token ceiling; there is no unlimited key."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Human-readable key name (used later as the typed confirmation when revoking)."),
		),
		mcp.WithNumber("monthly_token_limit",
			mcp.Required(),
			mcp.Description("Hard monthly token ceiling for the key; must be greater than zero. Requests beyond it are rejected with 429 until the month rolls over."),
		),
		mcp.WithNumber("rate_limit_rpm",
			mcp.Description("Optional requests-per-minute limit (default 60)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleCreateInferenceKey(cfg))

	s.AddTool(mcp.NewTool("revoke_inference_key",
		mcp.WithDescription("Revoke an inference proxy key. Destructive and irreversible: applications using the key fail immediately. confirm must equal the exact key name."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
		mcp.WithString("key_id",
			mcp.Required(),
			mcp.Description("Inference key UUID to revoke (from list_inference_keys)."),
		),
		mcp.WithString("confirm",
			mcp.Description("Must equal the exact key name to execute. This is the typed confirmation for a destructive operation."),
		),
	), handleRevokeInferenceKey(cfg))
}

func handleListEmbeddingPipelines(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/managed-services/"+serviceID+"/embedding-pipelines")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result == nil {
			return mcp.NewToolResultText("No embedding pipelines found on this service."), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetEmbeddingPipelineRuns(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		pipelineID, _ := args["pipeline_id"].(string)
		if serviceID == "" || pipelineID == "" {
			return mcp.NewToolResultError("service_id and pipeline_id are required"), nil
		}
		base := "/managed-services/" + serviceID + "/embedding-pipelines/" + pipelineID + "/runs"
		if runID, ok := args["run_id"].(string); ok && runID != "" {
			result, err := apiGet(ctx, cfg, base+"/"+runID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if result == nil {
				return mcp.NewToolResultText(fmt.Sprintf("Run %s not found.", runID)), nil
			}
			return mcp.NewToolResultText(formatJSON(result)), nil
		}
		result, err := apiGet(ctx, cfg, base)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result == nil {
			return mcp.NewToolResultText("No runs found for this pipeline (continuous pipelines have no discrete runs)."), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleCreateEmbeddingPipeline(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		databaseName, _ := args["database_name"].(string)
		sourceTable, _ := args["source_table"].(string)
		textColumnsRaw, _ := args["text_columns"].(string)
		modelProvider, _ := args["model_provider"].(string)
		embeddingModel, _ := args["embedding_model"].(string)
		dimensions, _ := args["model_dimensions"].(float64)
		providerAPIKey, _ := args["provider_api_key"].(string)

		if serviceID == "" || databaseName == "" || sourceTable == "" || textColumnsRaw == "" ||
			modelProvider == "" || embeddingModel == "" || dimensions == 0 || providerAPIKey == "" {
			return mcp.NewToolResultError("service_id, database_name, source_table, text_columns, model_provider, embedding_model, model_dimensions and provider_api_key are required"), nil
		}
		textColumns := parseCSVList(textColumnsRaw)
		if len(textColumns) == 0 {
			return mcp.NewToolResultError("text_columns must contain at least one column name"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("creating embedding pipeline on table %s.%s of service %s", databaseName, sourceTable, serviceID)); denied != nil {
			return denied, nil
		}

		body := map[string]interface{}{
			"database_name":    databaseName,
			"source_table":     sourceTable,
			"text_columns":     textColumns,
			"model_provider":   modelProvider,
			"embedding_model":  embeddingModel,
			"model_dimensions": int(dimensions),
			"provider_api_key": providerAPIKey,
		}
		if v, ok := args["source_schema"].(string); ok && v != "" {
			body["source_schema"] = v
		}
		if v, ok := args["target_table"].(string); ok && v != "" {
			body["target_table"] = v
		}
		if v, ok := args["target_schema"].(string); ok && v != "" {
			body["target_schema"] = v
		}
		if v, ok := args["provider_base_url"].(string); ok && v != "" {
			body["provider_base_url"] = v
		}
		if v, ok := args["batch_size"].(float64); ok && v > 0 {
			body["batch_size"] = int(v)
		}
		if v, ok := args["poll_interval_seconds"].(float64); ok && v > 0 {
			body["poll_interval_seconds"] = int(v)
		}
		if v, ok := args["mode"].(string); ok && v != "" {
			body["mode"] = v
		}
		if v, ok := args["schedule_cron"].(string); ok && v != "" {
			body["schedule_cron"] = v
		}
		if v, ok := args["source_filter"].(string); ok && v != "" {
			body["source_filter"] = v
		}
		if v, ok := args["max_row_retries"].(float64); ok && v > 0 {
			body["max_row_retries"] = int(v)
		}

		result, err := apiPost(ctx, cfg, "/managed-services/"+serviceID+"/embedding-pipelines", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleTriggerEmbeddingJobRun(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		pipelineID, _ := args["pipeline_id"].(string)
		if serviceID == "" || pipelineID == "" {
			return mcp.NewToolResultError("service_id and pipeline_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("triggering an embedding run for pipeline %s", pipelineID)); denied != nil {
			return denied, nil
		}
		result, err := apiPost(ctx, cfg, "/managed-services/"+serviceID+"/embedding-pipelines/"+pipelineID+"/runs", nil)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handlePauseEmbeddingPipeline(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		pipelineID, _ := args["pipeline_id"].(string)
		if serviceID == "" || pipelineID == "" {
			return mcp.NewToolResultError("service_id and pipeline_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("pausing embedding pipeline %s", pipelineID)); denied != nil {
			return denied, nil
		}
		if _, err := apiPost(ctx, cfg, "/managed-services/"+serviceID+"/embedding-pipelines/"+pipelineID+"/pause", nil); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Embedding pipeline %s paused.", pipelineID)), nil
	}
}

func handleResumeEmbeddingPipeline(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		pipelineID, _ := args["pipeline_id"].(string)
		if serviceID == "" || pipelineID == "" {
			return mcp.NewToolResultError("service_id and pipeline_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("resuming embedding pipeline %s", pipelineID)); denied != nil {
			return denied, nil
		}
		if _, err := apiPost(ctx, cfg, "/managed-services/"+serviceID+"/embedding-pipelines/"+pipelineID+"/resume", nil); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Embedding pipeline %s resumed.", pipelineID)), nil
	}
}

func handleDeleteEmbeddingPipeline(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		pipelineID, _ := args["pipeline_id"].(string)
		if serviceID == "" || pipelineID == "" {
			return mcp.NewToolResultError("service_id and pipeline_id are required"), nil
		}

		// Resolve the pipeline to get its metadata for the typed confirmation message.
		pipeline, err := apiGet(ctx, cfg, "/managed-services/"+serviceID+"/embedding-pipelines/"+pipelineID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("could not resolve pipeline %s: %s", pipelineID, err.Error())), nil
		}
		if pipeline == nil {
			return mcp.NewToolResultError("embedding pipeline not found"), nil
		}

		confirm, _ := args["confirm"].(string)
		if confirm == "" {
			srcTable, _ := pipeline["source_table"].(string)
			dbName, _ := pipeline["database_name"].(string)
			return mcp.NewToolResultError(fmt.Sprintf(
				"Not executed: deleting embedding pipeline %s (source %s.%s) is a destructive operation. To proceed, re-run with confirm set to the exact pipeline UUID %q.",
				pipelineID, dbName, srcTable, pipelineID,
			)), nil
		}
		if confirm != pipelineID {
			return mcp.NewToolResultError(fmt.Sprintf(
				"Not executed: typed confirmation mismatch. confirm was %q but the pipeline UUID is %q. Nothing was changed.",
				confirm, pipelineID,
			)), nil
		}

		removeData, _ := args["remove_data"].(bool)
		path := "/managed-services/" + serviceID + "/embedding-pipelines/" + pipelineID
		if removeData {
			path += "?remove_data=true"
		}
		if _, err := apiDelete(ctx, cfg, path); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if removeData {
			return mcp.NewToolResultText(fmt.Sprintf("Embedding pipeline %s deleted; companion vector table will be dropped.", pipelineID)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Embedding pipeline %s deleted; companion vector table was kept.", pipelineID)), nil
	}
}

func handleVectorSearch(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		databaseName, _ := args["database_name"].(string)
		table, _ := args["table"].(string)
		if serviceID == "" || databaseName == "" || table == "" {
			return mcp.NewToolResultError("service_id, database_name and table are required"), nil
		}

		body := map[string]interface{}{
			"database_name": databaseName,
			"table":         table,
		}
		if v, ok := args["schema"].(string); ok && v != "" {
			body["schema"] = v
		}
		if v, ok := args["embedding_column"].(string); ok && v != "" {
			body["embedding_column"] = v
		}
		queryText, _ := args["query_text"].(string)
		vectorRaw, _ := args["vector"].(string)
		if (queryText == "") == (vectorRaw == "") {
			return mcp.NewToolResultError("provide exactly one of query_text (with pipeline_id) or vector"), nil
		}
		if queryText != "" {
			pipelineID, _ := args["pipeline_id"].(string)
			if pipelineID == "" {
				return mcp.NewToolResultError("pipeline_id is required with query_text so the text is embedded with the same model as the indexed vectors"), nil
			}
			body["query_text"] = queryText
			body["pipeline_id"] = pipelineID
		}
		if vectorRaw != "" {
			var vec []float32
			if err := json.Unmarshal([]byte(vectorRaw), &vec); err != nil {
				return mcp.NewToolResultError("vector must be a JSON array of numbers, e.g. [0.1, -0.2, 0.3]"), nil
			}
			if len(vec) == 0 {
				return mcp.NewToolResultError("vector must contain at least one element"), nil
			}
			body["vector"] = vec
		}
		if v, ok := args["top_k"].(float64); ok && v > 0 {
			body["top_k"] = int(v)
		}
		if v, ok := args["metric"].(string); ok && v != "" {
			body["metric"] = v
		}
		if v, ok := args["filters"].(string); ok && v != "" {
			var filters []map[string]interface{}
			if err := json.Unmarshal([]byte(v), &filters); err != nil {
				return mcp.NewToolResultError(`filters must be a JSON array like [{"column":"category","op":"eq","value":"docs"}]`), nil
			}
			body["filters"] = filters
		}
		if v, ok := args["include_columns"].(string); ok && v != "" {
			body["include_columns"] = parseCSVList(v)
		}

		result, err := apiPost(ctx, cfg, "/managed-services/"+serviceID+"/vector-search", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetInferenceUsage(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("org_id is required"), nil
		}
		path := "/orgs/" + orgID + "/inference/usage"
		sep := "?"
		if from, ok := args["from"].(string); ok && from != "" {
			path += sep + "from=" + from
			sep = "&"
		}
		if to, ok := args["to"].(string); ok && to != "" {
			path += sep + "to=" + to
			sep = "&"
		}
		if groupBy, ok := args["group_by"].(string); ok && groupBy != "" {
			path += sep + "group_by=" + groupBy
		}
		_ = strings.Contains(path, "?")
		result, err := apiGet(ctx, cfg, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleListInferenceKeys(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("org_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/orgs/"+orgID+"/inference/keys")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result == nil {
			return mcp.NewToolResultText("No inference keys found for this organization."), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleCreateInferenceKey(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		name, _ := args["name"].(string)
		tokenLimit, _ := args["monthly_token_limit"].(float64)
		if orgID == "" || name == "" || tokenLimit <= 0 {
			return mcp.NewToolResultError("org_id, name and a positive monthly_token_limit are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("creating inference key %q (monthly token limit %d)", name, int64(tokenLimit))); denied != nil {
			return denied, nil
		}
		body := map[string]interface{}{
			"name":                name,
			"monthly_token_limit": int64(tokenLimit),
		}
		if v, ok := args["rate_limit_rpm"].(float64); ok && v > 0 {
			body["rate_limit_rpm"] = int(v)
		}
		result, err := apiPost(ctx, cfg, "/orgs/"+orgID+"/inference/keys", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Inference key created. The secret below is shown EXACTLY ONCE and can never be retrieved again; store it now (e.g. in the application's secret manager).\n\n%s",
			formatJSON(result),
		)), nil
	}
}

func handleRevokeInferenceKey(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		keyID, _ := args["key_id"].(string)
		if orgID == "" || keyID == "" {
			return mcp.NewToolResultError("org_id and key_id are required"), nil
		}

		// Resolve the key to get its name for the typed confirmation check.
		keysResult, err := apiGet(ctx, cfg, "/orgs/"+orgID+"/inference/keys")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("could not list inference keys: %s", err.Error())), nil
		}

		// Extract the key entry from the response.
		var keyName, keyPrefix string
		if keysResult != nil {
			if items, ok := keysResult["keys"].([]interface{}); ok {
				for _, item := range items {
					if m, ok := item.(map[string]interface{}); ok {
						if id, _ := m["id"].(string); id == keyID {
							keyName, _ = m["name"].(string)
							keyPrefix, _ = m["key_prefix"].(string)
							break
						}
					}
				}
			}
		}
		if keyName == "" {
			return mcp.NewToolResultError(fmt.Sprintf("inference key %s not found in organization %s", keyID, orgID)), nil
		}

		confirm, _ := args["confirm"].(string)
		if confirm == "" {
			return mcp.NewToolResultError(fmt.Sprintf(
				"Not executed: revoking inference key %s (prefix %s) is a destructive operation; applications using it fail immediately. To proceed, re-run with confirm set to the exact key name %q.",
				keyID, keyPrefix, keyName,
			)), nil
		}
		if confirm != keyName {
			return mcp.NewToolResultError(fmt.Sprintf(
				"Not executed: typed confirmation mismatch. confirm was %q but the key %s is named %q. Nothing was changed.",
				confirm, keyID, keyName,
			)), nil
		}

		if _, err := apiDelete(ctx, cfg, "/orgs/"+orgID+"/inference/keys/"+keyID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Inference key %q (%s) revoked.", keyName, keyID)), nil
	}
}
