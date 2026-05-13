package handler

import (
	"net/http"
	"strconv"

	"sirenagent/internal/service"

	"github.com/gin-gonic/gin"
)

// AgentHandler Agent 相关 HTTP 处理器
type AgentHandler struct {
	svc *service.AgentService
}

func NewAgentHandler(svc *service.AgentService) *AgentHandler {
	return &AgentHandler{svc: svc}
}

// Create 创建智能体
// POST /api/v1/agents
func (h *AgentHandler) Create(c *gin.Context) {
	var req service.CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	agent, err := h.svc.Create(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": agent})
}

// List 列出智能体
// GET /api/v1/agents?status=active&page=1&size=20
func (h *AgentHandler) List(c *gin.Context) {
	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	agents, total, err := h.svc.List(status, page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  agents,
		"total": total,
		"page":  page,
		"size":  size,
	})
}

// Get 获取智能体详情
// GET /api/v1/agents/:id
func (h *AgentHandler) Get(c *gin.Context) {
	id := c.Param("id")
	agent, err := h.svc.GetAgent(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "智能体不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": agent})
}

// Chat 与智能体对话（核心 API）
// POST /api/v1/agents/:id/chat
func (h *AgentHandler) Chat(c *gin.Context) {
	id := c.Param("id")
	var req service.ChatRequest
	req.AgentID = id

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.svc.Chat(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// ChatStream 流式对话（SSE）
// GET /api/v1/agents/:id/chat/stream?message=xxx
func (h *AgentHandler) ChatStream(c *gin.Context) {
	id := c.Param("id")

	req := service.ChatRequest{
		AgentID: id,
		Message: c.Query("message"),
		UserID:  c.Query("user_id"),
		Stream:  true,
	}

	events, err := h.svc.ChatStream(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	for event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
	}

	fmt.Fprint(c.Writer, "event: done\ndata: {}\n\n")
	c.Writer.Flush()
}

// Delete 删除智能体
// DELETE /api/v1/agents/:id
func (h *AgentHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.svc.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

import (
	"encoding/json"
)
