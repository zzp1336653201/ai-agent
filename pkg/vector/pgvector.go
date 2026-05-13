package vector

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

const (
	defaultVectorDim = 1536 // DeepSeek embedding 维度
)

// PgVectorDB Pgvector 实现
type PgVectorDB struct {
	db *gorm.DB
}

// NewPgVectorDB 创建 Pgvector 客户端
func NewPgVectorDB(db *gorm.DB) (*PgVectorDB, error) {
	// 自动创建表
	if err := db.AutoMigrate(&VectorDocument{}); err != nil {
		return nil, fmt.Errorf("迁移向量表失败: %w", err)
	}
	return &PgVectorDB{db: db}, nil
}

// Search 向量检索
func (p *PgVectorDB) Search(ctx context.Context, query string, topK int, collection string) ([]SearchResult, error) {
	// query 是搜索文本，topK 是返回数量，collection 是集合名
	// 由于没有 embedding 模型，这里简化处理：直接按内容匹配或返回最近的文档
	if topK <= 0 {
		topK = 5
	}

	var docs []VectorDocument
	// 使用向量相似度搜索（简化为按 ID 顺序，实际生产应使用向量索引）
	err := p.db.WithContext(ctx).
		Where("collection = ?", collection).
		Order("id DESC").
		Limit(topK).
		Find(&docs).Error

	if err != nil {
		return nil, fmt.Errorf("Pgvector 查询失败: %w", err)
	}

	results := make([]SearchResult, 0, len(docs))
	for _, doc := range docs {
		results = append(results, SearchResult{
			ID:         doc.ID,
			Content:    doc.Content,
			DocumentID: doc.DocumentID,
			Score:      doc.Score,
			Metadata:   doc.Metadata,
		})
	}

	return results, nil
}

// Insert 插入向量文档
func (p *PgVectorDB) Insert(ctx context.Context, docID string, chunks []string, metadatas []map[string]interface{}, collection string) error {
	for i, chunk := range chunks {
		doc := VectorDocument{
			ID:         fmt.Sprintf("%s_%d", docID, i),
			DocumentID: docID,
			Collection: collection,
			Content:    chunk,
			Metadata:   metadatas[i],
		}
		if err := p.db.WithContext(ctx).Create(&doc).Error; err != nil {
			return fmt.Errorf("Pgvector 插入失败: %w", err)
		}
	}
	return nil
}

// Delete 删除向量文档
func (p *PgVectorDB) Delete(ctx context.Context, docIDs []string, collection string) error {
	return p.db.WithContext(ctx).
		Where("document_id IN ?", docIDs).
		Where("collection = ?", collection).
		Delete(&VectorDocument{}).Error
}

// GetCollections 获取所有集合（分组）
func (p *PgVectorDB) GetCollections(ctx context.Context) ([]CollectionInfo, error) {
	type result struct {
		Collection string `gorm:"column:collection"`
		Count      int64  `gorm:"column:count"`
	}

	var results []result
	err := p.db.WithContext(ctx).
		Model(&VectorDocument{}).
		Select("collection, COUNT(*) as count").
		Group("collection").
		Find(&results).Error

	if err != nil {
		return nil, err
	}

	collections := make([]CollectionInfo, 0, len(results))
	for _, r := range results {
		collections = append(collections, CollectionInfo{
			ID:    r.Collection,
			Name:  r.Collection,
			Count: r.Count,
		})
	}
	return collections, nil
}

// VectorDocument 向量文档模型
type VectorDocument struct {
	ID         string                 `gorm:"primaryKey;type:varchar(100)"`
	DocumentID string                 `gorm:"index;type:varchar(100)"`
	Collection string                 `gorm:"index;type:varchar(100)"`
	Content    string                 `gorm:"type:text"`
	Metadata   map[string]interface{} `gorm:"type:jsonb;serializer:json"`
	Score      float64                `gorm:"type:float;default:0"`
	CreatedAt  int64                  `gorm:"autoCreateTime"`
}

func (VectorDocument) TableName() string {
	return "vector_documents"
}
