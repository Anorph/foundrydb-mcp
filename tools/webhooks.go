package tools

import (
	"context"
	"fmt"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterWebhookTools registers customer webhook tools. Webhooks are
// user-scoped (top-level /webhooks routes), so cfg drives the direct HTTP
// calls and no organization resolution is needed.
func RegisterWebhookTools(s *server.MCPServer, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("list_webhooks",
		mcp.WithDescription("List webhook endpoints. A webhook delivers platform events (service lifecycle, backups, alerts) to a URL you control."),
	), handleListWebhooks(cfg))

	s.AddTool(mcp.NewTool("get_webhook",
		mcp.WithDescription("Get a webhook endpoint's detail: its URL, subscribed event types, and active state."),
		mcp.WithString("webhook_id",
			mcp.Required(),
			mcp.Description("Webhook UUID"),
		),
	), handleGetWebhook(cfg))

	s.AddTool(mcp.NewTool("create_webhook",
		mcp.WithDescription("Create a webhook endpoint. The platform POSTs a signed JSON payload to the URL whenever a subscribed event fires. The response includes a signing secret (shown once) used to verify deliveries. Valid event types: service.running, service.error, service.stopped, service.deleted, backup.completed, backup.failed, alert.fired, alert.resolved."),
		mcp.WithString("url",
			mcp.Required(),
			mcp.Description("HTTPS URL that will receive event POSTs"),
		),
		mcp.WithString("events",
			mcp.Required(),
			mcp.Description("Comma-separated event types to subscribe to, e.g. \"service.running,backup.failed\". Use the full list to receive everything."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleCreateWebhook(cfg))

	s.AddTool(mcp.NewTool("test_webhook",
		mcp.WithDescription("Send a test event to a webhook endpoint to verify it is reachable and your receiver handles the payload. Dispatched asynchronously; check list_webhook_deliveries for the result."),
		mcp.WithString("webhook_id",
			mcp.Required(),
			mcp.Description("Webhook UUID"),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleTestWebhook(cfg))

	s.AddTool(mcp.NewTool("list_webhook_deliveries",
		mcp.WithDescription("List recent delivery attempts for a webhook endpoint, including response status and any error. Use this to debug why events are not arriving."),
		mcp.WithString("webhook_id",
			mcp.Required(),
			mcp.Description("Webhook UUID"),
		),
	), handleListWebhookDeliveries(cfg))

	s.AddTool(mcp.NewTool("delete_webhook",
		mcp.WithDescription("Delete a webhook endpoint. The platform stops delivering events to it. Reversible by recreating the webhook."),
		mcp.WithString("webhook_id",
			mcp.Required(),
			mcp.Description("Webhook UUID"),
		),
		mcp.WithBoolean("confirm",
			mcp.Description(confirmFlagDescription),
		),
	), handleDeleteWebhook(cfg))
}

func handleListWebhooks(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := apiGet(ctx, cfg, "/webhooks")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetWebhook(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		webhookID, _ := req.GetArguments()["webhook_id"].(string)
		if webhookID == "" {
			return mcp.NewToolResultError("webhook_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/webhooks/"+webhookID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleCreateWebhook(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		url, _ := args["url"].(string)
		events, _ := args["events"].(string)
		if url == "" || events == "" {
			return mcp.NewToolResultError("url and events are required"), nil
		}

		if denied := requireConfirmFlag(args, fmt.Sprintf("creating a webhook to %s", url)); denied != nil {
			return denied, nil
		}

		body := map[string]interface{}{
			"url":    url,
			"events": splitAndTrim(events),
		}
		result, err := apiPost(ctx, cfg, "/webhooks", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Webhook created. Store the signing secret now; it is not shown again.\n\n%s",
			formatJSON(result),
		)), nil
	}
}

func handleTestWebhook(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		webhookID, _ := args["webhook_id"].(string)
		if webhookID == "" {
			return mcp.NewToolResultError("webhook_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("sending a test event to webhook %s", webhookID)); denied != nil {
			return denied, nil
		}
		result, err := apiPost(ctx, cfg, "/webhooks/"+webhookID+"/test", map[string]interface{}{})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Test event dispatched.\n\n%s", formatJSON(result))), nil
	}
}

func handleListWebhookDeliveries(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		webhookID, _ := req.GetArguments()["webhook_id"].(string)
		if webhookID == "" {
			return mcp.NewToolResultError("webhook_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/webhooks/"+webhookID+"/deliveries")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleDeleteWebhook(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		webhookID, _ := args["webhook_id"].(string)
		if webhookID == "" {
			return mcp.NewToolResultError("webhook_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting webhook %s", webhookID)); denied != nil {
			return denied, nil
		}
		result, err := apiDelete(ctx, cfg, "/webhooks/"+webhookID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Webhook %s deleted.\n\n%s", webhookID, formatJSON(result))), nil
	}
}
