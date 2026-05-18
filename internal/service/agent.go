package service

import (
	"context"
	"fmt"
	"time"

	"sirenagent/internal/core"
	"sirenagent/internal/model"
	"sirenagent/pkg/llm"
)

// KnowledgeFetcher 异步获取知识的回调函数
// 在创建智能体时，如果开启 auto_fetch，后台自动搜索相关文档入库
type KnowledgeFetcher func(ctx context.Context, agentID, systemPrompt, description string)

// AgentService 智能体管理服务
type AgentService struct {
	engine      *core.AgentEngine
	repo        AgentRepository
	fetchKnowledge KnowledgeFetcher // 可选：异步知识获取
}

type AgentRepository interface {
	Create(agent *model.Agent) error
	GetByID(id string) (*model.Agent, error)
	List(status string, page, size int) ([]*model.Agent, int64, error)
	Update(agent *model.Agent) error
	Delete(id string) error
}

func NewAgentService(engine *core.AgentEngine, repo AgentRepository) *AgentService {
	return &AgentService{engine: engine, repo: repo}
}

// SetKnowledgeFetcher 设置异步知识获取回调（main.go 中注入）
func (s *AgentService) SetKnowledgeFetcher(fetch KnowledgeFetcher) {
	s.fetchKnowledge = fetch
}

// ChatRequest 对话请求
type ChatRequest struct {
	AgentID string `json:"agent_id" binding:"required"`
	Message string `json:"message" binding:"required"`
	UserID  string `json:"user_id"`
	Stream  bool   `json:"stream"` // 是否流式响应
}

// ChatResponse 对话响应
type ChatResponse struct {
	ID        string                   `json:"id"`
	Answer    string                   `json:"answer"`
	Turns     int                      `json:"turns"`
	TokenUsage llm.TokenUsage         `json:"token_usage"`
	Sources   []core.SourceInfo        `json:"sources"`
	ToolCalls []*core.ToolCallRecord   `json:"tool_calls"`
}

// CreateAgent 创建智能体
func (s *AgentService) Create(req *CreateAgentRequest) (*model.Agent, error) {
	agent := model.NewAgent(req.Name, req.Description, req.SystemPrompt)
	if req.Model != "" { agent.Model = req.Model }
	if req.Temperature > 0 { agent.Temperature = req.Temperature }
	if req.MaxTokens > 0 { agent.MaxTokens = req.MaxTokens }
	if req.MemoryType != "" { agent.MemoryType = req.MemoryType }

	// 自动分配知识库：用户未指定则使用 AgentID 作为知识库ID
	if req.KnowledgeBaseID != "" {
		agent.KnowledgeBaseID = req.KnowledgeBaseID
	} else {
		agent.KnowledgeBaseID = agent.ID // 使用 AgentID 作为知识库ID（一一对应）
	}

	// 将知识库集合信息写入 SystemPrompt，让 Agent 知道自己的知识库位置
	collectionName := core.KnowledgeBaseCollection(agent.KnowledgeBaseID)
	agent.SystemPrompt = fmt.Sprintf(
		"%s\n\n【知识库信息】你的专属知识库集合名为「%s」，当需要查询知识库时使用 rag_search 工具。",
		agent.SystemPrompt, collectionName,
	)

	if err := s.repo.Create(agent); err != nil {
		return nil, fmt.Errorf("创建失败: %w", err)
	}
	fmt.Printf("[AgentService] 创建智能体: %s (ID=%s, 知识库=%s, 集合=%s)\n",
		agent.Name, agent.ID, agent.KnowledgeBaseID, collectionName)

	// 如果开启自动获取知识，后台异步执行
	if req.AutoFetchKnowledge && s.fetchKnowledge != nil {
		fmt.Printf("[AgentService] ⏳ 正在后台自动获取相关知识...\n")
		go s.fetchKnowledge(context.Background(), agent.ID, agent.SystemPrompt, agent.Description)
	}

	return agent, nil
}

// GetAgent 获取智能体
func (s *AgentService) GetAgent(id string) (*model.Agent, error) {
	return s.repo.GetByID(id)
}

// ListAgents 列出智能体
func (s *AgentService) List(status string, page, size int) ([]*model.Agent, int64, error) {
	return s.repo.List(status, page, size)
}

// Chat 与智能体对话 — 核心入口
func (s *AgentService) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// 加载 Agent 定义
	agent, err := s.repo.GetByID(req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("智能体不存在: %w", err)
	}
	if agent.Status != "active" {
		return nil, fmt.Errorf("智能体 %s 已停用", agent.Name)
	}

	// 执行 Agent 推理（ReAct 循环）
	result, err := s.engine.Run(ctx, agent, req.Message)
	if err != nil {
		return nil, fmt.Errorf("执行失败: %w", err)
	}

	return &ChatResponse{
		ID:        model.NewConversation(agent.ID, req.UserID, truncateMsg(req.Message)).ID,
		Answer:    result.Answer,
		Turns:     result.Turns,
		TokenUsage: result.TokenUsage,
		Sources:   result.Sources,
		ToolCalls: result.ToolCalls,
	}, nil
}

// ChatStream 流式对话
func (s *AgentService) ChatStream(ctx context.Context, req *ChatRequest) (<-chan core.StreamEvent, error) {
	agent, err := s.repo.GetByID(req.AgentID)
	if err != nil {
		return nil, err
	}
	return s.engine.RunStream(ctx, agent, req.Message)
}

// DeleteAgent 删除智能体
func (s *AgentService) Delete(id string) error { return s.repo.Delete(id) }

// Update 更新智能体
func (s *AgentService) Update(id string, req *UpdateAgentRequest) (*model.Agent, error) {
	agent, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("智能体不存在: %w", err)
	}

	if req.Name != "" {
		agent.Name = req.Name
	}
	if req.Description != "" {
		agent.Description = req.Description
	}
	if req.SystemPrompt != "" {
		agent.SystemPrompt = req.SystemPrompt
	}
	if req.Model != "" {
		agent.Model = req.Model
	}
	if req.Temperature > 0 {
		agent.Temperature = req.Temperature
	}
	if req.MaxTokens > 0 {
		agent.MaxTokens = req.MaxTokens
	}
	if req.MemoryType != "" {
		agent.MemoryType = req.MemoryType
	}
	if req.Status != "" {
		agent.Status = req.Status
	}

	agent.UpdatedAt = time.Now()

	if err := s.repo.Update(agent); err != nil {
		return nil, fmt.Errorf("更新失败: %w", err)
	}
	fmt.Printf("[AgentService] 更新智能体: %s (ID=%s)\n", agent.Name, agent.ID)
	return agent, nil
}

// ==================== 请求/响应类型 ====================

type CreateAgentRequest struct {
	Name               string   `json:"name" binding:"required"`
	Description        string   `json:"description"`
	SystemPrompt       string   `json:"system_prompt" binding:"required"`
	Model              string   `json:"model"`
	Temperature        float64  `json:"temperature"`
	MaxTokens          int      `json:"max_tokens"`
	MemoryType         string   `json:"memory_type"`
	Tools              []string `json:"tools"`
	KnowledgeBaseID    string   `json:"knowledge_base_id"`   // 不传则自动生成
	AutoFetchKnowledge  bool    `json:"auto_fetch_knowledge"` // 是否自动获取相关知识
}

type UpdateAgentRequest struct {
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	SystemPrompt string  `json:"system_prompt"`
	Model        string  `json:"model"`
	Temperature  float64 `json:"temperature"`
	MaxTokens    int     `json:"max_tokens"`
	MemoryType   string  `json:"memory_type"`
	Status       string  `json:"status"` // active|inactive
}

func truncateMsg(s string) string {
	if len(s) > 50 { return s[:50] + "..." }
	return s
}
