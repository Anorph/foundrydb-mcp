module github.com/anorph/foundrydb-mcp

go 1.24

require (
	github.com/anorph/foundrydb-sdk-go v0.8.0
	github.com/mark3labs/mcp-go v0.46.0
)

require (
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
)

// Compliance methods (GenerateComplianceReport, ListComplianceReports, ComplianceSigningKeys)
// are present in the local SDK source but not yet published as a tagged release.
