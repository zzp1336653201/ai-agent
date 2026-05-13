package service

import (
	"context"
	"fmt"

	"sirenagent/internal/model"
	"sirenagent/pkg/vector"
)

// DocumentService 文档管理服务
type DocumentService struct {
	repo     DocumentRepository
	vectorDB *vector.ChromaDB
}

type DocumentRepository interface {
	Create(doc *model.Document) error
	GetByID(id string) (*model.Document, error)
	List(page, size int) ([]*model.Document, int64, error)
	Delete(id string) error
}

func NewDocumentService(repo DocumentRepository, vectorDB *vector.ChromaDB) *DocumentService {
	return &DocumentService{repo: repo, vectorDB: vectorDB}
}

// UploadRequest 文档上传请求
type UploadRequest struct {
	Title   string `json:"title" binding:"required"`
	Content string `json:"content" binding:"required"`
	Type    string `json:"type"` // pdf|docx|md|txt
}

// Upload 上传文档并分块入库
func (s *DocumentService) Upload(ctx context.Context, req *UploadRequest) (*model.Document, error) {
	doc := model.NewDocument(req.Title, req.Content, req.Type, "")
	if err := s.repo.Create(doc); err != nil {
		return nil, fmt.Errorf("保存文档失败: %w", err)
	}

	// 分块处理
	chunks := s.chunkText(req.Content)

	// 构建元数据
	metadatas := make([]map[string]interface{}, len(chunks))
	for i := range chunks {
		metadatas[i] = map[string]interface{}{
			"doc_id": doc.ID,
			"title":  req.Title,
			"chunk_index": i,
			"type":   req.Type,
		}
	}

	// 插入向量库
	if s.vectorDB != nil {
		if err := s.vectorDB.Insert(ctx, doc.ID, chunks, metadatas, "agent_knowledge"); err != nil {
			return nil, fmt.Errorf("向量入库失败: &w", err)
		}
	}

	return doc, nil
}

// chunkText 文本分块 — RAG 的基础能力
// 策略：固定长度 + 重叠窗口（sliding window）
func (s *DocumentService) chunkText(text string) []string {
	const chunkSize = 500
	const overlap = 50

	chunks := make([]string, 0)
	runes := []rune(text)

	for i := 0; i < len(runes); i += (chunkSize - overlap) {
		end := i + chunkSize
		if end > len(runes) { end = len(runes) }
		chunks = append(chunks, string(runes[i:end]))
		if end >= len(runes) { break }
	}

	return chunks
}

// ListDocuments 列出文档
func (s *DocumentService) List(page, size int) ([]*model.Document, int64, error) {
	return s.repo.List(page, size)
}

// Delete 删除文档
func (s *DocumentService) Delete(ctx context.Context, id string) error {
	if s.vectorDB != nil {
		s.vectorDB.Delete(ctx, []string{id}, "agent_knowledge")
	}
	return s.repo.Delete(id)
}

// Get 获取文档详情
func (s *DocumentService) Get(id string) (*model.Document, error) {
	return s.repo.GetByID(id)
}
