package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaProvider_Embed_Batch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var req struct {
			Input interface{} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		// 判断是否是批量请求
		var count int
		switch v := req.Input.(type) {
		case []interface{}:
			count = len(v)
		case string:
			count = 1
		}

		embeddings := make([][]float64, count)
		for i := 0; i < count; i++ {
			embeddings[i] = []float64{0.1, 0.2, 0.3}
		}

		resp := map[string]interface{}{
			"embeddings": embeddings,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOllamaProvider(server.URL)
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
	}
}

func TestOllamaProvider_Embed_Fallback(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req struct {
			Input interface{} `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		// 第一次批量请求失败（返回空 embeddings），后续单个请求成功
		var embeddings [][]float64
		switch req.Input.(type) {
		case []interface{}:
			// 批量请求：返回不匹配的数量，触发 fallback
			embeddings = [][]float64{{0.1, 0.2}}
		case string:
			embeddings = [][]float64{{0.1, 0.2, 0.3}}
		}

		resp := map[string]interface{}{"embeddings": embeddings}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOllamaProvider(server.URL)
	embeddings, err := provider.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
	if callCount < 2 {
		t.Errorf("expected fallback to individual requests, callCount = %d", callCount)
	}
}

func TestOllamaProvider_Embed_Empty(t *testing.T) {
	provider := NewOllamaProvider("http://localhost")
	embeddings, err := provider.Embed(context.Background(), []string{})
	if err != nil {
		t.Fatalf("Embed empty error: %v", err)
	}
	if len(embeddings) != 0 {
		t.Errorf("expected 0 embeddings, got %d", len(embeddings))
	}
}

func TestOllamaProvider_Embed_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := NewOllamaProvider(server.URL)
	_, err := provider.Embed(context.Background(), []string{"test"})
	if err == nil {
		t.Error("expected error for 500 response")
	}
}
