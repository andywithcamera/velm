package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"velm/internal/auth"
	"velm/internal/db"
)

// stubRuntime replaces the db calls the service-call handler makes and records
// which of them ran. Restored by t.Cleanup.
type stubRuntime struct {
	app         db.RegisteredApp
	appFound    bool
	method      db.AppDefinitionMethod
	methodFound bool
	allowed     bool
	tableOwner  db.RegisteredApp
	tableFound  bool
	record      map[string]any
	recordErr   error
	executeErr  error

	recordFetched bool
	executed      bool
	fetchedTables []string
	fetchActors   []db.ScriptActor
}

func withStubRuntime(t *testing.T, stub *stubRuntime) *stubRuntime {
	t.Helper()
	prevResolveApp := runtimeResolveApp
	prevResolveMethod := runtimeResolveServiceMethod
	prevAllowed := runtimeServiceMethodAllowed
	prevByTable := runtimeResolveAppByTable
	prevGetRecord := runtimeGetScriptRecord
	prevExecute := runtimeExecuteServiceMethod
	t.Cleanup(func() {
		runtimeResolveApp = prevResolveApp
		runtimeResolveServiceMethod = prevResolveMethod
		runtimeServiceMethodAllowed = prevAllowed
		runtimeResolveAppByTable = prevByTable
		runtimeGetScriptRecord = prevGetRecord
		runtimeExecuteServiceMethod = prevExecute
	})

	runtimeResolveApp = func(ctx context.Context, name string) (db.RegisteredApp, bool, error) {
		return stub.app, stub.appFound, nil
	}
	runtimeResolveServiceMethod = func(ctx context.Context, app db.RegisteredApp, call string, requirePublic bool) (db.RegisteredApp, db.AppDefinitionMethod, bool, error) {
		return stub.app, stub.method, stub.methodFound, nil
	}
	runtimeServiceMethodAllowed = func(ctx context.Context, app db.RegisteredApp, method db.AppDefinitionMethod, userID string) (bool, error) {
		return stub.allowed, nil
	}
	runtimeResolveAppByTable = func(ctx context.Context, tableName string) (db.RegisteredApp, bool, error) {
		return stub.tableOwner, stub.tableFound, nil
	}
	runtimeGetScriptRecord = func(ctx context.Context, tableName, id string, actor db.ScriptActor) (map[string]any, error) {
		stub.recordFetched = true
		stub.fetchedTables = append(stub.fetchedTables, tableName)
		stub.fetchActors = append(stub.fetchActors, actor)
		return stub.record, stub.recordErr
	}
	runtimeExecuteServiceMethod = func(ctx context.Context, call db.AppServiceMethodCall) (db.ScriptExecutionResult, error) {
		stub.executed = true
		return db.ScriptExecutionResult{}, stub.executeErr
	}
	return stub
}

func serviceCallRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/app-runtime/call", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func newStubRuntime() *stubRuntime {
	return &stubRuntime{
		app:         db.RegisteredApp{ID: "app-1", Name: "devworks"},
		appFound:    true,
		methodFound: true,
		allowed:     true,
		tableOwner:  db.RegisteredApp{ID: "app-1", Name: "devworks"},
		tableFound:  true,
		record:      map[string]any{"_id": "rec-1"},
	}
}

// TestServiceCallAuthorizesBeforeRecordFetch is the regression test for #134:
// an unauthorized caller triggered a record lookup whose error text was
// reflected back, giving a table/record-id oracle.
func TestServiceCallAuthorizesBeforeRecordFetch(t *testing.T) {
	stub := withStubRuntime(t, newStubRuntime())
	stub.allowed = false

	rec := httptest.NewRecorder()
	handleAppRuntimeServiceCall(rec, serviceCallRequest(t, `{"app":"devworks","call":"getTicket","table_name":"itsm_ticket","record_id":"rec-1"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if stub.recordFetched {
		t.Fatal("record was fetched before authorization")
	}
	if stub.executed {
		t.Fatal("script executed for unauthorized caller")
	}
}

func TestServiceCallDoesNotLeakRecordErrors(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		configure func(*stubRuntime)
	}{
		{
			name: "missing record",
			body: `{"app":"devworks","call":"getTicket","table_name":"itsm_ticket","record_id":"nope"}`,
			configure: func(s *stubRuntime) {
				s.recordErr = context.Canceled // any error text, must not be reflected
			},
		},
		{
			name: "unknown table",
			body: `{"app":"devworks","call":"getTicket","table_name":"does_not_exist","record_id":"rec-1"}`,
			configure: func(s *stubRuntime) {
				s.tableFound = false
			},
		},
		{
			name: "table owned by another app",
			body: `{"app":"devworks","call":"getTicket","table_name":"crm_lead","record_id":"rec-1"}`,
			configure: func(s *stubRuntime) {
				s.tableOwner = db.RegisteredApp{ID: "app-2", Name: "crm"}
			},
		},
		{
			name:      "protected table",
			body:      `{"app":"devworks","call":"getTicket","table_name":"_user","record_id":"rec-1"}`,
			configure: func(s *stubRuntime) {},
		},
		{
			name:      "unsafe identifier",
			body:      `{"app":"devworks","call":"getTicket","table_name":"itsm_ticket; DROP TABLE x","record_id":"rec-1"}`,
			configure: func(s *stubRuntime) {},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStubRuntime()
			tc.configure(stub)
			stub = withStubRuntime(t, stub)

			rec := httptest.NewRecorder()
			handleAppRuntimeServiceCall(rec, serviceCallRequest(t, tc.body))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			body := rec.Body.String()
			for _, leaked := range []string{"not found", "does_not_exist", "itsm_ticket", "nope", "drop table"} {
				if strings.Contains(strings.ToLower(body), leaked) {
					t.Errorf("response body %q leaks %q", body, leaked)
				}
			}
			if stub.executed {
				t.Fatal("script executed despite invalid request")
			}
		})
	}
}

func TestServiceCallHappyPathStillWorks(t *testing.T) {
	stub := withStubRuntime(t, newStubRuntime())

	rec := httptest.NewRecorder()
	handleAppRuntimeServiceCall(rec, serviceCallRequest(t, `{"app":"devworks","call":"getTicket","table_name":"itsm_ticket","record_id":"rec-1"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !stub.recordFetched {
		t.Fatal("record should be fetched for an authorized caller")
	}
	if !stub.executed {
		t.Fatal("script should execute for an authorized caller")
	}
}

// The record read happens after authorization, but it must still carry the
// caller's identity so #146's row-level security filters the row. Reordering
// the handler must not turn this into an unrestricted read.
func TestServiceCallFetchesRecordAsCaller(t *testing.T) {
	stub := withStubRuntime(t, newStubRuntime())

	rec := httptest.NewRecorder()
	req := serviceCallRequest(t, `{"app":"devworks","call":"getTicket","table_name":"itsm_ticket","record_id":"rec-1"}`)
	req = req.WithContext(auth.WithUserContext(req.Context(), "user-42", "a@example.com", "Andy", "user"))
	handleAppRuntimeServiceCall(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(stub.fetchActors) != 1 {
		t.Fatalf("record fetched %d times, want 1", len(stub.fetchActors))
	}
	if got := stub.fetchActors[0]; got.UserID() != "user-42" || got.IsSystem() {
		t.Fatalf("record fetched as %q (system=%t), want the calling user, not the system actor", got.UserID(), got.IsSystem())
	}
}

func TestServiceCallWithoutTableSkipsRecordLookup(t *testing.T) {
	stub := withStubRuntime(t, newStubRuntime())

	rec := httptest.NewRecorder()
	handleAppRuntimeServiceCall(rec, serviceCallRequest(t, `{"app":"devworks","call":"getUserProfile"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if stub.recordFetched {
		t.Fatal("no table_name means no record lookup")
	}
	if !stub.executed {
		t.Fatal("script should execute")
	}
}

func TestValidateRuntimeTableNameRejectsProtectedTables(t *testing.T) {
	withStubRuntime(t, newStubRuntime())
	app := db.RegisteredApp{ID: "app-1"}
	for _, table := range []string{"_user", "_property", "_page", " _role "} {
		if err := validateRuntimeTableName(context.Background(), app, table); err == nil {
			t.Errorf("validateRuntimeTableName(%q) = nil, want error", table)
		}
	}
	if err := validateRuntimeTableName(context.Background(), app, "itsm_ticket"); err != nil {
		t.Errorf("itsm_ticket rejected: %v", err)
	}
}
