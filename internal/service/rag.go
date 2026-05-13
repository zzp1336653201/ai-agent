package service

import (
	"context"
	"fmt"
	"strings"

	"sirenagent/internal/core"
	"sirenagent/pkg/llm"
	"sirenagent/pkg/vector"
)

// RAGService 检索增强生成服务（升级版）
// 对应职位要求：RAG 技术、记忆管理、混合检索
type RAGService struct {
	llm      llm.LLMProvider
	vectorDB vector.VectorProvider
	memory   core.MemoryManager
}

func NewRAGService(llmProvider llm.LLMProvider, vectorDB vector.VectorProvider, memory core.MemoryManager) *RAGService {
	return &RAGService{
		llm:      llmProvider,
		vectorDB: vectorDB,
		memory:   memory,
	}
}

// QueryRequest 查询请求
type QueryRequest struct {
	Query      string `json:"query"`
	TopK       int    `json:"top_k"`
	Collection string `json:"collection"`
	UseMemory  bool   `json:"use_memory"` // 是否结合记忆检索
	Hybrid     bool   `json:"hybrid"`    // 是否使用混合检索
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

// Query 执行 RAG 检索增强生成 — 核心流程：
// 用户提问 → 向量检索 + 记忆检索 → 上下文构建 → Prompt 注入 → LLM 生成 → 返回答案+来源
func (s *RAGService) Query(ctx context.Context, req *QueryRequest) (*QueryResponse, error) {
	if req.TopK == 0 { req.TopK = 5 }
	if req.Collection == "" { req.Collection = "agent_knowledge" }

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

	// Step 3: 构建上下文（Prompt 工程）
	context := s.buildContext(vecResults, memories)

	// Step 4: 构建 Prompt 并调用 LLM
	prompt := s.buildPrompt(req.Query, context)

	resp, err := s.llm.Generate(ctx, &llm.GenerateRequest{
		Prompt: prompt,
		System: "你是一个专业的智能助手。基于提供的上下文信息回答用户问题。如果上下文中没有相关信息，请诚实告知无法从提供的信息中找到答案。",
	})
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}

	bestScore := float64(0)
	for _, si := range sourceInfos { if si.Score > bestScore { bestScore = si.Score } }

	return &QueryResponse{
		Answer:           resp.Content,
		Sources:          sourceInfos,
		MemoryUsed:       memories,
		Score:            bestScore,
		TokenUsage:       resp.TokenUsage,
		RetrievalMethod: method,
	}, nil
}

// buildContext 构建 RAG 上下文 — Prompt 工程的关键环节
func (s *RAGService) buildContext(vectorResults []core.SearchResult, memories []MemoryRef) string {
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
