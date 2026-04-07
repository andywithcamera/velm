package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"velm/internal/db"
	"velm/internal/utils"
	"strconv"
	"strings"
)

func handleAccessControlPage(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	users, err := db.ListAuthzUsers(ctx)
	if err != nil {
		http.Error(w, "Failed to load users", http.StatusInternalServerError)
		return
	}
	roles, err := db.ListRoles(ctx)
	if err != nil {
		http.Error(w, "Failed to load roles", http.StatusInternalServerError)
		return
	}
	permissions, err := db.ListPermissions(ctx)
	if err != nil {
		http.Error(w, "Failed to load permissions", http.StatusInternalServerError)
		return
	}
	userRoleAssignments, err := db.ListUserRoleAssignments(ctx)
	if err != nil {
		http.Error(w, "Failed to load user role assignments", http.StatusInternalServerError)
		return
	}
	rolePermissionAssignments, err := db.ListRolePermissionAssignments(ctx)
	if err != nil {
		http.Error(w, "Failed to load role permission assignments", http.StatusInternalServerError)
		return
	}

	data := newViewData(w, r, "/admin/access", "Access Control", "Admin")
	data["View"] = "access-control"
	data["AuthzUsers"] = users
	data["AuthzRoles"] = roles
	data["AuthzPermissions"] = permissions
	data["UserRoleAssignments"] = userRoleAssignments
	data["RolePermissionAssigns"] = rolePermissionAssignments
	data["AccessControlStatusText"] = r.URL.Query().Get("status")
	data["PermissionPrefillResource"] = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("resource")))
	data["PermissionPrefillAction"] = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("action")))
	data["PermissionPrefillScope"] = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("scope")))
	data["PermissionPrefillDescription"] = strings.TrimSpace(r.URL.Query().Get("description"))
	data["PermissionReturnTo"] = sanitizeLoginNext(strings.TrimSpace(r.URL.Query().Get("return_to")))

	if err := templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "Failed to render access control view", http.StatusInternalServerError)
	}
}

func handleCreateRole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(strings.ToLower(r.FormValue("name")))
	description := strings.TrimSpace(r.FormValue("description"))
	priority, err := strconv.Atoi(strings.TrimSpace(r.FormValue("priority")))
	if err != nil || priority < 0 {
		http.Error(w, "Invalid priority", http.StatusBadRequest)
		return
	}
	if !db.IsSafeIdentifier(name) {
		http.Error(w, "Invalid role name", http.StatusBadRequest)
		return
	}

	if err := db.CreateRole(context.Background(), name, description, priority); err != nil {
		http.Error(w, "Failed to create role", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/access?status=role_saved", http.StatusSeeOther)
}

func handleCreatePermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	resource := strings.TrimSpace(strings.ToLower(r.FormValue("resource")))
	action := strings.TrimSpace(strings.ToLower(r.FormValue("action")))
	scope := strings.TrimSpace(strings.ToLower(r.FormValue("scope")))
	description := strings.TrimSpace(r.FormValue("description"))
	returnTo := sanitizeLoginNext(strings.TrimSpace(r.FormValue("return_to")))

	if !db.IsSafeIdentifier(resource) || !db.IsSafeIdentifier(action) || !db.IsSafeIdentifier(scope) {
		http.Error(w, "Invalid permission fields", http.StatusBadRequest)
		return
	}

	if err := db.CreatePermission(context.Background(), resource, action, scope, description); err != nil {
		http.Error(w, "Failed to create permission", http.StatusInternalServerError)
		return
	}

	if returnTo != "/" {
		if target, err := url.Parse(returnTo); err == nil {
			q := target.Query()
			if q.Get("active") == "" {
				q.Set("status", "permission_saved")
			}
			target.RawQuery = q.Encode()
			http.Redirect(w, r, target.String(), http.StatusSeeOther)
			return
		}
	}

	http.Redirect(w, r, "/admin/access?status=permission_saved", http.StatusSeeOther)
}

func handleGrantUserRole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(r.FormValue("user_id"))
	roleID := strings.TrimSpace(r.FormValue("role_id"))
	if !utils.IsValidUUID(roleID) {
		http.Error(w, "Invalid role", http.StatusBadRequest)
		return
	}
	if !utils.IsValidUUID(userID) {
		http.Error(w, "Invalid user id", http.StatusBadRequest)
		return
	}

	appID := strings.TrimSpace(r.FormValue("app_id"))
	if appID != "" && !db.IsSafeIdentifier(appID) {
		http.Error(w, "Invalid app id", http.StatusBadRequest)
		return
	}

	if err := db.GrantUserRole(context.Background(), userID, roleID, appID); err != nil {
		http.Error(w, "Failed to assign role", http.StatusInternalServerError)
		return
	}
	if err := db.BumpUserSessionVersion(context.Background(), userID); err != nil {
		http.Error(w, "Failed to rotate user session", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/access?status=user_role_granted", http.StatusSeeOther)
}

func handleRevokeUserRole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(r.FormValue("user_id"))
	if !utils.IsValidUUID(userID) {
		http.Error(w, "Invalid user id", http.StatusBadRequest)
		return
	}
	roleID := strings.TrimSpace(r.FormValue("role_id"))
	if !utils.IsValidUUID(roleID) {
		http.Error(w, "Invalid role id", http.StatusBadRequest)
		return
	}
	appID := strings.TrimSpace(r.FormValue("app_id"))
	if appID != "" && !db.IsSafeIdentifier(appID) {
		http.Error(w, "Invalid app id", http.StatusBadRequest)
		return
	}

	if err := db.RevokeUserRole(context.Background(), userID, roleID, appID); err != nil {
		if errors.Is(err, db.ErrLastGlobalAdminRemoval) {
			http.Redirect(w, r, "/admin/access?status=last_admin_guard_blocked", http.StatusSeeOther)
			return
		}
		http.Error(w, "Failed to revoke role", http.StatusInternalServerError)
		return
	}
	if err := db.BumpUserSessionVersion(context.Background(), userID); err != nil {
		http.Error(w, "Failed to rotate user session", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/access?status=user_role_revoked", http.StatusSeeOther)
}

func handleGrantRolePermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	roleID := strings.TrimSpace(r.FormValue("role_id"))
	if !utils.IsValidUUID(roleID) {
		http.Error(w, "Invalid role id", http.StatusBadRequest)
		return
	}
	permissionID := strings.TrimSpace(r.FormValue("permission_id"))
	if !utils.IsValidUUID(permissionID) {
		http.Error(w, "Invalid permission id", http.StatusBadRequest)
		return
	}

	if err := db.GrantRolePermission(context.Background(), roleID, permissionID); err != nil {
		http.Error(w, "Failed to grant permission", http.StatusInternalServerError)
		return
	}
	if err := db.BumpSessionVersionForRole(context.Background(), roleID); err != nil {
		http.Error(w, "Failed to rotate role sessions", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/access?status=role_permission_granted", http.StatusSeeOther)
}

func handleRevokeRolePermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	roleID := strings.TrimSpace(r.FormValue("role_id"))
	if !utils.IsValidUUID(roleID) {
		http.Error(w, "Invalid role id", http.StatusBadRequest)
		return
	}
	permissionID := strings.TrimSpace(r.FormValue("permission_id"))
	if !utils.IsValidUUID(permissionID) {
		http.Error(w, "Invalid permission id", http.StatusBadRequest)
		return
	}

	if err := db.RevokeRolePermission(context.Background(), roleID, permissionID); err != nil {
		if errors.Is(err, db.ErrLastGlobalAdminRemoval) {
			http.Redirect(w, r, "/admin/access?status=last_admin_guard_blocked", http.StatusSeeOther)
			return
		}
		http.Error(w, "Failed to revoke permission", http.StatusInternalServerError)
		return
	}
	if err := db.BumpSessionVersionForRole(context.Background(), roleID); err != nil {
		http.Error(w, "Failed to rotate role sessions", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/access?status=role_permission_revoked", http.StatusSeeOther)
}
