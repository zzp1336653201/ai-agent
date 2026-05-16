package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

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
	llmProvider := llm.NewLLMProvider(cfg.LLM.Provider, cfg.LLM.Endpoint, cfg.LLM.APIKey, cfg.LLM.Model)
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

	// 初始化 MCP 外部工具服务
	if cfg.MCP.Enabled && len(cfg.MCP.Servers) > 0 {
		mcpTools := registerMCPTools(agentEngine, &cfg.MCP)
		sugar.Infof("✅ 已注册 %d 个 MCP 外部工具", mcpTools)
	}

	// 设置 Prompt 管理器
	agentEngine.SetPromptManager(promptMgr)

	// 6.1 初始化 Evaluator-Optimizer 评估优化器
	evaluator := core.NewEvaluator(llmProvider, cfg.LLM.Model)
	agentEngine.SetEvaluator(evaluator)
	sugar.Info("✅ Evaluator-Optimizer 评估优化器已启用 (最多2轮优化)")

	// 6.2 初始化 Guardrails 防护系统
	if cfg.Guardrails.Enabled {
		guardrailMgr := core.NewGuardrailManager()
		if cfg.Guardrails.EnableSensitive {
			guardrailMgr.AddInputGuardrail(core.NewSensitiveContentGuardrail())
			sugar.Info("✅ Guardrail: 敏感内容检测已启用")
		}
		if cfg.Guardrails.EnablePII {
			guardrailMgr.AddInputGuardrail(core.NewPIIGuardrail())
			sugar.Info("✅ Guardrail: PII隐私信息检测已启用")
		}
		if cfg.Guardrails.MaxInputLen > 0 {
			guardrailMgr.AddInputGuardrail(core.NewLengthGuardrail(cfg.Guardrails.MaxInputLen))
			sugar.Info("✅ Guardrail: 输入长度限制 (%d字)", cfg.Guardrails.MaxInputLen)
		}
		// 输出防护
		guardrailMgr.AddOutputGuardrail(core.NewOutputSensitivityGuardrail())
		if cfg.Guardrails.EnableQuality {
			guardrailMgr.AddOutputGuardrail(core.NewOutputQualityGuardrail())
			sugar.Info("✅ Guardrail: 输出质量检查已启用")
		}
		// 工具防护 — 预设风险等级
		core.SetupDefaultToolRisks(guardrailMgr.ToolGuardrail())
		if cfg.Guardrails.EnableRateLimit {
			sugar.Info("✅ Guardrail: 工具速率限制已启用")
		} else {
			// 不启用速率限制时，清除所有速率限制
			// (保留风险等级设置，仅关闭限流)
		}
		agentEngine.SetGuardrailManager(guardrailMgr)
		sugar.Info("✅ Guardrails 防护系统初始化完成")
	} else {
		sugar.Info("⏭️ Guardrails 防护系统已禁用")
	}

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
	ragSvc := service.NewRAGService(llmProvider, vectorDB, memoryMgr, cfg.LLM.Model)
	ragSvc.SetAgentEngine(agentEngine) // 注入 Agent 引擎，支持工具调用

	// 10. 创建 Handler 层
	agentHandler := handler.NewAgentHandler(agentSvc)
	workflowHandler := handler.NewWorkflowHandler(workflowSvc)
	docHandler := handler.NewDocumentHandler(docSvc)
	queryHandler := handler.NewQueryHandler(ragSvc)
	traceHandler := handler.NewTraceHandler()

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

	router.RegisterRoutes(r, agentHandler, workflowHandler, docHandler, queryHandler, traceHandler)

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
	engine.RegisterTool(core.NewWebSearchTool())           // 网络搜索
	engine.RegisterTool(core.NewRAGSearchTool(engine))     // RAG 知识库检索
	engine.RegisterTool(core.NewHTTPRequestTool())         // HTTP API 调用
	engine.RegisterTool(core.NewCalculatorTool())          // 计算器
	engine.RegisterTool(core.NewFileReadTool())            // 文件读取
	engine.RegisterTool(core.NewGetCurrentDateTimeTool())  // 当前时间日期（本地，无需网络）
}

// registerMCPTools 初始化 MCP 外部工具并注册到 Agent 引擎
func registerMCPTools(engine *core.AgentEngine, mcpCfg *config.MCPConfig) int {
	totalTools := 0

	for _, srv := range mcpCfg.Servers {
		// 检查是否使用 HTTP 模式（args 包含 --port）
		port := extractPort(srv.Args)
		if port > 0 {
			// HTTP 模式：启动子进程 + HTTP 客户端
			startMCPProcess(srv.Command, srv.Args, srv.Env)

			client := core.NewHTTPMCPClient(srv.Name, fmt.Sprintf("http://localhost:%d", port))
			// 重试连接（服务器启动可能需要几秒）
			var lastErr error
			for retry := 0; retry < 5; retry++ {
				time.Sleep(2 * time.Second) // 每次等 2 秒
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := client.Initialize(ctx)
				cancel()
				if err == nil {
					lastErr = nil
					break
				}
				lastErr = err
				fmt.Printf("  ⏳ MCP %s 连接中... (第%d次, %v)\n", srv.Name, retry+1, err)
			}
			if lastErr != nil {
				fmt.Printf("⚠️  MCP HTTP %s 连接失败: %v\n", srv.Name, lastErr)
				continue
			}

			for _, toolInfo := range client.ListTools() {
				wrappedTool := core.AsHTTPMCPTool(client, toolInfo)
				engine.RegisterTool(wrappedTool)
				totalTools++
				fmt.Printf("  ➕ MCP 工具: %s — %s\n", toolInfo.Name, truncateString(toolInfo.Description, 60))
			}
		} else {
			// Stdio 模式（兼容）
			client, err := core.NewStdioMCPClient(srv.Name, srv.Command, srv.Args, srv.Env)
			if err != nil {
				fmt.Printf("⚠️  MCP 服务 %s 连接失败: %v\n", srv.Name, err)
				continue
			}
			// 等待异步初始化完成
			time.Sleep(3 * time.Second)
			for _, toolInfo := range client.ListTools() {
				wrappedTool := core.AsMCPTool(client, toolInfo)
				engine.RegisterTool(wrappedTool)
				totalTools++
				fmt.Printf("  ➕ MCP 工具: %s\n", toolInfo.Name)
			}
		}
	}

	return totalTools
}

// extractPort 从 args 中提取 --port 端口号
func extractPort(args []string) int {
	for i, a := range args {
		if a == "--port" && i+1 < len(args) {
			port := 0
			fmt.Sscanf(args[i+1], "%d", &port)
			return port
		}
	}
	return 0
}

// startMCPProcess 后台启动 MCP 子进程
func startMCPProcess(command string, args []string, env map[string]string) {
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("⚠️  启动 MCP 进程失败: %v\n", err)
		return
	}
	// 后台 goroutine 等待进程退出
	go func() {
		if err := cmd.Wait(); err != nil {
			fmt.Printf("⚠️  MCP 进程已退出: %v\n", err)
		}
	}()
	fmt.Printf("  🚀 MCP 后台进程已启动 (PID: %d)\n", cmd.Process.Pid)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
