package vector

import (
	"context"
)

// MockVectorProvider Mock 向量数据库（用于开发测试）
type MockVectorProvider struct{}

// NewMockVectorProvider 创建 Mock 向量数据库
func NewMockVectorProvider() *MockVectorProvider {
	return &MockVectorProvider{}
}

// Search Mock 检索
func (m *MockVectorProvider) Search(ctx context.Context, query string, topK int, collection string) ([]SearchResult, error) {
	return []SearchResult{}, nil
}

// Insert Mock 插入
func (m *MockVectorProvider) Insert(ctx context.Context, docID string, chunks []string, metadatas []map[string]interface{}, collection string) error {
	return nil
}

// Delete Mock 删除
func (m *MockVectorProvider) Delete(ctx context.Context, docIDs []string, collection string) error {
	return nil
}

// InsertWithVectors Mock 插入带向量
func (m *MockVectorProvider) InsertWithVectors(ctx context.Context, docID string, chunks []string, vectors [][]float32, metadatas []map[string]interface{}, collection string) error {
	return nil
}

// SearchByVector Mock 向量检索
func (m *MockVectorProvider) SearchByVector(ctx context.Context, vector []float32, topK int, collection string) ([]SearchResult, error) {
	return []SearchResult{}, nil
}

// GetCollections Mock 获取集合
func (m *MockVectorProvider) GetCollections(ctx context.Context) ([]CollectionInfo, error) {
	return []CollectionInfo{}, nil
}
