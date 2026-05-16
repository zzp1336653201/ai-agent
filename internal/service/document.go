package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sirenagent/internal/model"
	"sirenagent/pkg/vector"
)

// DocumentService 文档管理服务
type DocumentService struct {
	repo     DocumentRepository
	vectorDB vector.VectorProvider
}

type DocumentRepository interface {
	Create(doc *model.Document) error
	GetByID(id string) (*model.Document, error)
	List(page, size int) ([]*model.Document, int64, error)
	ListByAgent(agentID string, page, size int) ([]*model.Document, int64, error)
	ListByCategory(category string, page, size int) ([]*model.Document, int64, error)
	Delete(id string) error
}

func NewDocumentService(repo DocumentRepository, vectorDB vector.VectorProvider) *DocumentService {
	return &DocumentService{repo: repo, vectorDB: vectorDB}
}

// UploadRequest 文档上传请求（知识管理员使用）
type UploadRequest struct {
	Title    string   `json:"title" binding:"required"`
	Content  string   `json:"content" binding:"required"`
	Type     string   `json:"type"`     // txt|md|html|pdf|docx — 留空自动检测
	Category string   `json:"category"` // product|tech|faq|policy|manual|other
	Tags     []string `json:"tags"`     // 标签列表
	Summary  string   `json:"summary"`  // 文档摘要（可选）
	AgentID  string   `json:"agent_id"` // 所属 Agent（可选，空=全局知识库）
}

// ImportURLRequest 从 URL 导入知识
type ImportURLRequest struct {
	URL      string   `json:"url" binding:"required"`
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
	AgentID  string   `json:"agent_id"`
}

// Upload 上传文档并分块入库
func (s *DocumentService) Upload(ctx context.Context, req *UploadRequest) (*model.Document, error) {
	// 自动检测类型：如果是 HTML 内容，自动解析
	content := req.Content
	docType := req.Type
	if docType == "" {
		if IsHTMLLike(content) {
			docType = "html"
		} else {
			docType = "txt"
		}
	}

	// HTML 类型自动解析为纯文本（保留结构化信息）
	rawContent := content // 保留原始内容
	if docType == "html" {
		parsed := ParseHTML(content)
		content = parsed.Content
		if req.Title == "" && parsed.Title != "" {
			req.Title = parsed.Title
		}
		fmt.Printf("[DocumentService] HTML 解析完成: title=%q, chunks=%d, chars=%d\n",
			parsed.Title, len(parsed.Chunks), len([]rune(content)))
	}

	// 构建标签 JSON
	tagsJSON := "[]"
	if len(req.Tags) > 0 {
		escaped := make([]string, len(req.Tags))
		for i, t := range req.Tags {
			escaped[i] = fmt.Sprintf("%q", t)
		}
		tagsJSON = "[" + strings.Join(escaped, ",") + "]"
	}

	doc := model.NewDocument(req.Title, rawContent, docType, "", req.AgentID)
	doc.Summary = req.Summary
	doc.Category = req.Category
	doc.Tags = tagsJSON
	doc.CreatedBy = "admin"
	if err := s.repo.Create(doc); err != nil {
		return nil, fmt.Errorf("保存文档失败: %w", err)
	}

	// 确定向量集合名称
	collection := s.resolveCollection(req.AgentID)

	// 智能分块（HTML 内容已有解析后的块，直接用）
	var chunks []string
	var chunkInfo []ChunkInfo

	if docType == "html" {
		parsed := ParseHTML(rawContent)
		if len(parsed.Chunks) > 0 {
			chunks = parsed.Chunks
		} else {
			chunks = smartChunkFallback(content)
		}
		chunkInfo = parsed.ChunkInfo
	} else {
		chunks = smartChunkFallback(content)
	}

	doc.ChunkCount = len(chunks)

	// 构建元数据（包含分类、标签、标题信息）
	metadatas := make([]map[string]interface{}, len(chunks))
	for i := range chunks {
		heading := ""
		if i < len(chunkInfo) {
			heading = chunkInfo[i].Heading
		}
		metadatas[i] = map[string]interface{}{
			"doc_id":      doc.ID,
			"title":       req.Title,
			"heading":     heading,
			"chunk_index": i,
			"type":        docType,
			"category":    req.Category,
			"tags":        req.Tags,
			"agent_id":    req.AgentID,
		}
	}

	// 插入向量库
	if s.vectorDB != nil {
		fmt.Printf("[DocumentService] 向量入库: doc=%s, chunks=%d, collection=%s\n",
			doc.ID, len(chunks), collection)
		if err := s.vectorDB.Insert(ctx, doc.ID, chunks, metadatas, collection); err != nil {
			return nil, fmt.Errorf("向量入库失败: %w", err)
		}
	}

	fmt.Printf("[DocumentService] 文档上传成功: %q (ID=%s, type=%s, category=%s, chunks=%d)\n",
		req.Title, doc.ID, docType, req.Category, len(chunks))
	return doc, nil
}

// ImportFromURL 从 URL 导入知识文档
func (s *DocumentService) ImportFromURL(ctx context.Context, req *ImportURLRequest) (*model.Document, error) {
	if !IsURL(req.URL) {
		return nil, fmt.Errorf("无效的 URL: %s", req.URL)
	}

	fmt.Printf("[DocumentService] 开始从 URL 导入知识: %s\n", req.URL)

	parsed, err := FetchAndParseURL(req.URL, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("抓取 URL 失败: %w", err)
	}

	title := parsed.Title
	if title == "" {
		title = extractTitleFromURL(req.URL)
	}

	// 直接使用 ParseHTML 的解析结果构建文档
	tagsJSON := "[]"
	if len(req.Tags) > 0 {
		escaped := make([]string, len(req.Tags))
		for i, t := range req.Tags {
			escaped[i] = fmt.Sprintf("%q", t)
		}
		tagsJSON = "[" + strings.Join(escaped, ",") + "]"
	}

	doc := model.NewDocument(title, parsed.Content, "url", "", req.AgentID)
	doc.Summary = TruncateContent(parsed.Content, 200)
	doc.Category = req.Category
	doc.Tags = tagsJSON
	doc.SourceURL = req.URL
	doc.CreatedBy = "admin"
	doc.ChunkCount = len(parsed.Chunks)

	if err := s.repo.Create(doc); err != nil {
		return nil, fmt.Errorf("保存文档失败: %w", err)
	}

	collection := s.resolveCollection(req.AgentID)

	// 构建元数据
	metadatas := make([]map[string]interface{}, len(parsed.Chunks))
	for i := range parsed.Chunks {
		heading := ""
		if i < len(parsed.ChunkInfo) {
			heading = parsed.ChunkInfo[i].Heading
		}
		metadatas[i] = map[string]interface{}{
			"doc_id":      doc.ID,
			"title":       title,
			"heading":     heading,
			"chunk_index": i,
			"type":        "url",
			"category":    req.Category,
			"tags":        req.Tags,
			"source_url":  req.URL,
			"agent_id":    req.AgentID,
		}
	}

	if s.vectorDB != nil {
		fmt.Printf("[DocumentService] URL导入向量入库: doc=%s, chunks=%d, collection=%s\n",
			doc.ID, len(parsed.Chunks), collection)
		if err := s.vectorDB.Insert(ctx, doc.ID, parsed.Chunks, metadatas, collection); err != nil {
			return nil, fmt.Errorf("向量入库失败: %w", err)
		}
	}

	fmt.Printf("[DocumentService] URL导入成功: %q (URL=%s, chunks=%d)\n", title, req.URL, len(parsed.Chunks))
	return doc, nil
}

// resolveCollection 根据 AgentID 解析向量集合名称
func (s *DocumentService) resolveCollection(agentID string) string {
	if agentID == "" {
		return "agent_knowledge" // 全局默认集合
	}
	return "kb_" + agentID
}

// smartChunkFallback 智能分块（保底策略）
func smartChunkFallback(text string) []string {
	chunks, _ := smartChunk(text)
	if len(chunks) > 0 {
		return chunks
	}
	return nil
}

// ListDocuments 列出文档
func (s *DocumentService) List(page, size int) ([]*model.Document, int64, error) {
	return s.repo.List(page, size)
}

// ListByCategory 按分类列出文档
func (s *DocumentService) ListByCategory(category string, page, size int) ([]*model.Document, int64, error) {
	return s.repo.ListByCategory(category, page, size)
}

// Delete 删除文档
func (s *DocumentService) Delete(ctx context.Context, id string) error {
	doc, err := s.repo.GetByID(id)
	if err != nil || doc == nil {
		if s.vectorDB != nil {
			s.vectorDB.Delete(ctx, []string{id}, "agent_knowledge")
		}
		return s.repo.Delete(id)
	}

	collection := s.resolveCollection(doc.AgentID)
	if s.vectorDB != nil {
		fmt.Printf("[DocumentService] 删除向量: doc=%s, collection=%s\n", id, collection)
		s.vectorDB.Delete(ctx, []string{id}, collection)
	}
	return s.repo.Delete(id)
}

// Get 获取文档详情
func (s *DocumentService) Get(id string) (*model.Document, error) {
	return s.repo.GetByID(id)
}

// extractTitleFromURL 从 URL 中提取标题
func extractTitleFromURL(rawURL string) string {
	// 从路径最后一段提取
	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		last = strings.ReplaceAll(last, "-", " ")
		last = strings.ReplaceAll(last, "_", " ")
		// 去掉扩展名
		if idx := strings.LastIndex(last, "."); idx > 0 {
			last = last[:idx]
		}
		if last != "" {
			return last
		}
	}
	return "URL导入文档"
}
