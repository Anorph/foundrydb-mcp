package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterFileServiceTools registers tools for files services: S3-compatible
// object storage buckets with scoped access keys, presigned URLs, a console
// object browser, and quota enforcement. A files service can also be attached
// to a hosted app, which injects S3_* connection variables automatically.
func RegisterFileServiceTools(s *server.MCPServer, cfg foundrydb.Config) {
	s.AddTool(mcp.NewTool("list_file_services",
		mcp.WithDescription("List the files services (S3-compatible object storage buckets) visible to the authenticated user."),
	), handleListFileServices(cfg))

	s.AddTool(mcp.NewTool("get_file_service",
		mcp.WithDescription("Get a files service: status, bucket name and S3 endpoint, quotas, and measured storage usage."),
		mcp.WithString("file_service_id",
			mcp.Required(),
			mcp.Description("Files service UUID."),
		),
	), handleGetFileService(cfg))

	s.AddTool(mcp.NewTool("create_file_service",
		mcp.WithDescription("Create a files service: an S3-compatible bucket with versioning and server-side encryption. Use create_files_access_key for S3 credentials and presign_files_url for browser-direct uploads/downloads. Provisioning is asynchronous; poll get_file_service until status is Running."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Service name (3-63 chars, DNS-compatible), unique to the owner."),
		),
		mcp.WithString("zone",
			mcp.Description("UpCloud zone selecting the bucket's region (e.g. se-sto1). Defaults to the platform default; only zones in the europe and us peering regions are supported."),
		),
		mcp.WithNumber("quota_gb_soft",
			mcp.Description("Optional stored-GB threshold that triggers a notification when crossed (default 400)."),
		),
		mcp.WithNumber("quota_gb_hard",
			mcp.Description("Optional stored-GB ceiling: once exceeded, upload presigning and key creation are blocked until usage drops (default 500)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm creation (provisions a billable bucket)."),
		),
	), handleCreateFileService(cfg))

	s.AddTool(mcp.NewTool("delete_file_service",
		mcp.WithDescription("Delete a files service: the bucket contents, the bucket, and every access key minted for it are removed. This is irreversible."),
		mcp.WithString("file_service_id",
			mcp.Required(),
			mcp.Description("Files service UUID."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm deletion."),
		),
	), handleDeleteFileService(cfg))

	s.AddTool(mcp.NewTool("create_files_access_key",
		mcp.WithDescription("Mint a scoped S3 credential for a files service. The secret access key is returned exactly once in this response and can never be retrieved again, so store it immediately. Use the key with any S3 client against the service's bucket endpoint. Blocked while the service is over its hard storage quota."),
		mcp.WithString("file_service_id",
			mcp.Required(),
			mcp.Description("Files service UUID (must be Running)."),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("User-facing label for the key."),
		),
		mcp.WithString("permissions",
			mcp.Required(),
			mcp.Description("Access level the key grants: read, write, or readwrite."),
		),
		mcp.WithString("prefix",
			mcp.Description("Optional object key prefix scoping the key (e.g. uploads/). Empty grants the whole bucket. Must not start with a slash."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm minting the credential."),
		),
	), handleCreateFilesAccessKey(cfg))

	s.AddTool(mcp.NewTool("list_files_access_keys",
		mcp.WithDescription("List a files service's access keys: name, access key ID, prefix scope, permissions, status, and last use. Secret halves are never included."),
		mcp.WithString("file_service_id",
			mcp.Required(),
			mcp.Description("Files service UUID."),
		),
	), handleListFilesAccessKeys(cfg))

	s.AddTool(mcp.NewTool("revoke_files_access_key",
		mcp.WithDescription("Revoke a files access key. The provider credential is deleted and the stored secret destroyed; revocation is permanent, mint a new key to restore access."),
		mcp.WithString("file_service_id",
			mcp.Required(),
			mcp.Description("Files service UUID."),
		),
		mcp.WithString("key_id",
			mcp.Required(),
			mcp.Description("Access key UUID to revoke (the id field from list_files_access_keys, not the access key ID)."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the revocation (cannot be undone)."),
		),
	), handleRevokeFilesAccessKey(cfg))

	s.AddTool(mcp.NewTool("presign_files_url",
		mcp.WithDescription("Issue a presigned S3 URL for one object in a files service's bucket. The URL works directly against the bucket endpoint without credentials until it expires (default 15 minutes, max 7 days). PUT presigning is blocked while the service is over its hard storage quota."),
		mcp.WithString("file_service_id",
			mcp.Required(),
			mcp.Description("Files service UUID (must be Running)."),
		),
		mcp.WithString("method",
			mcp.Required(),
			mcp.Description("HTTP method to presign: GET, PUT, HEAD, or DELETE (uppercase)."),
		),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Object key the URL operates on (e.g. uploads/report.pdf)."),
		),
		mcp.WithNumber("expires_seconds",
			mcp.Description("URL lifetime in seconds (default 900, max 604800)."),
		),
		mcp.WithString("content_type",
			mcp.Description("Optional Content-Type signed into a PUT URL; the upload must then send the same header."),
		),
	), handlePresignFilesURL(cfg))

	s.AddTool(mcp.NewTool("list_files_objects",
		mcp.WithDescription("List one page of a files service's bucket objects (key, size, last modified). Pass the returned next_cursor as cursor to continue the listing."),
		mcp.WithString("file_service_id",
			mcp.Required(),
			mcp.Description("Files service UUID (must be Running)."),
		),
		mcp.WithString("prefix",
			mcp.Description("Optional key prefix filter (e.g. uploads/)."),
		),
		mcp.WithString("cursor",
			mcp.Description("Optional continuation cursor from the previous page."),
		),
		mcp.WithNumber("max",
			mcp.Description("Optional page size (default 100, max 1000)."),
		),
	), handleListFilesObjects(cfg))

	s.AddTool(mcp.NewTool("delete_files_object",
		mcp.WithDescription("Delete one object from a files service's bucket. This is irreversible (subject to bucket versioning)."),
		mcp.WithString("file_service_id",
			mcp.Required(),
			mcp.Description("Files service UUID."),
		),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Object key to delete."),
		),
		mcp.WithBoolean("confirm",
			mcp.Description("Set true to confirm the deletion."),
		),
	), handleDeleteFilesObject(cfg))
}

func handleListFileServices(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := apiGet(ctx, cfg, "/files-services")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result == nil {
			return mcp.NewToolResultText("No files services found."), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleGetFileService(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["file_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("file_service_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/files-services/"+id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result == nil {
			return mcp.NewToolResultText(fmt.Sprintf("Files service %s not found.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleCreateFileService(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		name, _ := args["name"].(string)
		if name == "" {
			return mcp.NewToolResultError("name is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("creating files service %q", name)); denied != nil {
			return denied, nil
		}
		body := map[string]interface{}{"name": name}
		if zone, ok := args["zone"].(string); ok && zone != "" {
			body["zone"] = zone
		}
		if soft, ok := args["quota_gb_soft"].(float64); ok && soft > 0 {
			body["quota_gb_soft"] = int(soft)
		}
		if hard, ok := args["quota_gb_hard"].(float64); ok && hard > 0 {
			body["quota_gb_hard"] = int(hard)
		}
		result, err := apiPost(ctx, cfg, "/files-services", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleDeleteFileService(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["file_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("file_service_id is required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting files service %s (removes the bucket and all objects)", id)); denied != nil {
			return denied, nil
		}
		if _, err := apiDelete(ctx, cfg, "/files-services/"+id); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Files service %s deletion scheduled.", id)), nil
	}
}

func handleCreateFilesAccessKey(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["file_service_id"].(string)
		name, _ := args["name"].(string)
		permissions, _ := args["permissions"].(string)
		if id == "" || name == "" || permissions == "" {
			return mcp.NewToolResultError("file_service_id, name and permissions are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("minting %s access key %q for files service %s", permissions, name, id)); denied != nil {
			return denied, nil
		}
		body := map[string]interface{}{
			"name":        name,
			"permissions": permissions,
		}
		if prefix, ok := args["prefix"].(string); ok && prefix != "" {
			body["prefix"] = prefix
		}
		result, err := apiPost(ctx, cfg, "/files-services/"+id+"/access-keys", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(
			"Store the secret_access_key now: it is shown only in this response and cannot be retrieved again.\n" +
				formatJSON(result)), nil
	}
}

func handleListFilesAccessKeys(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["file_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("file_service_id is required"), nil
		}
		result, err := apiGet(ctx, cfg, "/files-services/"+id+"/access-keys")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result == nil {
			return mcp.NewToolResultText(fmt.Sprintf("No access keys found for files service %s.", id)), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleRevokeFilesAccessKey(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["file_service_id"].(string)
		keyID, _ := args["key_id"].(string)
		if id == "" || keyID == "" {
			return mcp.NewToolResultError("file_service_id and key_id are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("revoking access key %s of files service %s", keyID, id)); denied != nil {
			return denied, nil
		}
		if _, err := apiDelete(ctx, cfg, "/files-services/"+id+"/access-keys/"+keyID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Access key %s revoked.", keyID)), nil
	}
}

func handlePresignFilesURL(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["file_service_id"].(string)
		method, _ := args["method"].(string)
		key, _ := args["key"].(string)
		if id == "" || method == "" || key == "" {
			return mcp.NewToolResultError("file_service_id, method and key are required"), nil
		}
		body := map[string]interface{}{
			"method": method,
			"key":    key,
		}
		if expires, ok := args["expires_seconds"].(float64); ok && expires > 0 {
			body["expires_seconds"] = int(expires)
		}
		if ct, ok := args["content_type"].(string); ok && ct != "" {
			body["content_type"] = ct
		}
		result, err := apiPost(ctx, cfg, "/files-services/"+id+"/presign", body)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleListFilesObjects(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["file_service_id"].(string)
		if id == "" {
			return mcp.NewToolResultError("file_service_id is required"), nil
		}

		path := "/files-services/" + id + "/objects"
		sep := "?"
		if prefix, ok := args["prefix"].(string); ok && prefix != "" {
			path += sep + "prefix=" + prefix
			sep = "&"
		}
		if cursor, ok := args["cursor"].(string); ok && cursor != "" {
			path += sep + "cursor=" + cursor
			sep = "&"
		}
		if max, ok := args["max"].(float64); ok && max > 0 {
			path += sep + "max=" + itoa(int(max))
		}
		_ = strings.Contains(path, "?") // keep sep used
		result, err := apiGet(ctx, cfg, path)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result == nil {
			return mcp.NewToolResultText("No objects found."), nil
		}
		return mcp.NewToolResultText(formatJSON(result)), nil
	}
}

func handleDeleteFilesObject(cfg foundrydb.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id, _ := args["file_service_id"].(string)
		key, _ := args["key"].(string)
		if id == "" || key == "" {
			return mcp.NewToolResultError("file_service_id and key are required"), nil
		}
		if denied := requireConfirmFlag(args, fmt.Sprintf("deleting object %q from files service %s", key, id)); denied != nil {
			return denied, nil
		}
		if _, err := apiDelete(ctx, cfg, "/files-services/"+id+"/objects/"+key); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Object %q deleted from files service %s.", key, id)), nil
	}
}
