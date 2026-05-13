package service

import (
	"context"
	"fmt"

	"sirenagent/internal/core"
	"sirenagent/internal/model"
)

// AgentService 智能体管理服务
type AgentService struct {
	engine    *core.AgentEngine
	repo      AgentRepository
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
	TokenUsage core.TokenUsage         `json:"token_usage"`
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

	if err := s.repo.Create(agent); err != nil {
		return nil, fmt.Errorf("创建失败: %w", err)
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

// ==================== 请求/响应类型 ====================

type CreateAgentRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	SystemPrompt string `json:"system_prompt" binding:"required"`
	Model       string `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int    `json:"max_tokens"`
	MemoryType  string `json:"memory_type"`
	Tools       []string `json:"tools"`
}

func truncateMsg(s string) string {
	if len(s) > 50 { return s[:50] + "..." }
	return s
}
