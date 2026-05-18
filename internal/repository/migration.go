package repository

import (
	"fmt"

	"sirenagent/internal/model"

	"gorm.io/gorm"
)

// AutoMigrate 自动迁移所有模型到数据库
func AutoMigrate(db *gorm.DB) error {
	err := db.AutoMigrate(
		&model.Agent{},
		&model.Conversation{},
		&model.Message{},
		&model.Memory{},
		&model.Document{},
		&model.Workflow{},
		&model.WorkflowNode{},
		&model.WorkflowEdge{},
		&model.WorkflowExecution{},
		&model.SocialAccount{},
		&model.SocialPost{},
		&model.PromptTemplate{},
		&model.ToolDefinition{},
		&model.TestRecord{},
	)
	if err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}

	// 添加表级中文注释
	addTableComments(db)

	fmt.Println("[Migration] 数据库表迁移完成")
	return nil
}

// addTableComments 为每张表添加 PostgreSQL COMMENT ON TABLE 中文注释
func addTableComments(db *gorm.DB) {
	comments := []struct {
		table   string
		comment string
	}{
		{"agents", "智能体定义表 - 存储 Agent 的名称、模型、System Prompt、工具配置等。核心表，Agent 对话(Chat/Stream)接口高频读取"},
		{"conversations", "会话记录表 - 存储用户与 Agent 的对话会话。每次 Chat 调用都会创建或关联到此表"},
		{"messages", "对话消息表 - 存储每条对话消息内容、角色、Token消耗。高频写入，每次对话交互都会新增记录"},
		{"memories", "Agent 长期记忆表 - 存储 Agent 需要记住的用户偏好、事实、历史交互摘要。RAG 混合检索时调用"},
		{"documents", "知识库文档表 - 存储上传的文档元数据、分块状态。知识库上传/列表接口高频读写，向量入库的入口"},
		{"workflows", "工作流定义表 - 存储自动化工作流的名称、触发方式、版本。工作流列表/执行接口调用"},
		{"workflow_nodes", "工作流节点表 - 存储工作流的每个步骤节点配置(LLM/工具/条件/并行等)。执行工作流时全量加载"},
		{"workflow_edges", "工作流边表 - 存储工作流节点间的连接关系和条件表达式。与 nodes 一起加载构建执行图"},
		{"workflow_executions", "工作流执行记录表 - 存储每次工作流执行的输入、输出、状态、耗时。查看执行历史时查询"},
		{"social_accounts", "社交账号绑定表 - 存储用户在抖音/小红书/视频号等平台的授权信息和令牌"},
		{"social_posts", "社交发布任务表 - 存储待发布的内容、媒体文件、定时时间、发布状态。内容发布流程的核心"},
		{"prompt_templates", "Prompt 模板表 - 存储可复用的 System Prompt/RAG Prompt 模板及变量定义"},
		{"tool_definitions", "工具注册定义表 - 存储注册到 Agent 的工具名称、描述、JSON Schema。Agent 初始化时加载"},
		{"test_records", "测试记录表 - 存储前端测试面板的一键测试运行结果。仅测试面板使用，可随时清空"},
	}

	for _, c := range comments {
		sql := fmt.Sprintf("COMMENT ON TABLE %s IS '%s'", c.table, c.comment)
		if err := db.Exec(sql).Error; err != nil {
			fmt.Printf("[Migration] 表注释 %s 设置失败: %v\n", c.table, err)
		}
	}
	fmt.Println("[Migration] 表级中文注释已添加")
}
