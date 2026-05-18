package router

import (
	"sirenagent/internal/handler"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册所有路由
// 对应职位要求：前后端接口开发、RESTful API 设计
func RegisterRoutes(
	r *gin.Engine,
	agentHandler *handler.AgentHandler,
	workflowHandler *handler.WorkflowHandler,
	docHandler *handler.DocumentHandler,
	queryHandler *handler.QueryHandler,
	traceHandler *handler.TraceHandler,
	testHandler *handler.TestHandler,
) {
	api := r.Group("/api/v1")
	{
		// ========== Agent 智能体接口 ==========
		agents := api.Group("/agents")
		{
			agents.POST("", agentHandler.Create)          // 创建智能体
			agents.GET("", agentHandler.List)              // 列出智能体
			agents.GET("/:id", agentHandler.Get)           // 获取智能体详情
			agents.POST("/:id/chat", agentHandler.Chat)    // 与智能体对话
			agents.GET("/:id/chat/stream", agentHandler.ChatStream) // SSE 流式对话
			agents.DELETE("/:id", agentHandler.Delete)     // 删除智能体
		}

		// ========== 工作流接口 ==========
		workflows := api.Group("/workflows")
		{
			workflows.POST("", workflowHandler.Create)             // 创建工作流
			workflows.GET("", workflowHandler.List)                // 列出工作流
			workflows.GET("/:id", workflowHandler.Get)             // 获取工作流详情
			workflows.POST("/:id/execute", workflowHandler.Execute) // 执行工作流
			workflows.GET("/:id/executions", workflowHandler.GetExecutions) // 执行记录
		}

		// ========== 知识库/文档接口 ==========
		docs := api.Group("/documents")
		{
			docs.POST("", docHandler.Upload)              // 上传文档（JSON 或文件上传）
			docs.POST("/import-url", docHandler.ImportFromURL) // 从 URL 导入知识
			docs.GET("", docHandler.List)                 // 列出文档（支持 ?agent_id= / ?category=）
			docs.GET("/:id", docHandler.Get)              // 获取文档详情
			docs.DELETE("/:id", docHandler.Delete)        // 删除文档
		}

		// ========== RAG 问答接口 ==========
		api.POST("/query", queryHandler.Query) // 检索增强问答

		// ========== Trace 追踪记录 ==========
		traces := api.Group("/traces")
		{
			traces.GET("", traceHandler.List)       // 列出追踪记录
			traces.GET("/:id", traceHandler.Get)    // 查看单条追踪详情
		}

		// ========== 测试记录 ==========
		tests := api.Group("/tests")
		{
			tests.POST("", testHandler.SaveRecord)              // 保存测试记录
			tests.GET("", testHandler.ListRecords)               // 列出测试记录
			tests.DELETE("/:id", testHandler.DeleteRecord)       // 删除单条记录
			tests.DELETE("", testHandler.DeleteAllRecords)       // 清空全部记录
			tests.GET("/stats", testHandler.GetTestStats)        // 测试统计
			tests.POST("/run-all", testHandler.RunAllTests)      // 获取测试用例列表
		}

		// ========== 健康检查 ==========
		api.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{"status": "ok", "service": "sirenagent"})
		})
	}
}
