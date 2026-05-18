package vector

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

const (
	defaultVectorDim = 1536 // DeepSeek embedding 维度
)

// Vector 用于 pgvector 的向量类型（兼容 GORM）
type Vector []float32

// Value 实现 driver.Valuer，将向量转为 pgvector 字符串格式 [1.0,2.0,3.0]
func (v Vector) Value() (driver.Value, error) {
	if len(v) == 0 {
		return "[]", nil
	}
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

// Scan 实现 sql.Scanner，从 pgvector 字符串解析向量
func (v *Vector) Scan(src interface{}) error {
	switch src := src.(type) {
	case string:
		return v.parse(src)
	case []byte:
		return v.parse(string(src))
	default:
		return fmt.Errorf("Vector.Scan: unsupported type %T", src)
	}
}

func (v *Vector) parse(s string) error {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" || s == "{}" {
		*v = Vector{}
		return nil
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	parts := strings.Split(s, ",")
	result := make(Vector, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		f, err := strconv.ParseFloat(p, 32)
		if err != nil {
			return fmt.Errorf("Vector.parse: invalid float %q: %w", p, err)
		}
		result = append(result, float32(f))
	}
	*v = result
	return nil
}

// PgVectorDB Pgvector 实现
type PgVectorDB struct {
	db       *gorm.DB
	embedder func(ctx context.Context, texts []string) ([][]float32, error)
}

// NewPgVectorDB 创建 Pgvector 客户端
func NewPgVectorDB(db *gorm.DB) (*PgVectorDB, error) {
	// 确保 pgvector 扩展已安装
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		// 某些环境可能没有 superuser 权限，打印警告但不阻断
		fmt.Printf("[PgVectorDB] 警告: 无法创建 vector 扩展: %v (如已安装可忽略)\n", err)
	}
	// 自动创建表
	if err := db.AutoMigrate(&VectorDocument{}); err != nil {
		return nil, fmt.Errorf("迁移向量表失败: %w", err)
	}
	return &PgVectorDB{db: db}, nil
}

// SetEmbedder 设置嵌入器，用于 Search 时自动将文本转为向量
func (p *PgVectorDB) SetEmbedder(embedder func(ctx context.Context, texts []string) ([][]float32, error)) {
	p.embedder = embedder
}

// Search 向量检索（有 embedder 时做真正的向量检索，否则降级为文本匹配）
func (p *PgVectorDB) Search(ctx context.Context, query string, topK int, collection string) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 5
	}

	// 如果有 embedder，优先走向量检索
	if p.embedder != nil && query != "" {
		embeddings, err := p.embedder(ctx, []string{query})
		if err == nil && len(embeddings) > 0 && len(embeddings[0]) > 0 {
			return p.SearchByVector(ctx, embeddings[0], topK, collection)
		}
		fmt.Printf("[PgVectorDB] embedding 失败，降级为文本匹配: %v\n", err)
	}

	// 降级：文本关键词匹配 + 按创建时间倒序
	var docs []VectorDocument
	err := p.db.WithContext(ctx).
		Where("collection = ?", collection).
		Where("content ILIKE ?", "%"+query+"%").
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

// SearchByVector 基于向量相似度检索（cosine distance，值越小越相似）
func (p *PgVectorDB) SearchByVector(ctx context.Context, vector []float32, topK int, collection string) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 5
	}

	vec := Vector(vector)
	value, _ := vec.Value()
	vectorStr := value.(string)

	var docs []VectorDocument
	err := p.db.WithContext(ctx).
		Raw(`
			SELECT *, embedding <=> ?::vector AS distance
			FROM vector_documents
			WHERE collection = ?
			ORDER BY embedding <=> ?::vector
			LIMIT ?
		`, vectorStr, collection, vectorStr, topK).
		Scan(&docs).Error

	if err != nil {
		return nil, fmt.Errorf("Pgvector 向量检索失败: %w", err)
	}

	results := make([]SearchResult, 0, len(docs))
	for _, doc := range docs {
		// distance 越小越相似，转换为 0-1 的 score（1 表示最相似）
		score := 1.0 - doc.Distance
		if score < 0 {
			score = 0
		}
		results = append(results, SearchResult{
			ID:         doc.ID,
			Content:    doc.Content,
			DocumentID: doc.DocumentID,
			Score:      score,
			Metadata:   doc.Metadata,
		})
	}

	return results, nil
}

// Insert 插入向量文档（不带向量，兼容旧调用）
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

// InsertWithVectors 插入向量文档（带向量，用于真正的语义检索）
func (p *PgVectorDB) InsertWithVectors(ctx context.Context, docID string, chunks []string, vectors [][]float32, metadatas []map[string]interface{}, collection string) error {
	if len(chunks) != len(vectors) || len(chunks) != len(metadatas) {
		return fmt.Errorf("chunks(%d)/vectors(%d)/metadatas(%d) 数量不匹配", len(chunks), len(vectors), len(metadatas))
	}
	for i, chunk := range chunks {
		doc := VectorDocument{
			ID:         fmt.Sprintf("%s_%d", docID, i),
			DocumentID: docID,
			Collection: collection,
			Content:    chunk,
			Embedding:  Vector(vectors[i]),
			Metadata:   metadatas[i],
		}
		if err := p.db.WithContext(ctx).Create(&doc).Error; err != nil {
			return fmt.Errorf("Pgvector 向量插入失败: %w", err)
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
	Embedding  Vector                 `gorm:"type:vector(1536)"`
	Metadata   map[string]interface{} `gorm:"type:jsonb;serializer:json"`
	Score      float64                `gorm:"type:float;default:0"`
	Distance   float64                `gorm:"-"` // 临时字段，用于 Raw 查询的 distance 结果
	CreatedAt  int64                  `gorm:"autoCreateTime"`
}

func (VectorDocument) TableName() string {
	return "vector_documents"
}
