package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterAppJobTools registers tools for app service jobs: cron-scheduled and
// ad-hoc container runs (image, command, env layered over the app's own
// configuration) executing on the app's VM, with retries, runtime caps, a
// concurrency cap, and per-invocation logs.
func RegisterAppJobTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("jobs_create",
		mcp.WithDescription("Create a job on a hosted app service: a container run with an optional five-field cron schedule (minute granularity, descriptors like @daily accepted). Without a schedule the job only runs when triggered via jobs_run. Image, command, and env default to the app's own configuration; overrides are layered on top (injected database connection variables remain available). A service supports at most 20 jobs."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID the job runs on."),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Job name (lowercase alphanumerics and hyphens, max 63 chars), unique per app service."),
		),
		mcp.WithString("schedule_cron",
			mcp.Description("Optional cron expression (e.g. */15 * * * * or @daily) evaluated in the job's timezone. Omit for a manual-only job."),
		),
		mcp.WithString("timezone",
			mcp.Description("IANA timezone the schedule fires in (e.g. Europe/Stockholm). Defaults to UTC."),
		),
		mcp.WithBoolean("enabled",
			mcp.Description("Whether the schedule fires. Defaults to true; manual runs work either way."),
		),
		mcp.WithString("image_ref",
			mcp.Description("Optional OCI image reference overriding the app's image for this job."),
		),
		mcp.WithString("command",
			mcp.Description("Optional container command override as a JSON string array in exec form, e.g. [\"python\",\"manage.py\",\"cleanup\"]. Never a shell."),
		),
		mcp.WithString("env",
			mcp.Description("Optional extra environment as comma-separated KEY=VALUE pairs, layered over the app's env (job keys win). Must not collide with platform-injected MDB_* or DATABASE_URL."),
		),
		mcp.WithNumber("max_retries",
			mcp.Description("Retries after a failed or timed out run (0-5). Defaults to 0."),
		),
		mcp.WithNumber("retry_backoff_seconds",
			mcp.Description("Delay before a retry attempt (10-3600). Defaults to 60."),
		),
		mcp.WithNumber("max_runtime_seconds",
			mcp.Description("Hard runtime cap per run, after which the container is killed and the run records timed_out (10-21600). Defaults to 3600."),
		),
		mcp.WithNumber("concurrency_cap",
			mcp.Description("Maximum simultaneously active runs (1-5); a cron fire at the cap records a skipped invocation. Defaults to 1."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm creation."),
		),
	), handleJobsCreate(c))

	s.AddTool(mcp.NewTool("jobs_list",
		mcp.WithDescription("List the job definitions of a hosted app service, including schedules, overrides, retry policy, and next/last run times."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
	), handleJobsList(c))

	s.AddTool(mcp.NewTool("jobs_run",
		mcp.WithDescription("Trigger a manual run of a job on a Running app service. Returns the queued invocation; execution is asynchronous, so poll jobs_invocations until the status is terminal (succeeded, failed, timed_out). Fails with a conflict when the job is already at its concurrency cap."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID (must be Running)."),
		),
		mcp.WithString("job_id",
			mcp.Required(),
			mcp.Description("Job UUID (from jobs_list)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the run (starts a container on the app VM)."),
		),
	), handleJobsRun(c))

	s.AddTool(mcp.NewTool("jobs_invocations",
		mcp.WithDescription("Inspect a job's run history. Without invocation_id, lists invocations newest first (status, trigger, attempt, duration, exit code, captured log tail). With invocation_id, returns that single invocation in full."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithString("job_id",
			mcp.Required(),
			mcp.Description("Job UUID (from jobs_list)."),
		),
		mcp.WithString("invocation_id",
			mcp.Description("Optional invocation UUID to fetch one run instead of the list."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Page size when listing (1-200, default 50)."),
		),
		mcp.WithNumber("offset",
			mcp.Description("Rows to skip when listing (default 0)."),
		),
	), handleJobsInvocations(c))

	s.AddTool(mcp.NewTool("jobs_invocation_logs",
		mcp.WithDescription("Fetch the full logs of one job invocation from the app VM (its transient systemd unit). Requests the fetch and polls internally up to 60 seconds. Invocations that never ran (skipped) have no logs."),
		mcp.WithString("app_service_id",
			mcp.Required(),
			mcp.Description("App service UUID."),
		),
		mcp.WithString("job_id",
			mcp.Required(),
			mcp.Description("Job UUID (from jobs_list)."),
		),
		mcp.WithString("invocation_id",
			mcp.Required(),
			mcp.Description("Invocation UUID (from jobs_invocations)."),
		),
		mcp.WithNumber("lines",
			mcp.Description("Number of trailing log lines to fetch (1-1000, default 200)."),
		),
	), handleJobsInvocationLogs(c))
}

func handleJobsCreate(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		appServiceID, _ := args["app_service_id"].(string)
		name, _ := args["name"].(string)
		if appServiceID == "" || name == "" {
			return mcp.NewToolResultError("app_service_id and name are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("creating job %q on app service %s", name, appServiceID)); denied != nil {
			return denied, nil
		}

		createReq := foundrydb.AppJobCreateRequest{Name: name}
		if cron, ok := args["schedule_cron"].(string); ok && cron != "" {
			createReq.ScheduleCron = &cron
		}
		if tz, ok := args["timezone"].(string); ok && tz != "" {
			createReq.Timezone = tz
		}
		if enabled, ok := args["enabled"].(bool); ok {
			createReq.Enabled = &enabled
		}
		if imageRef, ok := args["image_ref"].(string); ok && imageRef != "" {
			createReq.ImageRef = &imageRef
		}
		if cmd, ok := args["command"].(string); ok && cmd != "" {
			var argv []string
			if err := json.Unmarshal([]byte(cmd), &argv); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("command must be a JSON string array, e.g. [\"python\",\"main.py\"]: %s", err.Error())), nil
			}
			createReq.Command = argv
		}
		if env, ok := args["env"].(string); ok && env != "" {
			createReq.Env = parseEnvPairs(env)
		}
		if v, ok := args["max_retries"].(float64); ok {
			n := int(v)
			createReq.MaxRetries = &n
		}
		if v, ok := args["retry_backoff_seconds"].(float64); ok && v > 0 {
			n := int(v)
			createReq.RetryBackoffSeconds = &n
		}
		if v, ok := args["max_runtime_seconds"].(float64); ok && v > 0 {
			n := int(v)
			createReq.MaxRuntimeSeconds = &n
		}
		if v, ok := args["concurrency_cap"].(float64); ok && v > 0 {
			n := int(v)
			createReq.ConcurrencyCap = &n
		}

		job, err := c.CreateAppJob(ctx, appServiceID, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(job)), nil
	}
}

func handleJobsList(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		appServiceID, _ := args["app_service_id"].(string)
		if appServiceID == "" {
			return mcp.NewToolResultError("app_service_id is required"), nil
		}
		jobs, err := c.ListAppJobs(ctx, appServiceID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(jobs) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No jobs found on app service %s.", appServiceID)), nil
		}
		return mcp.NewToolResultText(formatJSON(jobs)), nil
	}
}

func handleJobsRun(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		appServiceID, _ := args["app_service_id"].(string)
		jobID, _ := args["job_id"].(string)
		if appServiceID == "" || jobID == "" {
			return mcp.NewToolResultError("app_service_id and job_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("running job %s on app service %s", jobID, appServiceID)); denied != nil {
			return denied, nil
		}
		inv, err := c.RunAppJob(ctx, appServiceID, jobID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Job run queued. Poll jobs_invocations with invocation_id %s until the status is terminal.\n\n%s",
			inv.ID, formatJSON(inv))), nil
	}
}

func handleJobsInvocations(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		appServiceID, _ := args["app_service_id"].(string)
		jobID, _ := args["job_id"].(string)
		if appServiceID == "" || jobID == "" {
			return mcp.NewToolResultError("app_service_id and job_id are required"), nil
		}

		if invocationID, ok := args["invocation_id"].(string); ok && invocationID != "" {
			inv, err := c.GetAppJobInvocation(ctx, appServiceID, jobID, invocationID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if inv == nil {
				return mcp.NewToolResultText(fmt.Sprintf("Invocation %s not found.", invocationID)), nil
			}
			return mcp.NewToolResultText(formatJSON(inv)), nil
		}

		limit := 0
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		offset := 0
		if v, ok := args["offset"].(float64); ok && v > 0 {
			offset = int(v)
		}
		invocations, err := c.ListAppJobInvocations(ctx, appServiceID, jobID, limit, offset)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(invocations) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No invocations found for job %s.", jobID)), nil
		}
		return mcp.NewToolResultText(formatJSON(invocations)), nil
	}
}

func handleJobsInvocationLogs(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		appServiceID, _ := args["app_service_id"].(string)
		jobID, _ := args["job_id"].(string)
		invocationID, _ := args["invocation_id"].(string)
		if appServiceID == "" || jobID == "" || invocationID == "" {
			return mcp.NewToolResultError("app_service_id, job_id and invocation_id are required"), nil
		}

		lines := 0
		if v, ok := args["lines"].(float64); ok && v > 0 {
			lines = int(v)
			if lines > 1000 {
				lines = 1000
			}
		}

		taskID, err := c.RequestAppJobInvocationLogs(ctx, appServiceID, jobID, invocationID, lines)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to request invocation logs: %s", err.Error())), nil
		}

		// Poll for the async log fetch result with a 60-second timeout (12 attempts * 5 seconds).
		for attempt := 0; attempt < 12; attempt++ {
			time.Sleep(5 * time.Second)

			result, err := c.GetAppJobInvocationLogs(ctx, appServiceID, jobID, invocationID, taskID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to poll invocation logs: %s", err.Error())), nil
			}
			switch result.Status {
			case "COMPLETED":
				return mcp.NewToolResultText(formatJSON(result)), nil
			case "FAILED", "TIMEOUT", "CANCELLED":
				return mcp.NewToolResultError(fmt.Sprintf("invocation log fetch failed: %s", result.ErrorMessage)), nil
			}
			// Still pending, continue polling
		}

		return mcp.NewToolResultError("timed out waiting for invocation logs after 60 seconds"), nil
	}
}
