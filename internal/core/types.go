package core

import (
	"context"
	"fmt"

	"sirenagent/internal/model"
	"sirenagent/pkg/llm"
	"sirenagent/pkg/vector"
)

// ==================== Agent 引擎核心类型 ====================

// Logger 简化日志接口（兼容 zap.SugaredLogger）
type Logger interface {
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Debugf(format string, args ...interface{})
}

// AgentEngine 智能体引擎（核心调度器）
type AgentEngine struct {
	llm       llm.LLMProvider
	vectorDB  vector.VectorProvider
	memoryMgr MemoryManager
	tools     map[string]Tool
	prompts   PromptManager
	config    EngineConfig
	guardrail *GuardrailManager  // 防护管理器（可选）
	evaluator *Evaluator         // 评估优化器（可选）
	logger    Logger             // 结构化日志（可选）
}

// EngineConfig 引擎配置
type EngineConfig struct {
	MaxIterations int
	MemoryTTL     int
	ToolsTimeout  int
}

// ==================== Tool 工具系统 ====================

// Tool 接口 - Agent 可调用的工具
type Tool interface {
	Name() string
	Description() string // 给 LLM 看的描述，用于判断是否调用
	Parameters() map[string]interface{} // JSON Schema 参数定义
	Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error)
}

// ToolResult 工具执行结果
type ToolResult struct {
	Content string                 `json:"content"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

func NewToolResult(content string) *ToolResult {
	return &ToolResult{Content: content}
}

func NewToolError(err error) *ToolResult {
	return &ToolResult{Error: err.Error()}
}

// BaseTool 基础工具（方便实现）
type BaseTool struct {
	name        string
	description string
	parameters  map[string]interface{}
}

func NewBaseTool(name, description string, parameters map[string]interface{}) *BaseTool {
	return &BaseTool{
		name:        name,
		description: description,
		parameters:  parameters,
	}
}

func (t *BaseTool) Name() string { return t.name }
func (t *BaseTool) Description() string { return t.description }
func (t *BaseTool) Parameters() map[string]interface{} { return t.parameters }

// ToolRegistry 工具注册表
type ToolRegistry struct {
	tools map[string]Tool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

func (r *ToolRegistry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *ToolRegistry) List() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ToLLMFormat 将工具列表转换为 LLM 可识别的格式（Function Calling）
func (r *ToolRegistry) ToLLMFormat() []llm.ToolDefinition {
	definitions := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		definitions = append(definitions, llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}
	return definitions
}

// ==================== 记忆管理 ====================

// MemoryManager 记忆管理器接口
type MemoryManager interface {
	SaveShortTerm(ctx context.Context, agentID, userID, content string) error
	GetShortTerm(ctx context.Context, agentID, userID string, limit int) ([]*model.Memory, error)
	SaveLongTerm(ctx context.Context, memory *model.Memory) error
	SearchLongTerm(ctx context.Context, agentID, query string, topK int) ([]*model.Memory, error)
	CleanExpired(ctx context.Context, agentID string) (int64, error)
}

// ==================== Prompt 管理 ====================

// PromptManager Prompt 模板管理
type PromptManager interface {
	Render(templateName string, variables map[string]string) (string, error)
	RegisterTemplate(name, template, category string, variables []PromptVariable)
	ListByCategory(category string) []*model.PromptTemplate
}

// PromptVariable Prompt 变量定义
type PromptVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Default     string `json:"default"`
	Required    bool   `json:"required"`
}

// ==================== 向量存储抽象 ====================

// VectorStore 向量存储接口
type VectorStore interface {
	Search(ctx context.Context, query string, topK int, collection string) ([]SearchResult, error)
	Insert(ctx context.Context, docID string, chunks []string, metadatas []map[string]interface{}, collection string) error
	Delete(ctx context.Context, docIDs []string, collection string) error
}

// SearchResult 统一检索结果
type SearchResult struct {
	ID         string
	Content    string
	DocumentID string
	Score      float64
	Metadata   map[string]interface{}
}

// ==================== 执行结果 ====================

// AgentRunResult Agent 执行结果
type AgentRunResult struct {
	Answer      string            `json:"answer"`       // 最终答案
	Turns       int               `json:"turns"`        // 轮次
	ToolCalls   []*ToolCallRecord `json:"tool_calls"`   // 工具调用记录
	TokenUsage  llm.TokenUsage    `json:"token_usage"`  // Token 使用
	Sources     []SourceInfo      `json:"sources"`      // 来源信息
}

// ToolCallRecord 工具调用记录
type ToolCallRecord struct {
	ToolName string                 `json:"tool_name"`
	Input    map[string]interface{} `json:"input"`
	Output   *ToolResult            `json:"output"`
	LatencyMs int64                 `json:"latency_ms"`
}

// SourceInfo 来源引用信息
type SourceInfo struct {
	DocumentID string  `json:"document_id"`
	Title      string  `json:"title"`
	Score      float64 `json:"score"`
}

// NewAgentEngine 创建智能体引擎
func NewAgentEngine(llmProvider llm.LLMProvider, vectorDB vector.VectorProvider, memoryMgr MemoryManager, config EngineConfig) *AgentEngine {
	return &AgentEngine{
		llm:       llmProvider,
		vectorDB:  vectorDB,
		memoryMgr: memoryMgr,
		tools:     make(map[string]Tool),
		config:    config,
	}
}

// RegisterTool 注册工具到引擎
func (e *AgentEngine) RegisterTool(tool Tool) {
	e.tools[tool.Name()] = tool
}

// SetPromptManager 设置 Prompt 管理器
func (e *AgentEngine) SetPromptManager(pm PromptManager) {
	e.prompts = pm
}

// SetGuardrailManager 设置防护管理器
func (e *AgentEngine) SetGuardrailManager(gm *GuardrailManager) {
	e.guardrail = gm
}

// SetEvaluator 设置评估优化器
func (e *AgentEngine) SetEvaluator(ev *Evaluator) {
	e.evaluator = ev
}

// SetLogger 设置结构化日志器
func (e *AgentEngine) SetLogger(l Logger) {
	e.logger = l
}

// logf 安全地输出日志（即使 logger 未设置也不 panic）
func (e *AgentEngine) logf(level string, format string, args ...interface{}) {
	if e.logger == nil {
		return
	}
	switch level {
	case "debug":
		e.logger.Debugf(format, args...)
	case "info":
		e.logger.Infof(format, args...)
	case "warn":
		e.logger.Warnf(format, args...)
	case "error":
		e.logger.Errorf(format, args...)
	}
}

// GetTool 获取工具
func (e *AgentEngine) GetTool(name string) (Tool, bool) {
	t, ok := e.tools[name]
	return t, ok
}

// ListTools 列出所有已注册工具
func (e *AgentEngine) ListTools() []Tool {
	result := make([]Tool, 0, len(e.tools))
	for _, t := range e.tools {
		result = append(result, t)
	}
	return result
}

// ToolsToLLMFormat 将注册的工具转换为 LLM 格式
func (e *AgentEngine) ToolsToLLMFormat() []llm.ToolDefinition {
	definitions := make([]llm.ToolDefinition, 0, len(e.tools))
	for _, tool := range e.tools {
		definitions = append(definitions, llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}
	return definitions
}

// Validate 验证引擎状态
func (e *AgentEngine) Validate() error {
	if e.llm == nil {
		return fmt.Errorf("LLM 提供者未初始化")
	}
	if e.vectorDB == nil {
		return fmt.Errorf("向量数据库未初始化")
	}
	if e.memoryMgr == nil {
		return fmt.Errorf("记忆管理器未初始化")
	}
	return nil
}
