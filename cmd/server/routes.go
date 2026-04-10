package main

import (
	"net/http"
	"velm/internal/auth"
	"velm/internal/authz"
	"velm/internal/db"
	"velm/internal/security"
)

func registerRoutes() {
	fs := http.FileServer(http.Dir("./web/static"))
	withAuth := func(permission authz.Permission, handler http.Handler) http.Handler {
		return auth.RequireAuth(
			requireFreshSession(
				security.RequestLog(
					security.Audit(
						authz.RequirePermission(
							permission,
							security.WithAppScope(
								auth.RequireCSRF(handler, store),
							),
						),
					),
				),
			),
			store,
		)
	}

	http.Handle("/", security.RequestLog(http.HandlerFunc(handleRootRoute)))
	http.Handle("/health", security.RequestLog(http.HandlerFunc(handleHealth)))
	http.Handle("/healthz", security.RequestLog(http.HandlerFunc(handleHealth)))
	http.Handle("/p/", withAuth(authz.PermissionView, http.HandlerFunc(handlePage)))
	http.Handle("/static/", security.RequestLog(http.StripPrefix("/static/", fs)))
	http.Handle("/api/monitor/client", security.RequestLog(auth.RequireCSRF(http.HandlerFunc(handleClientMonitoringBeacon), store)))
	http.Handle(observabilityWebhookPath, security.RequestLog(security.Audit(http.HandlerFunc(handleObservabilityWebhook))))
	http.Handle("/api/menu/", withAuth(authz.PermissionView, http.HandlerFunc(handleMenu)))
	http.Handle("/api/search/", withAuth(authz.PermissionView, http.HandlerFunc(handleSearch)))
	http.Handle("/api/notifications/panel", withAuth(authz.PermissionView, http.HandlerFunc(handleNotificationsPanel)))
	http.Handle("/api/notifications/read-all", withAuth(authz.PermissionView, http.HandlerFunc(handleMarkAllNotificationsRead)))
	http.Handle("/api/notifications/open", withAuth(authz.PermissionView, http.HandlerFunc(handleOpenNotification)))
	http.Handle("/api/export/", withAuth(authz.PermissionView, http.HandlerFunc(handleExportCSV)))
	http.Handle("/api/reference-options", withAuth(authz.PermissionView, http.HandlerFunc(handleReferenceLookup)))
	http.Handle("/api/realtime/stream", withAuth(authz.PermissionView, http.HandlerFunc(handleRealtimeStream)))
	http.Handle("/api/realtime/record-version", withAuth(authz.PermissionView, http.HandlerFunc(handleRealtimeRecordVersion)))
	http.Handle("/api/realtime/presence/heartbeat", withAuth(authz.PermissionView, http.HandlerFunc(handleRealtimePresenceHeartbeat)))
	http.Handle("/docs/manage", withAuth(authz.PermissionView, http.HandlerFunc(handleDocsManage)))
	http.Handle("/docs", withAuth(authz.PermissionView, http.HandlerFunc(handleDocs)))
	http.Handle("/d/", withAuth(authz.PermissionView, http.HandlerFunc(handleDocsArticle)))
	http.Handle("/knowledge", withAuth(authz.PermissionView, http.HandlerFunc(handleLegacyDocsRedirect)))
	http.Handle("/api/preferences/list-view", withAuth(authz.PermissionView, http.HandlerFunc(handleListViewPreference)))
	http.Handle("/api/preferences/list-view/saved", withAuth(authz.PermissionView, http.HandlerFunc(handleListViewSavedViews)))
	http.Handle("/builder/schema", withAuth(authz.PermissionWrite, http.HandlerFunc(handleSchemaBuilderPage)))
	http.Handle("/builder/pages", withAuth(authz.PermissionWrite, http.HandlerFunc(handlePageBuilder)))
	http.Handle("/admin/app-editor", withAuth(authz.PermissionWrite, http.HandlerFunc(handlePageBuilder)))
	http.Handle("/map", withAuth(authz.PermissionView, http.HandlerFunc(handleRelationshipMapPage)))
	http.Handle("/task", withAuth(authz.PermissionView, http.HandlerFunc(handleTaskBoardPage)))
	http.Handle("/task/", withAuth(authz.PermissionView, http.HandlerFunc(handleTaskBoardPage)))
	http.Handle("/t/", withAuth(authz.PermissionView, http.HandlerFunc(handleTableView)))
	http.Handle("/f/", withAuth(
		authz.PermissionView,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleForm(w, r)
		}),
	))
	http.Handle("/api/form/security-preview", withAuth(authz.PermissionView, http.HandlerFunc(handleFormSecurityPreview)))
	http.Handle("/api/save/", withAuth(authz.PermissionWrite, http.HandlerFunc(db.HandleSave)))
	http.Handle("/api/comments/add", withAuth(authz.PermissionWrite, http.HandlerFunc(handleAddRecordComment)))
	http.Handle("/api/bulk/update/", withAuth(authz.PermissionWrite, http.HandlerFunc(handleBulkUpdate)))
	http.Handle("/api/delete/", withAuth(authz.PermissionWrite, http.HandlerFunc(handleSoftDeleteRecord)))
	http.Handle("/api/restore/", withAuth(authz.PermissionWrite, http.HandlerFunc(handleRestoreRecord)))
	http.Handle("/api/builder/table/create", withAuth(authz.PermissionWrite, http.HandlerFunc(handleCreateBuilderTable)))
	http.Handle("/api/builder/table/update", withAuth(authz.PermissionWrite, http.HandlerFunc(handleUpdateBuilderTable)))
	http.Handle("/api/builder/table/delete", withAuth(authz.PermissionWrite, http.HandlerFunc(handleDeleteBuilderTable)))
	http.Handle("/api/builder/column/create", withAuth(authz.PermissionWrite, http.HandlerFunc(handleCreateBuilderColumn)))
	http.Handle("/api/builder/column/update", withAuth(authz.PermissionWrite, http.HandlerFunc(handleUpdateBuilderColumn)))
	http.Handle("/api/builder/column/delete", withAuth(authz.PermissionWrite, http.HandlerFunc(handleDeleteBuilderColumn)))
	http.Handle("/api/builder/schema/apply", withAuth(authz.PermissionWrite, http.HandlerFunc(handleApplyBuilderSchema)))
	http.Handle("/api/app-editor/app/create", withAuth(authz.PermissionWrite, http.HandlerFunc(handleCreateAppEditorApp)))
	http.Handle("/api/app-editor/object/save", withAuth(authz.PermissionWrite, http.HandlerFunc(handleAppEditorObjectSave)))
	http.Handle("/api/app-editor/object/delete", withAuth(authz.PermissionWrite, http.HandlerFunc(handleAppEditorObjectDelete)))
	http.Handle("/api/app-editor/publish", withAuth(authz.PermissionWrite, http.HandlerFunc(handleAppEditorPublish)))
	http.Handle("/api/pages/save", withAuth(authz.PermissionWrite, http.HandlerFunc(handleSavePageBuilder)))
	http.Handle("/api/root-route/save", withAuth(authz.PermissionWrite, http.HandlerFunc(handleSaveRootRouteTarget)))
	http.Handle("/api/docs/preview", withAuth(authz.PermissionView, http.HandlerFunc(handleDocsPreview)))
	http.Handle("/api/docs/library/save", withAuth(authz.PermissionWrite, http.HandlerFunc(handleSaveDocsLibrary)))
	http.Handle("/api/docs/library/archive", withAuth(authz.PermissionWrite, http.HandlerFunc(handleArchiveDocsLibrary)))
	http.Handle("/api/docs/article/save", withAuth(authz.PermissionWrite, http.HandlerFunc(handleSaveDocsArticle)))
	http.Handle("/api/docs/article/archive", withAuth(authz.PermissionWrite, http.HandlerFunc(handleArchiveDocsArticle)))
	http.Handle("/admin/access", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleAccessControlPage)))
	http.Handle("/admin/audit", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleAuditView)))
	http.Handle("/admin/monitoring", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleMonitoringView)))
	http.Handle("/admin/scripts", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleScriptsView)))
	http.Handle("/admin/run-script", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleRunScriptPage)))
	http.Handle("/api/scripts/scope", withAuth(authz.PermissionView, http.HandlerFunc(handleScriptScope)))
	http.Handle("/api/scripts/test", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleTestScriptDryRun)))
	http.Handle("/api/scripts/run-adhoc", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleRunAdhocScript)))
	http.Handle("/api/app-runtime/call", auth.RequireAuth(
		requireFreshSession(
			security.RequestLog(
				security.Audit(
					auth.RequireCSRF(http.HandlerFunc(handleAppRuntimeServiceCall), store),
				),
			),
		),
		store,
	))
	http.Handle("/api/", auth.RequireAuth(
		requireFreshSession(
			security.RequestLog(
				security.Audit(
					auth.RequireCSRF(http.HandlerFunc(handleAppRuntimeEndpoint), store),
				),
			),
		),
		store,
	))
	http.Handle("/api/authz/roles/create", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleCreateRole)))
	http.Handle("/api/authz/permissions/create", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleCreatePermission)))
	http.Handle("/api/authz/user-roles/grant", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleGrantUserRole)))
	http.Handle("/api/authz/user-roles/revoke", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleRevokeUserRole)))
	http.Handle("/api/authz/role-permissions/grant", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleGrantRolePermission)))
	http.Handle("/api/authz/role-permissions/revoke", withAuth(authz.PermissionAdmin, http.HandlerFunc(handleRevokeRolePermission)))
	http.Handle("/forbidden", auth.RequireAuth(requireFreshSession(security.RequestLog(http.HandlerFunc(handleForbiddenPage))), store))
	http.Handle("/auth/login", security.RequestLog(security.Audit(http.HandlerFunc(loginPostHandler))))
	http.Handle("/auth/login/", security.RequestLog(security.Audit(http.HandlerFunc(loginPostHandler))))
	http.Handle("/logout", withAuth(authz.PermissionView, http.HandlerFunc(handleLogout)))
	http.Handle("/login", security.RequestLog(http.HandlerFunc(handleLoginPage)))
	http.Handle("/login/", security.RequestLog(http.HandlerFunc(handleLoginPage)))
}
