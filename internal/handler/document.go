package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"sirenagent/internal/model"
	"sirenagent/internal/service"

	"github.com/gin-gonic/gin"
)

// DocumentHandler 文档管理 HTTP 处理器
type DocumentHandler struct {
	svc *service.DocumentService
}

func NewDocumentHandler(svc *service.DocumentService) *DocumentHandler {
	return &DocumentHandler{svc: svc}
}

// Upload 上传文档（JSON 或 form-data 文件上传）
// JSON: 直接传 title/content/type/category/tags/agent_id
// form-data: 上传文件 + 表单字段
func (h *DocumentHandler) Upload(c *gin.Context) {
	contentType := c.GetHeader("Content-Type")

	// 文件上传模式（multipart/form-data）
	if len(contentType) >= 24 && contentType[:24] == "multipart/form-data" {
		h.uploadFile(c)
		return
	}

	// JSON 模式
	var req service.UploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Title == "" {
		// 从内容中自动提取标题
		req.Title = autoExtractTitle(req.Content)
	}

	doc, err := h.svc.Upload(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": doc})
}

// uploadFile 处理文件上传（支持 .html, .txt, .md 文件）
func (h *DocumentHandler) uploadFile(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传文件 (字段名: file)"})
		return
	}

	// 读取文件内容
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "打开文件失败"})
		return
	}
	defer src.Close()

	buf := make([]byte, file.Size)
	if _, err := src.Read(buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取文件失败"})
		return
	}
	content := string(buf)

	// 自动检测类型
	docType := detectFileType(file.Filename)
	title := c.PostForm("title")
	if title == "" {
		title = file.Filename
	}

	category := c.PostForm("category")
	agentID := c.PostForm("agent_id")

	// 标签（逗号分隔）
	tagsStr := c.PostForm("tags")
	var tags []string
	if tagsStr != "" {
		tags = splitTags(tagsStr)
	}

	req := &service.UploadRequest{
		Title:    title,
		Content:  content,
		Type:     docType,
		Category: category,
		Tags:     tags,
		AgentID:  agentID,
	}

	doc, err := h.svc.Upload(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "上传失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": doc, "message": fmt.Sprintf("文件 %s 上传成功，已分块 %d 个", file.Filename, doc.ChunkCount)})
}

// ImportFromURL 从 URL 导入知识
func (h *DocumentHandler) ImportFromURL(c *gin.Context) {
	var req service.ImportURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	doc, err := h.svc.ImportFromURL(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"data":    doc,
		"message": fmt.Sprintf("URL 导入成功: %s (已分块 %d 个)", req.URL, doc.ChunkCount),
	})
}

// List 列出文档（支持按 agent_id / category 过滤）
func (h *DocumentHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	agentID := c.Query("agent_id")
	category := c.Query("category")

	// 如果指定了 category，走按分类查询
	if category != "" {
		docs, total, err := h.svc.ListByCategory(category, page, size)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": docs, "total": total, "page": page, "size": size})
		return
	}

	docs, total, err := h.svc.List(page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 如果指定了 agent_id，过滤
	if agentID != "" {
		filtered := make([]*model.Document, 0)
		for _, d := range docs {
			if d.AgentID == agentID {
				filtered = append(filtered, d)
			}
		}
		docs = filtered
		total = int64(len(filtered))
	}

	c.JSON(http.StatusOK, gin.H{"data": docs, "total": total, "page": page, "size": size})
}

// Get 获取文档详情
func (h *DocumentHandler) Get(c *gin.Context) {
	id := c.Param("id")
	doc, err := h.svc.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": doc})
}

// Delete 删除文档
func (h *DocumentHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// ==================== 辅助函数 ====================

// detectFileType 根据文件名检测文档类型
func detectFileType(filename string) string {
	for i := len(filename) - 1; i >= 0; i-- {
		if filename[i] == '.' {
			ext := filename[i+1:]
			switch ext {
			case "html", "htm":
				return "html"
			case "md", "markdown":
				return "md"
			case "txt":
				return "txt"
			case "pdf":
				return "pdf"
			case "docx", "doc":
				return "docx"
			default:
				return "txt"
			}
		}
	}
	return "txt"
}

// autoExtractTitle 从内容中自动提取标题
func autoExtractTitle(content string) string {
	// 从 Markdown 标题提取
	lines := ([]rune)(content)
	if len(lines) > 0 {
		lineStr := string(lines)
		// 找第一个 # 开头的行
		for _, line := range splitLines(lineStr) {
			trimmed := trimSpaceLeft(line)
			if len(trimmed) > 2 && trimmed[0] == '#' && trimmed[1] == ' ' {
				return trimSpaceLeft(trimmed[2:])
			}
		}
	}
	return "未命名文档"
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimSpaceLeft(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:]
}

// splitTags 将逗号分隔的标签字符串转为数组
func splitTags(s string) []string {
	var tags []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			tag := stringsTrimSpace(s[start:i])
			if tag != "" {
				tags = append(tags, tag)
			}
			start = i + 1
		}
	}
	tag := stringsTrimSpace(s[start:])
	if tag != "" {
		tags = append(tags, tag)
	}
	return tags
}

func stringsTrimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	if start >= end {
		return ""
	}
	return s[start:end]
}
