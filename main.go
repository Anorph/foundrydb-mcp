package main

import (
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/anorph/foundrydb-mcp/client"
	"github.com/anorph/foundrydb-mcp/config"
	"github.com/anorph/foundrydb-mcp/tools"
)

func main() {
	cfg := config.Load()
	apiClient := client.New(cfg.APIURL, cfg.Username, cfg.Password)

	s := server.NewMCPServer(
		"foundrydb",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	tools.RegisterServiceTools(s, apiClient)
	tools.RegisterUserTools(s, apiClient)
	tools.RegisterBackupTools(s, apiClient)
	tools.RegisterMonitoringTools(s, apiClient)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
