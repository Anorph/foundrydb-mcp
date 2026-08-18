package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterAIDataTools registers the AI data surface: embedding pipelines and
// their runs, vector search over the read-only data plane, the organization's
// inference provider inventory and proxy policy, its provider chain and the
// per-surface overrides on it, and the inference proxy's keys, usage, and free
// token allowance.
//
// Configuring a provider (writing the org's raw OpenAI/Anthropic/Mistral/Azure
// API key) is deliberately not exposed here, and the omission is the point:
// provider secrets must not transit an agent transcript. Configuration happens
// in the UI or through the SDKs, where the caller controls the transport. This
// surface reads provider configs and deletes them, and never writes a secret.
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

	s.AddTool(mcp.NewTool("list_inference_providers",
		mcp.WithDescription("List an organization's configured AI inference providers (openai, anthropic, mistral, azure_openai, groq, foundrydb_managed): provider, base URL, EU-endpoint flag, enabled state, and whether an API key is stored. Provider API keys are never returned."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
	), handleListInferenceProviders(cfg))

	s.AddTool(mcp.NewTool("delete_inference_provider",
		mcp.WithDescription("Remove an organization's config for one AI inference provider. Subsequent proxy calls routed to that provider fail until it is configured again; there is no fallback to any platform key."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
		mcp.WithString("provider",
			mcp.Required(),
			mcp.Description("Provider to remove: openai, anthropic, mistral, azure_openai, or groq."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleDeleteInferenceProvider(cfg))

	s.AddTool(mcp.NewTool("get_inference_settings",
		mcp.WithDescription("Get an organization's inference proxy policy: EU-only routing, the monthly cost circuit breaker (limit in cents), and whether the cost circuit is currently open. Returns not-configured when the settings have never been set."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
	), handleGetInferenceSettings(cfg))

	s.AddTool(mcp.NewTool("update_inference_settings",
		mcp.WithDescription("Update an organization's inference proxy policy: toggle EU-only routing, set the monthly cost limit (cents), and optionally reset an open cost circuit breaker. monthly_cost_limit_cents is required the first time the settings are configured."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
		mcp.WithBoolean("eu_only",
			mcp.Description("Restrict routing to EU-resident providers only."),
		),
		mcp.WithNumber("monthly_cost_limit_cents",
			mcp.Description("Monthly cost circuit-breaker limit in cents. Required the first time the settings are configured."),
		),
		mcp.WithBoolean("reset_circuit",
			mcp.Description("Close an open cost circuit breaker, resuming inference for the current cycle."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleUpdateInferenceSettings(cfg))

	s.AddTool(mcp.NewTool("get_inference_provider_chain",
		mcp.WithDescription("Get an organization's ordered inference provider preference chain, whether every provider in it routes EU-resident, and the per-surface overrides currently in place. Platform AI surfaces (chat, advisor, embedding, agent, explainer) resolve their upstream through this chain unless a surface override replaces it."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
	), handleGetInferenceProviderChain(cfg))

	s.AddTool(mcp.NewTool("set_inference_provider_chain",
		mcp.WithDescription("Replace an organization's ordered inference provider chain wholesale. Entries are provider identifiers (openai, anthropic, mistral, azure_openai, groq, foundrydb_managed), unique, optionally closed by the literal terminator 'none' to state that resolution stops there with no platform fallback. Per-surface overrides are untouched and echoed back."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
		mcp.WithString("provider_chain",
			mcp.Required(),
			mcp.Description("Comma-separated ordered provider chain, e.g. foundrydb_managed,mistral,none."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleSetInferenceProviderChain(cfg))

	s.AddTool(mcp.NewTool("set_inference_surface_override",
		mcp.WithDescription("Replace the inference provider chain for ONE platform AI surface (chat, advisor, embedding, agent, or explainer). While the override exists, that surface resolves through it instead of the org-level chain. The chain follows the same rules as the org-level chain."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
		mcp.WithString("surface",
			mcp.Required(),
			mcp.Description("Surface to override: chat, advisor, embedding, agent, or explainer."),
		),
		mcp.WithString("provider_chain",
			mcp.Required(),
			mcp.Description("Comma-separated ordered provider chain for this surface, e.g. groq,none."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleSetInferenceSurfaceOverride(cfg))

	s.AddTool(mcp.NewTool("delete_inference_surface_override",
		mcp.WithDescription("Remove one surface's inference provider chain override so the org-level chain applies to it again. Idempotent: deleting an absent override succeeds."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
		mcp.WithString("surface",
			mcp.Required(),
			mcp.Description("Surface whose override to remove: chat, advisor, embedding, agent, or explainer."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleDeleteInferenceSurfaceOverride(cfg))

	s.AddTool(mcp.NewTool("get_inference_usage",
		mcp.WithDescription("Get aggregated AI inference usage for an organization: calls, tokens, and cost per model or per key. Defaults to the current month grouped by model. The response also carries free_tier, the organization's monthly free token allowance standing, which always describes the CURRENT calendar month regardless of the window asked for, because the allowance is a monthly meter rather than an aggregate of the window."),
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

	s.AddTool(mcp.NewTool("get_inference_free_tier",
		mcp.WithDescription("Get an organization's monthly free inference token allowance standing for the current calendar month: monthly_tokens (the allowance), tokens_used, tokens_remaining, and cycle_month. Use this to answer \"how much free inference is left\" and to explain when serverless inference starts costing money, since allowance tokens are metered exactly like paid ones but recorded at zero cost and are consumed BEFORE any billing starts. Only platform-served (foundrydb_managed) token calls draw on the allowance: a call to the organization's own third-party provider is billed on that provider's account, and an image generation is priced per image and reports no tokens, so neither consumes it. The allowance resets at each month boundary."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
	), handleGetInferenceFreeTier(cfg))

	s.AddTool(mcp.NewTool("list_inference_keys",
		mcp.WithDescription("List an organization's inference proxy keys: name, key prefix, status, token ceiling, and current-cycle usage. Key prefixes only; secrets are never returned after creation."),
		mcp.WithString("org_id",
			mcp.Required(),
			mcp.Description("Organization UUID."),
		),
	), handleListInferenceKeys(cfg))

	s.AddTool(mcp.NewTool("create_inference_key",
		mcp.WithDescription("Mint a new inference key (fdb-inf-...) for an organization. The secret is returned exactly once in this tool's result and can never be retrieved again: store it immediately. Every key has a hard monthly token ceiling; there is no unlimited key, and rate_limit_rpm additionally caps its requests per minute. The result carries an activation_note: the key does NOT work at the inference endpoint the instant it is minted, because the key hash reaches the data plane through an edge config reconcile, so a call sent immediately after minting can answer invalid_key and should simply be retried a few seconds later."),
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
		mcp.WithString("service_id",
			mcp.Description("Optional inference service UUID to scope the key to. The key then works against that one service's endpoint and is refused on every other endpoint exactly as an unknown endpoint would be, so a leaked application credential exposes one deployment instead of the organization's whole fleet. Deleting that service deletes the key with it. Omit for the default org-scoped key, usable against any of the organization's inference services."),
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

// orgInferencePath is the root of an organization's inference management
// plane. Every provider, key, settings, chain, and usage resource hangs
// beneath it.
func orgInferencePath(orgID string) string {
	return "/organizations/" + url.PathEscape(orgID) + "/inference"
}

// orgInferenceChainPath is the organization's provider chain resource; the
// per-surface overrides hang beneath it.
func orgInferenceChainPath(orgID string) string {
	return orgInferencePath(orgID) + "/chain"
}

func handleListInferenceProviders(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("org_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, orgInferencePath(orgID)+"/providers")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if providers, ok := result["providers"].([]interface{}); !ok || len(providers) == 0 {
			return mcp.NewToolResultText("No inference providers configured for this organization."), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleDeleteInferenceProvider(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		provider, _ := args["provider"].(string)
		if orgID == "" || provider == "" {
			return mcp.NewToolResultError("org_id and provider are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("removing inference provider %q from organization %s", provider, orgID)); denied != nil {
			return denied, nil
		}
		path := orgInferencePath(orgID) + "/providers/" + url.PathEscape(provider)
		if _, err := apiDelete(ctx, cfg, path); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Inference provider %q removed from organization %s.", provider, orgID)), nil
	}
}

func handleGetInferenceSettings(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("org_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, orgInferencePath(orgID)+"/settings")
		if err != nil {
			// Settings that were never configured answer 404, which is an
			// answer rather than a failure.
			if strings.Contains(err.Error(), "API error 404") {
				return mcp.NewToolResultText(fmt.Sprintf("Inference settings are not configured for organization %s.", orgID)), nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleUpdateInferenceSettings(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("org_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("updating inference settings for organization %s", orgID)); denied != nil {
			return denied, nil
		}
		body := map[string]interface{}{}
		if v, ok := args["eu_only"].(bool); ok {
			body["eu_only"] = v
		}
		if v, ok := args["monthly_cost_limit_cents"].(float64); ok {
			body["monthly_cost_limit_cents"] = int64(v)
		}
		if v, ok := args["reset_circuit"].(bool); ok {
			body["reset_circuit"] = v
		}
		result, err := apiPut(ctx, cfg, orgInferencePath(orgID)+"/settings", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetInferenceProviderChain(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("org_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, orgInferenceChainPath(orgID))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleSetInferenceProviderChain(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		chainRaw, _ := args["provider_chain"].(string)
		if orgID == "" || chainRaw == "" {
			return mcp.NewToolResultError("org_id and provider_chain are required"), nil
		}
		chain := parseCSVList(chainRaw)
		if len(chain) == 0 {
			return mcp.NewToolResultError("provider_chain must contain at least one entry"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("replacing the inference provider chain of organization %s with [%s]", orgID, chainRaw)); denied != nil {
			return denied, nil
		}
		result, err := apiPut(ctx, cfg, orgInferenceChainPath(orgID), map[string]interface{}{"provider_chain": chain})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleSetInferenceSurfaceOverride(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		surface, _ := args["surface"].(string)
		chainRaw, _ := args["provider_chain"].(string)
		if orgID == "" || surface == "" || chainRaw == "" {
			return mcp.NewToolResultError("org_id, surface and provider_chain are required"), nil
		}
		chain := parseCSVList(chainRaw)
		if len(chain) == 0 {
			return mcp.NewToolResultError("provider_chain must contain at least one entry"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("overriding the %s surface's inference provider chain for organization %s with [%s]", surface, orgID, chainRaw)); denied != nil {
			return denied, nil
		}
		path := orgInferenceChainPath(orgID) + "/overrides/" + url.PathEscape(surface)
		result, err := apiPut(ctx, cfg, path, map[string]interface{}{"provider_chain": chain})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleDeleteInferenceSurfaceOverride(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		surface, _ := args["surface"].(string)
		if orgID == "" || surface == "" {
			return mcp.NewToolResultError("org_id and surface are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("removing the %s surface's inference provider chain override for organization %s", surface, orgID)); denied != nil {
			return denied, nil
		}
		path := orgInferenceChainPath(orgID) + "/overrides/" + url.PathEscape(surface)
		if _, err := apiDelete(ctx, cfg, path); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Inference chain override for surface %q removed from organization %s; the org-level chain applies to it again.", surface, orgID)), nil
	}
}

func handleGetInferenceFreeTier(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("org_id is required"), nil
		}
		// The allowance standing rides on the usage summary, which is the one call
		// that reports it; the rows are not needed here and are discarded.
		result, err := apiGet(ctx, cfg, orgInferencePath(orgID)+"/usage")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		freeTier, ok := result["free_tier"]
		if !ok || freeTier == nil {
			return mcp.NewToolResultText("The free inference token allowance standing is not available for this organization right now."), nil
		}
		return mcp.NewToolResultText(formatJSON(freeTier)), nil
	}
}

func handleGetInferenceUsage(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["org_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("org_id is required"), nil
		}
		path := orgInferencePath(orgID) + "/usage"
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
		result, err := apiGet(ctx, cfg, orgInferencePath(orgID)+"/keys")
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
		// An empty service_id is the absent one: it means org-scoped, never a
		// scope the server has to reject.
		if v, ok := args["service_id"].(string); ok && v != "" {
			body["service_id"] = v
		}
		result, err := apiPost(ctx, cfg, orgInferencePath(orgID)+"/keys", body)
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
		keysResult, err := apiGet(ctx, cfg, orgInferencePath(orgID)+"/keys")
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

		if _, err := apiDelete(ctx, cfg, orgInferencePath(orgID)+"/keys/"+url.PathEscape(keyID)); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Inference key %q (%s) revoked.", keyName, keyID)), nil
	}
}
