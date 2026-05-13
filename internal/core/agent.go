package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"sirenagent/internal/model"
	"sirenagent/pkg/llm"
)

// Run Agent 执行入口 - ReAct 模式推理循环
// 核心思想：Thought → Action(Tool Call) → Observation → Thought → ... → Final Answer
func (e *AgentEngine) Run(ctx context.Context, agent *model.Agent, userMessage string) (*AgentRunResult, error) {
	if err := e.Validate(); err != nil {
		return nil, fmt.Errorf("引擎验证失败: %w", err)
	}

	result := &AgentRunResult{
		Turns:     0,
		ToolCalls: make([]*ToolCallRecord, 0),
		Sources:   make([]SourceInfo, 0),
	}

	// 1. 构建 System Prompt（包含角色定义、工具列表、记忆）
	systemPrompt := e.buildSystemPrompt(agent)

	// 2. 加载短期记忆
	memoryCtx := context.WithoutCancel(ctx)
	shortTermMemories, _ := e.memoryMgr.GetShortTerm(memoryCtx, agent.ID, "default", 10)

	// 3. 构建消息历史
	messages := []*llm.Message{
		{Role: "system", Content: systemPrompt},
	}

	// 注入短期记忆
	if len(shortTermMemories) > 0 {
		memContent := "[近期对话记忆]\n"
		for _, m := range shortTermMemories {
			memContent += fmt.Sprintf("- %s\n", m.Content)
		}
		messages = append(messages, &llm.Message{
			Role:    "system",
			Content: memContent,
		})
	}

	// 4. 用户消息
	messages = append(messages, &llm.Message{Role: "user", Content: userMessage})

	// 5. ReAct 推理循环
	var totalToken llm.TokenUsage
	for turn := 0; turn < e.config.MaxIterations; turn++ {
		result.Turns = turn + 1

		// 调用 LLM（带工具定义）
		toolsDef := e.ToolsToLLMFormat()
		resp, err := e.llm.Chat(ctx, messages)
		if err != nil {
			return nil, fmt.Errorf("第%d轮 LLM 调用失败: %w", turn+1, err)
		}
		totalToken.PromptTokens += resp.TokenUsage.PromptTokens
		totalToken.CompletionTokens += resp.TokenUsage.CompletionTokens

		// 检查是否有工具调用
		if len(resp.Message.ToolCalls) == 0 {
			// 无工具调用，说明是最终答案
			result.Answer = resp.Message.Content
			break
		}

		// 有工具调用：执行工具并注入结果
		for _, tc := range resp.Message.ToolCalls {
			record := &ToolCallRecord{
				ToolName: tc.Function.Name,
			}

			startTime := time.Now()

			// 解析参数
			var params map[string]interface{}
			if tc.Function.Arguments != "" {
				json.Unmarshal([]byte(tc.Function.Arguments), &params)
			}
			if params == nil {
				params = make(map[string]interface{})
			}
			record.Input = params

			// 执行工具
			tool, exists := e.tools[tc.Function.Name]
			if !exists {
				record.Output = NewToolError(fmt.Errorf("未知工具: %s", tc.Function.Name))
			} else {
				toolCtx, cancel := context.WithTimeout(ctx, time.Duration(e.config.ToolsTimeout)*time.Second)
				output, toolErr := tool.Execute(toolCtx, params)
				cancel()
				if toolErr != nil {
					record.Output = NewToolError(toolErr)
				} else {
					record.Output = output
				}
			}

			record.LatencyMs = time.Since(startTime).Milliseconds()
			result.ToolCalls = append(result.ToolCalls, record)

			// 将工具调用和结果加入消息历史
			messages = append(messages, resp.Message) // assistant 的 tool_calls 消息

			toolResultContent := record.Output.Content
			if record.Output.Error != "" {
				toolResultContent = fmt.Sprintf("错误: %s", record.Output.Error)
			}
			messages = append(messages, &llm.Message{
				Role:       "tool",
				Content:    toolResultContent,
				Name:       tc.Function.Name,
				ToolCallID: tc.ID,
			})
		}
	}

	// 如果循环结束还没有答案，取最后一条 assistant 消息
	if result.Answer == "" && len(messages) > 0 {
		lastMsg := messages[len(messages)-1]
		if lastMsg.Role == "assistant" {
			result.Answer = lastMsg.Content
		}
	}

	result.TokenUsage = totalToken

	// 6. 保存本次交互到短期记忆
	go func() {
		memoryCtx := context.Background()
		content := fmt.Sprintf("用户: %s | 助手: %s", userMessage, result.Answer)
		e.memoryMgr.SaveShortTerm(memoryCtx, agent.ID, "default", content)
	}()

	return result, nil
}

// RunStream 流式执行 Agent
func (e *AgentEngine) RunStream(ctx context.Context, agent *model.Agent, userMessage string) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 100)

	// 在后台 goroutine 执行完整流程，将事件发送到 channel
	go func() {
		defer close(ch)
		ch <- StreamEvent{Type: EventStart, Data: map[string]interface{}{"message": "开始处理"}}

		result, err := e.Run(ctx, agent, userMessage)
		if err != nil {
			ch <- StreamEvent{Type: EventError, Data: map[string]interface{}{"error": err.Error()}}
			return
		}
		ch <- StreamEvent{Type: EventComplete, Data: map[string]interface{}{
			"answer":      result.Answer,
			"turns":       result.Turns,
			"tool_calls":  len(result.ToolCalls),
			"token_usage": result.TokenUsage.TotalTokens,
		}}
	}()

	return ch, nil
}

// ==================== Prompt 工程相关方法 ====================

// buildSystemPrompt 构建完整的 System Prompt
// 这是 Prompt 工程的核心 — 将角色、能力、约束、工具全部整合到一条 System Prompt 中
func (e *AgentEngine) buildSystemPrompt(agent *model.Agent) string {
	parts := []string{}

	// 角色定义
	parts = append(parts, fmt.Sprintf("# 角色\n你是一个名为「%s」的 AI 智能体。", agent.Name))
	parts = append(parts, agent.SystemPrompt)
	parts = append(parts, "")

	// 能力描述
	parts = append(parts, "# 你的能力")
	parts = append(parts, "- 你可以调用多种工具来完成用户的请求")
	parts = append(parts, "- 你会通过「思考→行动→观察」的循环来逐步解决问题")
	parts = append(parts, "")

	// 工具列表
	if len(e.tools) > 0 {
		parts = append(parts, "# 可用工具")
		for name, tool := range e.tools {
			parts = append(parts, fmt.Sprintf("- **%s**: %s", name, tool.Description()))
		}
		parts = append(parts, "")
	}

	// 行为约束
	parts = append(parts, "# 行为规范")
	parts = append(parts, "1. 先理解用户意图，再决定是否需要调用工具")
	parts = append(parts, "2. 每次只调用一个工具，观察结果后再决定下一步")
	parts = append(parts, "3. 如果无法从上下文或工具获取答案，诚实告知用户")
	parts = append(parts, "4. 回答要简洁准确，避免冗余")
	parts = append(parts, "5. 使用中文回答")
	parts = append(parts, "")

	// 输出格式
	parts = append(parts, "# 输出格式")
	parts = append(parts, "当你需要使用工具时，请按以下格式输出函数调用：")
	parts = append(parts, ``)
	parts = append(parts, "当你可以直接回答时，直接给出答案即可。")
	parts = append(parts, "")

	return strings.Join(parts, "\n")
}

// ==================== 多智能体协作 ====================

// MultiAgentRunner 多智能体协作调度器
type MultiAgentRunner struct {
	engines map[string]*AgentEngine // agentID -> engine
}

func NewMultiAgentRunner() *MultiAgentRunner {
	return &MultiAgentRunner{
		engines: make(map[string]*AgentEngine),
	}
}

func (r *MultiAgentRunner) RegisterEngine(agentID string, engine *AgentEngine) {
	r.engines[agentID] = engine
}

// Orchestrator 编排模式 — 一个主 Agent 协调多个子 Agent
// 场景：用户提问 → 主 Agent 分析任务 → 分发给子 Agent → 汇总结果
func (r *MultiAgentRunner) Orchestrate(
	ctx context.Context,
	mainAgentID string,
	userMessage string,
	subTasks []SubTask,
) (*OrchestrationResult, error) {
	mainEngine, ok := r.engines[mainAgentID]
	if !ok {
		return nil, fmt.Errorf("主智能体 %s 未注册", mainAgentID)
	}

	result := &OrchestrationResult{
		MainResult: nil,
		SubResults: make(map[string]*AgentRunResult),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, len(subTasks))

	// 并行执行子任务
	for _, task := range subTasks {
		wg.Add(1)
		go func(t SubTask) {
			defer wg.Done()
			subEngine, exists := r.engines[t.TargetAgentID]
			if !exists {
				errChan <- fmt.Errorf("子智能体 %s 未注册", t.TargetAgentID)
				return
			}

			// TODO: 从数据库/配置加载 Agent 定义
			dummyAgent := &model.Agent{
				ID:           t.TargetAgentID,
				Name:         t.TargetAgentID,
				SystemPrompt: t.TaskDescription,
			}

			subResult, err := subEngine.Run(ctx, dummyAgent, t.TaskInput)
			if err != nil {
				errChan <- err
				return
			}

			mu.Lock()
			result.SubResults[t.TaskName] = subResult
			mu.Unlock()
		}(task)
	}

	wg.Wait()
	close(errChan)

	// 收集错误
	var errors []string
	for err := range errChan {
		errors = append(errors, err.Error())
	}

	// 主 Agent 汇总子任务结果
	if len(result.SubResults) > 0 {
		summary := r.buildSubTaskSummary(result.SubResults)
		finalInput := fmt.Sprintf("%s\n\n以下是各子任务的执行结果：\n%s", userMessage, summary)

		dummyMainAgent := &model.Agent{
			ID:           mainAgentID,
			Name:         mainAgentID,
			SystemPrompt: "你是主协调员，负责汇总子任务的结果并给用户一个完整的回答。",
		}
		mainResult, err := mainEngine.Run(ctx, dummyMainAgent, finalInput)
		if err != nil {
			return nil, err
		}
		result.MainResult = mainResult
	}

	if len(errors) > 0 {
		result.Errors = errors
	}

	return result, nil
}

func (r *MultiAgentRunner) buildSubTaskSummary(subResults map[string]*AgentRunResult) string {
	var parts []string
	for name, res := range subResults {
		part := fmt.Sprintf("【%s】(轮次:%d, 工具调用:%d次)\n%s", name, res.Turns, len(res.ToolCalls), res.Answer)
		parts = append(parts, part)
	}
	return strings.Join(parts, "\n\n")
}

// SubTask 子任务定义
type SubTask struct {
	TaskName        string `json:"task_name"`
	TaskDescription string `json:"task_description"`
	TaskInput       string `json:"task_input"`
	TargetAgentID   string `json:"target_agent_id"`
}

// OrchestrationResult 编排结果
type OrchestrationResult struct {
	MainResult *AgentRunResult            `json:"main_result"`
	SubResults map[string]*AgentRunResult `json:"sub_results"`
	Errors     []string                   `json:"errors,omitempty"`
}

// ==================== 流式事件类型 ====================

const (
	EventStart    = "start"
	EventThinking = "thinking"
	EventToolCall = "tool_call"
	EventContent  = "content"
	EventComplete = "complete"
	EventError    = "error"
)

type StreamEvent struct {
	Type string                 `json:"type"`    // 事件类型
	Data map[string]interface{} `json:"data"`    // 事件数据
}
