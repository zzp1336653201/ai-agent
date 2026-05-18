package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"sirenagent/internal/model"
	"sirenagent/pkg/llm"
)

// ==================== 工作流引擎 ====================
// 核心能力：可视化编排、条件分支、并行执行、定时触发
// 对应职位要求：工作流工具配置到自动化流程系统的搭建

// WorkflowEngine 工作流引擎
type WorkflowEngine struct {
	agentEngine *AgentEngine
	store       WorkflowStore
	executions  map[string]*WorkflowExecution // 运行中的执行
	mu          sync.RWMutex
}

type WorkflowStore interface {
	GetWorkflow(ctx context.Context, id string) (*model.Workflow, error)
	SaveExecution(exec *model.WorkflowExecution) error
	UpdateExecution(exec *model.WorkflowExecution) error
	// 节点/边加载（由 WorkflowEngine 在执行时调用）
	GetNodes(workflowID string) ([]*model.WorkflowNode, error)
	GetEdges(workflowID string) ([]*model.WorkflowEdge, error)
}

func NewWorkflowEngine(agentEngine *AgentEngine, store WorkflowStore) *WorkflowEngine {
	return &WorkflowEngine{
		agentEngine: agentEngine,
		store:       store,
		executions:  make(map[string]*WorkflowExecution),
	}
}

// Execute 执行工作流 — 核心调度逻辑
func (e *WorkflowEngine) Execute(ctx context.Context, workflowID string, input map[string]interface{}) (*WorkflowResult, error) {
	// 1. 加载工作流定义
	workflow, err := e.store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("加载工作流失败: %w", err)
	}
	if workflow.Status != "active" {
		return nil, fmt.Errorf("工作流 %s 状态非活跃", workflow.Name)
	}

	// 2. 加载节点和边
	nodes, err := e.store.GetNodes(workflowID)
	if err != nil {
		return nil, fmt.Errorf("加载工作流节点失败: %w", err)
	}
	edges, err := e.store.GetEdges(workflowID)
	if err != nil {
		return nil, fmt.Errorf("加载工作流边失败: %w", err)
	}

	// 3. 创建执行记录
	exec := NewWorkflowExecution(workflowID)
	if err = e.store.SaveExecution(exec.model); err != nil {
		return nil, err
	}

	e.mu.Lock()
	e.executions[exec.ID] = exec
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		delete(e.executions, exec.ID)
		e.mu.Unlock()
	}()

	// 4. 构建执行图
	graph := BuildExecutionGraph(workflow, nodes, edges)

	// 5. 从 Start 节点开始执行
	result := e.executeGraph(ctx, exec, graph, input)

	return result, nil
}

// executeGraph 图遍历执行
func (e *WorkflowEngine) executeGraph(ctx context.Context, exec *WorkflowExecution, graph *ExecutionGraph, input map[string]interface{}) *WorkflowResult {
	currentNodes := graph.GetStartNodes()
	variables := make(map[string]interface{})
	for k, v := range input {
		variables[k] = v }
	visited := make(map[string]bool)
	maxSteps := 50 // 防止死循环

	for step := 0; step < maxSteps && len(currentNodes) > 0; step++ {
		var nextNodeIDs []string

		for _, node := range currentNodes {
			if visited[node.ID] {
				continue
			}
			visited[node.ID] = true

			nodeResult := e.executeNode(ctx, node, variables)
			if nodeResult.Error != nil {
				return &WorkflowResult{
					Status:   "failed",
					ErrorMsg: fmt.Sprintf("节点[%s]执行失败: %v", node.Name, nodeResult.Error),
					Output:   variables,
				}
			}

			// 将节点输出合并到变量中
			for k, v := range nodeResult.Output {
				variables[k] = v
			}

			// 记录节点执行结果
			exec.AddStep(node.ID, node.NodeType, nodeResult.Output, nil)

			// 检查是否到达结束节点
			if node.NodeType == "end" {
				return &WorkflowResult{
					Status: "success",
					Output: variables,
				}
			}

			// 获取下一个节点
			nextIDs := graph.GetNextNodes(node.ID, variables)
			nextNodeIDs = append(nextNodeIDs, nextIDs...)
		}

		currentNodes = graph.GetNodesByIDs(nextNodeIDs)
	}

	return &WorkflowResult{
		Status:   "success",
		Output:   variables,
		ErrorMsg: "",
	}
}

// executeNode 执行单个节点
func (e *WorkflowEngine) executeNode(ctx context.Context, node *GraphNode, variables map[string]interface{}) *NodeResult {
	switch node.NodeType {
	case "start":
		return &NodeResult{Output: variables}
	case "end":
		return &NodeResult{Output: variables}
	case "llm":
		return e.executeLLMNode(ctx, node, variables)
	case "tool":
		return e.executeToolNode(ctx, node, variables)
	case "condition":
		return e.executeConditionNode(node, variables)
	case "http":
		return e.executeHTTPNode(ctx, node, variables)
	case "parallel":
		return e.executeParallelNode(ctx, node, variables)
	default:
		return &NodeResult{
			Output: variables,
			Error:  fmt.Errorf("未知节点类型: %s", node.NodeType),
		}
	}
}

// executeLLMNode LLM 节点 - 调用 AI 模型生成内容
// 这是 Agent 应用的核心：让工作流具备智能决策能力
func (e *WorkflowEngine) executeLLMNode(ctx context.Context, node *GraphNode, variables map[string]interface{}) *NodeResult {
	config := node.Config

	promptTemplate, _ := config["prompt_template"].(string)
	modelName, _ := config["model"].(string)
	systemPrompt, _ := config["system"].(string)
	outputKey, _ := config["output_key"].(string)
	if outputKey == "" {
		outputKey = "llm_output"
	}

	// 渲染 Prompt 模板（变量替换）
	prompt := renderTemplate(promptTemplate, variables)

	req := &llm.GenerateRequest{
		Prompt:      prompt,
		Model:       modelName,
		System:      systemPrompt,
		Temperature: getFloatDefault(config["temperature"], 0.7),
		MaxTokens:   getIntDefault(config["max_tokens"], 2048),
	}

	resp, err := e.agentEngine.llm.Generate(ctx, req)
	if err != nil {
		return &NodeResult{Error: fmt.Errorf("LLM 调用失败: %w", err)}
	}

	return &NodeResult{
		Output: map[string]interface{}{outputKey: resp.Content},
	}
}

// executeToolNode 工具调用节点 - 让工作流能调用外部系统
func (e *WorkflowEngine) executeToolNode(ctx context.Context, node *GraphNode, variables map[string]interface{}) *NodeResult {
	config := node.Config

	toolName, _ := config["tool_name"].(string)
	paramsRaw, _ := config["params"].(map[string]interface{})
	outputKey, _ := config["output_key"].(string)
	if outputKey == "" {
		outputKey = "tool_output"
	}

	// 渲染参数模板
	params := make(map[string]interface{})
	for k, v := range paramsRaw {
		if s, ok := v.(string); ok {
			params[k] = renderTemplate(s, variables)
		} else {
			params[k] = v
		}
	}

	tool, exists := e.agentEngine.GetTool(toolName)
	if !exists {
		return &NodeResult{Error: fmt.Errorf("工具 %s 未注册", toolName)}
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		return &NodeResult{Error: err}
	}

	output := map[string]interface{}{}
	if result.Data != nil {
		for k, v := range result.Data {
			output[k] = v
		}
	}
	output[outputKey] = result.Content

	return &NodeResult{Output: output}
}

// executeConditionNode 条件判断节点 - 支持工作流分支
func (e *WorkflowEngine) executeConditionNode(node *GraphNode, variables map[string]interface{}) *NodeResult {
	config := node.Config
	conditionExpr, _ := config["condition"].(string)

	// 简单的条件求值（生产环境可用 govaluate 库）
	result := evaluateCondition(conditionExpr, variables)
	outputKey, _ := config["output_key"].(string)
	if outputKey == "" {
		outputKey = "_condition_result"
	}

	return &NodeResult{
		Output: map[string]interface{}{
			outputKey: result,
		},
	}
}

// executeHTTPNode HTTP 调用节点 - 对接外部 API
func (e *WorkflowEngine) executeHTTPNode(ctx context.Context, node *GraphNode, variables map[string]interface{}) *NodeResult {
	config := node.Config
	url, _ := config["url"].(string)
	method, _ := config["method"].(string)
	_, _ = config["headers"].(map[string]interface{})
	bodyTemplate, _ := config["body"].(string)
	outputKey, _ := config["output_key"].(string)
	if outputKey == "" {
		outputKey = "http_output"
	}

	renderedURL := renderTemplate(url, variables)
	_ = renderTemplate(bodyTemplate, variables)

	// TODO: 实际 HTTP 调用，这里简化返回模拟数据
	output := map[string]interface{}{
		outputKey: map[string]interface{}{
			"url":    renderedURL,
			"status": 200,
			"body":   fmt.Sprintf("模拟响应: %s %s", method, renderedURL),
		},
	}

	return &NodeResult{Output: output}
}

// executeParallelNode 并行执行节点 - 多任务并发
func (e *WorkflowEngine) executeParallelNode(ctx context.Context, node *GraphNode, variables map[string]interface{}) *NodeResult {
	subNodes, _ := node.Config["nodes"].([]interface{})

	var wg sync.WaitGroup
	results := make([]map[string]interface{}, len(subNodes))
	errors := make([]error, len(subNodes))

	for i, subNodeRaw := range subNodes {
		wg.Add(1)
		go func(idx int, subNodeRaw interface{}) {
			defer wg.Done()
			subConfig, _ := subNodeRaw.(map[string]interface{})
			subNode := &GraphNode{
				ID:        fmt.Sprintf("parallel_%d", idx),
				NodeType:  subConfig["type"].(string),
				Config:    subConfig,
			}
			res := e.executeNode(ctx, subNode, variables)
			results[idx] = res.Output
			errors[idx] = res.Error
		}(i, subNodeRaw)
	}

	wg.Wait()

	output := map[string]interface{}{"parallel_results": results}
	for i, err := range errors {
		if err != nil {
			output[fmt.Sprintf("error_%d", i)] = err.Error()
		}
	}

	return &NodeResult{Output: output}
}

// ==================== 数据结构 ====================

// ExecutionGraph 执行图
type ExecutionGraph struct {
	nodes    map[string]*GraphNode
	edges    []*GraphEdge
	startIDs []string
	endIDs   []string
}

// GraphNode 图节点
type GraphNode struct {
	ID       string                 `json:"id"`
	NodeType string                 `json:"node_type"` // start|end|llm|tool|condition|parallel|http
	Name     string                 `json:"name"`
	Config   map[string]interface{} `json:"config"`
}

// GraphEdge 图边
type GraphEdge struct {
	SourceID  string `json:"source_id"`
	TargetID  string `json:"target_id"`
	Condition string `json:"condition"`
}

// NodeResult 节点执行结果
type NodeResult struct {
	Output map[string]interface{} `json:"output"`
	Error  error                  `json:"error"`
}

// WorkflowResult 工作流执行结果
type WorkflowResult struct {
	Status   string                   `json:"status"`
	Output   map[string]interface{}   `json:"output"`
	ErrorMsg string                   `json:"error_msg"`
}

func (r *WorkflowResult) OutputJSON() string {
	data, _ := json.Marshal(r.Output)
	return string(data)
}

// WorkflowExecution 工作流执行上下文
type WorkflowExecution struct {
	ID     string
	model  *model.WorkflowExecution
	steps  []ExecutionStep
}

type ExecutionStep struct {
	NodeID   string                 `json:"node_id"`
	NodeType string                 `json:"node_type"`
	Input    map[string]interface{} `json:"input"`
	Output   map[string]interface{} `json:"output"`
	Error    string                 `json:"error,omitempty"`
	StartAt  time.Time              `json:"start_at"`
	EndAt    time.Time              `json:"end_at"`
}

func NewWorkflowExecution(workflowID string) *WorkflowExecution {
	return &WorkflowExecution{
		ID:    uuid.New().String(),
		model: &model.WorkflowExecution{
			WorkflowID: workflowID,
			Status:     "running",
			StartedAt:  time.Now(),
		},
		steps: make([]ExecutionStep, 0),
	}
}

func (e *WorkflowExecution) AddStep(nodeID, nodeType string, output map[string]interface{}, err error) {
	step := ExecutionStep{
		NodeID:   nodeID,
		NodeType: nodeType,
		Output:   output,
		StartAt:  time.Now(),
		EndAt:    time.Now(),
	}
	if err != nil {
		step.Error = err.Error()
	}
	e.steps = append(e.steps, step)
}

// ==================== 图构建 ====================

// BuildExecutionGraph 从 Workflow 模型和节点/边数据构建执行图
func BuildExecutionGraph(wf *model.Workflow, nodes []*model.WorkflowNode, edges []*model.WorkflowEdge) *ExecutionGraph {
	graph := &ExecutionGraph{
		nodes: make(map[string]*GraphNode),
		edges: make([]*GraphEdge, 0),
	}

	for _, n := range nodes {
		config := make(map[string]interface{})
		if n.Config != "" {
			json.Unmarshal([]byte(n.Config), &config)
		}
		graph.nodes[n.ID] = &GraphNode{
			ID:       n.ID,
			NodeType: n.NodeType,
			Name:     n.Name,
			Config:   config,
		}
		if n.NodeType == "start" {
			graph.startIDs = append(graph.startIDs, n.ID)
		}
		if n.NodeType == "end" {
			graph.endIDs = append(graph.endIDs, n.ID)
		}
	}

	for _, e := range edges {
		graph.edges = append(graph.edges, &GraphEdge{
			SourceID:  e.SourceID,
			TargetID:  e.TargetID,
			Condition: e.Condition,
		})
	}

	return graph
}

func (g *ExecutionGraph) GetStartNodes() []*GraphNode {
	if g.startIDs == nil || len(g.startIDs) == 0 {
		// 默认返回所有 start 类型节点
		var starts []*GraphNode
		for _, n := range g.nodes {
			if n.NodeType == "start" {
				starts = append(starts, n)
			}
		}
		return starts
	}
	return g.GetNodesByIDs(g.startIDs)
}

func (g *ExecutionGraph) GetNextNodes(sourceID string, variables map[string]interface{}) []string {
	var next []string
	for _, edge := range g.edges {
		if edge.SourceID == sourceID {
			if edge.Condition == "" || evaluateCondition(edge.Condition, variables) {
				next = append(next, edge.TargetID)
			}
		}
	}
	return next
}

func (g *ExecutionGraph) GetNodesByIDs(ids []string) []*GraphNode {
	result := make([]*GraphNode, 0, len(ids))
	for _, id := range ids {
		if n, ok := g.nodes[id]; ok {
			result = append(result, n)
		}
	}
	return result
}

// ==================== 辅助函数 ====================

func renderTemplate(template string, vars map[string]interface{}) string {
	result := template
	for key, val := range vars {
		placeholder := fmt.Sprintf("${%s}", key)
		if s, ok := val.(string); ok {
			result = strings.ReplaceAll(result, placeholder, s)
		} else if b, err := json.Marshal(val); err == nil {
			result = strings.ReplaceAll(result, placeholder, string(b))
		}
	}
	return result
}

func evaluateCondition(expr string, variables map[string]interface{}) bool {
	// 简化版条件求值：检查变量是否存在且为 truthy
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true
	}
	val, exists := variables[expr]
	return exists && val != nil && val != false && val != "" && val != 0
}

func getFloatDefault(v interface{}, def float64) float64 {
	if f, ok := v.(float64); ok { return f }
	return def
}

func getIntDefault(v interface{}, def int) int {
	switch v.(type) {
	case float64:
		return int(v.(float64))
	case int:
		return v.(int)
	}
	return def
}


