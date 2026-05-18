package handler

import (
	"fmt"
	"net/http"
	"time"

	"sirenagent/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TestHandler 测试记录 HTTP 处理器
type TestHandler struct {
	db *gorm.DB
}

func NewTestHandler(db *gorm.DB) *TestHandler {
	return &TestHandler{db: db}
}

// SaveRecord 保存测试记录
// POST /api/v1/tests
func (h *TestHandler) SaveRecord(c *gin.Context) {
	var req struct {
		TestName string `json:"test_name" binding:"required"`
		Category string `json:"category" binding:"required"` // agent|document|chat|workflow|system
		Status   string `json:"status" binding:"required"`   // success|failed
		Request  string `json:"request"`                     // JSON 字符串
		Response string `json:"response"`                    // JSON 字符串
		Duration int64  `json:"duration"`                    // 耗时(ms)
		ErrorMsg string `json:"error_msg"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	record := &model.TestRecord{
		ID:        fmt.Sprintf("test_%d", time.Now().UnixNano()),
		TestName:  req.TestName,
		Category:  req.Category,
		Status:    req.Status,
		Request:   req.Request,
		Response:  req.Response,
		Duration:  req.Duration,
		ErrorMsg:  req.ErrorMsg,
		CreatedAt: time.Now(),
	}

	if err := h.db.Create(record).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": record})
}

// ListRecords 列出测试记录
// GET /api/v1/tests?category=agent&page=1&size=20
func (h *TestHandler) ListRecords(c *gin.Context) {
	category := c.Query("category")
	page := parseInt(c.DefaultQuery("page", "1"), 1)
	size := parseInt(c.DefaultQuery("size", "20"), 20)
	status := c.Query("status")

	var records []*model.TestRecord
	var total int64

	query := h.db.Model(&model.TestRecord{})
	if category != "" {
		query = query.Where("category = ?", category)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	offset := (page - 1) * size
	if err := query.Offset(offset).Limit(size).Order("created_at DESC").Find(&records).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": records, "total": total, "page": page, "size": size})
}

// DeleteRecord 删除测试记录
// DELETE /api/v1/tests/:id
func (h *TestHandler) DeleteRecord(c *gin.Context) {
	id := c.Param("id")
	if err := h.db.Where("id = ?", id).Delete(&model.TestRecord{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// DeleteAllRecords 清空测试记录
// DELETE /api/v1/tests
func (h *TestHandler) DeleteAllRecords(c *gin.Context) {
	if err := h.db.Where("1 = 1").Delete(&model.TestRecord{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清空失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已清空所有测试记录"})
}

// RunAllTests 一键运行所有测试
// POST /api/v1/tests/run-all
func (h *TestHandler) RunAllTests(c *gin.Context) {
	// 这个端点仅返回需要前端执行的测试用例列表
	// 实际测试由前端逐个调用 API 并记录结果
	tests := []map[string]string{
		{"name": "健康检查", "category": "system", "endpoint": "/health", "method": "GET"},
		{"name": "创建智能体", "category": "agent", "endpoint": "/agents", "method": "POST"},
		{"name": "列出智能体", "category": "agent", "endpoint": "/agents", "method": "GET"},
		{"name": "上传文档", "category": "document", "endpoint": "/documents", "method": "POST"},
		{"name": "列出文档", "category": "document", "endpoint": "/documents", "method": "GET"},
		{"name": "RAG 查询", "category": "chat", "endpoint": "/query", "method": "POST"},
		{"name": "创建工作流", "category": "workflow", "endpoint": "/workflows", "method": "POST"},
		{"name": "列出工作流", "category": "workflow", "endpoint": "/workflows", "method": "GET"},
	}
	c.JSON(http.StatusOK, gin.H{"data": tests, "total": len(tests)})
}

// GetTestStats 获取测试统计
// GET /api/v1/tests/stats
func (h *TestHandler) GetTestStats(c *gin.Context) {
	var stats []struct {
		Category string `json:"category"`
		Total    int64  `json:"total"`
		Success  int64  `json:"success"`
		Failed   int64  `json:"failed"`
	}

	rows, err := h.db.Model(&model.TestRecord{}).
		Select("category, count(*) as total, sum(case when status = 'success' then 1 else 0 end) as success, sum(case when status = 'failed' then 1 else 0 end) as failed").
		Group("category").Rows()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var s struct {
			Category string `json:"category"`
			Total    int64  `json:"total"`
			Success  int64  `json:"success"`
			Failed   int64  `json:"failed"`
		}
		rows.Scan(&s.Category, &s.Total, &s.Success, &s.Failed)
		stats = append(stats, s)
	}

	c.JSON(http.StatusOK, gin.H{"data": stats})
}

func parseInt(s string, defaultVal int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return defaultVal
	}
	return n
}


