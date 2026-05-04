package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMockProviderReturnsSources(t *testing.T) {
	p := NewMockProvider()
	got, err := p.Search(context.Background(), "eino agent", 2)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Provider != "mock" {
		t.Fatalf("Provider = %q, want mock", got[0].Provider)
	}
	if got[0].Query != "eino agent" {
		t.Fatalf("Query = %q, want eino agent", got[0].Query)
	}
}

func TestDeduplicateSourcesByURL(t *testing.T) {
	in := []Source{
		{ID: "a", URL: "https://example.com/a", Title: "A"},
		{ID: "b", URL: "https://example.com/a", Title: "B"},
		{ID: "c", URL: "https://example.com/c", Title: "C"},
	}
	got := Deduplicate(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "src_1" || got[1].ID != "src_2" {
		t.Fatalf("IDs = %q, %q; want src_1, src_2", got[0].ID, got[1].ID)
	}
}

func TestGoogleProviderParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "eino agent" {
			t.Fatalf("q = %q, want eino agent", got)
		}
		if got := r.URL.Query().Get("num"); got != "2" {
			t.Fatalf("num = %q, want 2", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"items": [
				{"title": "One", "link": "https://example.com/1", "snippet": "First"},
				{"title": "Two", "link": "https://example.com/2", "snippet": "Second"}
			]
		}`))
	}))
	defer server.Close()

	p := NewGoogleProvider(GoogleConfig{
		APIKey:  "key",
		CSEID:   "cx",
		BaseURL: server.URL,
		Client:  server.Client(),
	})

	got, err := p.Search(context.Background(), "eino agent", 2)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Title != "One" || got[0].Provider != "google" {
		t.Fatalf("first source = %+v", got[0])
	}
}

func TestGoogleProviderHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	p := NewGoogleProvider(GoogleConfig{
		APIKey:  "key",
		CSEID:   "cx",
		BaseURL: server.URL,
		Client:  server.Client(),
	})

	_, err := p.Search(context.Background(), "bad query", 1)
	if err == nil {
		t.Fatal("Search returned nil error, want HTTP error")
	}
	if !strings.Contains(err.Error(), "google") || !strings.Contains(err.Error(), "bad query") {
		t.Fatalf("error = %q, want provider and query", err.Error())
	}
}
