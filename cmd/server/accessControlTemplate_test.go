package main

import (
	"bytes"
	"strings"
	"testing"

	"velm/internal/db"
)

// TestAccessControlPageOffersGroupsNotDirectGrants pins the #199 R3 change: the
// page must offer membership, and the only remaining mention of a direct
// user-role grant is the read-only deprecation notice.
func TestAccessControlPageOffersGroupsNotDirectGrants(t *testing.T) {
	t.Chdir("../..")
	tpl := parseTemplates(t)

	data := map[string]any{
		"CSRFToken":             "csrf-token",
		"Breadcrumbs":           []Breadcrumb{},
		"AuthzUsers":            []db.AuthzUser{{ID: "11111111-1111-1111-1111-111111111111", Name: "billybot", Email: "billybot@example.test"}},
		"AuthzRoles":            []db.AuthzRole{{ID: "22222222-2222-2222-2222-222222222222", Name: "admin"}},
		"AuthzGroups":           []db.AuthzGroup{{ID: "33333333-3333-3333-3333-333333333333", Name: "Administrators", Roles: "admin", IsSystem: true, MemberCnt: 2}},
		"AuthzPermissions":      []db.AuthzPermission{{ID: "44444444-4444-4444-4444-444444444444", Resource: "platform", Action: "admin", Scope: "global"}},
		"RolePermissionAssigns": []db.RolePermissionAssignment{},
		"GroupMemberships": []db.AuthzGroupMembership{{
			UserID: "11111111-1111-1111-1111-111111111111", UserName: "billybot", Email: "billybot@example.test",
			GroupID: "33333333-3333-3333-3333-333333333333", GroupName: "Administrators", Roles: "admin",
		}},
		"UserRoleAssignments": []db.UserRoleAssignment{{
			UserID: "11111111-1111-1111-1111-111111111111", UserName: "billybot", Email: "billybot@example.test",
			RoleID: "22222222-2222-2222-2222-222222222222", RoleName: "admin", AppID: "",
		}},
		"DirectGrantCount": 1,
	}

	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "access-control-view", data); err != nil {
		t.Fatalf("render access-control-view: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		`action="/api/authz/group-memberships/grant"`,
		`name="group_id"`,
		`Administrators — admin`,
		`Group Memberships`,
		`Direct role grants (deprecated)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}

	// No write path to a direct grant may be rendered. The grant endpoint must
	// not appear at all; the revoke endpoint must not appear as a form action.
	for _, unwanted := range []string{
		`/api/authz/user-roles/grant`,
		`action="/api/authz/user-roles/revoke"`,
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered page still offers a direct role-grant write path: %q", unwanted)
		}
	}
}

// TestAccessControlPageWithoutDirectGrantsOmitsTheNotice keeps the deprecation
// block from becoming permanent furniture once the grants are gone.
func TestAccessControlPageWithoutDirectGrantsOmitsTheNotice(t *testing.T) {
	t.Chdir("../..")
	tpl := parseTemplates(t)

	data := map[string]any{
		"CSRFToken":             "csrf-token",
		"Breadcrumbs":           []Breadcrumb{},
		"AuthzUsers":            []db.AuthzUser{},
		"AuthzRoles":            []db.AuthzRole{},
		"AuthzPermissions":      []db.AuthzPermission{},
		"RolePermissionAssigns": []db.RolePermissionAssignment{},
		"AuthzGroups":           []db.AuthzGroup{},
		"GroupMemberships":      []db.AuthzGroupMembership{},
		"UserRoleAssignments":   []db.UserRoleAssignment{},
		"DirectGrantCount":      0,
	}

	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "access-control-view", data); err != nil {
		t.Fatalf("render access-control-view: %v", err)
	}
	if strings.Contains(buf.String(), "Direct role grants (deprecated)") {
		t.Error("deprecation notice rendered with zero direct grants")
	}
}

// TestAccessControlStatusText covers the slug-to-sentence mapping. The guard
// refusal is the only status rendered as danger, and an unknown slug renders
// nothing rather than echoing a query value back into the page.
func TestAccessControlStatusText(t *testing.T) {
	cases := []struct {
		slug   string
		want   string
		danger bool
	}{
		{slug: "group_membership_added", want: "Added to group. Roles from that group apply immediately."},
		{slug: "group_membership_removed", want: "Removed from group. Remaining roles still apply."},
		{slug: "last_admin_guard_blocked", want: "Blocked: that would leave this instance with no global admin. Add another admin first.", danger: true},
		{slug: "role_saved", want: "Role created."},
		{slug: "permission_saved", want: "Permission created."},
		{slug: "role_permission_granted", want: "Permission attached to role."},
		{slug: "role_permission_revoked", want: "Permission detached from role."},
		{slug: "", want: ""},
		{slug: "<script>alert(1)</script>", want: ""},
	}
	for _, c := range cases {
		got, danger := accessControlStatus(c.slug)
		if got != c.want {
			t.Errorf("accessControlStatus(%q) text = %q, want %q", c.slug, got, c.want)
		}
		if danger != c.danger {
			t.Errorf("accessControlStatus(%q) danger = %v, want %v", c.slug, danger, c.danger)
		}
	}
}
