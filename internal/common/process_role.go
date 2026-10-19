package common

import "strings"

const (
	ProcessRoleAllInOne = "all-in-one"
	ProcessRoleAPI      = "api"
	ProcessRoleWorker   = "worker"
)

// ResolveProcessRole keeps self-hosted installs on the single-process default.
// Hosted deployments set "api" so database-wide maintenance runs in workers.
func ResolveProcessRole(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ProcessRoleAPI:
		return ProcessRoleAPI
	case ProcessRoleWorker:
		return ProcessRoleWorker
	default:
		return ProcessRoleAllInOne
	}
}
