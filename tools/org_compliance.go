package tools

import (
	"context"
	"encoding/json"

	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterComplianceTools registers MCP tools for generating and retrieving
// signed compliance evidence packets. Packets carry a detached Ed25519 signature
// that auditors can verify against the platform's published public key set.
func RegisterComplianceTools(s *server.MCPServer, c *foundrydb.Client) {
	s.AddTool(mcp.NewTool("generate_compliance_report",
		mcp.WithDescription("Generate a signed, immutable compliance evidence packet (SOC2 or GDPR Article 30 ROPA) for an organization. The packet maps platform operational data (residency, encryption, backups, access control, change management, audit trail) to framework controls and carries a detached Ed25519 signature verifiable against the published public key. Returns the report id, the packet, and the signature."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
		mcp.WithString("framework", mcp.Required(), mcp.Description("soc2 or gdpr_ropa")),
	), handleGenerateComplianceReport(c))

	s.AddTool(mcp.NewTool("list_compliance_reports",
		mcp.WithDescription("List the signed compliance evidence packets generated for an organization, with framework, generation time, and signing key id."),
		mcp.WithString("organization_id", mcp.Required(), mcp.Description("Organization UUID")),
	), handleListComplianceReports(c))

	s.AddTool(mcp.NewTool("get_compliance_signing_keys",
		mcp.WithDescription("Return the platform's published Ed25519 public key set used to verify compliance evidence packets. Auditors use these keys to confirm packet provenance."),
	), handleGetComplianceSigningKeys(c))
}

func handleGenerateComplianceReport(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		orgID, _ := args["organization_id"].(string)
		framework, _ := args["framework"].(string)
		if orgID == "" || framework == "" {
			return mcp.NewToolResultError("organization_id and framework are required"), nil
		}
		report, err := c.GenerateComplianceReport(ctx, orgID, framework)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		jsonBytes, err := json.Marshal(report)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
}

func handleListComplianceReports(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		orgID, _ := req.GetArguments()["organization_id"].(string)
		if orgID == "" {
			return mcp.NewToolResultError("organization_id is required"), nil
		}
		reports, err := c.ListComplianceReports(ctx, orgID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		jsonBytes, err := json.Marshal(reports)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
}

func handleGetComplianceSigningKeys(c *foundrydb.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		keys, err := c.ComplianceSigningKeys(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		jsonBytes, err := json.Marshal(keys)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(jsonBytes)), nil
	}
}
