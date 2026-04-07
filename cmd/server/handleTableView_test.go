package main

import (
	"net/url"
	"testing"
)

func TestPaginationURLsUsesHasNextForInexactCounts(t *testing.T) {
	values := url.Values{
		"q":    []string{"state=in_progress"},
		"size": []string{"50"},
	}

	prevURL, nextURL := paginationURLs(values, "base_task", 2, 3, true, false)
	if prevURL != "/t/base_task?page=1&q=state%3Din_progress&size=50" {
		t.Fatalf("prevURL = %q", prevURL)
	}
	if nextURL != "/t/base_task?page=3&q=state%3Din_progress&size=50" {
		t.Fatalf("nextURL = %q", nextURL)
	}

	prevURL, nextURL = paginationURLs(values, "base_task", 2, 3, false, false)
	if prevURL != "/t/base_task?page=1&q=state%3Din_progress&size=50" {
		t.Fatalf("prevURL without hasNext = %q", prevURL)
	}
	if nextURL != "" {
		t.Fatalf("nextURL without hasNext = %q, want empty", nextURL)
	}
}

func TestPaginationURLsUsesExactTotalPages(t *testing.T) {
	prevURL, nextURL := paginationURLs(url.Values{}, "base_task", 1, 3, false, true)
	if prevURL != "" {
		t.Fatalf("prevURL = %q, want empty", prevURL)
	}
	if nextURL != "/t/base_task?page=2" {
		t.Fatalf("nextURL = %q", nextURL)
	}
}
