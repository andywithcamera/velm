package db

import "testing"

func TestNormalizeUserNotificationInputDefaultsLevelAndHref(t *testing.T) {
	input, err := normalizeUserNotificationInput(UserNotificationCreateInput{
		UserID: "550e8400-e29b-41d4-a716-446655440000",
		Title:  "Assigned",
		Href:   "/f/_work/550e8400-e29b-41d4-a716-446655440001",
	})
	if err != nil {
		t.Fatalf("normalizeUserNotificationInput() error = %v", err)
	}
	if input.Level != "info" {
		t.Fatalf("Level = %q, want %q", input.Level, "info")
	}
	if input.Href != "/f/_work/550e8400-e29b-41d4-a716-446655440001" {
		t.Fatalf("Href = %q", input.Href)
	}
}

func TestNormalizeUserNotificationInputRejectsBadHref(t *testing.T) {
	_, err := normalizeUserNotificationInput(UserNotificationCreateInput{
		UserID: "550e8400-e29b-41d4-a716-446655440000",
		Title:  "Assigned",
		Href:   "javascript:alert(1)",
	})
	if err == nil {
		t.Fatal("expected invalid href error")
	}
}

func TestNormalizeScriptNotificationRequestDefaultsRecipient(t *testing.T) {
	request, err := NormalizeScriptNotificationRequest(map[string]any{
		"title": "Watch this",
		"body":  "Something changed",
	}, "550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("NormalizeScriptNotificationRequest() error = %v", err)
	}
	if len(request.UserIDs) != 1 || request.UserIDs[0] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("UserIDs = %#v", request.UserIDs)
	}
}

func TestNormalizeScriptNotificationRequestAcceptsMultipleRecipients(t *testing.T) {
	request, err := NormalizeScriptNotificationRequest(map[string]any{
		"title":   "Watch this",
		"userID":  "550e8400-e29b-41d4-a716-446655440000",
		"userIDs": []any{"550e8400-e29b-41d4-a716-446655440001", "550e8400-e29b-41d4-a716-446655440000"},
	}, "")
	if err != nil {
		t.Fatalf("NormalizeScriptNotificationRequest() error = %v", err)
	}
	if len(request.UserIDs) != 2 {
		t.Fatalf("len(UserIDs) = %d, want 2", len(request.UserIDs))
	}
}

func TestNormalizeScriptNotificationRequestMissingOptionalFieldsStayBlank(t *testing.T) {
	request, err := NormalizeScriptNotificationRequest(map[string]any{
		"title":  "Watch this",
		"userID": "550e8400-e29b-41d4-a716-446655440000",
	}, "")
	if err != nil {
		t.Fatalf("NormalizeScriptNotificationRequest() error = %v", err)
	}
	if request.Body != "" || request.Href != "" || request.Level != "" {
		t.Fatalf("request = %#v, expected blank optional fields", request)
	}
}
