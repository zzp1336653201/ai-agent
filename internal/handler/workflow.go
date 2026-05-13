package handler

import (
	"net/http"
	"strconv"

	"sirenagent/internal/service"

	"github.com/gin-gonic/gin"
)

// WorkflowHandler 工作流相关 HTTP 处理器
type WorkflowHandler struct {
	svc *service.WorkflowService
}

func NewWorkflowHandler(svc *service.WorkflowService) *WorkflowHandler {
	return &WorkflowHandler{svc: svc}
}

// Create 创建工作流
func (h *WorkflowHandler) Create(c *gin.Context) {
	var req service.CreateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	wf, err := h.svc.Create(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": wf})
}

// List 列出工作流
func (h *WorkflowHandler) List(c *gin.Context) {
	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	wfs, total, err := h.svc.List(status, page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": wfs, "total": total, "page": page, "size": size})
}

// Get 获取工作流详情（含节点和边）
func (h *WorkflowHandler) Get(c *gin.Context) {
	id := c.Param("id")
	wf, err := h.svc.GetWorkflow(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "工作流不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": wf})
}

// Execute 执行工作流
func (h *WorkflowHandler) Execute(c *gin.Context) {
	var req service.ExecuteWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.svc.Execute(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// GetExecutions 获取执行记录
func (h *WorkflowHandler) GetExecutions(c *gin.Context) {
	workflowID := c.Param("id")
	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	executions, total, err := h.svc.GetExecutions(workflowID, status, page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": executions, "total": total, "page": page, "size": size})
}
