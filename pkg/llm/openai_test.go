package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIProvider_Embed(t *testing.T) {
	// 启动 mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var req struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request error: %v", err)
		}

		// 构造 mock 响应
		resp := map[string]interface{}{
			"data": make([]map[string]interface{}, len(req.Input)),
			"usage": map[string]interface{}{
				"total_tokens": len(req.Input) * 10,
			},
		}
		for i := range req.Input {
			resp["data"].([]map[string]interface{})[i] = map[string]interface{}{
				"embedding": []float64{0.1, 0.2, 0.3},
				"index":     i,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "test-key", "text-embedding-3-small")
	embeddings, err := provider.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
	for i, emb := range embeddings {
		if len(emb) != 3 {
			t.Errorf("embeddings[%d] len = %d, want 3", i, len(emb))
		}
		if emb[0] != 0.1 || emb[1] != 0.2 || emb[2] != 0.3 {
			t.Errorf("embeddings[%d] = %v, want [0.1 0.2 0.3]", i, emb)
		}
	}
}

func TestOpenAIProvider_Embed_Empty(t *testing.T) {
	provider := NewOpenAIProvider("http://localhost", "key", "model")
	embeddings, err := provider.Embed(context.Background(), []string{})
	if err != nil {
		t.Fatalf("Embed empty error: %v", err)
	}
	if len(embeddings) != 0 {
		t.Errorf("expected 0 embeddings, got %d", len(embeddings))
	}
}

func TestOpenAIProvider_Embed_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "key", "model")
	_, err := provider.Embed(context.Background(), []string{"test"})
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestOpenAIProvider_Embed_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{invalid"))
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "key", "model")
	_, err := provider.Embed(context.Background(), []string{"test"})
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
