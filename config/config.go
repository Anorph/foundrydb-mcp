package config

import (
	"fmt"
	"os"
)

// Config holds the MCP server runtime configuration, loaded from environment variables.
type Config struct {
	APIURL   string
	Token    string
	Username string
	Password string
}

// Load reads configuration from environment variables.
// Credentials are required to prevent running with default admin credentials
// in misconfigured deployments. Either FOUNDRYDB_TOKEN (preferred: supports
// scoped API tokens) or FOUNDRYDB_USERNAME + FOUNDRYDB_PASSWORD must be set.
func Load() *Config {
	apiURL := os.Getenv("FOUNDRYDB_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:10090"
	}

	token := os.Getenv("FOUNDRYDB_TOKEN")
	username := os.Getenv("FOUNDRYDB_USERNAME")
	password := os.Getenv("FOUNDRYDB_PASSWORD")

	if token == "" {
		if username == "" {
			fmt.Fprintln(os.Stderr, "FATAL: set FOUNDRYDB_TOKEN (preferred) or FOUNDRYDB_USERNAME + FOUNDRYDB_PASSWORD")
			os.Exit(1)
		}
		if password == "" {
			fmt.Fprintln(os.Stderr, "FATAL: FOUNDRYDB_PASSWORD environment variable is required when using FOUNDRYDB_USERNAME")
			os.Exit(1)
		}
	}

	return &Config{
		APIURL:   apiURL,
		Token:    token,
		Username: username,
		Password: password,
	}
}
