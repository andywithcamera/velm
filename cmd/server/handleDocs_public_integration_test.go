package main

// Public docs against a real Postgres (#372): the flag has to survive a round
// trip through the column and change what an anonymous request receives. A unit
// test cannot show that; this one drives the real handlers with no session.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"velm/internal/db"
)

func publicDocsFixture(t *testing.T, ctx context.Context, public bool, status string) (librarySlug, articleID, title string) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	librarySlug = "zz-public-docs-" + suffix
	title = "Public fixture " + suffix

	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO _docs_library (slug, name, description, visibility, owner_user_id, status)
		VALUES ($1, $2, 'fixture', 'private', '', 'active')
	`, librarySlug, "ZZ Public Docs "+suffix); err != nil {
		t.Fatalf("insert library: %v", err)
	}

	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO _docs_article (docs_library_id, slug, title, markdown_body, rendered_html, status, tags, version_num, published_at, owner_user_id, is_public)
		SELECT _id, 'fixture-article', $2, 'Body of the fixture article.', '', $3, '', 1,
		       CASE WHEN $3 = 'published' THEN NOW() ELSE NULL END, '', $4
		FROM _docs_library WHERE slug = $1
		RETURNING _id::text
	`, librarySlug, title, status, public).Scan(&articleID); err != nil {
		t.Fatalf("insert article: %v", err)
	}

	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM _docs_article WHERE docs_library_id IN (SELECT _id FROM _docs_library WHERE slug = $1)`, librarySlug)
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM _docs_library WHERE slug = $1`, librarySlug)
	})
	return librarySlug, articleID, title
}

func TestPublicArticleIsReadableWithoutASession(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	_, articleID, title := publicDocsFixture(t, ctx, true, "published")

	req := httptest.NewRequest(http.MethodGet, "/d/"+articleID, nil)
	recorder := httptest.NewRecorder()
	handleDocsArticle(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("guest GET /d/<public article> = %d, want 200 (body: %s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), title) {
		t.Error("public article body is missing the article title")
	}
	if !strings.Contains(recorder.Body.String(), "Body of the fixture article.") {
		t.Error("public article body is missing the rendered markdown")
	}
}

func TestNonPublicArticleIsNotFoundWithoutASession(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	_, articleID, title := publicDocsFixture(t, ctx, false, "published")

	req := httptest.NewRequest(http.MethodGet, "/d/"+articleID, nil)
	recorder := httptest.NewRecorder()
	handleDocsArticle(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("guest GET /d/<private article> = %d, want 404", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), title) {
		t.Error("404 response leaked the private article title")
	}
}

// A public flag on a draft is not a grant: unpublished work stays unpublished.
func TestPublicDraftArticleIsNotFoundWithoutASession(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	_, articleID, _ := publicDocsFixture(t, ctx, true, "draft")

	req := httptest.NewRequest(http.MethodGet, "/d/"+articleID, nil)
	recorder := httptest.NewRecorder()
	handleDocsArticle(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("guest GET /d/<public draft> = %d, want 404", recorder.Code)
	}
}

func TestGuestCatalogListsOnlyPublicArticles(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	_, _, publicTitle := publicDocsFixture(t, ctx, true, "published")
	_, _, privateTitle := publicDocsFixture(t, ctx, false, "published")

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	recorder := httptest.NewRecorder()
	handleDocs(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("guest GET /docs = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, publicTitle) {
		t.Error("guest catalog is missing the public article")
	}
	if strings.Contains(body, privateTitle) {
		t.Error("guest catalog lists a non-public article")
	}
}

// The flag must travel through the save path, not just the read path: flipping
// it in the editor is the only way an operator can publish an article.
func TestSaveDocsArticlePersistsPublicFlag(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	librarySlug, articleID, _ := publicDocsFixture(t, ctx, false, "published")

	var libraryID string
	if err := db.Pool.QueryRow(ctx, `SELECT _id::text FROM _docs_library WHERE slug = $1`, librarySlug).Scan(&libraryID); err != nil {
		t.Fatalf("read library id: %v", err)
	}

	userID := seedDocsAdminUser(t, ctx)

	if _, err := saveDocsArticle(ctx, userID, librarySlug, docsArticle{
		Slug:         "fixture-article",
		Title:        "Fixture flipped public",
		MarkdownBody: "Now readable.",
		Status:       "published",
		IsPublic:     true,
	}, "fixture-article"); err != nil {
		t.Fatalf("saveDocsArticle: %v", err)
	}

	var stored bool
	if err := db.Pool.QueryRow(ctx, `SELECT is_public FROM _docs_article WHERE _id::text = $1`, articleID).Scan(&stored); err != nil {
		t.Fatalf("read is_public: %v", err)
	}
	if !stored {
		t.Error("saveDocsArticle did not persist is_public")
	}

	recorder := httptest.NewRecorder()
	handleDocsArticle(recorder, httptest.NewRequest(http.MethodGet, "/d/"+articleID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("guest GET after publish = %d, want 200", recorder.Code)
	}
}

// seedDocsAdminUser creates an admin through the real bootstrap path, so the
// save handler's own authorization check runs against a user the product knows
// how to make — no hand-written rows the schema might not accept.
func seedDocsAdminUser(t *testing.T, ctx context.Context) string {
	t.Helper()
	email := fmt.Sprintf("zz-docs-admin-%d@example.test", time.Now().UnixNano())
	result, err := db.BootstrapUser(ctx, db.BootstrapUserInput{
		Email:      email,
		Name:       "ZZ Docs Admin",
		Password:   "zz-docs-admin-pass-123",
		GrantAdmin: true,
	})
	if err != nil {
		t.Fatalf("bootstrap admin user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM _user_role WHERE user_id = $1::uuid`, result.UserID)
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM _user WHERE _id::text = $1`, result.UserID)
	})
	return result.UserID
}
