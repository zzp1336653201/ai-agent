package core

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ==================== Guardrail 类型定义 ====================

// GuardrailAction 防护措施动作
type GuardrailAction string

const (
	ActionAllow GuardrailAction = "allow" // 放行
	ActionBlock GuardrailAction = "block" // 阻止（直接拒绝）
	ActionMask  GuardrailAction = "mask"  // 屏蔽敏感内容后放行
	ActionWarn  GuardrailAction = "warn"  // 放行但记录警告
	ActionFlag  GuardrailAction = "flag"  // 标记需要人工审核
)

// GuardrailResult 防护检查结果
type GuardrailResult struct {
	Action     GuardrailAction `json:"action"`
	Reason     string          `json:"reason"`
	Detail     string          `json:"detail,omitempty"`
	MaskedText string          `json:"masked_text,omitempty"` // mask 后的内容
}

func (r *GuardrailResult) IsAllowed() bool {
	return r.Action == ActionAllow || r.Action == ActionWarn || r.Action == ActionMask
}

func (r *GuardrailResult) IsBlocked() bool {
	return r.Action == ActionBlock || r.Action == ActionFlag
}

// ==================== 输入 Guardrail ====================

// InputGuardrail 用户输入防护
type InputGuardrail interface {
	Check(input string) *GuardrailResult
}

// SensitiveContentGuardrail 敏感内容检查
type SensitiveContentGuardrail struct {
	patterns []struct {
		pattern *regexp.Regexp
		label   string
	}
}

func NewSensitiveContentGuardrail() *SensitiveContentGuardrail {
	g := &SensitiveContentGuardrail{}
	// 基础敏感词规则（生产环境建议接入第三方内容安全API）
	patterns := map[string]string{
		"攻击性语言": `(你他[马妈]的|傻[逼瓜]|去死|滚蛋|操你)`,
		"色情内容":   `(色情|裸聊|约炮|一夜情|成人片|av[电影])`,
		"违法信息":   `(赌博|彩票|开户|刷单|传销|毒品|枪支)`,
		"钓鱼链接":   `(免费领取|点击领取|输入密码|验证码发送)`,
	}
	for label, p := range patterns {
		re, err := regexp.Compile(p)
		if err == nil {
			g.patterns = append(g.patterns, struct {
				pattern *regexp.Regexp
				label   string
			}{pattern: re, label: label})
		}
	}
	return g
}

func (g *SensitiveContentGuardrail) Check(input string) *GuardrailResult {
	for _, p := range g.patterns {
		if p.pattern.MatchString(input) {
			return &GuardrailResult{
				Action: ActionBlock,
				Reason: fmt.Sprintf("输入包含%s", p.label),
			}
		}
	}
	return &GuardrailResult{Action: ActionAllow}
}

// PIIGuardrail 个人隐私信息检测
type PIIGuardrail struct {
	patterns []struct {
		pattern *regexp.Regexp
		label   string
		mask    string
	}
}

func NewPIIGuardrail() *PIIGuardrail {
	g := &PIIGuardrail{}
	rules := []struct {
		pattern string
		label   string
		mask    string
	}{
		{`1[3-9]\d{9}`, "手机号", "138****1234"},
		{`\d{17}[\dXx]`, "身份证号", "****ID****"},
		{`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`, "邮箱地址", "***@***.com"},
		{`(银行卡|卡号)[：:\s]*\d{16,19}`, "银行卡号", "****银行卡****"},
	}
	for _, r := range rules {
		re, err := regexp.Compile(r.pattern)
		if err == nil {
			g.patterns = append(g.patterns, struct {
				pattern *regexp.Regexp
				label   string
				mask    string
			}{pattern: re, label: r.label, mask: r.mask})
		}
	}
	return g
}

func (g *PIIGuardrail) Check(input string) *GuardrailResult {
	masked := input
	for _, p := range g.patterns {
		if p.pattern.MatchString(input) {
			masked = p.pattern.ReplaceAllString(masked, p.mask)
		}
	}
	if masked != input {
		return &GuardrailResult{
			Action:     ActionMask,
			Reason:     "检测到个人隐私信息，已自动屏蔽",
			MaskedText: masked,
		}
	}
	return &GuardrailResult{Action: ActionAllow}
}

// LengthGuardrail 输入长度限制
type LengthGuardrail struct {
	MaxChars int
}

func NewLengthGuardrail(maxChars int) *LengthGuardrail {
	if maxChars <= 0 {
		maxChars = 10000
	}
	return &LengthGuardrail{MaxChars: maxChars}
}

func (g *LengthGuardrail) Check(input string) *GuardrailResult {
	runes := []rune(input)
	if len(runes) > g.MaxChars {
		return &GuardrailResult{
			Action: ActionBlock,
			Reason: fmt.Sprintf("输入过长（%d字），最大允许%d字", len(runes), g.MaxChars),
		}
	}
	return &GuardrailResult{Action: ActionAllow}
}

// ==================== 输出 Guardrail ====================

// OutputGuardrail Agent 输出防护
type OutputGuardrail interface {
	Check(output string) *GuardrailResult
}

// OutputSensitivityGuardrail 输出内容安全检查
type OutputSensitivityGuardrail struct {
	inputGuardrail *SensitiveContentGuardrail
	piiGuardrail   *PIIGuardrail
}

func NewOutputSensitivityGuardrail() *OutputSensitivityGuardrail {
	return &OutputSensitivityGuardrail{
		inputGuardrail: NewSensitiveContentGuardrail(),
		piiGuardrail:   NewPIIGuardrail(),
	}
}

func (g *OutputSensitivityGuardrail) Check(output string) *GuardrailResult {
	// 检查敏感内容
	result := g.inputGuardrail.Check(output)
	if result.IsBlocked() {
		return &GuardrailResult{
			Action: ActionBlock,
			Reason: "输出包含敏感内容",
			Detail: result.Reason,
		}
	}
	// 检查 PII
	result = g.piiGuardrail.Check(output)
	if result.Action == ActionMask {
		return result // 返回已屏蔽的版本
	}
	return &GuardrailResult{Action: ActionAllow}
}

// OutputQualityGuardrail 输出质量检查（基础版）
type OutputQualityGuardrail struct{}

func NewOutputQualityGuardrail() *OutputQualityGuardrail {
	return &OutputQualityGuardrail{}
}

func (g *OutputQualityGuardrail) Check(output string) *GuardrailResult {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return &GuardrailResult{
			Action: ActionBlock,
			Reason: "输出为空",
		}
	}
	if len([]rune(trimmed)) < 3 {
		return &GuardrailResult{
			Action: ActionWarn,
			Reason: "输出过短，可能未完整回答",
		}
	}
	return &GuardrailResult{Action: ActionAllow}
}

// ==================== 工具 Guardrail ====================

// ToolRisk 工具风险等级
type ToolRisk string

const (
	ToolRiskLow    ToolRisk = "low"
	ToolRiskMedium ToolRisk = "medium"
	ToolRiskHigh   ToolRisk = "high"
)

// ToolGuardrail 工具调用防护
type ToolGuardrail struct {
	riskLevels map[string]ToolRisk // toolName -> risk level
	rateLimits map[string]*rateLimiter
	mu         sync.Mutex
}

func NewToolGuardrail() *ToolGuardrail {
	return &ToolGuardrail{
		riskLevels: make(map[string]ToolRisk),
		rateLimits: make(map[string]*rateLimiter),
	}
}

// SetRisk 设置工具风险等级
func (g *ToolGuardrail) SetRisk(toolName string, risk ToolRisk) {
	g.riskLevels[toolName] = risk
	// 高风险工具默认限流
	if risk == ToolRiskHigh {
		g.SetRateLimit(toolName, 5, time.Minute) // 每分钟最多5次
	}
}

// GetRisk 获取工具风险等级
func (g *ToolGuardrail) GetRisk(toolName string) ToolRisk {
	if risk, ok := g.riskLevels[toolName]; ok {
		return risk
	}
	return ToolRiskLow // 默认低风险
}

// SetRateLimit 设置速率限制
func (g *ToolGuardrail) SetRateLimit(toolName string, maxCalls int, window time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rateLimits[toolName] = &rateLimiter{
		maxCalls: maxCalls,
		window:   window,
		calls:    make([]time.Time, 0),
	}
}

// CheckTool 检查工具调用是否允许
func (g *ToolGuardrail) CheckTool(toolName string) *GuardrailResult {
	risk := g.GetRisk(toolName)

	// 检查速率限制
	rl, exists := g.rateLimits[toolName]
	if exists {
		if !rl.allow() {
			return &GuardrailResult{
				Action: ActionBlock,
				Reason: fmt.Sprintf("工具 %s 调用过于频繁，请稍后再试", toolName),
			}
		}
	}

	if risk == ToolRiskHigh {
		return &GuardrailResult{
			Action: ActionFlag,
			Reason: fmt.Sprintf("工具 %s 为高风险操作，需要确认", toolName),
		}
	}

	return &GuardrailResult{Action: ActionAllow}
}

// ToolCheckResult 工具检查结果，含人工确认状态
type ToolCheckResult struct {
	*GuardrailResult
	RequiresConfirmation bool
}

// CheckToolWithParams 检查工具调用（含参数校验）
func (g *ToolGuardrail) CheckToolWithParams(toolName string, params map[string]interface{}) *ToolCheckResult {
	result := g.CheckTool(toolName)

	// 高风险工具需要确认
	if g.GetRisk(toolName) == ToolRiskHigh {
		result.Action = ActionFlag
		result.Reason = fmt.Sprintf("工具 %s 为高风险操作，已标记需确认（参数: %v）", toolName, params)
		return &ToolCheckResult{
			GuardrailResult:      result,
			RequiresConfirmation: true,
		}
	}

	return &ToolCheckResult{GuardrailResult: result}
}

// ==================== GuardrailManager ====================

// GuardrailManager 防护管理器 — 统一所有 Guardrail
type GuardrailManager struct {
	inputGuardrails  []InputGuardrail
	outputGuardrails []OutputGuardrail
	toolGuardrail    *ToolGuardrail
	logger           func(format string, args ...interface{})
}

func NewGuardrailManager() *GuardrailManager {
	return &GuardrailManager{
		inputGuardrails:  make([]InputGuardrail, 0),
		outputGuardrails: make([]OutputGuardrail, 0),
		toolGuardrail:    NewToolGuardrail(),
		logger:           func(format string, args ...interface{}) { fmt.Printf("[Guardrail] "+format+"\n", args...) },
	}
}

// AddInputGuardrail 添加输入防护
func (m *GuardrailManager) AddInputGuardrail(g InputGuardrail) {
	m.inputGuardrails = append(m.inputGuardrails, g)
}

// AddOutputGuardrail 添加输出防护
func (m *GuardrailManager) AddOutputGuardrail(g OutputGuardrail) {
	m.outputGuardrails = append(m.outputGuardrails, g)
}

// SetToolLogger 设置日志函数
func (m *GuardrailManager) SetToolLogger(logger func(format string, args ...interface{})) {
	m.logger = logger
}

// ToolGuardrail 获取工具防护
func (m *GuardrailManager) ToolGuardrail() *ToolGuardrail {
	return m.toolGuardrail
}

// CheckInput 执行所有输入防护
func (m *GuardrailManager) CheckInput(input string) (string, *GuardrailResult) {
	for _, g := range m.inputGuardrails {
		result := g.Check(input)
		switch result.Action {
		case ActionBlock:
			m.logger("🛑 输入被阻止: %s", result.Reason)
			return "", result
		case ActionMask:
			m.logger("🔄 输入已屏蔽: %s", result.Reason)
			input = result.MaskedText
		case ActionWarn:
			m.logger("⚠️  输入警告: %s", result.Reason)
		default:
			// allow
		}
	}
	return input, &GuardrailResult{Action: ActionAllow}
}

// CheckOutput 执行所有输出防护
func (m *GuardrailManager) CheckOutput(output string) (string, *GuardrailResult) {
	for _, g := range m.outputGuardrails {
		result := g.Check(output)
		switch result.Action {
		case ActionBlock:
			m.logger("🛑 输出被阻止: %s", result.Reason)
			output = fmt.Sprintf("由于安全策略，该回答已被过滤。[原因: %s]", result.Reason)
			return output, result
		case ActionMask:
			m.logger("🔄 输出已屏蔽: %s", result.Reason)
			output = result.MaskedText
		case ActionWarn:
			m.logger("⚠️  输出警告: %s", result.Reason)
		default:
			// allow
		}
	}
	return output, &GuardrailResult{Action: ActionAllow}
}

// ==================== 速率限制器 ====================

type rateLimiter struct {
	mu       sync.Mutex
	maxCalls int
	window   time.Duration
	calls    []time.Time
}

func (r *rateLimiter) allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	// 清理过期记录
	cutoff := now.Add(-r.window)
	valid := make([]time.Time, 0)
	for _, t := range r.calls {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	r.calls = valid

	if len(r.calls) >= r.maxCalls {
		return false
	}
	r.calls = append(r.calls, now)
	return true
}

// ==================== 工具风险等级预设 ====================

// SetupDefaultToolRisks 设置默认工具风险等级
func SetupDefaultToolRisks(guardrail *ToolGuardrail) {
	// 低风险 — 纯查询类
	guardrail.SetRisk("get_current_datetime", ToolRiskLow)
	guardrail.SetRisk("rag_search", ToolRiskLow)
	guardrail.SetRisk("web_search", ToolRiskLow)

	// 中风险 — 可能有副作用
	guardrail.SetRisk("http_request", ToolRiskMedium)
	guardrail.SetRisk("file_read", ToolRiskMedium)
	guardrail.SetRisk("calculator", ToolRiskMedium)

	// 高风险 — 对外部系统有实际影响
	guardrail.SetRisk("social_publish", ToolRiskHigh)
	guardrail.SetRisk("playwright_browser", ToolRiskMedium) // MCP 浏览器工具
}
