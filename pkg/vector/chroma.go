package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// VectorProvider 向量数据库接口
type VectorProvider interface {
	Search(ctx context.Context, query string, topK int, collection string) ([]SearchResult, error)
	Insert(ctx context.Context, docID string, chunks []string, metadatas []map[string]interface{}, collection string) error
	Delete(ctx context.Context, docIDs []string, collection string) error
	GetCollections(ctx context.Context) ([]CollectionInfo, error)
}

// SearchResult 检索结果
type SearchResult struct {
	ID         string                 `json:"id"`
	Content    string                 `json:"content"`
	DocumentID string                 `json:"document_id"`
	Score      float64                `json:"score"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// CollectionInfo 集合信息
type CollectionInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Count    int64  `json:"count"`
}

// ChromaDB ChromaDB 向量数据库实现
type ChromaDB struct {
	endpoint string
	client   *http.Client
}

// NewChromaDB 创建 ChromaDB 客户端
func NewChromaDB(endpoint string) (*ChromaDB, error) {
	return &ChromaDB{
		endpoint: endpoint,
		client:   &http.Client{},
	}, nil
}

func (c *ChromaDB) Search(ctx context.Context, query string, topK int, collection string) ([]SearchResult, error) {
	reqBody := map[string]interface{}{
		"query_embeddings": query,
		"n_results":        topK,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/collections/%s/query", c.endpoint, collection)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ChromaDB 查询失败: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		IDs       [][]string          `json:"ids"`
		Distances [][]float64         `json:"distances"`
		Metadatas [][][]map[string]interface{} `json:"metadatas"`
		Documents [][]string           `json:"documents"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	searchResults := make([]SearchResult, 0)
	for i := range result.IDs {
		for j, id := range result.IDs[i] {
			var content string
			if len(result.Documents) > i && len(result.Documents[i]) > j {
				content = result.Documents[i][j]
			}
			var score float64 = 0
			if len(result.Distances) > i && len(result.Distances[i]) > j {
				score = 1 - result.Distances[i][j]
			}
			var metadata map[string]interface{}
			if len(result.Metadatas) > i && len(result.Metadatas[i]) > j {
				metadata = result.Metadatas[i][j]
			}
			searchResults = append(searchResults, SearchResult{
				ID:         id,
				Content:    content,
				DocumentID: id,
				Score:      score,
				Metadata:   metadata,
			})
		}
	}

	return searchResults, nil
}

func (c *ChromaDB) Insert(ctx context.Context, docID string, chunks []string, metadatas []map[string]interface{}, collection string) error {
	ids := make([]string, len(chunks))
	for i := range ids {
		ids[i] = fmt.Sprintf("%s_%d", docID, i)
	}

	reqBody := map[string]interface{}{
		"ids":       ids,
		"documents": chunks,
		"metadatas": metadatas,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/collections/%s/add", c.endpoint, collection)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ChromaDB 插入失败: status %d", resp.StatusCode)
	}

	return nil
}

func (c *ChromaDB) Delete(ctx context.Context, docIDs []string, collection string) error {
	reqBody := map[string]interface{}{
		"ids": docIDs,
	}

	jsonData, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/collections/%s/delete", c.endpoint, collection)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (c *ChromaDB) GetCollections(ctx context.Context) ([]CollectionInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/collections", nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Count int64  `json:"count"` // ChromaDB 可能不直接返回 count
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	collections := make([]CollectionInfo, 0, len(result))
	for _, c := range result {
		collections = append(collections, CollectionInfo{
			ID:   c.ID,
			Name: c.Name,
		})
	}
	return collections, nil
}
