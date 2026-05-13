package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"sirenagent/internal/config"
	"sirenagent/internal/core"
	"sirenagent/internal/handler"
	"sirenagent/internal/platform"
	"sirenagent/internal/router"
	"sirenagent/internal/service"
	"sirenagent/pkg/llm"
	"sirenagent/pkg/vector"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	// 1. 初始化日志
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	// 2. 加载配置
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("配置加载失败", zap.Error(err))
	}
	sugar := logger.Sugar()

	sugar.Infof("🚀 SirenAgent 智能体平台启动中...")

	// 3. 初始化 LLM 提供者（策略模式：Ollama / OpenAI / 豆包）
	llmProvider := llm.NewLLMProvider(cfg.LLM.Provider, cfg.LLM.Endpoint, cfg.LLM.APIKey)
	sugar.Infof("✅ LLM 提供者: %s (%s)", cfg.LLM.Provider, cfg.LLM.Model)

	// 4. 初始化向量数据库（支持 ChromaDB / Pgvector）
	var vectorDB vector.VectorProvider
	if cfg.VectorDB.Provider == "pgvector" {
		// 连接 PostgreSQL 作为向量数据库
		dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Database.Host, cfg.Database.Port, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.SSLMode)
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err != nil {
			logger.Fatal("PostgreSQL 连接失败", zap.Error(err))
		}
		sqlDB, _ := db.DB()
		defer sqlDB.Close()

		pgVDB, err := vector.NewPgVectorDB(db)
		if err != nil {
			logger.Fatal("Pgvector 初始化失败", zap.Error(err))
		}
		vectorDB = pgVDB
		sugar.Infof("✅ 向量数据库: Pgvector (PostgreSQL)")
	} else {
		// 使用 ChromaDB（开发模式下初始化失败使用 Mock）
		chromaDB, err := vector.NewChromaDB(cfg.VectorDB.Endpoint)
		if err != nil {
			sugar.Warnf("⚠️  ChromaDB 连接失败，使用 Mock 向量数据库: %v", err)
			vectorDB = vector.NewMockVectorProvider()
			sugar.Info("✅ 向量数据库: Mock (开发模式)")
		} else {
			vectorDB = chromaDB
			sugar.Infof("✅ 向量数据库: ChromaDB @ %s", cfg.VectorDB.Endpoint)
		}
	}

	// 5. 初始化记忆管理器
	memoryMgr := core.NewInMemoryMemoryManager()
	promptMgr := core.NewTemplatePromptManager()

	// 注册内置 Prompt 模板
	registerPromptTemplates(promptMgr)
	sugar.Info("✅ Prompt 模板已加载")

	// 6. 初始化 Agent 引擎 — 核心组件组装
	engineConfig := core.EngineConfig{
		MaxIterations: cfg.Agent.MaxIterations,
		MemoryTTL:     cfg.Agent.MemoryTTL,
		ToolsTimeout:  cfg.Agent.ToolsTimeout,
	}
	agentEngine := core.NewAgentEngine(llmProvider, vectorDB, memoryMgr, engineConfig)

	// 注册内置工具（工具调用系统）
	registerBuiltInTools(agentEngine)
	sugar.Infof("✅ 已注册 %d 个内置工具", len(agentEngine.ListTools()))

	// 设置 Prompt 管理器
	agentEngine.SetPromptManager(promptMgr)

	// 7. 初始化工作流引擎
	workflowEngine := core.NewWorkflowEngine(agentEngine, nil) // store 后续注入
	sugar.Info("✅ 工作流引擎就绪")

	// 8. 初始化社交媒体平台适配器
	platformAdapters := platform.NewPlatformAdapters(&cfg.SocialPlatforms)
	for name := range platformAdapters {
		sugar.Infof("✅ 平台适配器: %s", name)
	}

	// 9. 创建 Service 层（使用 Mock Repository，生产环境替换为真实 DB）
	agentSvc := service.NewAgentService(agentEngine, &MockAgentRepository{})
	workflowSvc := service.NewWorkflowService(workflowEngine, &MockWorkflowRepository{})
	docSvc := service.NewDocumentService(&MockDocumentRepository{}, vectorDB)
	ragSvc := service.NewRAGService(llmProvider, vectorDB, memoryMgr)

	// 10. 创建 Handler 层
	agentHandler := handler.NewAgentHandler(agentSvc)
	workflowHandler := handler.NewWorkflowHandler(workflowSvc)
	docHandler := handler.NewDocumentHandler(docSvc)
	queryHandler := handler.NewQueryHandler(ragSvc)

	// 11. 启动 HTTP 服务
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000", "http://localhost:5173", "http://127.0.0.1:5500"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))
	r.Use(gin.Recovery(), gin.Logger())

	// 12. 托管前端静态文件
	r.Static("/static", "./web")
	r.GET("/", func(c *gin.Context) {
		c.File("./web/index.html")
	})

	router.RegisterRoutes(r, agentHandler, workflowHandler, docHandler, queryHandler)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	go func() {
		if err := r.Run(addr); err != nil {
			logger.Fatal("HTTP 服务启动失败", zap.Error(err))
		}
	}()

	sugar.Infof("\n========================================")
	sugar.Infof("  🎯 SirenAgent AI 智能体平台")
	sugar.Infof("  📡 API 地址: http://localhost:%d", cfg.Server.Port)
	sugar.Infof("  📖 API 文档: http://localhost:%d/api/v1/health", cfg.Server.Port)
	sugar.Infof("  🔧 LLM: %s / Model: %s", cfg.LLM.Provider, cfg.LLM.Model)
	sugar.Infof("  💾 VectorDB: %s", cfg.VectorDB.Provider)
	sugar.Infof("  🛠 工具数: %d", len(agentEngine.ListTools()))
	_ = platformAdapters
	sugar.Infof("========================================\n")

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务...")
}

// registerBuiltInTools 注册内置工具集
func registerBuiltInTools(engine *core.AgentEngine) {
	engine.RegisterTool(core.NewWebSearchTool())       // 网络搜索
	engine.RegisterTool(core.NewRAGSearchTool(engine))  // RAG 知识库检索
	engine.RegisterTool(core.NewHTTPRequestTool())     // HTTP API 调用
	engine.RegisterTool(core.NewCalculatorTool())       // 计算器
	engine.RegisterTool(core.NewFileReadTool())         // 文件读取
}

// registerPromptTemplates 注册 Prompt 模板
func registerPromptTemplates(pm *core.TemplatePromptManager) {
	pm.RegisterTemplate(
		"agent_system",
		`# 角色
你是一个名为「{{name}}」的 AI 智能体。
## 描述
{{description}}
## 能力
- 你可以调用多种工具来完成任务
- 通过「思考→行动→观察」的循环逐步解决问题`,
		"system",
		[]core.PromptVariable{
			{Name: "name", Description: "智能体名称", Required: true},
			{Name: "description", Description: "智能体描述", Required: true},
		},
	)

	pm.RegisterTemplate(
		"rag_query",
		`基于以下上下文信息回答用户问题。
{{context}}
问题：{{query}}
要求：回答要准确且基于上下文内容，使用简洁清晰的中文回答。`,
		"rag",
		[]core.PromptVariable{
			{Name: "context", Description: "检索到的上下文文档", Required: true},
			{Name: "query", Description: "用户问题", Required: true},
		},
	)
}
