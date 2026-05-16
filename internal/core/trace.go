package core

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ==================== Trace 可观测性系统 ====================

// TraceID 生成
var (
	traceCounter int64
	traceMu      sync.Mutex
)

func generateTraceID() string {
	traceMu.Lock()
	defer traceMu.Unlock()
	traceCounter++
	return fmt.Sprintf("trace_%d_%d", time.Now().UnixMilli(), traceCounter)
}

// TraceRecord 单次Agent执行的完整追踪记录
type TraceRecord struct {
	TraceID        string            `json:"trace_id"`
	AgentName      string            `json:"agent_name"`
	AgentID        string            `json:"agent_id"`
	UserMessage    string            `json:"user_message"`
	StartTime      time.Time         `json:"start_time"`
	EndTime        time.Time         `json:"end_time"`
	DurationMs     int64             `json:"duration_ms"`
	TurnCount      int               `json:"turn_count"`
	ToolCallCount  int               `json:"tool_call_count"`
	TotalTokens    int               `json:"total_tokens"`
	PromptTokens   int               `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	GuardrailBlocked bool            `json:"guardrail_blocked"`
	EvaluatorScore int               `json:"evaluator_score"`
	ToolBreakdown  []ToolTrace       `json:"tool_breakdown,omitempty"`
	HasError       bool              `json:"has_error"`
	ErrorMsg       string            `json:"error_msg,omitempty"`
}

// ToolTrace 单个工具的追踪信息
type ToolTrace struct {
	ToolName  string `json:"tool_name"`
	DurationMs int64 `json:"duration_ms"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

// TraceStore 追踪记录存储（环形缓冲区，最多保留100条）
type TraceStore struct {
	mu     sync.RWMutex
	ring   []*TraceRecord
	maxLen int
	pos    int
	count  int
}

var GlobalTraceStore = NewTraceStore(100)

func NewTraceStore(maxLen int) *TraceStore {
	return &TraceStore{
		ring:   make([]*TraceRecord, maxLen),
		maxLen: maxLen,
	}
}

func (s *TraceStore) Push(record *TraceRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ring[s.pos] = record
	s.pos = (s.pos + 1) % s.maxLen
	if s.count < s.maxLen {
		s.count++
	}
}

func (s *TraceStore) List(limit int) []*TraceRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > s.maxLen {
		limit = s.maxLen
	}
	if limit > s.count {
		limit = s.count
	}

	result := make([]*TraceRecord, 0, limit)
	start := (s.pos - limit + s.maxLen) % s.maxLen
	for i := 0; i < limit; i++ {
		idx := (start + i) % s.maxLen
		if s.ring[idx] != nil {
			result = append(result, s.ring[idx])
		}
	}
	return result
}

func (s *TraceStore) GetByID(traceID string) *TraceRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := 0; i < s.maxLen; i++ {
		if s.ring[i] != nil && s.ring[i].TraceID == traceID {
			return s.ring[i]
		}
	}
	return nil
}

// PrintTraceSummary 打印追踪摘要到日志
func PrintTraceSummary(record *TraceRecord) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n═════════════════ Trace: %s ═════════════════\n", record.TraceID))
	sb.WriteString(fmt.Sprintf("  Agent: %s (%s)\n", record.AgentName, record.AgentID))
	sb.WriteString(fmt.Sprintf("  耗时: %dms | 轮次: %d | 工具调用: %d次\n", record.DurationMs, record.TurnCount, record.ToolCallCount))
	sb.WriteString(fmt.Sprintf("  Token: %d (prompt=%d, completion=%d)\n", record.TotalTokens, record.PromptTokens, record.CompletionTokens))
	if record.GuardrailBlocked {
		sb.WriteString(fmt.Sprintf("  🛡️ 被防护系统拦截\n"))
	}
	if record.EvaluatorScore > 0 {
		sb.WriteString(fmt.Sprintf("  📊 评估评分: %d/10\n", record.EvaluatorScore))
	}
	if len(record.ToolBreakdown) > 0 {
		sb.WriteString(fmt.Sprintf("  工具明细:\n"))
		for _, t := range record.ToolBreakdown {
			status := "✓"
			if !t.Success {
				status = "✗"
			}
			sb.WriteString(fmt.Sprintf("    %s %s (%dms)\n", status, t.ToolName, t.DurationMs))
		}
	}
	if record.HasError {
		sb.WriteString(fmt.Sprintf("  ❌ 错误: %s\n", record.ErrorMsg))
	}
	sb.WriteString(fmt.Sprintf("═══════════════════════════════════════════════\n"))
	fmt.Print(sb.String())
}
