package core

import (
	"context"
	"fmt"
	"strings"

	"sirenagent/internal/model"
	"sirenagent/pkg/llm"
)

// ==================== Evaluator-Optimizer 模式 ====================

// AnswerQuality 答案质量评分
type AnswerQuality struct {
	Score          int    // 1-10 分
	Issues         []string // 存在的问题
	MissingPoints  []string // 遗漏的要点
	Suggestions    []string // 优化建议
	Passed         bool    // 是否通过评估（>= 7分）
}

// Evaluator 答案评估器 — 对Agent输出做质量评分
type Evaluator struct {
	llm     llm.LLMProvider
	model   string
	enabled bool
	maxRounds int // 最大优化轮次
}

func NewEvaluator(llmProvider llm.LLMProvider, model string) *Evaluator {
	return &Evaluator{
		llm:       llmProvider,
		model:     model,
		enabled:   true,
		maxRounds: 2, // 默认最多优化2次
	}
}

// SetEnabled 开关
func (ev *Evaluator) SetEnabled(enabled bool) { ev.enabled = enabled }

// Evaluate 评估Agent的回答质量
func (ev *Evaluator) Evaluate(ctx context.Context, userMsg, answer string, toolCalls []*ToolCallRecord) *AnswerQuality {
	quality := &AnswerQuality{Score: 8, Passed: true} // 默认通过

	// 1. 空回答检查
	if strings.TrimSpace(answer) == "" {
		quality.Score = 1
		quality.Issues = append(quality.Issues, "回答为空")
		quality.Passed = false
		return quality
	}

	// 2. 长度检查（太短可能没回答完整）
	runes := []rune(answer)
	if len(runes) < 20 {
		quality.Score = 4
		quality.Issues = append(quality.Issues, "回答过短")
		quality.Passed = false
		return quality
	}

	// 3. 使用LLM评估（高阶语义检查）
	if ev.llm != nil && len(runes) > 50 {
		prompt := fmt.Sprintf(`你是一个AI回答质量评估员。请评估以下回答的质量。

用户问题：%s

AI回答：%s

工具调用：%d次

请从以下维度评估（1-10分）：
1. 回答是否直接回应了用户问题
2. 回答是否清晰完整
3. 是否在必要时引用了来源

请给出：
- 总体评分（1-10分）
- 存在的问题（如有）
- 优化建议（如有）
- 评估结果：通过/需优化

格式：
评分: <数字>
问题: <逗号分隔>
建议: <逗号分隔>
结论: <通过或需优化>`, userMsg, answer, len(toolCalls))

		resp, err := ev.llm.Generate(ctx, &llm.GenerateRequest{
			Model:       ev.model,
			Prompt:      prompt,
			System:      "你是一个严格但公平的AI回答质量评估员。只基于事实评估，不预设立场。",
			Temperature: 0.3,
		})
		if err == nil {
			quality = ev.parseEvaluation(resp.Content)
		}
	}

	return quality
}

// Optimize 根据评估结果优化回答
func (ev *Evaluator) Optimize(ctx context.Context, userMsg, currentAnswer string, quality *AnswerQuality) (string, error) {
	if quality.Passed {
		return currentAnswer, nil
	}

	suggestions := strings.Join(quality.Suggestions, "；")
	if suggestions == "" {
		suggestions = "请检查回答是否完整、准确"
	}

	prompt := fmt.Sprintf(`你需要优化以下AI回答。

原始用户问题：%s

当前回答：%s

评估发现的问题：%s

优化建议：%s

请给出优化后的回答。要求：
1. 保持事实准确性
2. 更直接地回应问题
3. 结构更清晰
4. 合理补充遗漏要点

优化后的回答：`, userMsg, currentAnswer, strings.Join(quality.Issues, "；"), suggestions)

	resp, err := ev.llm.Generate(ctx, &llm.GenerateRequest{
		Model:       ev.model,
		Prompt:      prompt,
		System:      "你是一个回答优化助手。你的任务是优化AI回答，使其更准确、完整、清晰。保持原有事实不变。",
		Temperature: 0.5,
	})
	if err != nil {
		return currentAnswer, fmt.Errorf("优化失败: %w", err)
	}

	return resp.Content, nil
}

// OptimizeLoop 完整的优化循环
func (ev *Evaluator) OptimizeLoop(ctx context.Context, userMsg, answer string, toolCalls []*ToolCallRecord) (string, *AnswerQuality, int) {
	if !ev.enabled || answer == "" {
		return answer, &AnswerQuality{Score: 8, Passed: true}, 0
	}

	optimized := answer
	rounds := 0

	for rounds < ev.maxRounds {
		quality := ev.Evaluate(ctx, userMsg, optimized, toolCalls)
		if quality.Passed {
			fmt.Printf("[Evaluator] ✅ 第%d轮评估通过 (评分:%d/10)\n", rounds+1, quality.Score)
			return optimized, quality, rounds + 1
		}

		fmt.Printf("[Evaluator] 🔄 第%d轮需优化 (评分:%d/10, 问题:%v)\n",
			rounds+1, quality.Score, quality.Issues)

		improved, err := ev.Optimize(ctx, userMsg, optimized, quality)
		if err != nil {
			fmt.Printf("[Evaluator] ⚠️ 优化失败: %v，使用原回答\n", err)
			return optimized, quality, rounds + 1
		}

		// 如果优化后没变化，停止
		if improved == optimized {
			return optimized, quality, rounds + 1
		}

		optimized = improved
		rounds++
	}

	return optimized, &AnswerQuality{Score: 8, Passed: true}, rounds
}

// parseEvaluation 解析LLM返回的评估结果
func (ev *Evaluator) parseEvaluation(content string) *AnswerQuality {
	quality := &AnswerQuality{Score: 8, Passed: true}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "评分:") {
			fmt.Sscanf(line, "评分: %d", &quality.Score)
		} else if strings.HasPrefix(line, "问题:") {
			parts := strings.Split(strings.TrimPrefix(line, "问题:"), ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					quality.Issues = append(quality.Issues, p)
				}
			}
		} else if strings.HasPrefix(line, "建议:") {
			parts := strings.Split(strings.TrimPrefix(line, "建议:"), ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					quality.Suggestions = append(quality.Suggestions, p)
				}
			}
		} else if strings.HasPrefix(line, "结论:") {
			conclusion := strings.TrimPrefix(line, "结论:")
			quality.Passed = strings.Contains(conclusion, "通过")
		}
	}

	// 确保评分范围
	if quality.Score < 1 {
		quality.Score = 1
	}
	if quality.Score > 10 {
		quality.Score = 10
	}

	return quality
}
