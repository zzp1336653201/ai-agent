package service

import (
	"context"
	"encoding/json"
	"fmt"

	"sirenagent/internal/core"
	"sirenagent/internal/model"
)

// WorkflowService 工作流管理服务
type WorkflowService struct {
	engine *core.WorkflowEngine
	repo   WorkflowRepository
}

type WorkflowRepository interface {
	Create(wf *model.Workflow) error
	GetByID(id string) (*model.Workflow, error)
	List(status string, page, size int) ([]*model.Workflow, int64, error)
	Update(wf *model.Workflow) error
	Delete(id string) error
	GetWorkflowForExecution(ctx context.Context, id string) (*model.Workflow, error)
	SaveExecution(exec *model.WorkflowExecution) error
	UpdateExecution(exec *model.WorkflowExecution) error
	ListExecutions(workflowID string, status string, page, size int) ([]*model.WorkflowExecution, int64, error)
	// 节点/边管理
	SaveNodes(workflowID string, nodes []*model.WorkflowNode) error
	SaveEdges(workflowID string, edges []*model.WorkflowEdge) error
	GetNodes(workflowID string) ([]*model.WorkflowNode, error)
	GetEdges(workflowID string) ([]*model.WorkflowEdge, error)
}

func NewWorkflowService(engine *core.WorkflowEngine, repo WorkflowRepository) *WorkflowService {
	return &WorkflowService{engine: engine, repo: repo}
}

// CreateWorkflowRequest 创建工作流请求
type CreateWorkflowRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Trigger     string `json:"trigger"`     // manual|cron|webhook|event
	Nodes       []NodeDef `json:"nodes"`    // 节点定义
	Edges       []EdgeDef `json:"edges"`    // 边定义
}

type NodeDef struct {
	ID       string                 `json:"id" binding:"required"`
	Type     string                 `json:"type" binding:"required"` // start|end|llm|tool|condition|parallel|http
	Name     string                 `json:"name"`
	Config   map[string]interface{} `json:"config"`
	X        int                    `json:"x"`
	Y        int                    `json:"y"`
}
type EdgeDef struct {
	Source   string `json:"source" binding:"required"`
	Target   string `json:"target" binding:"required"`
	Condition string `json:"condition"`
}

// 创建工作流
func (s *WorkflowService) Create(req *CreateWorkflowRequest) (*model.Workflow, error) {
	wf := &model.Workflow{
		Name:        req.Name,
		Description: req.Description,
		Trigger:     req.Trigger,
		Status:      "active",
	}

	if err := s.repo.Create(wf); err != nil {
		return nil, fmt.Errorf("创建工作流失败: %w", err)
	}

	// 保存节点
	if len(req.Nodes) > 0 {
		nodes := make([]*model.WorkflowNode, 0, len(req.Nodes))
		for i, nd := range req.Nodes {
			configJSON := "{}"
			if nd.Config != nil {
				cfgBytes, err := json.Marshal(nd.Config)
				if err == nil {
					configJSON = string(cfgBytes)
				}
			}
			nodes = append(nodes, &model.WorkflowNode{
				ID:         nd.ID,
				WorkflowID: wf.ID,
				NodeType:   nd.Type,
				Name:       nd.Name,
				Config:     configJSON,
				PositionX:  nd.X,
				PositionY:  nd.Y,
				SortOrder:  i,
			})
		}
		if err := s.repo.SaveNodes(wf.ID, nodes); err != nil {
			return nil, fmt.Errorf("保存工作流节点失败: %w", err)
		}
	}

	// 保存边
	if len(req.Edges) > 0 {
		edges := make([]*model.WorkflowEdge, 0, len(req.Edges))
		for _, ed := range req.Edges {
			edges = append(edges, &model.WorkflowEdge{
				WorkflowID: wf.ID,
				SourceID:   ed.Source,
				TargetID:   ed.Target,
				Condition:  ed.Condition,
			})
		}
		if err := s.repo.SaveEdges(wf.ID, edges); err != nil {
			return nil, fmt.Errorf("保存工作流边失败: %w", err)
		}
	}

	return wf, nil
}

// ExecuteRequest 执行工作流请求
type ExecuteWorkflowRequest struct {
	WorkflowID string                 `json:"workflow_id" binding:"required"`
	Input      map[string]interface{} `json:"input"`
	Async      bool                   `json:"async"` // 是否异步执行
}

// 执行工作流
func (s *WorkflowService) Execute(ctx context.Context, req *ExecuteWorkflowRequest) (*core.WorkflowResult, error) {
	result, err := s.engine.Execute(ctx, req.WorkflowID, req.Input)
	if err != nil {
		return nil, fmt.Errorf("工作流执行失败: %w", err)
	}
	return result, nil
}

// ListWorkflows 列出工作流
func (s *WorkflowService) List(status string, page, size int) ([]*model.Workflow, int64, error) {
	if page == 0 { page = 1 }
	if size == 0 || size > 50 { size = 20 }
	return s.repo.List(status, page, size)
}

// GetWorkflow 获取工作流详情
func (s *WorkflowService) GetWorkflow(id string) (*model.Workflow, error) {
	return s.repo.GetByID(id)
}

// GetExecutions 获取执行记录列表
func (s *WorkflowService) GetExecutions(workflowID, status string, page, size int) ([]*model.WorkflowExecution, int64, error) {
	return s.repo.ListExecutions(workflowID, status, page, size)
}
