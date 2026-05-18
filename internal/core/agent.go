package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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

	// ===== Trace: 开始追踪 =====
	traceID := generateTraceID()
	traceStart := time.Now()
	trace := &TraceRecord{
		TraceID:     traceID,
		AgentName:   agent.Name,
		AgentID:     agent.ID,
		UserMessage: truncateForLog(userMessage, 100),
		StartTime:   traceStart,
	}

	// ===== Guardrail 1: 输入防护 =====
	processedInput := userMessage
	if e.guardrail != nil {
		safeInput, result := e.guardrail.CheckInput(userMessage)
		if result.IsBlocked() {
			return &AgentRunResult{
				Answer: fmt.Sprintf("🚫 您的输入已被安全策略拦截。[原因: %s]\n\n请修改后重新提问。", result.Reason),
				Turns:  0,
			}, nil
		}
		processedInput = safeInput
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
	messages = append(messages, &llm.Message{Role: "user", Content: processedInput})

	// 5. ReAct 推理循环
	var totalToken llm.TokenUsage
	tools := e.ToolsToLLMFormat()
	numTools := len(tools)
	logPrefix := fmt.Sprintf("[Agent:%s]", agent.Name)

	e.logf("info", "Agent Run started: agent=%s message_len=%d tools=%d", agent.Name, len(userMessage), numTools)
	fmt.Printf("%s 开始推理, 用户消息: %s, 可用工具: %d个\n", logPrefix, truncateForLog(userMessage, 80), numTools)

	// 判断用户消息是否属于"实时数据"类 — 如果是，强制优先调工具
	needsRealTime := isRealtimeQuery(userMessage)
	if needsRealTime && numTools > 0 {
		fmt.Printf("%s [决策] 用户问题涉及实时数据，启用强制工具调用模式\n", logPrefix)
	}

	for turn := 0; turn < e.config.MaxIterations; turn++ {
		result.Turns = turn + 1

		// 构建对话历史为单个 prompt
		historyPrompt := ""
		for _, msg := range messages {
			if msg.Role == "system" && !strings.Contains(msg.Content, "可用工具") {
				continue
			}
			if msg.Role == "tool" {
				historyPrompt += fmt.Sprintf("\n[工具结果: %s]", msg.Content)
			}
			role := "用户"
			if msg.Role == "assistant" {
				role = "助手"
			} else if msg.Role == "system" {
				continue
			}
			historyPrompt += fmt.Sprintf("\n%s: %s", role, msg.Content)
		}

		// 调用 LLM（带工具定义）
		req := &llm.GenerateRequest{
			Model:       "deepseek-chat",
			Prompt:      historyPrompt,
			System:      systemPrompt,
			Tools:       tools,
			Temperature: 0.7,
		}

		// 实时数据类问题 + 第一轮 → 设置 tool_choice=auto 确保 LLM 优先调工具
		if needsRealTime && turn == 0 {
			req.ToolChoice = "auto"
			fmt.Printf("%s [第%d轮] 启用 tool_choice=auto 强制优先工具调用\n", logPrefix, turn+1)
		}

		// 注入 Agent 上下文到 context（工具层可通过 ctx 获取当前 Agent 信息和知识库标识）
		toolCtx := context.WithValue(ctx, ContextKeyKnowledgeBaseID, agent.KnowledgeBaseID)
		toolCtx = context.WithValue(toolCtx, ContextKeyAgentID, agent.ID)

		fmt.Printf("%s [第%d轮] 调用 LLM (tools=%d)...\n", logPrefix, turn+1, len(tools))
		resp, err := e.llm.Generate(ctx, req)
		if err != nil {
			e.logf("error", "Agent LLM call failed: agent=%s turn=%d error=%v", agent.Name, turn+1, err)
			return nil, fmt.Errorf("第%d轮 LLM 调用失败: %w", turn+1, err)
		}
		totalToken.PromptTokens += resp.TokenUsage.PromptTokens
		totalToken.CompletionTokens += resp.TokenUsage.CompletionTokens

		// 检查是否有工具调用（两种方式：1. Function Calling 2. 文本解析）
		toolCalls := resp.ToolCalls

		// 如果没有 function calling，尝试从文本中解析工具调用
		if len(toolCalls) == 0 {
			toolCalls = e.parseToolCallsFromText(resp.Content)
			if len(toolCalls) > 0 {
				fmt.Printf("%s [第%d轮] 从文本解析到 %d 个工具调用\n", logPrefix, turn+1, len(toolCalls))
			}
		} else {
			fmt.Printf("%s [第%d轮] LLM 返回 %d 个原生函数调用\n", logPrefix, turn+1, len(toolCalls))
		}

		if len(toolCalls) == 0 {
			// 无工具调用，说明是最终答案
			result.Answer = resp.Content
			fmt.Printf("%s [第%d轮] 无工具调用，直接返回答案 (长度:%d)\n", logPrefix, turn+1, len(resp.Content))
			break
		}

		// 有工具调用：执行工具并注入结果
		for _, tc := range toolCalls {
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

			fmt.Printf("%s [第%d轮] → 执行工具: %s, 参数: %v\n", logPrefix, turn+1, tc.Function.Name, params)

			// ===== Guardrail 2: 工具调用防护 =====
			if e.guardrail != nil {
				toolCheck := e.guardrail.ToolGuardrail().CheckToolWithParams(tc.Function.Name, params)
			if toolCheck.IsBlocked() {
				msg := fmt.Sprintf("🛑 工具调用被安全策略拦截。[原因: %s]", toolCheck.Reason)
				record.Output = NewToolResult(msg)
				e.logf("warn", "Agent tool blocked: agent=%s turn=%d tool=%s reason=%s", agent.Name, turn+1, tc.Function.Name, toolCheck.Reason)
				fmt.Printf("%s [第%d轮] 🛑 工具被拦截: %s\n", logPrefix, turn+1, toolCheck.Reason)
				continue // 跳过执行，进入下一轮
			}
				if toolCheck.RequiresConfirmation {
					msg := fmt.Sprintf("⚠️ 工具 %s 为高风险操作，已确认后执行（参数: %v）", tc.Function.Name, params)
					fmt.Printf("%s [第%d轮] ⚠️ 高风险工具已确认: %s\n", logPrefix, turn+1, tc.Function.Name)
					_ = msg // 日志记录，继续执行
				}
			}

			// 执行工具
			tool, exists := e.tools[tc.Function.Name]
			if !exists {
				record.Output = NewToolError(fmt.Errorf("未知工具: %s", tc.Function.Name))
				fmt.Printf("%s [第%d轮] ✗ 未知工具: %s\n", logPrefix, turn+1, tc.Function.Name)
			} else {
				execCtx, cancel := context.WithTimeout(toolCtx, time.Duration(e.config.ToolsTimeout)*time.Second)
				output, toolErr := tool.Execute(execCtx, params)
				cancel()
				if toolErr != nil {
					record.Output = NewToolError(toolErr)
					e.logf("error", "Agent tool failed: agent=%s turn=%d tool=%s error=%v", agent.Name, turn+1, tc.Function.Name, toolErr)
					fmt.Printf("%s [第%d轮] ✗ 工具 %s 执行错误: %v\n", logPrefix, turn+1, tc.Function.Name, toolErr)
				} else {
					record.Output = output
					outputPreview := truncateForLog(output.Content, 120)
					e.logf("info", "Agent tool success: agent=%s turn=%d tool=%s latency_ms=%d", agent.Name, turn+1, tc.Function.Name, time.Since(startTime).Milliseconds())
					fmt.Printf("%s [第%d轮] ✓ 工具 %s 执行成功, 结果: %s\n", logPrefix, turn+1, tc.Function.Name, outputPreview)
				}
			}

			record.LatencyMs = time.Since(startTime).Milliseconds()
			result.ToolCalls = append(result.ToolCalls, record)

			// 将工具调用和结果加入消息历史
			assistantMsg := &llm.Message{
				Role:      "assistant",
				Content:   resp.Content,
				ToolCalls: []llm.ToolCall{tc},
			}
			messages = append(messages, assistantMsg)

			toolContent := record.Output.Content
			if record.Output.Error != "" {
				toolContent = fmt.Sprintf("错误: %s", record.Output.Error)
			}
			messages = append(messages, &llm.Message{
				Role:       "tool",
				Content:    toolContent,
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
	e.logf("info", "Agent Run completed: agent=%s turns=%d tool_calls=%d tokens=%d", agent.Name, result.Turns, len(result.ToolCalls), totalToken.TotalTokens)
	fmt.Printf("%s 推理完成, 轮次:%d, 工具调用:%d次, Token:%d\n", logPrefix, result.Turns, len(result.ToolCalls), totalToken.TotalTokens)

	// ===== Trace: 记录工具明细 =====
	for _, tc := range result.ToolCalls {
		trace.ToolBreakdown = append(trace.ToolBreakdown, ToolTrace{
			ToolName:   tc.ToolName,
			DurationMs: tc.LatencyMs,
			Success:    tc.Output != nil && tc.Output.Error == "",
			Error:      toolResultError(tc.Output),
		})
	}
	trace.TurnCount = result.Turns
	trace.ToolCallCount = len(result.ToolCalls)
	trace.TotalTokens = totalToken.TotalTokens
	trace.PromptTokens = totalToken.PromptTokens
	trace.CompletionTokens = totalToken.CompletionTokens

	// 格式化答案排版（分段、列表间距等）
	result.Answer = formatAnswer(result.Answer)

	// ===== Guardrail 3: 输出防护 =====
	if e.guardrail != nil && result.Answer != "" {
		safeOutput, outputResult := e.guardrail.CheckOutput(result.Answer)
		if outputResult.Action == ActionMask {
			fmt.Printf("%s 🛡️ 输出已自动屏蔽敏感信息\n", logPrefix)
		}
		if outputResult.Action == ActionWarn {
			fmt.Printf("%s ⚠️ 输出质量警告: %s\n", logPrefix, outputResult.Reason)
			safeOutput = safeOutput + "\n\n> ⚠️ *该回答可能存在质量问题，仅供参考*"
		}
		result.Answer = safeOutput
	}

	// ===== Evaluator-Optimizer: 评估并优化回答质量 =====
	if e.evaluator != nil && result.Answer != "" && result.Turns > 0 {
		optimized, quality, rounds := e.evaluator.OptimizeLoop(ctx, processedInput, result.Answer, result.ToolCalls)
		if rounds > 0 {
			e.logf("info", "Agent answer optimized: agent=%s score=%d rounds=%d", agent.Name, quality.Score, rounds)
			fmt.Printf("%s 📊 评估优化: 评分=%d/10, 优化轮次=%d, 长度=%d→%d字\n",
				logPrefix, quality.Score, rounds,
				len([]rune(result.Answer)), len([]rune(optimized)))
			result.Answer = optimized
		}
	}

	// ===== Trace: 最终化并推送 =====
	trace.EndTime = time.Now()
	trace.DurationMs = trace.EndTime.Sub(traceStart).Milliseconds()
	trace.TotalTokens = totalToken.TotalTokens
	trace.PromptTokens = totalToken.PromptTokens
	trace.CompletionTokens = totalToken.CompletionTokens
	trace.TurnCount = result.Turns
	trace.ToolCallCount = len(result.ToolCalls)
	GlobalTraceStore.Push(trace)
	PrintTraceSummary(trace)

	// 6. 保存本次交互到短期记忆
	go func() {
		memoryCtx := context.Background()
		content := fmt.Sprintf("用户: %s | 助手: %s", userMessage, result.Answer)
		e.memoryMgr.SaveShortTerm(memoryCtx, agent.ID, "default", content)
	}()

	return result, nil
}

// toolResultError 获取工具执行错误信息
func toolResultError(tr *ToolResult) string {
	if tr == nil {
		return ""
	}
	return tr.Error
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

	// 工具列表
	if len(e.tools) > 0 {
		parts = append(parts, "# ===== 可用工具 (使用 Function Calling 调用) =====")
		for name, tool := range e.tools {
			parts = append(parts, fmt.Sprintf("- **%s**: %s", name, tool.Description()))
		}
		parts = append(parts, "")
	}

	// ========== 实时数据触发规则（强制优先） ==========
	parts = append(parts, "# ⚠️ 实时数据触发规则（必须遵守）")
	parts = append(parts, "")
	parts = append(parts, "【强制规则】当用户询问以下任何类型的信息时，你必须通过工具获取，绝不能依赖自身的训练数据回答：")
	parts = append(parts, "")
	parts = append(parts, "1. **时间日期类**: 当前时间、今天日期、星期几、现在几点、当前年份等任何与「现在」相关的信息")
	parts = append(parts, "2. **天气类**: 天气情况、温度、天气预报、空气质量等")
	parts = append(parts, "3. **新闻类**: 最新消息、热点资讯、时事要闻、行业动态等")
	parts = append(parts, "4. **价格/行情类**: 股票价格、汇率、加密货币价格、商品价格等")
	parts = append(parts, "5. **地理位置/导航类**: 当前位置、路线规划、距离计算、周边信息等")
	parts = append(parts, "6. **知识查询类**: 你不确定的知识点、最新技术、人物、事件等")
	parts = append(parts, "7. **网络数据类**: 网页内容、API 数据、实时统计信息等")
	parts = append(parts, "")
	parts = append(parts, "违规后果：如果使用自身知识回答而非调用工具获取实时数据，将被视为错误行为。")
	parts = append(parts, "")

	// 行为规范
	parts = append(parts, "# 工作流程")
	parts = append(parts, "1. 收到用户问题后，首先判断是否需要实时数据（参考上方分类）")
	parts = append(parts, "2. 如果需要实时数据 → **必须**调用工具获取，不能直接回答")
	parts = append(parts, "3. 如果不需要实时数据且你能直接回答 → 直接给出答案")
	parts = append(parts, "4. 每次调用一个工具，获取结果后再决定下一步（思考→行动→观察循环）")
	parts = append(parts, "5. 最终回答要简洁准确，使用中文")
	parts = append(parts, "")

	// 工具调用方式
	parts = append(parts, "# 工具调用方式")
	parts = append(parts, "你支持原生 Function Calling 和文本 JSON 格式两种方式：")
	parts = append(parts, "- **优先使用原生 Function Calling**（通过工具定义直接返回）")
	parts = append(parts, "- 如果无法使用原生方式，输出 JSON 格式调用：")
	parts = append(parts, `  {"tool": "工具名称", "params": {"参数名": "参数值"}}`)
	parts = append(parts, "")
	parts = append(parts, "示例（搜索实时信息）：")
	parts = append(parts, `  {"tool": "web_search", "params": {"query": "当前北京时间"}}`)
	parts = append(parts, "")

	return strings.Join(parts, "\n")
}

// parseToolCallsFromText 从文本中解析工具调用
// 当 LLM 不支持 function calling 或者返回文本格式工具调用时使用此方法
// 支持的格式：
//   - JSON: {"tool": "xxx", "params": {...}}
//   - XML: <tool name="xxx"><param name="key">value</param></tool>
//   - Markdown 代码块中的 JSON: ```json {"tool": "xxx", "params": {...}} ```
func (e *AgentEngine) parseToolCallsFromText(content string) []llm.ToolCall {
	var calls []llm.ToolCall

	// 尝试多种格式解析
	calls = append(calls, e.parseJSONToolCalls(content)...)
	if len(calls) > 0 {
		return calls
	}

	calls = append(calls, e.parseJSONCodeBlockToolCalls(content)...)
	if len(calls) > 0 {
		return calls
	}

	return calls
}

// parseJSONToolCalls 解析纯文本中的 JSON 工具调用格式: {"tool": "xxx", "params": {...}}
func (e *AgentEngine) parseJSONToolCalls(content string) []llm.ToolCall {
	var calls []llm.ToolCall

	// 搜索所有可能的 JSON 工具调用（支持单引号和双引号两种格式）
	start := 0
	for {
		idx := strings.Index(content[start:], `{"tool"`)
		if idx == -1 {
			idx = strings.Index(content[start:], `{'tool'`)
			if idx == -1 {
				break
			}
		}

		// 找到可能的 JSON 开始位置
		jsonStart := start + idx

		// 尝试找到完整的 JSON 对象（到第一个 }）
		depth := 0
		jsonEnd := -1
		for i := jsonStart; i < len(content); i++ {
			if content[i] == '{' || content[i] == '\'' {
				depth++
			} else if content[i] == '}' || content[i] == '\'' {
				depth--
				if depth == 0 {
					jsonEnd = i + 1
					break
				}
			}
		}

		if jsonEnd == -1 {
			break
		}

		jsonStr := content[jsonStart:jsonEnd]

		// 解析 JSON（尝试替换单引号为双引号以兼容不同格式）
		var toolCall struct {
			Tool   string                 `json:"tool"`
			Params map[string]interface{} `json:"params"`
		}

		parsedStr := strings.ReplaceAll(jsonStr, "'", "\"")

		if err := json.Unmarshal([]byte(parsedStr), &toolCall); err == nil {
			if toolCall.Tool != "" {
				// 检查工具是否存在
				if _, exists := e.tools[toolCall.Tool]; exists {
					argsJSON, _ := json.Marshal(toolCall.Params)
					calls = append(calls, llm.ToolCall{
						ID: fmt.Sprintf("call_%d", len(calls)),
						Function: &llm.FunctionCall{
							Name:      toolCall.Tool,
							Arguments: string(argsJSON),
						},
					})
				}
			}
		}

		start = jsonEnd
	}

	return calls
}

// parseJSONCodeBlockToolCalls 解析 Markdown 代码块中的 JSON 工具调用
// 格式: ```json {"tool": "xxx", "params": {...}} ```
func (e *AgentEngine) parseJSONCodeBlockToolCalls(content string) []llm.ToolCall {
	var calls []llm.ToolCall

	// 查找 ```json 代码块
	marker := "```json"
	for {
		startIdx := strings.Index(content, marker)
		if startIdx == -1 {
			break
		}
		blockStart := startIdx + len(marker)

		// 找到代码块结束
		endIdx := strings.Index(content[blockStart:], "```")
		if endIdx == -1 {
			break
		}
		jsonStr := strings.TrimSpace(content[blockStart : blockStart+endIdx])

		// 解析 JSON 数组或单个对象
		var toolCall struct {
			Tool   string                 `json:"tool"`
			Params map[string]interface{} `json:"params"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &toolCall); err == nil && toolCall.Tool != "" {
			if _, exists := e.tools[toolCall.Tool]; exists {
				argsJSON, _ := json.Marshal(toolCall.Params)
				calls = append(calls, llm.ToolCall{
					ID: fmt.Sprintf("call_%d", len(calls)),
					Function: &llm.FunctionCall{
						Name:      toolCall.Tool,
						Arguments: string(argsJSON),
					},
				})
			}
		}

		content = content[blockStart+endIdx+3:]
	}

	return calls
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

// ==================== Handoff 模式 — 去中心化Agent交接 ====================

// HandoffData Agent交接的数据上下文
type HandoffData struct {
	FromAgentID   string `json:"from_agent_id"`
	FromAgentName string `json:"from_agent_name"`
	ToAgentID     string `json:"to_agent_id"`
	Context       string `json:"context"`       // 已完成的上下文
	UserQuery     string `json:"user_query"`    // 原始用户问题
	HandoffReason string `json:"handoff_reason"` // 交接原因
}

// HandoffTool 让Agent可以主动将任务交接给其他Agent
type HandoffTool struct {
	runner *MultiAgentRunner
}

func NewHandoffTool(runner *MultiAgentRunner) *HandoffTool {
	return &HandoffTool{runner: runner}
}

func (t *HandoffTool) Name() string { return "handoff" }
func (t *HandoffTool) Description() string {
	return "将任务交接给另一个专门的Agent处理。当你遇到超出自己专业范围的任务，或者需要其他Agent配合时使用此工具。"
}
func (t *HandoffTool) Parameters() map[string]interface{} {
	// 动态获取可用的Agent列表
	agentList := t.runner.ListAgentNames()
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target_agent": map[string]interface{}{
				"type":        "string",
				"description": fmt.Sprintf("目标Agent名称。可选: %s", strings.Join(agentList, ", ")),
			},
			"reason": map[string]interface{}{
				"type":        "string",
				"description": "交接原因说明，让目标Agent了解为什么转给它处理",
			},
			"context_summary": map[string]interface{}{
				"type":        "string",
				"description": "已经完成的工作总结，让目标Agent了解当前进展",
			},
		},
		"required": []string{"target_agent", "reason"},
	}
}

func (t *HandoffTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	targetName, _ := params["target_agent"].(string)
	reason, _ := params["reason"].(string)
	contextSummary, _ := params["context_summary"].(string)

	if targetName == "" {
		return NewToolResult("交接失败：未指定目标Agent"), nil
	}

	// 查找目标Agent
	targetID := t.runner.FindAgentIDByName(targetName)
	if targetID == "" {
		return NewToolResult(fmt.Sprintf("交接失败：找不到Agent「%s」，可选Agent: %s",
			targetName, strings.Join(t.runner.ListAgentNames(), ", "))), nil
	}

	handoffResult := fmt.Sprintf("✅ 已交接给「%s」\n原因: %s\n上下文: %s\n请等待「%s」处理完成。",
		targetName, reason, contextSummary, targetName)

	return NewToolResult(handoffResult), nil
}

// HandoffChain 管理Agent交接链 — 执行一系列Agent的按序交接
func (r *MultiAgentRunner) HandoffChain(
	ctx context.Context,
	startAgentID string,
	userMessage string,
) (*HandoffChainResult, error) {
	result := &HandoffChainResult{
		Chain: make([]*HandoffChainLink, 0),
	}

	currentAgentID := startAgentID
	currentQuery := userMessage
	contextBuf := ""
	maxHandoffs := 5
	handoffCount := 0

	for handoffCount < maxHandoffs {
		engine, exists := r.engines[currentAgentID]
		if !exists {
			return nil, fmt.Errorf("Agent %s 未注册", currentAgentID)
		}

		// 为当前Agent创建handoff工具，让它可以交接给其他Agent
		handoffTool := NewHandoffTool(r)
		engine.RegisterTool(handoffTool)

		// 构建上下文消息
		finalQuery := currentQuery
		if contextBuf != "" {
			finalQuery = fmt.Sprintf("用户问题: %s\n\n已完成的工作:\n%s\n\n请继续处理。", currentQuery, contextBuf)
		}

		link := &HandoffChainLink{
			AgentID:   currentAgentID,
			AgentName: currentAgentID,
		}

		dummyAgent := &model.Agent{
			ID:           currentAgentID,
			Name:         currentAgentID,
			SystemPrompt: fmt.Sprintf("你是「%s」。你可以处理当前任务，也可以通过handoff工具交接给其他Agent。如果任务超出范围，及时交接。", link.AgentName),
		}

		agentResult, err := engine.Run(ctx, dummyAgent, finalQuery)
		if err != nil {
			link.Error = err.Error()
			link.Success = false
			result.Chain = append(result.Chain, link)
			result.HasError = true
			result.FinalAnswer = fmt.Sprintf("处理失败: %s", err.Error())
			return result, nil
		}

		link.Result = agentResult.Answer
		link.Success = true
		link.ToolCalls = len(agentResult.ToolCalls)
		result.Chain = append(result.Chain, link)

		// 检查是否触发了handoff（通过检查是否调用了handoff工具）
		handoffFound := false
		for _, tc := range agentResult.ToolCalls {
			if tc.ToolName == "handoff" {
				targetName, _ := tc.Input["target_agent"].(string)
				reason, _ := tc.Input["reason"].(string)

				targetID := r.FindAgentIDByName(targetName)
				if targetID != "" && targetID != currentAgentID {
					contextBuf = fmt.Sprintf("%s\n---\n[%s已完成]: %s", contextBuf, link.AgentName, agentResult.Answer)
					currentAgentID = targetID
					currentQuery = reason
					handoffFound = true
					handoffCount++
					break
				}
			}
		}

		if !handoffFound {
			// 没有handoff，说明当前Agent完成了最终任务
			result.FinalAnswer = agentResult.Answer
			result.FinalAgentID = currentAgentID
			return result, nil
		}
	}

	result.HasError = true
	result.FinalAnswer = fmt.Sprintf("交接链过长（超过%d次），已自动终止。最后处理的Agent: %s", maxHandoffs, currentAgentID)
	return result, nil
}

// ListAgentNames 列出所有已注册的Agent名称
func (r *MultiAgentRunner) ListAgentNames() []string {
	names := make([]string, 0)
	for id := range r.engines {
		names = append(names, id)
	}
	return names
}

// FindAgentIDByName 通过名称查找AgentID
func (r *MultiAgentRunner) FindAgentIDByName(name string) string {
	// 先按ID精确匹配
	if _, exists := r.engines[name]; exists {
		return name
	}
	// 按名称模糊匹配
	for id := range r.engines {
		if id == name {
			return id
		}
	}
	return ""
}

// HandoffChainResult 交接链执行结果
type HandoffChainResult struct {
	Chain        []*HandoffChainLink `json:"chain"`
	FinalAnswer  string              `json:"final_answer"`
	FinalAgentID string              `json:"final_agent_id"`
	HasError     bool                `json:"has_error"`
}

// HandoffChainLink 交接链中的一个环节
type HandoffChainLink struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Result    string `json:"result"`
	ToolCalls int    `json:"tool_calls"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
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

// ==================== Context Key（跨层传参，避免接口污染） ====================

type contextKey string

const (
	// ContextKeyKnowledgeBaseID Agent当前使用的知识库ID，用于工具层路由到正确的向量集合
	ContextKeyKnowledgeBaseID contextKey = "knowledge_base_id"
	// ContextKeyAgentID 当前运行的Agent ID
	ContextKeyAgentID contextKey = "agent_id"
)

// KnowledgeBaseCollection 根据知识库ID生成向量集合名称
func KnowledgeBaseCollection(kbID string) string {
	if kbID == "" {
		return "agent_knowledge" // 全局默认集合
	}
	return "kb_" + kbID
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

// ==================== 辅助函数 ====================

// truncateForLog 截断字符串用于日志输出（防止日志过长）
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// isRealtimeQuery 判断用户消息是否涉及实时数据需求
// 通过关键词匹配触发实时数据相关的工具调用
func isRealtimeQuery(message string) bool {
	msg := strings.ToLower(message)

	// 实时数据关键词分组
	timeKeywords := []string{
		"时间", "日期", "星期", "几号", "现在", "今天", "昨天", "明天",
		"当前", "目前", "最新", "实时", "此刻", "此时此刻",
		"time", "date", "today", "now", "current",
	}
	weatherKeywords := []string{
		"天气", "温度", "气温", "下雨", "下雪", "刮风", "雾霾", "晴朗",
		"weather", "temperature", "forecast",
	}
	newsKeywords := []string{
		"新闻", "资讯", "热点", "时事", "报道", "快讯",
		"news", "headline", "breaking",
	}
	priceKeywords := []string{
		"股价", "股票", "汇率", "价格", "行情", "比特币", "基金",
		"stock", "price", "rate", "market",
	}

	// 组合所有关键词
	allKeywords := append([]string{}, timeKeywords...)
	allKeywords = append(allKeywords, weatherKeywords...)
	allKeywords = append(allKeywords, newsKeywords...)
	allKeywords = append(allKeywords, priceKeywords...)

	for _, kw := range allKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}

	return false
}

// formatAnswer 美化 AI 回答排版
// 规则：
//   - 标题前后加空行
//   - 列表项之间加空行
//   - 连续的紧凑段落自动分段
//   - 中文与数字/英文之间加空格
func formatAnswer(s string) string {
	if s == "" {
		return s
	}

	// 1. 标题前后加空行（## 或 ### 等）
	headingRe := regexp.MustCompile(`(?m)^(#{1,6}\s+.+)$`)
	s = headingRe.ReplaceAllString(s, "\n$1\n")

	// 2. 列表项（- 或 1. 开头）之间加空行
	s = regexp.MustCompile(`(\n(?:[-*]\s|\d+\.\s).+)\n((?:[-*]\s|\d+\.\s).+)`).ReplaceAllString(s, "$1\n$2")

	// 3. 表格前后加空行（| xxx | 开头）
	s = regexp.MustCompile(`(?m)^(\|.+\|)`).ReplaceAllString(s, "\n$1\n")

	// 4. 多个空行合并为单个空行
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")

	// 5. 修剪首尾空行
	s = strings.TrimSpace(s)

	return s
}
