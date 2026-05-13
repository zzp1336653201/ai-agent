package handler

import (
	"net/http"

	"sirenagent/internal/service"

	"github.com/gin-gonic/gin"
)

// QueryHandler RAG 问答 HTTP 处理器
type QueryHandler struct {
	svc *service.RAGService
}

func NewQueryHandler(svc *service.RAGService) *QueryHandler {
	return &QueryHandler{svc: svc}
}

// Query RAG 检索问答
func (h *QueryHandler) Query(c *gin.Context) {
	var req service.QueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.svc.Query(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}
