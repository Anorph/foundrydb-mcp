package config

import "os"

// Config holds the MCP server runtime configuration, loaded from environment variables.
type Config struct {
	APIURL   string
	Username string
	Password string
}

// Load reads configuration from environment variables with sensible defaults for local development.
func Load() *Config {
	apiURL := os.Getenv("FOUNDRYDB_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:10090"
	}
	username := os.Getenv("FOUNDRYDB_USERNAME")
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("FOUNDRYDB_PASSWORD")
	if password == "" {
		password = "admin"
	}
	return &Config{
		APIURL:   apiURL,
		Username: username,
		Password: password,
	}
}
