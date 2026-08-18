package tools

import (
	"context"
	"fmt"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterInferenceServiceTools registers the managed inference service surface:
// open-weight LLMs served by vLLM, exposing an OpenAI-compatible endpoint. This
// is the service management plane (create, list, get, delete an inference
// service); it is distinct from the inference proxy tools (keys and usage) in
// ai_data.go.
//
// There are two SKUs. Serverless multiplexes onto a platform-owned shared pool,
// takes no GPU plan, serves only curated models a pool already answers for, and
// is billed per token (or per image) against the published rate card, with the
// organization's monthly free token allowance consumed first. Dedicated rents a
// whole card, serves curated or Hugging Face models, supports LoRA adapters and
// keep-warm, and is billed per GPU-hour while the card is allocated.
//
// Either way the service is called at its own endpoint_base_url with an fdb-inf
// key, and the model field is foundrydb_managed/<served_model_name>.
func RegisterInferenceServiceTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("list_serverless_inference_models",
		mcp.WithDescription("List the curated models a serverless inference service can be created for RIGHT NOW (those a platform shared pool is already serving), each with its published per-token or per-image price. Ask this before create_serverless_inference_service, which refuses any other model. Each entry carries model_id (the id a create takes), display_name, capability (chat, embeddings, rerank, or image), and deprecated (still bindable, but end of life upstream, so do not pick it as a default). Pricing is attached as price_eur_per_1m_prompt_tokens / price_eur_per_1m_completion_tokens for a token-priced model, or price_eur_per_image for an image model; a model with no published rate reports pricing_available=false, which means its price is not set yet rather than that it is free. An empty list is the honest \"serverless has nothing to offer yet\" answer, not an error. The dedicated SKU is NOT limited to this list: it rents its own card and can serve any curated or Hugging Face model that fits."),
	), handleListServerlessInferenceModels(c))

	s.AddTool(mcp.NewTool("list_inference_model_rates",
		mcp.WithDescription("List the price in force right now for every curated model that has one, so a create flow can quote what a serverless service will cost before anyone commits. This is the same rate the metering path bills at, so the quote and the bill cannot diverge. Rates are returned both in their raw wire units (prompt_microcents_per_1k, completion_microcents_per_1k, image_microcents_per_unit) and converted to EUR per 1M tokens or EUR per image. A model with no rate is omitted rather than reported at zero: absence means \"price not set yet\", never \"free\". Prices apply to serverless (per-token) billing; a dedicated GPU service is billed per GPU-hour by its plan instead and is not priced by this table."),
	), handleListInferenceModelRates(c))

	s.AddTool(mcp.NewTool("create_serverless_inference_service",
		mcp.WithDescription("Create a SERVERLESS inference service: one curated model bound to a platform-owned shared GPU pool, with no customer GPU rented and nothing billed by the hour. Billing is per token (per image for the diffusion models) at the published rate card, and the organization's monthly free token allowance is consumed first. model_id must be a model list_serverless_inference_models reports as serving; anything else is refused (503 when no pool serves the model, 409 when every pool for it is at its binding ceiling). Serverless takes no plan, no zone, and no serving knobs: the card is the platform's and its serving shape is fixed. It also cannot serve Hugging Face models, LoRA adapters, or keep-warm; use create_inference_service with a GPU plan_name for any of those. The result carries endpoint_base_url, which is the OpenAI base URL to call with an fdb-inf key."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Service name, unique to the owner."),
		),
		mcp.WithString("model_id",
			mcp.Required(),
			mcp.Description("Curated catalog id a pool is serving right now, e.g. mistral-small. Take it from list_serverless_inference_models rather than guessing."),
		),
		mcp.WithBoolean("license_accepted",
			mcp.Description("Accept the model license. Required for an accept-gated curated model (e.g. Llama)."),
		),
		mcp.WithString("organization_id",
			mcp.Description("Optional organization UUID to assign the service to (you must be a member)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleCreateServerlessInferenceService(c))

	s.AddTool(mcp.NewTool("list_inference_services",
		mcp.WithDescription("List the managed inference services (open-weight LLMs served by vLLM on dedicated GPU servers) visible to the authenticated user, including model, GPU plan, and status."),
	), handleListInferenceServices(c))

	s.AddTool(mcp.NewTool("get_inference_service",
		mcp.WithDescription("Get a managed inference service: status, GPU plan, zone, and resolved model configuration (model source, served model name, context length). The write-only Hugging Face token is never returned."),
		mcp.WithString("inference_service_id",
			mcp.Required(),
			mcp.Description("Inference service UUID."),
		),
	), handleGetInferenceService(c))

	s.AddTool(mcp.NewTool("create_inference_service",
		mcp.WithDescription("Create a managed inference service on either SKU. Omit plan_name (or set inference_sku=serverless) to bind one curated model to a platform-owned shared GPU pool, with no customer GPU and billing per token at the published rate card; only models list_serverless_inference_models reports as serving can bind, and create_serverless_inference_service is the simpler tool for that path. Set plan_name to a GPU alias (gpu-l4-1, gpu-l40s-1, gpu-h100-1, gpu-b200-1 and their multi-card rungs) for a dedicated whole card, billed per GPU-hour for as long as the card is allocated rather than per token. Curated catalog models use model_source=curated; Hugging Face pulls (dedicated only) use model_source=huggingface. A conditional-commercial curated model (e.g. Llama) and every Hugging Face model require license_accepted=true. Dedicated provisioning is asynchronous; poll get_inference_service until status is Running. Serverless returns Running once the edge hostname is bound. Either way the result's endpoint_base_url is the OpenAI-compatible base URL to call, with an fdb-inf key and the model field foundrydb_managed/<served_model_name>."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Service name, unique to the owner."),
		),
		mcp.WithString("inference_sku",
			mcp.Description("Placement SKU: serverless (platform pool) or dedicated (customer GPU). When omitted, a GPU plan_name means dedicated and an omitted plan_name means serverless."),
		),
		mcp.WithString("plan_name",
			mcp.Description("GPU plan alias for dedicated services, e.g. gpu-l40s-1. Omit for serverless."),
		),
		mcp.WithString("model_id",
			mcp.Required(),
			mcp.Description("Curated catalog id (e.g. mistral-small) when model_source=curated, or the Hugging Face repo id (org/name) when model_source=huggingface."),
		),
		mcp.WithString("model_source",
			mcp.Required(),
			mcp.Description("Model source: \"curated\" (platform-licensed catalog model) or \"huggingface\" (customer-licensed on-demand pull)."),
		),
		mcp.WithString("served_model_name",
			mcp.Description("Name the OpenAI-compatible endpoint reports and clients pass as the \"model\" field. Defaults from the catalog for curated models; required for a Hugging Face model."),
		),
		mcp.WithString("hf_repo",
			mcp.Description("Hugging Face repository to load weights from. Resolved by the platform for curated models; cannot override a curated model's repository."),
		),
		mcp.WithString("hf_token",
			mcp.Description("Hugging Face access token for pulling a gated repository. SENSITIVE and write-only: it passes through the conversation transcript, is stored encrypted, and is never returned by the API. Omit for ungated models."),
		),
		mcp.WithString("dtype",
			mcp.Description("vLLM weight dtype: auto (default), bfloat16, or float16."),
		),
		mcp.WithNumber("max_model_len",
			mcp.Description("Cap on the served context length. Zero uses the catalog default (curated) or the model-derived maximum (Hugging Face)."),
		),
		mcp.WithNumber("gpu_memory_utilization",
			mcp.Description("Fraction of VRAM vLLM reserves for the KV cache, between 0.10 and 0.99. Zero uses the platform default (0.90)."),
		),
		mcp.WithNumber("tensor_parallel_size",
			mcp.Description("Split the model across N cards. Zero uses 1; values above 1 require a multi-card GPU plan and must divide the plan's card count."),
		),
		mcp.WithBoolean("license_accepted",
			mcp.Description("Accept the model license. Required before serving a conditional-commercial curated model (e.g. Llama) or any Hugging Face model."),
		),
		mcp.WithString("zone",
			mcp.Description("UpCloud GPU zone (e.g. fi-hel2). Defaults to the platform's GPU zone."),
		),
		mcp.WithString("organization_id",
			mcp.Description("Optional organization UUID to assign the service to (you must be a member)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleCreateInferenceService(c))

	s.AddTool(mcp.NewTool("check_inference_fit",
		mcp.WithDescription("Check whether a model, at a given context length, fits a GPU plan's VRAM, before provisioning anything. Read-only: nothing is created, nothing is billed, and no GPU is touched. The fit model is weights + KV cache (which grows with the context length) + a fixed serving overhead, all within the memory-utilization budget of the plan's VRAM. Returns fits, the memory breakdown (weights_gb, kv_cache_gb, overhead_gb, budget_gb, plan_vram_gb), max_context_that_fits, limiting_factor (weights when the weights alone overflow the budget so no context length helps, kv_cache when the weights fit but the requested context does not, fits when nothing did), and suggestions naming the closest fix (reduce_context, fp8_kv_cache, or larger_plan). create_inference_service and switch_inference_model enforce this same equation, so use this first to predict and explain their refusals instead of provisioning a GPU to find out."),
		mcp.WithString("model_source",
			mcp.Required(),
			mcp.Description("Model source: \"curated\" (platform-licensed catalog model) or \"huggingface\" (customer-licensed on-demand pull, whose Hugging Face config is fetched to size the model)."),
		),
		mcp.WithString("model_id",
			mcp.Required(),
			mcp.Description("Curated catalog id (e.g. llama-3.3-70b) when model_source=curated, or the Hugging Face repo id (org/name) when model_source=huggingface."),
		),
		mcp.WithString("plan_name",
			mcp.Required(),
			mcp.Description("GPU plan alias to test the model against, e.g. gpu-l4-1, gpu-l40s-1, gpu-h100-1."),
		),
		mcp.WithNumber("max_model_len",
			mcp.Description("Context length to size the KV cache at. Omit to use the catalog default (curated) or the model-derived maximum (Hugging Face), which is what a create would serve."),
		),
		mcp.WithString("quantization",
			mcp.Description("Weight quantization the model is served at, e.g. fp8 or awq. Omit to use the checkpoint's own format (already fp8 for a curated FP8 model)."),
		),
		mcp.WithString("kv_cache_dtype",
			mcp.Description("KV cache element type: auto (the model dtype, default) or fp8, which halves kv_cache_gb."),
		),
		mcp.WithNumber("gpu_memory_utilization",
			mcp.Description("Fraction of the plan's VRAM the budget is drawn from, between 0.10 and 0.99. Omit to use the platform default (0.90)."),
		),
	), handleCheckInferenceFit(c))

	s.AddTool(mcp.NewTool("get_inference_service_usage",
		mcp.WithDescription("Get a managed inference service's metered usage and cost as a time-bucketed series with rolled-up totals (calls, errors, input/output/total tokens, images generated, accrued cost in microcents, average latency), plus a month_to_date rollup that is unaffected by the requested window. Which figure is the CHARGE depends on the SKU: month_to_date.tokens for a serverless service, which bills per token, and month_to_date.gpu_hour (billed_hours, hourly_rate_eur, cost_eur) for a dedicated one, which bills per allocated GPU-hour and whose per-token cost stays zero. The other figure is a usage signal, not a bill. The images counter stays zero for a text model and is the only usage figure that moves for an image model, which meters per generated image rather than per output token. Usage is attributed to this service's own endpoint within the owning organization, so two services serving the same model never share usage, and the window never starts before the service was created."),
		mcp.WithString("inference_service_id",
			mcp.Required(),
			mcp.Description("Inference service UUID."),
		),
		mcp.WithString("since",
			mcp.Description("Look-back window as a Go duration (e.g. 1h, 24h) or an RFC3339 start time. Defaults to 24 hours; capped at 30 days."),
		),
	), handleGetInferenceServiceUsage(c))

	s.AddTool(mcp.NewTool("get_inference_service_metrics",
		mcp.WithDescription("Get a managed inference service's live serving telemetry: the vLLM server + GPU snapshot series over a recent window plus the most recent snapshot for realtime tiles (requests running/waiting, KV cache usage, generation/prompt token throughput, average TTFT/TPOT/end-to-end latency, and per-GPU utilisation, memory, temperature, and power). This is live vLLM + GPU telemetry, distinct from get_inference_service_usage which reports metered usage and cost."),
		mcp.WithString("inference_service_id",
			mcp.Required(),
			mcp.Description("Inference service UUID."),
		),
		mcp.WithString("since",
			mcp.Description("Look-back window as a Go duration (e.g. 30m, 1h) or an RFC3339 start time. Defaults to 30 minutes; capped at 24 hours."),
		),
	), handleGetInferenceServiceMetrics(c))

	s.AddTool(mcp.NewTool("switch_inference_model",
		mcp.WithDescription("Switch which curated catalog model a managed inference service serves, in place, without deleting or re-provisioning it. The service's model volume is replaced by a clone of the target model's pre-baked volume template when the platform holds one for the service's zone (minutes, no weight download) or by a fresh volume taking the ordinary download otherwise; the GPU server, GPU plan, endpoint hostname, TLS certificate, firewall rules, inference keys, and billing identity are unchanged, and the old volume is deleted only once the new model is in place. The service must be Running or Stopped and single-node, the target must be a different curated model that fits the current plan's VRAM (the service is never resized by a switch), and any active LoRA adapter must be demoted first. Returns the service in the SwitchingModel status; poll get_inference_service until it returns to the state it came from (a service switched while stopped serves the new model on its next start)."),
		mcp.WithString("inference_service_id",
			mcp.Required(),
			mcp.Description("Inference service UUID."),
		),
		mcp.WithString("model_id",
			mcp.Required(),
			mcp.Description("Curated catalog id to switch to (e.g. bge-reranker-v2-m3). Must differ from the model the service serves today and must fit the current plan's VRAM."),
		),
		mcp.WithBoolean("license_accepted",
			mcp.Description("Accept the target model's license. Required to be true when the target is a license-gated curated model (e.g. Llama). Ungated targets do not need it."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleSwitchInferenceModel(c))

	s.AddTool(mcp.NewTool("delete_inference_service",
		mcp.WithDescription("Delete a managed inference service. The platform tears down the vLLM runtime, ingress, certificates, DNS, floating IP, and the GPU server. This is irreversible."),
		mcp.WithString("inference_service_id",
			mcp.Required(),
			mcp.Description("Inference service UUID."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleDeleteInferenceService(c))

	s.AddTool(mcp.NewTool("list_inference_adapters",
		mcp.WithDescription("List the customer LoRA fine-tuned adapter versions relevant to a managed inference service, newest first: the versions bound to it (the currently active version plus its superseded history) together with the organization's uploaded, not-yet-promoted versions trained on this service's base model, so a freshly registered adapter can be promoted from here. Uploaded versions carry status \"uploaded\" until promoted; uploaded versions for another base model, organization, or service are not listed. Returns an empty list when nothing is bound or promotable. Once a version is active the service answers to it as foundrydb_managed/<served_model_name>."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Inference service UUID."),
		),
	), handleListInferenceAdapters(c))

	s.AddTool(mcp.NewTool("promote_inference_adapter",
		mcp.WithDescription("Promote a customer LoRA fine-tuned adapter version onto a managed inference service's serving GPU: the platform downloads the adapter weights from Files, verifies their hash, and hot-loads them into vLLM with no restart. The promoted version becomes active and any previously active version is marked superseded. Once active, the model is addressed as foundrydb_managed/<served_model_name>. Rollback uses this same tool: promote a prior (superseded) version to roll back. Requires manage-level authority."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Inference service UUID."),
		),
		mcp.WithString("adapter_id",
			mcp.Required(),
			mcp.Description("LoRA adapter UUID to promote."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handlePromoteInferenceAdapter(c))

	s.AddTool(mcp.NewTool("demote_inference_adapter",
		mcp.WithDescription("Stop serving the active customer LoRA fine-tuned adapter version on a managed inference service, without promoting a replacement: the version becomes superseded and the adapter is hot-unloaded from the running vLLM, so foundrydb_managed/<served_model_name> stops answering and the adapter slot is freed. This is the inverse of promote_inference_adapter and the only exit from active that does not require another version (an in-place model switch is refused while an adapter is active, and an active version cannot be deleted). The service keeps serving its base model, and the version stays in the registry so it can be promoted again. Requires manage-level authority."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("Inference service UUID."),
		),
		mcp.WithString("adapter_id",
			mcp.Required(),
			mcp.Description("LoRA adapter UUID to stop serving. Must be the version currently active on this service."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleDemoteInferenceAdapter(c))

	s.AddTool(mcp.NewTool("delete_inference_adapter",
		mcp.WithDescription("Remove a customer LoRA fine-tuned adapter version from the organization's serving registry. Use this to clean up an uploaded (never-promoted) or superseded (rolled-off) version so the registry does not accumulate stale rows. An actively-served version cannot be deleted (409): promote a different version or delete the inference service first. Organization-scoped; a cross-org, unknown, or already-removed adapter id returns not-found."),
		mcp.WithString("adapter_id",
			mcp.Required(),
			mcp.Description("LoRA adapter UUID to remove."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleDeleteInferenceAdapter(c))

	s.AddTool(mcp.NewTool("register_inference_adapter",
		mcp.WithDescription("Register an uploaded customer LoRA fine-tuned adapter version in the serving registry, making it promotable. Call this after the adapter artifact (adapter_model.safetensors and adapter_config.json) has been uploaded to the organization's Files bucket; promote_inference_adapter later binds the version to a GPU and hot-loads it. The row is organization-scoped and not yet bound to any service (it enters with status \"uploaded\"). The owning organization is resolved from your active organization, or from organization_id when you are a member of it."),
		mcp.WithString("base_model_id",
			mcp.Required(),
			mcp.Description("The base model the adapter was trained against. Must later match the serving service's model id or Hugging Face repo."),
		),
		mcp.WithString("served_model_name",
			mcp.Required(),
			mcp.Description("Customer-facing name the adapter answers to, becoming foundrydb_managed/<served_model_name>. Letters, digits, '.', '_' and '-' only, at most 128 characters."),
		),
		mcp.WithNumber("version",
			mcp.Required(),
			mcp.Description("Monotonic version per (organization, served model name). Must be at least 1."),
		),
		mcp.WithString("files_bucket",
			mcp.Required(),
			mcp.Description("The organization's Files bucket holding the adapter artifact."),
		),
		mcp.WithString("files_key_prefix",
			mcp.Required(),
			mcp.Description("Files key prefix holding adapter_model.safetensors and adapter_config.json."),
		),
		mcp.WithString("adapter_sha256",
			mcp.Required(),
			mcp.Description("64-character lowercase hex sha256 of adapter_model.safetensors, re-verified after download before loading."),
		),
		mcp.WithNumber("size_bytes",
			mcp.Required(),
			mcp.Description("Size of the adapter artifact in bytes. Must not be negative."),
		),
		mcp.WithString("base_model_license",
			mcp.Description("The base-model license that travels with the weights. Optional."),
		),
		mcp.WithString("organization_id",
			mcp.Description("Optional organization UUID to register the adapter under (you must be a member). Defaults to your active organization."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleRegisterInferenceAdapter(c))
}

func handleListInferenceServices(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		services, err := c.ListInferenceServices(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(services) == 0 {
			return mcp.NewToolResultText("No inference services found."), nil
		}
		return mcp.NewToolResultText(formatJSON(services)), nil
	}
}

func handleGetInferenceService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["inference_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("inference_service_id is required"), nil
		}
		svc, err := c.GetInferenceService(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if svc == nil {
			return mcp.NewToolResultText(fmt.Sprintf("Inference service %s not found.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(svc)), nil
	}
}

func handleCreateInferenceService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)
		sku, _ := args["inference_sku"].(string)
		planName, _ := args["plan_name"].(string)
		modelID, _ := args["model_id"].(string)
		modelSource, _ := args["model_source"].(string)
		if name == "" || modelID == "" || modelSource == "" {
			return mcp.NewToolResultError("name, model_id and model_source are required"), nil
		}
		if modelSource != foundrydb.InferenceModelSourceCurated && modelSource != foundrydb.InferenceModelSourceHuggingFace {
			return mcp.NewToolResultError(fmt.Sprintf("model_source must be %q or %q", foundrydb.InferenceModelSourceCurated, foundrydb.InferenceModelSourceHuggingFace)), nil
		}

		confirmTarget := fmt.Sprintf("creating inference service %q (model %s)", name, modelID)
		if planName != "" {
			confirmTarget = fmt.Sprintf("creating inference service %q (model %s on plan %s)", name, modelID, planName)
		}
		if denied := requireConfirmFlag(args, confirmTarget); denied != nil {
			return denied, nil
		}

		cfg := foundrydb.InferenceConfig{
			ModelID:     modelID,
			ModelSource: modelSource,
		}
		if v, ok := args["served_model_name"].(string); ok && v != "" {
			cfg.ServedModelName = v
		}
		if v, ok := args["hf_repo"].(string); ok && v != "" {
			cfg.HFRepo = v
		}
		if v, ok := args["hf_token"].(string); ok && v != "" {
			cfg.HFToken = v
		}
		if v, ok := args["dtype"].(string); ok && v != "" {
			cfg.DType = v
		}
		if v, ok := args["max_model_len"].(float64); ok && v > 0 {
			cfg.MaxModelLen = int(v)
		}
		if v, ok := args["gpu_memory_utilization"].(float64); ok && v > 0 {
			cfg.GPUMemoryUtilization = v
		}
		if v, ok := args["tensor_parallel_size"].(float64); ok && v > 0 {
			cfg.TensorParallelSize = int(v)
		}
		if v, ok := args["license_accepted"].(bool); ok {
			cfg.LicenseAccepted = v
		}

		createReq := foundrydb.InferenceServiceRequest{
			Name:         name,
			InferenceSKU: sku,
			PlanName:     planName,
			Inference:    &cfg,
		}
		if v, ok := args["zone"].(string); ok && v != "" {
			createReq.Zone = v
		}
		if v, ok := args["organization_id"].(string); ok && v != "" {
			createReq.OrganizationID = v
		}

		svc, err := c.CreateInferenceService(ctx, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(svc)), nil
	}
}

// microcentsPer1KToEURPerMTok converts a published token rate (microcents per
// one thousand tokens) into EUR per one million tokens, which is the unit every
// competitor quotes in and the only one a customer can compare.
func microcentsPer1KToEURPerMTok(microcentsPer1K int64) float64 {
	return float64(microcentsPer1K) / 100_000
}

// microcentsPerImageToEUR converts a published image rate (microcents per
// generated image) into EUR per image.
func microcentsPerImageToEUR(microcentsPerUnit int64) float64 {
	return float64(microcentsPerUnit) / 100_000_000
}

// pricedInferenceModelRate is one published rate in both the wire units the API
// returns and the currency units a person reads. Both are reported because the
// raw figures are what a client recomputes a bill from, while the EUR figures
// are what a quote is spoken in.
type pricedInferenceModelRate struct {
	ModelID                    string  `json:"model_id"`
	RateUnit                   string  `json:"rate_unit"`
	PromptMicrocentsPer1K      int64   `json:"prompt_microcents_per_1k,omitempty"`
	CompletionMicrocentsPer1K  int64   `json:"completion_microcents_per_1k,omitempty"`
	ImageMicrocentsPerUnit     int64   `json:"image_microcents_per_unit,omitempty"`
	PriceEURPer1MPromptTokens  float64 `json:"price_eur_per_1m_prompt_tokens,omitempty"`
	PriceEURPer1MCompletionTok float64 `json:"price_eur_per_1m_completion_tokens,omitempty"`
	PriceEURPerImage           float64 `json:"price_eur_per_image,omitempty"`
	EffectiveFrom              string  `json:"effective_from"`
}

// priceInferenceModelRate projects an API rate onto the dual-unit view above.
func priceInferenceModelRate(r foundrydb.InferenceModelRate) pricedInferenceModelRate {
	priced := pricedInferenceModelRate{
		ModelID:       r.ModelID,
		RateUnit:      r.RateUnit,
		EffectiveFrom: r.EffectiveFrom,
	}
	if r.RateUnit == foundrydb.InferenceModelRateUnitImage {
		priced.ImageMicrocentsPerUnit = r.ImageMicrocentsPerUnit
		priced.PriceEURPerImage = microcentsPerImageToEUR(r.ImageMicrocentsPerUnit)
		return priced
	}
	priced.PromptMicrocentsPer1K = r.PromptMicrocentsPer1K
	priced.CompletionMicrocentsPer1K = r.CompletionMicrocentsPer1K
	priced.PriceEURPer1MPromptTokens = microcentsPer1KToEURPerMTok(r.PromptMicrocentsPer1K)
	priced.PriceEURPer1MCompletionTok = microcentsPer1KToEURPerMTok(r.CompletionMicrocentsPer1K)
	return priced
}

func handleListInferenceModelRates(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rates, err := c.ListInferenceModelRates(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(rates) == 0 {
			return mcp.NewToolResultText("No managed model rates are published yet."), nil
		}
		priced := make([]pricedInferenceModelRate, 0, len(rates))
		for _, r := range rates {
			priced = append(priced, priceInferenceModelRate(r))
		}
		return mcp.NewToolResultText(formatJSON(priced)), nil
	}
}

// serverlessModelWithRate is one bindable serverless model joined with its
// published price. PricingAvailable is explicit so an unpriced model reads as
// "price not set yet" rather than as free, which a zeroed rate would imply.
type serverlessModelWithRate struct {
	ModelID          string  `json:"model_id"`
	DisplayName      string  `json:"display_name"`
	Capability       string  `json:"capability"`
	Serving          bool    `json:"serving"`
	Deprecated       bool    `json:"deprecated"`
	PricingAvailable bool    `json:"pricing_available"`
	RateUnit         string  `json:"rate_unit,omitempty"`
	PriceEURPer1MIn  float64 `json:"price_eur_per_1m_prompt_tokens,omitempty"`
	PriceEURPer1MOut float64 `json:"price_eur_per_1m_completion_tokens,omitempty"`
	PriceEURPerImage float64 `json:"price_eur_per_image,omitempty"`
}

func handleListServerlessInferenceModels(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		models, err := c.ListServerlessInferenceModels(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(models) == 0 {
			return mcp.NewToolResultText("No models are being served by a platform pool right now, so no serverless inference service can be created. Create a dedicated GPU service with create_inference_service instead."), nil
		}
		// The rate card is a separate read and is not required for the listing to
		// be useful: a rates failure leaves every model unpriced rather than
		// failing the answer to "what can I create".
		rateByModel := map[string]foundrydb.InferenceModelRate{}
		if rates, rateErr := c.ListInferenceModelRates(ctx); rateErr == nil {
			for _, r := range rates {
				rateByModel[r.ModelID] = r
			}
		}
		rows := make([]serverlessModelWithRate, 0, len(models))
		for _, m := range models {
			row := serverlessModelWithRate{
				ModelID:     m.ModelID,
				DisplayName: m.DisplayName,
				Capability:  m.Capability,
				Serving:     m.Serving,
				Deprecated:  m.Deprecated,
			}
			if rate, ok := rateByModel[m.ModelID]; ok {
				row.PricingAvailable = true
				row.RateUnit = rate.RateUnit
				if rate.RateUnit == foundrydb.InferenceModelRateUnitImage {
					row.PriceEURPerImage = microcentsPerImageToEUR(rate.ImageMicrocentsPerUnit)
				} else {
					row.PriceEURPer1MIn = microcentsPer1KToEURPerMTok(rate.PromptMicrocentsPer1K)
					row.PriceEURPer1MOut = microcentsPer1KToEURPerMTok(rate.CompletionMicrocentsPer1K)
				}
			}
			rows = append(rows, row)
		}
		return mcp.NewToolResultText(formatJSON(rows)), nil
	}
}

func handleCreateServerlessInferenceService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)
		modelID, _ := args["model_id"].(string)
		if name == "" || modelID == "" {
			return mcp.NewToolResultError("name and model_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("creating serverless inference service %q (model %s, billed per token)", name, modelID)); denied != nil {
			return denied, nil
		}
		licenseAccepted, _ := args["license_accepted"].(bool)
		orgID, _ := args["organization_id"].(string)

		svc, err := c.CreateServerlessInferenceService(ctx, name, modelID, orgID, licenseAccepted)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(svc)), nil
	}
}

func handleCheckInferenceFit(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		modelSource, _ := args["model_source"].(string)
		modelID, _ := args["model_id"].(string)
		planName, _ := args["plan_name"].(string)
		if modelSource == "" || modelID == "" || planName == "" {
			return mcp.NewToolResultError("model_source, model_id and plan_name are required"), nil
		}
		if modelSource != foundrydb.InferenceModelSourceCurated && modelSource != foundrydb.InferenceModelSourceHuggingFace {
			return mcp.NewToolResultError(fmt.Sprintf("model_source must be %q or %q", foundrydb.InferenceModelSourceCurated, foundrydb.InferenceModelSourceHuggingFace)), nil
		}

		fitReq := foundrydb.InferenceFitCheckRequest{
			ModelSource: modelSource,
			ModelID:     modelID,
			PlanName:    planName,
		}
		if v, ok := args["max_model_len"].(float64); ok && v > 0 {
			fitReq.MaxModelLen = int(v)
		}
		if v, ok := args["quantization"].(string); ok && v != "" {
			fitReq.Quantization = v
		}
		if v, ok := args["kv_cache_dtype"].(string); ok && v != "" {
			fitReq.KVCacheDType = v
		}
		if v, ok := args["gpu_memory_utilization"].(float64); ok && v > 0 {
			fitReq.GPUMemoryUtilization = v
		}

		result, err := c.CheckInferenceFit(ctx, fitReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetInferenceServiceUsage(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["inference_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("inference_service_id is required"), nil
		}
		since, _ := args["since"].(string)
		usage, err := c.GetInferenceServiceUsage(ctx, id, since)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if usage == nil {
			return mcp.NewToolResultText(fmt.Sprintf("Inference service %s not found.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(usage)), nil
	}
}

func handleGetInferenceServiceMetrics(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["inference_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("inference_service_id is required"), nil
		}
		since, _ := args["since"].(string)
		metrics, err := c.GetInferenceServiceMetrics(ctx, id, since)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if metrics == nil {
			return mcp.NewToolResultText(fmt.Sprintf("Inference service %s not found.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(metrics)), nil
	}
}

func handleSwitchInferenceModel(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["inference_service_id"].(string)
		modelID, _ := args["model_id"].(string)
		if id == "" || modelID == "" {
			return mcp.NewToolResultError("inference_service_id and model_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("switching inference service %s to model %s (its model disk is replaced)", id, modelID)); denied != nil {
			return denied, nil
		}

		switchReq := foundrydb.InferenceModelSwitchRequest{ModelID: modelID}
		if v, ok := args["license_accepted"].(bool); ok {
			switchReq.LicenseAccepted = v
		}

		svc, err := c.SwitchInferenceModel(ctx, id, switchReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(svc)), nil
	}
}

func handleDeleteInferenceService(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["inference_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("inference_service_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting inference service %s", id)); denied != nil {
			return denied, nil
		}
		if err := c.DeleteInferenceService(ctx, id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Inference service %s deletion scheduled.", id)), nil
	}
}

func handleListInferenceAdapters(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		adapters, err := c.ListInferenceServiceAdapters(ctx, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if adapters == nil {
			return mcp.NewToolResultText(fmt.Sprintf("Inference service %s not found.", id)), nil
		}
		if len(adapters) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No LoRA adapters bound to inference service %s.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(adapters)), nil
	}
}

func handlePromoteInferenceAdapter(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		adapterID, _ := args["adapter_id"].(string)
		if serviceID == "" || adapterID == "" {
			return mcp.NewToolResultError("service_id and adapter_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("promoting adapter %s onto inference service %s", adapterID, serviceID)); denied != nil {
			return denied, nil
		}
		adapter, err := c.PromoteInferenceAdapter(ctx, serviceID, adapterID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(adapter)), nil
	}
}

func handleDemoteInferenceAdapter(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		adapterID, _ := args["adapter_id"].(string)
		if serviceID == "" || adapterID == "" {
			return mcp.NewToolResultError("service_id and adapter_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf(
			"stopping adapter %s from serving on inference service %s (clients calling it will start failing)", adapterID, serviceID)); denied != nil {
			return denied, nil
		}
		adapter, err := c.DemoteInferenceAdapter(ctx, serviceID, adapterID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(adapter)), nil
	}
}

func handleDeleteInferenceAdapter(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		adapterID, _ := args["adapter_id"].(string)
		if adapterID == "" {
			return mcp.NewToolResultError("adapter_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("removing adapter %s from the serving registry", adapterID)); denied != nil {
			return denied, nil
		}
		if err := c.DeleteInferenceAdapter(ctx, adapterID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Adapter %s removed from the serving registry.", adapterID)), nil
	}
}

func handleRegisterInferenceAdapter(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		baseModelID, _ := args["base_model_id"].(string)
		servedModelName, _ := args["served_model_name"].(string)
		filesBucket, _ := args["files_bucket"].(string)
		filesKeyPrefix, _ := args["files_key_prefix"].(string)
		adapterSHA256, _ := args["adapter_sha256"].(string)
		if baseModelID == "" || servedModelName == "" || filesBucket == "" || filesKeyPrefix == "" || adapterSHA256 == "" {
			return mcp.NewToolResultError("base_model_id, served_model_name, files_bucket, files_key_prefix and adapter_sha256 are required"), nil
		}
		version, _ := args["version"].(float64)
		if version < 1 {
			return mcp.NewToolResultError("version is required and must be at least 1"), nil
		}
		sizeBytes, _ := args["size_bytes"].(float64)
		if sizeBytes < 0 {
			return mcp.NewToolResultError("size_bytes must not be negative"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("registering adapter %q version %d", servedModelName, int(version))); denied != nil {
			return denied, nil
		}

		registerReq := foundrydb.InferenceAdapterRegisterRequest{
			BaseModelID:     baseModelID,
			ServedModelName: servedModelName,
			Version:         int(version),
			FilesBucket:     filesBucket,
			FilesKeyPrefix:  filesKeyPrefix,
			AdapterSHA256:   adapterSHA256,
			SizeBytes:       int64(sizeBytes),
		}
		if v, ok := args["base_model_license"].(string); ok && v != "" {
			registerReq.BaseModelLicense = v
		}
		if v, ok := args["organization_id"].(string); ok && v != "" {
			registerReq.OrganizationID = v
		}

		adapter, err := c.RegisterInferenceAdapter(ctx, registerReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(adapter)), nil
	}
}
