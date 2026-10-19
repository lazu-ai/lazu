package model

// ProjectAction is independent of platform roles. Callers must supply the
// target's current project membership, never a role received from the client.
type ProjectAction string

const (
	ProjectManageMember      ProjectAction = "manage_member"
	ProjectInviteMember      ProjectAction = "invite_member"
	ProjectChangeRole        ProjectAction = "change_role"
	ProjectManagePersonalKey ProjectAction = "manage_personal_key"
	// ProjectDeletePersonalKey grants exactly what ProjectManagePersonalKey
	// grants. It exists so deletion can stay available on a Key whose owner is
	// no longer active, where editing, rotating and revealing are refused.
	ProjectDeletePersonalKey    ProjectAction = "delete_personal_key"
	ProjectReadPersonalResource ProjectAction = "read_personal_resource"
	ProjectManageSettings       ProjectAction = "manage_settings"
	ProjectManageBilling        ProjectAction = "manage_billing"
	ProjectTopUp                ProjectAction = "top_up"
)

func (m ProjectMember) IsManager() bool {
	return m.Status == ProjectMemberActive && (m.Role == ProjectRoleOwner || m.Role == ProjectRoleAdmin)
}

func CanProjectAction(viewer ProjectMember, target *ProjectMember, action ProjectAction, settings ProjectSettings) bool {
	if viewer.ProjectID <= 0 || viewer.UserID <= 0 || viewer.Status != ProjectMemberActive {
		return false
	}
	if viewer.Role != ProjectRoleOwner && viewer.Role != ProjectRoleAdmin && viewer.Role != ProjectRoleMember {
		return false
	}
	if target != nil && target.ProjectID != viewer.ProjectID {
		return false
	}
	switch action {
	case ProjectManageSettings, ProjectManageBilling:
		return viewer.Role == ProjectRoleOwner
	case ProjectTopUp:
		return viewer.Role == ProjectRoleOwner || (viewer.Role == ProjectRoleAdmin && settings.WhoCanTopup == "owner_admin")
	case ProjectReadPersonalResource:
		return target != nil && (viewer.IsManager() || viewer.UserID == target.UserID)
	case ProjectChangeRole:
		return target != nil && viewer.Role == ProjectRoleOwner && target.Role != ProjectRoleOwner && viewer.UserID != target.UserID
	case ProjectInviteMember:
		return target != nil && (target.Role == ProjectRoleMember && viewer.IsManager() || target.Role == ProjectRoleAdmin && viewer.Role == ProjectRoleOwner)
	case ProjectManagePersonalKey, ProjectDeletePersonalKey:
		if target != nil && viewer.UserID == target.UserID {
			return true
		}
		fallthrough
	case ProjectManageMember:
		if target == nil || viewer.UserID == target.UserID {
			return false
		}
		return viewer.Role == ProjectRoleOwner && (target.Role == ProjectRoleAdmin || target.Role == ProjectRoleMember) || viewer.Role == ProjectRoleAdmin && target.Role == ProjectRoleMember
	default:
		return false
	}
}
