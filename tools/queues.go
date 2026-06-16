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

// RegisterQueueTools registers tools for message queues hosted on PostgreSQL
// managed services. Queue messages live in the customer's database,
// transactional with their data; consumers (typically hosted apps) read them
// directly over their injected connection, while these tools cover the
// management plane plus brokered enqueue and depth inspection.
func RegisterQueueTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("queues_create",
		mcp.WithDescription("Create a message queue on a PostgreSQL managed service. The queue's tables are created inside the customer database (default defaultdb), transactional with their data. Provisioning is asynchronous: the queue starts in Provisioning; poll queues_list until it is Active before enqueueing. A service supports up to 50 queues."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("PostgreSQL managed service UUID (must be Running)."),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Queue name (lowercase alphanumerics, hyphens, underscores; max 128 chars), unique per service."),
		),
		mcp.WithString("database_name",
			mcp.Description("Database the queue lives in. Defaults to defaultdb."),
		),
		mcp.WithNumber("visibility_timeout_seconds",
			mcp.Description("How long a claimed message stays invisible before a crashed consumer's claim expires and it is redelivered (1-43200). Defaults to 30."),
		),
		mcp.WithNumber("max_attempts",
			mcp.Description("Deliveries a message gets before it is dropped or dead-lettered (1-100). Defaults to 5."),
		),
		mcp.WithBoolean("dlq_enabled",
			mcp.Description("Whether exhausted messages move to a dead-letter queue instead of being dropped. Defaults to true."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm creation (creates schema objects in the customer database)."),
		),
	), handleQueuesCreate(c))

	s.AddTool(mcp.NewTool("queues_list",
		mcp.WithDescription("List the message queues of a PostgreSQL managed service: name, database, settings, and provisioning status (Pending, Provisioning, Active, Deprovisioning, Failed)."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("PostgreSQL managed service UUID."),
		),
	), handleQueuesList(c))

	s.AddTool(mcp.NewTool("queues_delete",
		mcp.WithDescription("Delete a message queue. The queue's tables and all pending messages are removed from the customer database. Teardown is asynchronous: the queue moves to Deprovisioning and disappears from queues_list once the agent confirms removal. This is irreversible."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("PostgreSQL managed service UUID."),
		),
		mcp.WithString("queue_name",
			mcp.Required(),
			mcp.Description("Queue name (from queues_list)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm deletion (destroys pending messages)."),
		),
	), handleQueuesDelete(c))

	s.AddTool(mcp.NewTool("queues_enqueue",
		mcp.WithDescription("Enqueue a batch of up to 100 messages onto an Active queue. The batch is written in one transaction on the database VM (all-or-nothing) through a brokered agent task; this tool submits the batch and polls internally up to 60 seconds, returning the assigned message IDs in request order. Suits low-rate external producers; hosted apps should enqueue directly over their injected database connection."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("PostgreSQL managed service UUID."),
		),
		mcp.WithString("queue_name",
			mcp.Required(),
			mcp.Description("Queue name (must be Active)."),
		),
		mcp.WithString("messages",
			mcp.Required(),
			mcp.Description("JSON array of messages. Each element is either {\"payload\": {...}, \"delay_seconds\": n} or a bare JSON payload object (delay 0). delay_seconds postpones first visibility (0-43200). Payloads up to 256 KB each."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the enqueue (writes messages into the customer database)."),
		),
	), handleQueuesEnqueue(c))

	s.AddTool(mcp.NewTool("queues_stats",
		mcp.WithDescription("Get a depth snapshot of an Active queue: ready, in-flight, and dead-lettered message counts plus the age of the oldest ready message. Runs as a brokered agent task on the database VM; this tool submits the request and polls internally up to 60 seconds."),
		mcp.WithString("service_id",
			mcp.Required(),
			mcp.Description("PostgreSQL managed service UUID."),
		),
		mcp.WithString("queue_name",
			mcp.Required(),
			mcp.Description("Queue name (must be Active)."),
		),
	), handleQueuesStats(c))
}

func handleQueuesCreate(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		name, _ := args["name"].(string)
		if serviceID == "" || name == "" {
			return mcp.NewToolResultError("service_id and name are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("creating queue %q on service %s", name, serviceID)); denied != nil {
			return denied, nil
		}

		createReq := foundrydb.QueueCreateRequest{Name: name}
		if db, ok := args["database_name"].(string); ok && db != "" {
			createReq.DatabaseName = db
		}
		if v, ok := args["visibility_timeout_seconds"].(float64); ok && v > 0 {
			n := int(v)
			createReq.VisibilityTimeoutSeconds = &n
		}
		if v, ok := args["max_attempts"].(float64); ok && v > 0 {
			n := int(v)
			createReq.MaxAttempts = &n
		}
		if dlq, ok := args["dlq_enabled"].(bool); ok {
			createReq.DLQEnabled = &dlq
		}

		queue, err := c.CreateQueue(ctx, serviceID, createReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Queue created. Provisioning is asynchronous; poll queues_list until the status is Active.\n\n%s",
			formatJSON(queue))), nil
	}
}

func handleQueuesList(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		if serviceID == "" {
			return mcp.NewToolResultError("service_id is required"), nil
		}
		queues, err := c.ListQueues(ctx, serviceID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(queues) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No queues found on service %s.", serviceID)), nil
		}
		return mcp.NewToolResultText(formatJSON(queues)), nil
	}
}

func handleQueuesDelete(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		queueName, _ := args["queue_name"].(string)
		if serviceID == "" || queueName == "" {
			return mcp.NewToolResultError("service_id and queue_name are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting queue %q on service %s (destroys pending messages)", queueName, serviceID)); denied != nil {
			return denied, nil
		}
		queue, err := c.DeleteQueue(ctx, serviceID, queueName)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if queue == nil {
			return mcp.NewToolResultText(fmt.Sprintf("Queue %q not found on service %s (already deleted).", queueName, serviceID)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"Queue deletion scheduled. The queue disappears from queues_list once teardown completes.\n\n%s",
			formatJSON(queue))), nil
	}
}

// parseQueueMessages decodes the messages tool argument: a JSON array whose
// elements are either {"payload": ..., "delay_seconds": ...} envelopes or bare
// payload objects.
func parseQueueMessages(raw string) ([]foundrydb.QueueEnqueueMessage, error) {
	var elements []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &elements); err != nil {
		return nil, fmt.Errorf("messages must be a JSON array: %w", err)
	}
	if len(elements) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}
	out := make([]foundrydb.QueueEnqueueMessage, 0, len(elements))
	for i, el := range elements {
		var msg foundrydb.QueueEnqueueMessage
		if err := json.Unmarshal(el, &msg); err == nil && msg.Payload != nil {
			out = append(out, msg)
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(el, &payload); err != nil || payload == nil {
			return nil, fmt.Errorf("messages[%d] must be a JSON object (a payload, or {\"payload\": ..., \"delay_seconds\": ...})", i)
		}
		out = append(out, foundrydb.QueueEnqueueMessage{Payload: payload})
	}
	return out, nil
}

func handleQueuesEnqueue(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		queueName, _ := args["queue_name"].(string)
		messagesRaw, _ := args["messages"].(string)
		if serviceID == "" || queueName == "" || messagesRaw == "" {
			return mcp.NewToolResultError("service_id, queue_name and messages are required"), nil
		}
		messages, err := parseQueueMessages(messagesRaw)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("enqueueing %d message(s) onto queue %q on service %s", len(messages), queueName, serviceID)); denied != nil {
			return denied, nil
		}

		taskID, err := c.EnqueueQueueMessages(ctx, serviceID, queueName, foundrydb.QueueEnqueueRequest{Messages: messages})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to submit enqueue batch: %s", err.Error())), nil
		}

		// Poll for the async enqueue result with a 60-second timeout (12 attempts * 5 seconds).
		for attempt := 0; attempt < 12; attempt++ {
			time.Sleep(5 * time.Second)

			result, err := c.GetEnqueueResult(ctx, serviceID, queueName, taskID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("enqueue failed: %s", err.Error())), nil
			}
			if result.Status == "COMPLETED" {
				return mcp.NewToolResultText(formatJSON(result)), nil
			}
			// Still pending, continue polling
		}

		return mcp.NewToolResultError(fmt.Sprintf(
			"timed out waiting for enqueue result after 60 seconds; poll GET /managed-services/%s/queues/%s/messages?task_id=%s",
			serviceID, queueName, taskID)), nil
	}
}

func handleQueuesStats(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		serviceID, _ := args["service_id"].(string)
		queueName, _ := args["queue_name"].(string)
		if serviceID == "" || queueName == "" {
			return mcp.NewToolResultError("service_id and queue_name are required"), nil
		}

		taskID, err := c.RequestQueueStats(ctx, serviceID, queueName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to request queue stats: %s", err.Error())), nil
		}

		// Poll for the async stats result with a 60-second timeout (12 attempts * 5 seconds).
		for attempt := 0; attempt < 12; attempt++ {
			time.Sleep(5 * time.Second)

			result, err := c.GetQueueStats(ctx, serviceID, queueName, taskID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("queue stats failed: %s", err.Error())), nil
			}
			if result.Status == "COMPLETED" {
				return mcp.NewToolResultText(formatJSON(result)), nil
			}
			// Still pending, continue polling
		}

		return mcp.NewToolResultError("timed out waiting for queue stats after 60 seconds"), nil
	}
}
