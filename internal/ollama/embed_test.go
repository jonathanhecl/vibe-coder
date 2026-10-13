package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbedPostsBatchAndDecodes(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2],[0.3,0.4]]}`))
	}))
	defer srv.Close()

	client := NewHTTPWithAPIKey(srv.URL, "secret").(*HTTPClient)
	vectors, err := client.Embed(context.Background(), "nomic-embed-text", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if gotPath != "/api/embed" {
		t.Fatalf("expected /api/embed, got %q", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("expected the API key to be sent, got %q", gotAuth)
	}
	if gotBody.Model != "nomic-embed-text" || len(gotBody.Input) != 2 {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
	if len(vectors) != 2 || len(vectors[0]) != 2 {
		t.Fatalf("unexpected embeddings: %+v", vectors)
	}
	if vectors[1][0] != 0.3 {
		t.Fatalf("expected 0.3, got %v", vectors[1][0])
	}
}

func TestEmbedRequiresModel(t *testing.T) {
	client := NewHTTP("http://127.0.0.1:1").(*HTTPClient)
	if _, err := client.Embed(context.Background(), "  ", []string{"x"}); err == nil {
		t.Fatal("expected an error for an empty model")
	}
}

func TestEmbedEmptyInputsReturnsNil(t *testing.T) {
	client := NewHTTP("http://127.0.0.1:1").(*HTTPClient)
	vectors, err := client.Embed(context.Background(), "nomic-embed-text", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vectors != nil {
		t.Fatalf("expected nil vectors, got %+v", vectors)
	}
}

func TestEmbedRejectsMismatchedVectorCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2]]}`))
	}))
	defer srv.Close()

	client := NewHTTP(srv.URL).(*HTTPClient)
	if _, err := client.Embed(context.Background(), "nomic-embed-text", []string{"a", "b"}); err == nil {
		t.Fatal("expected an error when the vector count does not match the input count")
	}
}

func TestEmbedPropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	client := NewHTTP(srv.URL).(*HTTPClient)
	if _, err := client.Embed(context.Background(), "nomic-embed-text", []string{"a"}); err == nil {
		t.Fatal("expected an error on a non-200 response")
	}
}
