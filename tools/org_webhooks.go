package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterOrgWebhookTools registers organization-scoped webhook endpoint tools
// and the platform events feed. These complement the user-scoped webhook tools:
// organization endpoints receive events for every service in the organization,
// and the events feed is the queryable history behind webhook deliveries.
func RegisterOrgWebhookTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("create_org_webhook",
		mcp.WithDescription("Register an organization webhook endpoint that receives signed event notifications (HMAC-SHA256) for every service in the organization. Returns the signing secret exactly once."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
		mcp.WithString("url", mcp.Required(), mcp.Description("HTTPS endpoint URL that receives event deliveries")),
		mcp.WithString("events", mcp.Description("Comma-separated event types to subscribe to (e.g. backup.failed,pipeline.running). Empty subscribes to all events.")),
	), handleCreateOrgWebhook(c))

	s.AddTool(mcp.NewTool("list_org_webhooks",
		mcp.WithDescription("List all webhook endpoints of an organization, with delivery health (total delivered/failed, consecutive failures, disabled state)."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
	), handleListOrgWebhooks(c))

	s.AddTool(mcp.NewTool("delete_org_webhook",
		mcp.WithDescription("Delete an organization webhook endpoint."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
		mcp.WithString("webhook_id", mcp.Required(), mcp.Description("Webhook endpoint UUID")),
	), handleDeleteOrgWebhook(c))

	s.AddTool(mcp.NewTool("test_org_webhook",
		mcp.WithDescription("Send a test event to an organization webhook endpoint to verify connectivity and signature handling."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
		mcp.WithString("webhook_id", mcp.Required(), mcp.Description("Webhook endpoint UUID")),
	), handleTestOrgWebhook(c))

	s.AddTool(mcp.NewTool("list_org_webhook_deliveries",
		mcp.WithDescription("List recent deliveries of an organization webhook endpoint, including status, attempt count, retry schedule, and response code."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
		mcp.WithString("webhook_id", mcp.Required(), mcp.Description("Webhook endpoint UUID")),
	), handleListOrgWebhookDeliveries(c))

	s.AddTool(mcp.NewTool("replay_org_webhook_delivery",
		mcp.WithDescription("Replay a failed organization webhook delivery: enqueues a fresh delivery re-sending the original payload."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
		mcp.WithString("webhook_id", mcp.Required(), mcp.Description("Webhook endpoint UUID")),
		mcp.WithString("delivery_id", mcp.Required(), mcp.Description("Delivery UUID to replay")),
	), handleReplayOrgWebhookDelivery(c))

	s.AddTool(mcp.NewTool("rotate_org_webhook_secret",
		mcp.WithDescription("Rotate the signing secret of an organization webhook endpoint. Returns the new secret exactly once; the old secret stops being used immediately."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
		mcp.WithString("webhook_id", mcp.Required(), mcp.Description("Webhook endpoint UUID")),
	), handleRotateOrgWebhookSecret(c))

	s.AddTool(mcp.NewTool("enable_org_webhook",
		mcp.WithDescription("Re-enable an organization webhook endpoint disabled manually or auto-disabled after persistent delivery failures. Clears the failure streak."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
		mcp.WithString("webhook_id", mcp.Required(), mcp.Description("Webhook endpoint UUID")),
	), handleEnableOrgWebhook(c))

	s.AddTool(mcp.NewTool("list_events",
		mcp.WithDescription("Query the platform event feed (service lifecycle, backups, alerts, maintenance, pipelines) with cursor pagination."),
		mcp.WithString("event_type", mcp.Description("Filter to one event type (e.g. backup.failed, service.running, alert.fired, maintenance.completed, pipeline.failed)")),
		mcp.WithString("cursor", mcp.Description("Pagination cursor from a previous page's next_cursor")),
		mcp.WithString("limit", mcp.Description("Page size (default 50, max 200)")),
	), handleListEvents(c))
}

func handleCreateOrgWebhook(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		url, _ := args["url"].(string)
		if orgID == "" || url == "" {
			return mcp.NewToolResultError("organization_id and url are required"), nil
		}
		createReq := foundrydb.CreateWebhookRequest{URL: url, Events: []string{}}
		if eventsStr, ok := args["events"].(string); ok && eventsStr != "" {
			for _, ev := range strings.Split(eventsStr, ",") {
				if trimmed := strings.TrimSpace(ev); trimmed != "" {
					createReq.Events = append(createReq.Events, trimmed)
				}
			}
		}
		endpoint, err := c.CreateOrgWebhook(ctx, orgID, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Webhook created.\nID: %s\nSigning secret (shown only once, store it now): %s\n\nDetails:\n%s",
			endpoint.ID, endpoint.Secret, formatJSON(endpoint),
		)), nil
	}
}

func handleListOrgWebhooks(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		orgID, _ := req.GetArguments()["organization_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("organization_id is required"), nil
		}
		webhooks, err := c.ListOrgWebhooks(ctx, orgID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(webhooks) == 0 {
			return mcp.NewToolResultText("No webhook endpoints registered for this organization."), nil
		}
		return mcp.NewToolResultText(formatJSON(webhooks)), nil
	}
}

func handleDeleteOrgWebhook(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		webhookID, _ := args["webhook_id"].(string)
		if orgID == "" || webhookID == "" {
			return mcp.NewToolResultError("organization_id and webhook_id are required"), nil
		}
		if err := c.DeleteOrgWebhook(ctx, orgID, webhookID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Webhook endpoint deleted."), nil
	}
}

func handleTestOrgWebhook(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		webhookID, _ := args["webhook_id"].(string)
		if orgID == "" || webhookID == "" {
			return mcp.NewToolResultError("organization_id and webhook_id are required"), nil
		}
		if err := c.TestOrgWebhook(ctx, orgID, webhookID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Test event enqueued. The delivery worker sends it within a few seconds; check deliveries for the outcome."), nil
	}
}

func handleListOrgWebhookDeliveries(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		webhookID, _ := args["webhook_id"].(string)
		if orgID == "" || webhookID == "" {
			return mcp.NewToolResultError("organization_id and webhook_id are required"), nil
		}
		deliveries, err := c.ListOrgWebhookDeliveries(ctx, orgID, webhookID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(deliveries) == 0 {
			return mcp.NewToolResultText("No deliveries recorded for this webhook endpoint."), nil
		}
		return mcp.NewToolResultText(formatJSON(deliveries)), nil
	}
}

func handleReplayOrgWebhookDelivery(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		webhookID, _ := args["webhook_id"].(string)
		deliveryID, _ := args["delivery_id"].(string)
		if orgID == "" || webhookID == "" || deliveryID == "" {
			return mcp.NewToolResultError("organization_id, webhook_id, and delivery_id are required"), nil
		}
		replayed, err := c.ReplayOrgWebhookDelivery(ctx, orgID, webhookID, deliveryID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Delivery replayed.\nNew delivery ID: %s\n\nDetails:\n%s", replayed.ID, formatJSON(replayed),
		)), nil
	}
}

func handleListEvents(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		opts := foundrydb.ListEventsOptions{}
		if et, ok := args["event_type"].(string); ok {
			opts.EventType = et
		}
		if cursorStr, ok := args["cursor"].(string); ok && cursorStr != "" {
			cursor, err := strconv.ParseInt(cursorStr, 10, 64)
			if err != nil {
				return mcp.NewToolResultError("cursor must be an integer"), nil
			}
			opts.Cursor = cursor
		}
		if limitStr, ok := args["limit"].(string); ok && limitStr != "" {
			limit, err := strconv.Atoi(limitStr)
			if err != nil {
				return mcp.NewToolResultError("limit must be an integer"), nil
			}
			opts.Limit = limit
		}
		page, err := c.ListEvents(ctx, opts)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(page.Events) == 0 {
			return mcp.NewToolResultText("No events found."), nil
		}
		result := formatJSON(page.Events)
		if page.NextCursor != nil {
			result += fmt.Sprintf("\n\nNext page cursor: %d", *page.NextCursor)
		}
		return mcp.NewToolResultText(result), nil
	}
}

func handleRotateOrgWebhookSecret(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		webhookID, _ := args["webhook_id"].(string)
		if orgID == "" || webhookID == "" {
			return mcp.NewToolResultError("organization_id and webhook_id are required"), nil
		}
		secret, err := c.RotateOrgWebhookSecret(ctx, orgID, webhookID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Secret rotated. New signing secret (shown only once, store it now): %s", secret,
		)), nil
	}
}

func handleEnableOrgWebhook(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		webhookID, _ := args["webhook_id"].(string)
		if orgID == "" || webhookID == "" {
			return mcp.NewToolResultError("organization_id and webhook_id are required"), nil
		}
		if err := c.EnableOrgWebhook(ctx, orgID, webhookID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("Webhook endpoint enabled."), nil
	}
}
