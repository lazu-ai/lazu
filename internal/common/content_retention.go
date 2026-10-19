package common

// RequestContentRetentionDays covers every copy of request content, including
// object-storage captures and the first message/preview in Agent Sessions.
// Keep the existing environment name so deployed retention choices still apply.
func RequestContentRetentionDays() int {
	return GetEnvOrDefault("R2_ARCHIVE_RETENTION_DAYS", 180)
}
