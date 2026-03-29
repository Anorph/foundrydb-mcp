package client

// CreateServiceRequest is the payload for provisioning a new managed database service.
type CreateServiceRequest struct {
	Name          string  `json:"name"`
	DatabaseType  string  `json:"database_type"`
	Version       *string `json:"version,omitempty"`
	PlanName      string  `json:"plan_name"`
	Zone          *string `json:"zone,omitempty"`
	StorageSizeGB *int    `json:"storage_size_gb,omitempty"`
	StorageTier   *string `json:"storage_tier,omitempty"`
}

// CreateBackupRequest is the payload for triggering an on-demand backup.
type CreateBackupRequest struct {
	BackupMethod  *string `json:"backup_method,omitempty"`
	RetentionDays *int    `json:"retention_days,omitempty"`
}
