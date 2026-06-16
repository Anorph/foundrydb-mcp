package main

import (
	"fmt"
	"os"

	"github.com/anorph/foundrydb-mcp/config"
	"github.com/anorph/foundrydb-mcp/tools"
	"github.com/anorph/foundrydb-sdk-go/foundrydb"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	cfg := config.Load()
	sdkCfg := foundrydb.Config{
		APIURL:   cfg.APIURL,
		Token:    cfg.Token,
		Username: cfg.Username,
		Password: cfg.Password,
	}
	apiClient := foundrydb.New(sdkCfg)

	s := server.NewMCPServer(
		"foundrydb",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	tools.RegisterServiceTools(s, apiClient, sdkCfg)
	tools.RegisterUserTools(s, apiClient)
	tools.RegisterBackupTools(s, apiClient)
	tools.RegisterMonitoringTools(s, sdkCfg)
	tools.RegisterScalingTools(s, sdkCfg)
	tools.RegisterRecoveryTools(s, apiClient, sdkCfg)
	tools.RegisterPerformanceTools(s, sdkCfg)
	tools.RegisterMaintenanceTools(s, sdkCfg)
	tools.RegisterDataPipelineTools(s, apiClient)
	tools.RegisterWebhookTools(s, sdkCfg)
	tools.RegisterBillingTools(s, sdkCfg)
	tools.RegisterAppServiceTools(s, apiClient, sdkCfg)
	tools.RegisterAppJobTools(s, apiClient)
	tools.RegisterQueueTools(s, apiClient)
	tools.RegisterFileServiceTools(s, sdkCfg)
	tools.RegisterOrgWebhookTools(s, apiClient)
	tools.RegisterEdgeTools(s, apiClient)
	tools.RegisterAIDataTools(s, sdkCfg)
	tools.RegisterAIActionTools(s, sdkCfg)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
