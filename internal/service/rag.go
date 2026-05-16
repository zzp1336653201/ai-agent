package service

import (
	"context"
	"fmt"
	"strings"

	"sirenagent/internal/core"
	"sirenagent/internal/model"
	"sirenagent/pkg/llm"
	"sirenagent/pkg/vector"
)

// RAGService 检索增强生成服务（升级版）
// 对应职位要求：RAG 技术、记忆管理、混合检索
type RAGService struct {
	llm      llm.LLMProvider
	vectorDB vector.VectorProvider
	memory   core.MemoryManager
	model    string  // LLM 模型名称
	engine   *core.AgentEngine // Agent 引擎（用于支持工具调用）
}

func NewRAGService(llmProvider llm.LLMProvider, vectorDB vector.VectorProvider, memory core.MemoryManager, model string) *RAGService {
	return &RAGService{
		llm:      llmProvider,
		vectorDB: vectorDB,
		memory:   memory,
		model:    model,
	}
}

// SetAgentEngine 设置 Agent 引擎（使 RAG 支持工具调用）
func (s *RAGService) SetAgentEngine(engine *core.AgentEngine) {
	s.engine = engine
}

// QueryRequest 查询请求
type QueryRequest struct {
	Query      string `json:"query"`
	TopK       int    `json:"top_k"`
	Collection string `json:"collection"`       // 指定向量集合（可选）
	AgentID    string `json:"agent_id"`         // 指定 Agent（可选），用于自动路由到对应知识库
	UseMemory  bool   `json:"use_memory"`       // 是否结合记忆检索
	Hybrid     bool   `json:"hybrid"`           // 是否使用混合检索
}

// QueryResponse 查询响应
type QueryResponse struct {
	Answer      string              `json:"answer"`
	Sources     []SourceInfo        `json:"sources"`
	MemoryUsed  []MemoryRef         `json:"memory_used,omitempty"`
	Score       float64             `json:"score"`
	TokenUsage  llm.TokenUsage      `json:"token_usage"`
	RetrievalMethod string          `json:"retrieval_method"` // vector|hybrid|memory
}

type SourceInfo struct {
	DocumentID string  `json:"document_id"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
}
type MemoryRef struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

// resolveCollection 根据请求参数解析向量集合名称
func (s *RAGService) resolveCollection(req *QueryRequest) string {
	if req.Collection != "" {
		return req.Collection
	}
	if req.AgentID != "" {
		return "kb_" + req.AgentID
	}
	return "agent_knowledge" // 全局默认集合
}

// Query 执行 RAG 检索增强生成 — 核心流程：
// 用户提问 → 向量检索 + 记忆检索 → 上下文构建 → (有内容则RAG / 无内容则Agent工具调用) → 返回答案+来源
func (s *RAGService) Query(ctx context.Context, req *QueryRequest) (*QueryResponse, error) {
	if req.TopK == 0 { req.TopK = 5 }
	req.Collection = s.resolveCollection(req)

	var allSources []string
	var sourceInfos []SourceInfo
	var memories []MemoryRef

	method := "vector"

	// Step 1: 向量检索
	vecResults, err := s.vectorDB.Search(ctx, req.Query, req.TopK, req.Collection)
	if err != nil {
		return nil, fmt.Errorf("向量检索失败: %w", err)
	}
	for _, r := range vecResults {
		allSources = append(allSources, r.Content)
		sourceInfos = append(sourceInfos, SourceInfo{
			DocumentID: r.DocumentID,
			Score:      r.Score,
			Content:    r.Content,
		})
	}

	// Step 2: 记忆检索（可选，增强个性化）
	if req.UseMemory && s.memory != nil {
		memResults, _ := s.memory.SearchLongTerm(ctx, "", req.Query, 3)
		for _, m := range memResults {
			memories = append(memories, MemoryRef{
				ID:       m.ID,
				Category: m.Category,
				Summary:  m.Summary,
			})
			allSources = append(allSources, "[记忆] "+m.Summary)
		}
		if len(memories) > 0 { method = "hybrid" }
	}

	var answer string

	if len(sourceInfos) > 0 || len(memories) > 0 {
		// 有上下文 → 走传统 RAG 流程
		context := s.buildContext(vecResults, memories)
		prompt := s.buildPrompt(req.Query, context)
		resp, err := s.llm.Generate(ctx, &llm.GenerateRequest{
			Model:       s.model,
			Prompt:      prompt,
			System:      "你是一个专业的智能助手。基于提供的上下文信息准确回答用户问题。使用简洁清晰的中文回答。",
			Temperature: 0.7,
		})
		if err != nil {
			return nil, fmt.Errorf("LLM 调用失败: %w", err)
		}
		answer = resp.Content

	} else if s.engine != nil {
		// 知识库为空但有 Agent 引擎 → 走 Agent ReAct 循环（支持联网等工具调用）
		method = "agent"
		dummyAgent := &model.Agent{
			Name:         "RAG助手",
			SystemPrompt: "你是一个友好的AI助手，可以帮助用户解答问题。",
		}

		result, err := s.engine.Run(ctx, dummyAgent, req.Query)
		if err != nil {
			// Agent 执行失败，降级到直接 LLM 调用
			resp, llmErr := s.llm.Generate(ctx, &llm.GenerateRequest{
				Model:       s.model,
				Prompt:      req.Query,
				System:      "你是一个友好的AI助手，请用简洁的中文回答用户的问题。",
				Temperature: 0.7,
			})
			if llmErr != nil {
				return nil, fmt.Errorf("LLM 调用失败: %w", llmErr)
			}
			answer = resp.Content
		} else if result.Answer != "" {
			answer = result.Answer
		} else {
			// Agent 返回空答案，降级到 LLM
			resp, llmErr := s.llm.Generate(ctx, &llm.GenerateRequest{
				Model:       s.model,
				Prompt:      req.Query,
				System:      "你是一个友好的AI助手，请用简洁清晰的中文回答用户的问题。如果不知道答案，请诚实告知。",
				Temperature: 0.7,
			})
			if llmErr != nil {
				return nil, fmt.Errorf("LLM 调用失败: %w", llmErr)
			}
			answer = resp.Content
		}

	} else {
		// 无知识库也无 Agent 引擎 → 直接调 LLM
		resp, err := s.llm.Generate(ctx, &llm.GenerateRequest{
			Model:       s.model,
			Prompt:      req.Query,
			System:      "你是一个专业的智能助手。请用简洁清晰的中文回答用户的问题。",
			Temperature: 0.7,
		})
		if err != nil {
			return nil, fmt.Errorf("LLM 调用失败: %w", err)
		}
		answer = resp.Content
	}

	bestScore := float64(0)
	for _, si := range sourceInfos { if si.Score > bestScore { bestScore = si.Score } }

	return &QueryResponse{
		Answer:           answer,
		Sources:          sourceInfos,
		MemoryUsed:       memories,
		Score:            bestScore,
		TokenUsage:       llm.TokenUsage{},
		RetrievalMethod: method,
	}, nil
}

// buildContext 构建 RAG 上下文 — Prompt 工程的关键环节
func (s *RAGService) buildContext(vectorResults []vector.SearchResult, memories []MemoryRef) string {
	var parts []string

	// 知识库片段
	if len(vectorResults) > 0 {
		parts = append(parts, "【知识库文档】")
		for i, r := range vectorResults {
			parts = append(parts, fmt.Sprintf("文档%d [相关度:%.2f]: %s", i+1, r.Score, r.Content))
		}
		parts = append(parts, "")
	}

	// 记忆片段
	if len(memories) > 0 {
		parts = append(parts, "【历史交互记忆】")
		for _, m := range memories {
			parts = append(parts, fmt.Sprintf("- [%s] %s", m.Category, m.Summary))
		}
		parts = append(parts, "")
	}

	return strings.Join(parts, "\n")
}

// buildPrompt 构建 RAG Prompt 模板 — 可配置化
func (s *RAGService) buildPrompt(query, context string) string {
	return fmt.Sprintf(`请基于以下上下文信息回答用户的问题。

%s
问题：%s

要求：
1. 回答要准确且基于上下文内容
2. 引用具体的文档来源
3. 如果上下文中没有足够信息，请说明
4. 使用简洁清晰的中文回答

答案：`, context, query)
}
