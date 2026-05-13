package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"sirenagent/internal/model"
)

// ==================== 记忆管理实现 ====================
// 对应职位要求的"记忆管理"核心能力

// InMemoryMemoryManager 内存记忆管理器（开发/演示用）
// 生产环境应替换为 Redis + PostgreSQL 实现
type InMemoryMemoryManager struct {
	shortTerm map[string][]*model.Memory // agentID:userID -> memories
	longTerm  []*model.Memory            // 全量长期记忆（实际应存向量库）
	mu        sync.RWMutex
}

// NewInMemoryMemoryManager 创建内存记忆管理器
func NewInMemoryMemoryManager() *InMemoryMemoryManager {
	return &InMemoryMemoryManager{
		shortTerm: make(map[string][]*model.Memory),
		longTerm:  make([]*model.Memory, 0),
	}
}

func (m *InMemoryMemoryManager) SaveShortTerm(ctx context.Context, agentID, userID, content string) error {
	key := fmt.Sprintf("%s:%s", agentID, userID)
	mem := &model.Memory{
		ID:         model.NewMemory("", "", "interaction", content, "", 1).ID,
		AgentID:    agentID,
		UserID:     userID,
		Category:   "interaction",
		Content:    content,
		Summary:    truncate(content, 100), // 自动摘要用于检索
		Importance: 1,
		CreatedAt:  time.Now(),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.shortTerm[key]; !ok {
		m.shortTerm[key] = make([]*model.Memory, 0)
	}
	m.shortTerm[key] = append(m.shortTerm[key], mem)
	return nil
}

func (m *InMemoryMemoryManager) GetShortTerm(ctx context.Context, agentID, userID string, limit int) ([]*model.Memory, error) {
	key := fmt.Sprintf("%s:%s", agentID, userID)
	m.mu.RLock()
	defer m.mu.RUnlock()

	memories, ok := m.shortTerm[key]
	if !ok {
		return []*model.Memory{}, nil
	}

	// 返回最近的 limit 条记录
	result := make([]*model.Memory, 0, limit)
	start := len(memories) - limit
	if start < 0 {
		start = 0
	}
	for i := start; i < len(memories); i++ {
		result = append(result, memories[i])
	}
	return result, nil
}

func (m *InMemoryMemoryManager) SaveLongTerm(ctx context.Context, memory *model.Memory) error {
	memory.CreatedAt = time.Now()
	m.memory.UpdatedAt = time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.longTerm = append(m.longTerm, memory)
	return nil
}

func (m *InMemoryMemoryManager) SearchLongTerm(ctx context.Context, agentID, query string, topK int) ([]*model.Memory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 简单的关键词匹配（实际生产环境使用向量相似度搜索）
	queryLower := strings.ToLower(query)
	type scoredMem struct {
		mem   *model.Memory
		score float64
	}
	var scored []scoredMem

	for _, mem := range m.longTerm {
		if mem.AgentID != agentID && agentID != "" {
			continue
		}
		score := computeRelevance(queryLower, strings.ToLower(mem.Summary))
		if score > 0 {
			scored = append(scored, scoredMem{mem: mem, score: score})
		}
	}

	// 按分数降序排序
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	result := make([]*model.Memory, 0, topK)
	for i := 0; i < len(scored) && i < topK; i++ {
		result = append(result, scored[i].mem)
	}
	return result, nil
}

func (m *InMemoryMemoryManager) CleanExpired(ctx context.Context, agentID string) (int64, error) {
	now := time.Now()
	var cleaned int64

	m.mu.Lock()
	defer m.mu.Unlock()

	filtered := make([]*model.Memory, 0)
	for _, mem := range m.longTerm {
		if (agentID == "" || mem.AgentID == agentID) && !mem.ExpireAt.IsZero() && now.After(mem.ExpireAt) {
			cleaned++
		} else {
			filtered = append(filtered, mem)
		}
	}
	m.longTerm = filtered

	// 清理短期记忆过期条目
	for key, memories := range m.shortTerm {
		var valid []*model.Memory
		for _, mem := range memories {
			valid = append(valid, mem)
		}
		m.shortTerm[key] = valid
	}

	return cleaned, nil
}

// ==================== 辅助函数 ====================

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// computeRelevance 简单的相关性计算（关键词匹配）
func computeRelevance(query, text string) float64 {
	queryWords := strings.Fields(text)
	textWords := strings.Fields(text)

	hitCount := 0
	for _, qw := range queryWords {
		for _, tw := range textWords {
			if strings.Contains(tw, qw) || strings.Contains(qw, tw) {
				hitCount++
				break
			}
		}
	}

	if len(queryWords) == 0 {
		return 0
	}
	return float64(hitCount) / float64(len(queryWords))
}

// ==================== Prompt 模板管理器 ====================

// TemplatePromptManager Prompt 模板管理器实现
type TemplatePromptManager struct {
	templates map[string]*model.PromptTemplate
	mu        sync.RWMutex
}

func NewTemplatePromptManager() *TemplatePromptManager {
	return &TemplatePromptManager{
		templates: make(map[string]*model.PromptTemplate),
	}
}

func (m *TemplatePromptManager) RegisterTemplate(name, template, category string, variables []PromptVariable) {
	tmpl := &model.PromptTemplate{
		Name:     name,
		Category: category,
		Template: template,
		Version:  1,
	}
	if variables != nil {
		varsJSON, _ := json.Marshal(variables)
		tmpl.Variables = string(varsJSON)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates[name] = tmpl
}

func (m *TemplatePromptManager) Render(templateName string, variables map[string]string) (string, error) {
	m.mu.RLock()
	tmpl, ok := m.templates[templateName]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("模板 %s 不存在", templateName)
	}

	result := tmpl.Template
	for key, value := range variables {
		result = strings.ReplaceAll(result, fmt.Sprintf("{{%s}}", key), value)
	}
	return result, nil
}

func (m *TemplatePromptManager) ListByCategory(category string) []*model.PromptTemplate {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*model.PromptTemplate, 0)
	for _, t := range m.templates {
		if category == "" || t.Category == category {
			result = append(result, t)
		}
	}
	return result
}
