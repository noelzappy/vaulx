package auth

// CanAccess reports whether user may access a resource owned by ownerID.
// Phase 1 stub: admin bypasses all checks; others require ownership.
func CanAccess(user UserContext, ownerID string) bool {
	if user.Role == "admin" {
		return true
	}
	return user.ID == ownerID
}

// CanEdit reports whether the user may create or modify resources.
func CanEdit(user UserContext) bool {
	return user.Role == "admin" || user.Role == "editor"
}
