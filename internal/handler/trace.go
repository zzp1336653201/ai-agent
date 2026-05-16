package handler

import (
	"net/http"
	"strconv"

	"sirenagent/internal/core"

	"github.com/gin-gonic/gin"
)

// TraceHandler 追踪记录 HTTP 处理器
type TraceHandler struct{}

func NewTraceHandler() *TraceHandler {
	return &TraceHandler{}
}

// List 列出最近的追踪记录
func (h *TraceHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	traces := core.GlobalTraceStore.List(limit)
	c.JSON(http.StatusOK, gin.H{"data": traces, "total": len(traces)})
}

// Get 获取单条追踪详情
func (h *TraceHandler) Get(c *gin.Context) {
	traceID := c.Param("id")
	trace := core.GlobalTraceStore.GetByID(traceID)
	if trace == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追踪记录不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": trace})
}
