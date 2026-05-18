package model

import (
	"time"

	"github.com/google/uuid"
)

// ==================== 文档相关 ====================

// Document 知识库文档
type Document struct {
	ID         string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	Title      string    `json:"title" gorm:"type:varchar(255);not null;comment:文档标题"`
	Content    string    `json:"content" gorm:"type:text;comment:文档原始内容"`
	Summary    string    `json:"summary" gorm:"type:text;comment:文档摘要（知识管理员可手动编辑）"`
	Type       string    `json:"type" gorm:"type:varchar(20);comment:文档类型 txt|md|html|pdf|docx|url"`
	Category   string    `json:"category" gorm:"type:varchar(50);index;default:'';comment:分类 product|tech|faq|policy|manual|other"`
	Tags       string    `json:"tags" gorm:"type:jsonb;comment:标签数组JSON"`
	SourceURL  string    `json:"source_url" gorm:"type:varchar(500);default:'';comment:来源URL（从URL导入时自动记录）"`
	FilePath   string    `json:"file_path" gorm:"type:varchar(500);default:'';comment:本地文件路径"`
	AgentID    string    `json:"agent_id" gorm:"index;type:varchar(36);default:'';comment:所属AgentID（空=全局知识库）"`
	ChunkCount int       `json:"chunk_count" gorm:"default:0;comment:向量分块数量"`
	Status     string    `json:"status" gorm:"type:varchar(20);default:'active';comment:状态 active|archived|draft"`
	Metadata   string    `json:"metadata" gorm:"type:jsonb;comment:扩展元数据JSON"`
	CreatedBy  string    `json:"created_by" gorm:"type:varchar(100);default:'';comment:创建者标识 admin|agent|system"`
	CreatedAt  time.Time `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt  time.Time `json:"updated_at" gorm:"comment:更新时间"`
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
		Metadata:  "{}",
		Status:    "active",
		CreatedBy: "admin",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// ==================== Agent 相关 ====================

// Agent 智能体定义
type Agent struct {
	ID             string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	Name           string    `json:"name" gorm:"type:varchar(100);not null;comment:智能体名称"`
	Description    string    `json:"description" gorm:"type:text;comment:智能体功能描述"`
	SystemPrompt   string    `json:"system_prompt" gorm:"type:text;not null;comment:System Prompt角色设定"`
	Model          string    `json:"model" gorm:"type:varchar(50);comment:LLM模型名称"`
	Temperature    float64   `json:"temperature" gorm:"comment:生成温度参数 0-2"`
	MaxTokens      int       `json:"max_tokens" gorm:"comment:最大输出Token数"`
	Tools          string    `json:"tools" gorm:"type:jsonb;comment:可用的工具列表JSON"`
	MemoryType     string    `json:"memory_type" gorm:"type:varchar(20);comment:记忆类型 short|long|none"`
	KnowledgeBaseID string   `json:"knowledge_base_id" gorm:"type:varchar(36);default:'';comment:关联知识库ID（空=全局）"`
	Status         string    `json:"status" gorm:"type:varchar(20);default:'active';comment:状态 active|inactive"`
	CreatedAt      time.Time `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"comment:更新时间"`
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
		Tools:        "[]",
		MemoryType:   "short",
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

// Conversation 会话记录
type Conversation struct {
	ID        string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	AgentID   string    `json:"agent_id" gorm:"index;type:varchar(36);comment:关联智能体ID"`
	UserID    string    `json:"user_id" gorm:"index;type:varchar(100);comment:用户标识"`
	Title     string    `json:"title" gorm:"type:varchar(255);comment:会话标题"`
	Status    string    `json:"status" gorm:"type:varchar(20);default:'active';comment:状态 active|closed"`
	CreatedAt time.Time `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt time.Time `json:"updated_at" gorm:"comment:更新时间"`
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
	ID             string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	ConversationID string    `json:"conversation_id" gorm:"index;type:varchar(36);comment:关联会话ID"`
	Role           string    `json:"role" gorm:"type:varchar(20);not null;comment:角色 user|assistant|system|tool"`
	Content        string    `json:"content" gorm:"type:text;comment:消息内容"`
	ToolCalls      string    `json:"tool_calls" gorm:"type:jsonb;comment:工具调用记录JSON"`
	ToolCallID     string    `json:"tool_call_id" gorm:"type:varchar(100);comment:工具调用ID"`
	TokenUsage     int       `json:"token_usage" gorm:"type:int;default:0;comment:Token消耗数"`
	CreatedAt      time.Time `json:"created_at" gorm:"comment:创建时间"`
}

func NewMessage(conversationID, role, content string) *Message {
	return &Message{
		ID:             uuid.New().String(),
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		ToolCalls:      "[]",
		CreatedAt:      time.Now(),
	}
}

// Memory Agent 长期记忆
type Memory struct {
	ID         string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	AgentID    string    `json:"agent_id" gorm:"index;type:varchar(36);comment:关联智能体ID"`
	UserID     string    `json:"user_id" gorm:"index;type:varchar(100);comment:用户标识"`
	Category   string    `json:"category" gorm:"type:varchar(50);comment:记忆类别 preference|fact|interaction"`
	Content    string    `json:"content" gorm:"type:text;not null;comment:记忆内容"`
	Summary    string    `json:"summary" gorm:"type:text;comment:记忆摘要（用于向量检索）"`
	Importance int       `json:"importance" gorm:"type:int;default:1;comment:重要程度 1-5"`
	ExpireAt   time.Time `json:"expire_at" gorm:"comment:过期时间"`
	CreatedAt  time.Time `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt  time.Time `json:"updated_at" gorm:"comment:更新时间"`
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
	ID            string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	Name          string    `json:"name" gorm:"type:varchar(100);not null;comment:工作流名称"`
	Description   string    `json:"description" gorm:"type:text;comment:工作流描述"`
	Trigger       string    `json:"trigger" gorm:"type:varchar(50);not null;comment:触发方式 manual|cron|webhook|event"`
	TriggerConfig string    `json:"trigger_config" gorm:"type:jsonb;comment:触发器配置JSON"`
	Status        string    `json:"status" gorm:"type:varchar(20);default:'active';comment:状态 active|inactive"`
	Version       int       `json:"version" gorm:"default:1;comment:版本号"`
	CreatedBy     string    `json:"created_by" gorm:"type:varchar(100);comment:创建者"`
	CreatedAt     time.Time `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt     time.Time `json:"updated_at" gorm:"comment:更新时间"`
}

// WorkflowNode 工作流节点
type WorkflowNode struct {
	ID         string `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	WorkflowID string `json:"workflow_id" gorm:"index;type:varchar(36);comment:所属工作流ID"`
	NodeType   string `json:"node_type" gorm:"type:varchar(50);not null;comment:节点类型 start|end|llm|tool|condition|parallel|http"`
	Name       string `json:"name" gorm:"type:varchar(100);comment:节点名称"`
	Config     string `json:"config" gorm:"type:jsonb;comment:节点配置JSON"`
	PositionX  int    `json:"position_x" gorm:"comment:流程图X坐标"`
	PositionY  int    `json:"position_y" gorm:"comment:流程图Y坐标"`
	SortOrder  int    `json:"sort_order" gorm:"comment:排序序号"`
}

// WorkflowEdge 工作流边（连接线）
type WorkflowEdge struct {
	ID         string `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	WorkflowID string `json:"workflow_id" gorm:"index;type:varchar(36);comment:所属工作流ID"`
	SourceID   string `json:"source_id" gorm:"type:varchar(36);comment:起始节点ID"`
	TargetID   string `json:"target_id" gorm:"type:varchar(36);comment:目标节点ID"`
	Condition  string `json:"condition" gorm:"type:varchar(255);comment:条件表达式（可选）"`
}

// WorkflowExecution 工作流执行记录
type WorkflowExecution struct {
	ID         string     `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	WorkflowID string     `json:"workflow_id" gorm:"index;type:varchar(36);comment:所属工作流ID"`
	Status     string     `json:"status" gorm:"type:varchar(20);comment:状态 running|success|failed|cancelled"`
	Input      string     `json:"input" gorm:"type:jsonb;comment:执行输入参数JSON"`
	Output     string     `json:"output" gorm:"type:jsonb;comment:执行输出结果JSON"`
	ErrorMsg   string     `json:"error_msg" gorm:"type:text;comment:错误信息"`
	StartedAt  time.Time  `json:"started_at" gorm:"comment:开始执行时间"`
	FinishedAt *time.Time `json:"finished_at" gorm:"comment:执行完成时间"`
}

// ==================== 社交媒体相关 ====================

// SocialAccount 社交媒体账号绑定
type SocialAccount struct {
	ID           string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	UserID       string    `json:"user_id" gorm:"index;type:varchar(100);comment:用户标识"`
	Platform     string    `json:"platform" gorm:"type:varchar(30);not null;comment:平台 douyin|xiaohongshu|video_channel"`
	AccountName  string    `json:"account_name" gorm:"type:varchar(100);comment:平台账号名称"`
	OpenID       string    `json:"open_id" gorm:"type:varchar(200);comment:平台OpenID"`
	AccessToken  string    `json:"access_token" gorm:"type:varchar(500);comment:访问令牌"`
	RefreshToken string    `json:"refresh_token" gorm:"type:varchar(500);comment:刷新令牌"`
	ExpireAt     time.Time `json:"expire_at" gorm:"comment:令牌过期时间"`
	Status       string    `json:"status" gorm:"type:varchar(20);default:'active';comment:状态 active|inactive"`
	CreatedAt    time.Time `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"comment:更新时间"`
}

// SocialPost 社交媒体发布任务
type SocialPost struct {
	ID          string     `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	UserID      string     `json:"user_id" gorm:"index;type:varchar(100);comment:用户标识"`
	Platform    string     `json:"platform" gorm:"type:varchar(30);not null;comment:目标平台"`
	ContentType string     `json:"content_type" gorm:"type:varchar(20);comment:内容类型 text|image|video|article"`
	Title       string     `json:"title" gorm:"type:varchar(255);comment:发布标题"`
	Content     string     `json:"content" gorm:"type:text;comment:发布内容"`
	MediaURLs   string     `json:"media_urls" gorm:"type:jsonb;comment:媒体文件URL列表JSON"`
	Tags        string     `json:"tags" gorm:"type:jsonb;comment:标签列表JSON"`
	Status      string     `json:"status" gorm:"type:varchar(20);default:'pending';comment:状态 pending|publishing|success|failed"`
	PublishTime *time.Time `json:"publish_time" gorm:"comment:定时发布时间"`
	PostID      string     `json:"post_id" gorm:"type:varchar(200);comment:平台返回的帖子ID"`
	ErrorMsg    string     `json:"error_msg" gorm:"type:text;comment:错误信息"`
	WorkflowID  string     `json:"workflow_id" gorm:"type:varchar(36);comment:关联工作流ID"`
	CreatedAt   time.Time  `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"comment:更新时间"`
}

// ==================== Prompt 模板相关 ====================

// PromptTemplate Prompt 模板
type PromptTemplate struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	Name        string    `json:"name" gorm:"type:varchar(100);not null;comment:模板名称"`
	Category    string    `json:"category" gorm:"type:varchar(50);comment:模板类别 system|user|tool|rag"`
	Template    string    `json:"template" gorm:"type:text;not null;comment:模板内容"`
	Variables   string    `json:"variables" gorm:"type:jsonb;comment:变量定义列表JSON"`
	Description string    `json:"description" gorm:"type:text;comment:模板描述"`
	Version     int       `json:"version" gorm:"default:1;comment:版本号"`
	CreatedAt   time.Time `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"comment:更新时间"`
}

// ToolDefinition 工具注册定义
type ToolDefinition struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	Name        string    `json:"name" gorm:"type:varchar(100);uniqueIndex;not null;comment:工具名称（唯一）"`
	Description string    `json:"description" gorm:"type:text;not null;comment:工具描述（用于LLM判断是否调用）"`
	Schema      string    `json:"schema" gorm:"type:jsonb;not null;comment:JSON Schema参数定义"`
	HandlerPath string    `json:"handler_path" gorm:"type:varchar(255);comment:处理器路径"`
	IsBuiltIn   bool      `json:"is_built_in" gorm:"default:true;comment:是否内置工具"`
	Status      string    `json:"status" gorm:"type:varchar(20);default:'active';comment:状态 active|inactive"`
	CreatedAt   time.Time `json:"created_at" gorm:"comment:创建时间"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"comment:更新时间"`
}

// ==================== 测试记录相关 ====================

// TestRecord 测试记录（用于前端测试面板）
type TestRecord struct {
	ID        string    `json:"id" gorm:"primaryKey;type:varchar(36);comment:UUID主键"`
	TestName  string    `json:"test_name" gorm:"type:varchar(100);not null;comment:测试名称"`
	Category  string    `json:"category" gorm:"type:varchar(50);comment:测试分类 agent|document|chat|workflow|system"`
	Status    string    `json:"status" gorm:"type:varchar(20);comment:状态 success|failed|running"`
	Request   string    `json:"request" gorm:"type:text;comment:请求内容"`
	Response  string    `json:"response" gorm:"type:text;comment:返回内容"`
	Duration  int64     `json:"duration" gorm:"comment:耗时(毫秒)"`
	ErrorMsg  string    `json:"error_msg" gorm:"type:text;comment:错误信息"`
	CreatedAt time.Time `json:"created_at" gorm:"comment:创建时间"`
}

func NewTestRecord(testName, category string) *TestRecord {
	return &TestRecord{
		ID:        uuid.New().String(),
		TestName:  testName,
		Category:  category,
		Status:    "running",
		Request:   "{}",
		Response:  "{}",
		CreatedAt: time.Now(),
	}
}
