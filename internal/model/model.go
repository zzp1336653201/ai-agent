package model

import (
	"time"

	"github.com/google/uuid"
)

// ==================== 文档相关 ====================

// Document 知识库文档
type Document struct {
	ID         string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	Title      string    `json:"title" gorm:"type:varchar(255);not null"`
	Content    string    `json:"content" gorm:"type:text"`
	Summary    string    `json:"summary" gorm:"type:text"`            // 文档摘要（知识管理员可手动编辑）
	Type       string    `json:"type" gorm:"type:varchar(20)"`       // txt|md|html|pdf|docx|url
	Category   string    `json:"category" gorm:"type:varchar(50);index;default:''"` // 分类：product|tech|faq|policy|manual|other
	Tags       string    `json:"tags" gorm:"type:jsonb"`             // 标签数组 JSON，如 ["Go","入门","教程"]
	SourceURL  string    `json:"source_url" gorm:"type:varchar(500);default:''"`  // 来源URL（从URL导入时自动记录）
	FilePath   string    `json:"file_path" gorm:"type:varchar(500);default:''"`
	AgentID    string    `json:"agent_id" gorm:"index;type:varchar(36);default:''"` // 所属 Agent（空=全局知识库）
	ChunkCount int       `json:"chunk_count" gorm:"default:0"`       // 分块数量
	Status     string    `json:"status" gorm:"type:varchar(20);default:'active'"` // active|archived|draft
	Metadata   string    `json:"metadata" gorm:"type:jsonb"`         // JSON 格式扩展元数据
	CreatedBy  string    `json:"created_by" gorm:"type:varchar(100);default:''"` // 创建者标识（admin|agent|system）
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func NewDocument(title, content, docType, filePath, agentID string) *Document {
	return &Document{
		ID:        uuid.New().String(),
		Title:     title,
		Content:   content,
		Type:      docType,
		FilePath:  filePath,
		AgentID:   agentID,
		Tags:      "[]",
		Status:    "active",
		CreatedBy: "admin",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// ==================== Agent 相关 ====================

// Agent 智能体定义
type Agent struct {
	ID             string         `json:"id" gorm:"primaryKey;type:varchar(36)"`
	Name           string         `json:"name" gorm:"type:varchar(100);not null"`
	Description    string         `json:"description" gorm:"type:text"`
	SystemPrompt   string         `json:"system_prompt" gorm:"type:text;not null"` // System Prompt 模板
	Model          string         `json:"model" gorm:"type:varchar(50)"`
	Temperature    float64        `json:"temperature"`
	MaxTokens      int            `json:"max_tokens"`
	Tools          string         `json:"tools" gorm:"type:jsonb"` // 工具列表 JSON
	MemoryType     string         `json:"memory_type" gorm:"type:varchar(20)"` // short|long|none
	KnowledgeBaseID string        `json:"knowledge_base_id" gorm:"type:varchar(36);default:''"` // 关联的知识库ID（空=使用全局知识库）
	Status         string         `json:"status" gorm:"type:varchar(20);default:'active'"` // active|inactive
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func NewAgent(name, description, systemPrompt string) *Agent {
	return &Agent{
		ID:           uuid.New().String(),
		Name:         name,
		Description:  description,
		SystemPrompt: systemPrompt,
		Model:        "llama3.2",
		Temperature:  0.7,
		MaxTokens:    2048,
		MemoryType:   "short",
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

// Conversation 会话记录
type Conversation struct {
	ID        string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	AgentID   string    `json:"agent_id" gorm:"index;type:varchar(36)"`
	UserID    string    `json:"user_id" gorm:"index;type:varchar(100)"`
	Title     string    `json:"title" gorm:"type:varchar(255)"`
	Status    string    `json:"status" gorm:"type:varchar(20);default:'active'"` // active|closed
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewConversation(agentID, userID, title string) *Conversation {
	return &Conversation{
		ID:        uuid.New().String(),
		AgentID:   agentID,
		UserID:    userID,
		Title:     title,
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// Message 对话消息
type Message struct {
	ID             string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	ConversationID string    `json:"conversation_id" gorm:"index;type:varchar(36)"`
	Role           string    `json:"role" gorm:"type:varchar(20);not null"` // user|assistant|system|tool
	Content        string    `json:"content" gorm:"type:text"`
	ToolCalls      string    `json:"tool_calls" gorm:"type:jsonb"` // 工具调用记录 JSON
	ToolCallID     string    `json:"tool_call_id" gorm:"type:varchar(100)"`
	TokenUsage     int       `json:"token_usage" gorm:"type:int;default:0"`
	CreatedAt      time.Time `json:"created_at"`
}

func NewMessage(conversationID, role, content string) *Message {
	return &Message{
		ID:             uuid.New().String(),
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		CreatedAt:      time.Now(),
	}
}

// Memory Agent 长期记忆
type Memory struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	AgentID     string    `json:"agent_id" gorm:"index;type:varchar(36)"`
	UserID      string    `json:"user_id" gorm:"index;type:varchar(100)"`
	Category    string    `json:"category" gorm:"type:varchar(50)"` // preference|fact|interaction
	Content     string    `json:"content" gorm:"type:text;not null"`
	Summary     string    `json:"summary" gorm:"type:text"`         // 记忆摘要（用于向量检索）
	Importance  int       `json:"importance" gorm:"type:int;default:1"` // 1-5 重要程度
	ExpireAt    time.Time `json:"expire_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func NewMemory(agentID, userID, category, content, summary string, importance int) *Memory {
	return &Memory{
		ID:         uuid.New().String(),
		AgentID:    agentID,
		UserID:     userID,
		Category:   category,
		Content:    content,
		Summary:    summary,
		Importance: importance,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
}

// ==================== 工作流相关 ====================

// Workflow 自动化工作流定义
type Workflow struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	Name        string    `json:"name" gorm:"type:varchar(100);not null"`
	Description string    `json:"description" gorm:"type:text"`
	Trigger     string    `json:"trigger" gorm:"type:varchar(50);not null"` // manual|cron|webhook|event
	TriggerConfig string  `json:"trigger_config" gorm:"type:jsonb"`
	Status      string    `json:"status" gorm:"type:varchar(20);default:'active'"`
	Version     int       `json:"version" gorm:"default:1"`
	CreatedBy   string    `json:"created_by" gorm:"type:varchar(100)"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WorkflowNode 工作流节点
type WorkflowNode struct {
	ID          string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	WorkflowID  string `json:"workflow_id" gorm:"index;type:varchar(36)"`
	NodeType    string `json:"node_type" gorm:"type:varchar(50);not null"` // start|end|llm|tool|condition|parallel|http
	Name        string `json:"name" gorm:"type:varchar(100)"`
	Config      string `json:"config" gorm:"type:jsonb"` // 节点配置 JSON
	PositionX   int    `json:"position_x"`              // 流程图 X 坐标
	PositionY   int    `json:"position_y"`              // 流程图 Y 坐标
	SortOrder   int    `json:"sort_order"`
}

// WorkflowEdge 工作流边（连接线）
type WorkflowEdge struct {
	ID         string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	WorkflowID string `json:"workflow_id" gorm:"index;type:varchar(36)"`
	SourceID   string `json:"source_id" gorm:"type:varchar(36)"` // 起始节点 ID
	TargetID   string `json:"target_id" gorm:"type:varchar(36)"` // 目标节点 ID
	Condition  string `json:"condition" gorm:"type:varchar(255)"` // 条件表达式（可选）
}

// WorkflowExecution 工作流执行记录
type WorkflowExecution struct {
	ID          string     `json:"id" gorm:"primaryKey;type:varchar(36)"`
	WorkflowID  string     `json:"workflow_id" gorm:"index;type:varchar(36)"`
	Status      string     `json:"status" gorm:"type:varchar(20)"` // running|success|failed|cancelled
	Input       string     `json:"input" gorm:"type:jsonb"`
	Output      string     `json:"output" gorm:"type:jsonb"`
	ErrorMsg    string     `json:"error_msg" gorm:"type:text"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

// ==================== 社交媒体相关 ====================

// SocialAccount 社交媒体账号绑定
type SocialAccount struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	UserID      string    `json:"user_id" gorm:"index;type:varchar(100)"`
	Platform    string    `json:"platform" gorm:"type:varchar(30);not null"` // douyin|xiaohongshu|video_channel
	AccountName string    `json:"account_name" gorm:"type:varchar(100)"`
	OpenID      string    `json:"open_id" gorm:"type:varchar(200)"`
	AccessToken string    `json:"access_token" gorm:"type:varchar(500)"`
	RefreshToken string   `json:"refresh_token" gorm:"type:varchar(500)"`
	ExpireAt    time.Time `json:"expire_at"`
	Status      string    `json:"status" gorm:"type:varchar(20);default:'active'"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SocialPost 社交媒体发布任务
type SocialPost struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	UserID      string    `json:"user_id" gorm:"index;type:varchar(100)"`
	Platform    string    `json:"platform" gorm:"type:varchar(30);not null"`
	ContentType string    `json:"content_type" gorm:"type:varchar(20)"` // text|image|video|article
	Title       string    `json:"title" gorm:"type:varchar(255)"`
	Content     string    `json:"content" gorm:"type:text"`
	MediaURLs   string    `json:"media_urls" gorm:"type:jsonb"`
	Tags        string    `json:"tags" gorm:"type:jsonb"`
	Status      string    `json:"status" gorm:"type:varchar(20);default:'pending'"` // pending|publishing|success|failed
	PublishTime *time.Time `json:"publish_time"`                              // 定时发布时间
	PostID      string    `json:"post_id" gorm:"type:varchar(200)"`           // 平台返回的帖子ID
	ErrorMsg    string    `json:"error_msg" gorm:"type:text"`
	WorkflowID  string    `json:"workflow_id" gorm:"type:varchar(36)"`        // 关联的工作流
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ==================== Prompt 模板相关 ====================

// PromptTemplate Prompt 模板
type PromptTemplate struct {
	ID        string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	Name      string    `json:"name" gorm:"type:varchar(100);not null"`
	Category  string    `json:"category" gorm:"type:varchar(50)"` // system|user|tool|rag
	Template  string    `json:"template" gorm:"type:text;not nil"`
	Variables string    `json:"variables" gorm:"type:jsonb"` // 变量列表 [{name, description, default, required}]
	Description string  `json:"description" gorm:"type:text"`
	Version   int       `json:"version" gorm:"default:1"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ToolDefinition 工具注册定义
type ToolDefinition struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	Name        string    `json:"name" gorm:"type:varchar(100);uniqueIndex;not null"`
	Description string    `json:"description" gorm:"type:text;not null"` // 用于 LLM 判断是否需要调用
	Schema      string    `json:"schema" gorm:"type:jsonb;not nil"`     // JSON Schema 参数定义
	HandlerPath string   `json:"handler_path" gorm:"type:varchar(255)"` // 处理器路径
	IsBuiltIn   bool      `json:"is_built_in" gorm:"default:true"`     // 是否内置工具
	Status      string    `json:"status" gorm:"type:varchar(20);default:'active'"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
