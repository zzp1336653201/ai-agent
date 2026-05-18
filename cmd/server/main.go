package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sirenagent/internal/config"
	"sirenagent/internal/core"
	"sirenagent/internal/handler"
	"sirenagent/internal/platform"
	"sirenagent/internal/repository"
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

	// 3. 初始化数据库连接（共享给 VectorDB 和 Repository）
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Database.Host, cfg.Database.Port, cfg.Database.User, cfg.Database.Password, cfg.Database.DBName, cfg.Database.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		logger.Fatal("PostgreSQL 连接失败", zap.Error(err))
	}
	sqlDB, err := db.DB()
	if err != nil {
		logger.Fatal("获取 SQL DB 失败", zap.Error(err))
	}
	defer sqlDB.Close()
	sugar.Info("✅ PostgreSQL 数据库连接成功")

	// 4. 自动迁移数据库表
	if err := repository.AutoMigrate(db); err != nil {
		logger.Fatal("数据库迁移失败", zap.Error(err))
	}

	// 5. 初始化 Repository 层（真实数据库实现）
	agentRepo := repository.NewAgentRepo(db)
	docRepo := repository.NewDocumentRepo(db)
	workflowRepo := repository.NewWorkflowRepo(db)

	// 6. 初始化 LLM 提供者（策略模式：Ollama / OpenAI / 豆包）
	llmProvider := llm.NewLLMProvider(cfg.LLM.Provider, cfg.LLM.Endpoint, cfg.LLM.APIKey, cfg.LLM.Model)
	sugar.Infof("✅ LLM 提供者: %s (%s)", cfg.LLM.Provider, cfg.LLM.Model)

	// 7. 初始化向量数据库（支持 ChromaDB / Pgvector）
	var vectorDB vector.VectorProvider
	if cfg.VectorDB.Provider == "pgvector" {
		pgVDB, err := vector.NewPgVectorDB(db)
		if err != nil {
			logger.Fatal("Pgvector 初始化失败", zap.Error(err))
		}
		vectorDB = pgVDB
		sugar.Infof("✅ 向量数据库: Pgvector (PostgreSQL)")
	} else {
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

	// 8. 初始化记忆管理器
	memoryMgr := core.NewInMemoryMemoryManager()
	promptMgr := core.NewTemplatePromptManager()
	registerPromptTemplates(promptMgr)
	sugar.Info("✅ Prompt 模板已加载")

	// 9. 初始化 Agent 引擎
	engineConfig := core.EngineConfig{
		MaxIterations: cfg.Agent.MaxIterations,
		MemoryTTL:     cfg.Agent.MemoryTTL,
		ToolsTimeout:  cfg.Agent.ToolsTimeout,
	}
	agentEngine := core.NewAgentEngine(llmProvider, vectorDB, memoryMgr, engineConfig)
	registerBuiltInTools(agentEngine)
	sugar.Infof("✅ 已注册 %d 个内置工具", len(agentEngine.ListTools()))

	// MCP 外部工具
	if cfg.MCP.Enabled && len(cfg.MCP.Servers) > 0 {
		mcpTools := registerMCPTools(agentEngine, &cfg.MCP)
		sugar.Infof("✅ 已注册 %d 个 MCP 外部工具", mcpTools)
	}
	agentEngine.SetPromptManager(promptMgr)

	// Evaluator-Optimizer
	evaluator := core.NewEvaluator(llmProvider, cfg.LLM.Model)
	agentEngine.SetEvaluator(evaluator)
	sugar.Info("✅ Evaluator-Optimizer 评估优化器已启用 (最多2轮优化)")

	// Guardrails
	initGuardrails(cfg, agentEngine, sugar)

	// 10. 初始化工作流引擎（注入 WorkflowRepo 作为 Store）
	workflowEngine := core.NewWorkflowEngine(agentEngine, workflowRepo)
	sugar.Info("✅ 工作流引擎就绪 (实时持久化)")

	// 11. 初始化社交媒体平台适配器
	platformAdapters := platform.NewPlatformAdapters(&cfg.SocialPlatforms)
	for name := range platformAdapters {
		sugar.Infof("✅ 平台适配器: %s", name)
	}

	// 12. 创建 Service 层（注入真实 Repository）
	agentSvc := service.NewAgentService(agentEngine, agentRepo)
	workflowSvc := service.NewWorkflowService(workflowEngine, workflowRepo)
	docSvc := service.NewDocumentService(docRepo, vectorDB)

	// 注入异步知识获取功能：创建智能体时后台自动搜索相关文档入库
	agentSvc.SetKnowledgeFetcher(func(ctx context.Context, agentID, systemPrompt, description string) {
		start := time.Now()
		webTool, ok := agentEngine.GetTool("web_search")
		if !ok {
			fmt.Printf("[AutoFetch] web_search 工具未注册，跳过\n")
			return
		}

		// 从 SystemPrompt 和 Description 中提取关键词
		keywords := extractKeywords(systemPrompt, description)
		fmt.Printf("[AutoFetch] 提取关键词: %v\n", keywords)

		for _, keyword := range keywords {
			select {
			case <-ctx.Done():
				return
			default:
			}

			fmt.Printf("[AutoFetch] 搜索关键词: %s\n", keyword)
			result, err := webTool.Execute(ctx, map[string]interface{}{
				"query": keyword,
				"count": float64(3),
			})
			if err != nil {
				fmt.Printf("[AutoFetch] 搜索失败 %q: %v\n", keyword, err)
				continue
			}

			// 将搜索结果保存到 Agent 专属知识库
			title := fmt.Sprintf("自动获取 - %s", keyword)
			_, uploadErr := docSvc.Upload(ctx, &service.UploadRequest{
				Title:    title,
				Content:  fmt.Sprintf("关于「%s」的搜索结果：\n%s", keyword, result.Content),
				Type:     "txt",
				Category: "tech",
				AgentID:  agentID,
			})
			if uploadErr != nil {
				fmt.Printf("[AutoFetch] 上传失败 %q: %v\n", keyword, uploadErr)
			} else {
				fmt.Printf("[AutoFetch] ✅ 已保存: %s\n", title)
			}
		}
		fmt.Printf("[AutoFetch] 知识预热完成，耗时: %v\n", time.Since(start))
	})
	ragSvc := service.NewRAGService(llmProvider, vectorDB, memoryMgr, cfg.LLM.Model)
	ragSvc.SetAgentEngine(agentEngine)

	// 注册 knowledge_save 工具（依赖 docSvc 做文档元数据持久化）
	knowledgeSaveTool := core.NewKnowledgeSaveTool(agentEngine, func(ctx context.Context, title, content, category, agentID string) error {
		_, err := docSvc.Upload(ctx, &service.UploadRequest{
			Title:    title,
			Content:  content,
			Type:     "txt",
			Category: category,
			AgentID:  agentID,
		})
		return err
	})
	agentEngine.RegisterTool(knowledgeSaveTool)
	sugar.Infof("✅ 已注册 knowledge_save 工具（Agent 自主知识保存）")

	// 13. 创建 Handler 层
	agentHandler := handler.NewAgentHandler(agentSvc)
	workflowHandler := handler.NewWorkflowHandler(workflowSvc)
	docHandler := handler.NewDocumentHandler(docSvc)
	queryHandler := handler.NewQueryHandler(ragSvc)
	traceHandler := handler.NewTraceHandler()
	testHandler := handler.NewTestHandler(db)

	// 14. 启动 HTTP 服务
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

	r.Static("/static", "./web")
	r.GET("/", func(c *gin.Context) {
		c.File("./web/index.html")
	})

	router.RegisterRoutes(r, agentHandler, workflowHandler, docHandler, queryHandler, traceHandler, testHandler)

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
	sugar.Infof("  🗄  数据库持久化: 已启用")
	_ = platformAdapters
	sugar.Infof("========================================\n")

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务...")
}

// initGuardrails 初始化安全防护系统
func initGuardrails(cfg *config.Config, agentEngine *core.AgentEngine, sugar *zap.SugaredLogger) {
	if !cfg.Guardrails.Enabled {
		sugar.Info("⏭️ Guardrails 防护系统已禁用")
		return
	}
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
	guardrailMgr.AddOutputGuardrail(core.NewOutputSensitivityGuardrail())
	if cfg.Guardrails.EnableQuality {
		guardrailMgr.AddOutputGuardrail(core.NewOutputQualityGuardrail())
		sugar.Info("✅ Guardrail: 输出质量检查已启用")
	}
	core.SetupDefaultToolRisks(guardrailMgr.ToolGuardrail())
	if cfg.Guardrails.EnableRateLimit {
		sugar.Info("✅ Guardrail: 工具速率限制已启用")
	}
	agentEngine.SetGuardrailManager(guardrailMgr)
	sugar.Info("✅ Guardrails 防护系统初始化完成")
}

// registerBuiltInTools 注册内置工具集
func registerBuiltInTools(engine *core.AgentEngine) {
	engine.RegisterTool(core.NewWebSearchTool())
	engine.RegisterTool(core.NewRAGSearchTool(engine))
	engine.RegisterTool(core.NewHTTPRequestTool())
	engine.RegisterTool(core.NewCalculatorTool())
	engine.RegisterTool(core.NewFileReadTool())
	engine.RegisterTool(core.NewGetCurrentDateTimeTool())
}

// registerMCPTools 初始化 MCP 外部工具并注册到 Agent 引擎
func registerMCPTools(engine *core.AgentEngine, mcpCfg *config.MCPConfig) int {
	totalTools := 0
	for _, srv := range mcpCfg.Servers {
		port := extractPort(srv.Args)
		if port > 0 {
			startMCPProcess(srv.Command, srv.Args, srv.Env)
			client := core.NewHTTPMCPClient(srv.Name, fmt.Sprintf("http://localhost:%d", port))
			var lastErr error
			for retry := 0; retry < 5; retry++ {
				time.Sleep(2 * time.Second)
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
			client, err := core.NewStdioMCPClient(srv.Name, srv.Command, srv.Args, srv.Env)
			if err != nil {
				fmt.Printf("⚠️  MCP 服务 %s 连接失败: %v\n", srv.Name, err)
				continue
			}
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

// extractKeywords 从 SystemPrompt 和 Description 中提取搜索关键词
func extractKeywords(systemPrompt, description string) []string {
	// 收集所有可能的文本来源
	texts := []string{systemPrompt, description}
	var allText string
	for _, t := range texts {
		allText += " " + t
	}

	// 去掉标点符号和常见停用词
	stopWords := map[string]bool{
		"你": true, "我": true, "的": true, "了": true, "是": true, "在": true,
		"有": true, "和": true, "就": true, "不": true, "人": true, "都": true,
		"一": true, "个": true, "上": true, "也": true, "很": true, "到": true,
		"说": true, "要": true, "去": true, "会": true, "着": true,
		"没有": true, "看": true, "好": true, "自己": true, "这": true,
		"一个": true, "可以": true, "我们": true, "大家": true, "什么": true,
		"a": true, "an": true, "the": true, "is": true, "are": true, "to": true,
		"of": true, "in": true, "it": true, "that": true, "for": true, "on": true,
	}

	// 提取有意义的中文词组（2-6字）和英文单词
	seen := make(map[string]bool)
	var keywords []string

	// 提取中文词组
	runes := []rune(allText)
	for i := 0; i < len(runes); i++ {
		if runes[i] > 0x4e00 && runes[i] < 0x9fff { // 中文字符范围
			for length := 4; length >= 2; length-- {
				if i+length <= len(runes) {
					word := string(runes[i : i+length])
					if !stopWords[word] && !seen[word] && len([]rune(word)) >= 2 {
						seen[word] = true
						keywords = append(keywords, word)
					}
				}
			}
		}
	}

	// 提取英文单词（至少3个字母）
	current := ""
	for _, r := range allText {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			current += string(r)
		} else {
			if len(current) >= 3 {
				lower := strings.ToLower(current)
				if !stopWords[lower] && !seen[lower] {
					seen[lower] = true
					keywords = append(keywords, current)
				}
			}
			current = ""
		}
	}

	// 去重并限制最多5个关键词
	if len(keywords) > 5 {
		keywords = keywords[:5]
	}

	// 如果提取不到关键词，用 description 作为默认搜索
	if len(keywords) == 0 && description != "" {
		keywords = append(keywords, description)
	}

	return keywords
}

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
