package main

import (
	"context"

	"sirenagent/internal/model"
	"sirenagent/internal/service"
)

// MockAgentRepository Mock 智能体仓库
type MockAgentRepository struct{}

func (r *MockAgentRepository) Create(agent *model.Agent) error           { return nil }
func (r *MockAgentRepository) GetByID(id string) (*model.Agent, error)   { return nil, nil }
func (r *MockAgentRepository) List(status string, page, size int) ([]*model.Agent, int64, error) {
	return []*model.Agent{}, 0, nil
}
func (r *MockAgentRepository) Update(agent *model.Agent) error           { return nil }
func (r *MockAgentRepository) Delete(id string) error                  { return nil }

// MockWorkflowRepository Mock 工作流仓库
type MockWorkflowRepository struct{}

func (r *MockWorkflowRepository) Create(w *model.Workflow) error                          { return nil }
func (r *MockWorkflowRepository) GetByID(id string) (*model.Workflow, error)               { return nil, nil }
func (r *MockWorkflowRepository) List(status string, page, size int) ([]*model.Workflow, int64, error) {
	return []*model.Workflow{}, 0, nil
}
func (r *MockWorkflowRepository) Update(w *model.Workflow) error                          { return nil }
func (r *MockWorkflowRepository) Delete(id string) error                                 { return nil }
func (r *MockWorkflowRepository) GetWorkflowForExecution(ctx context.Context, id string) (*model.Workflow, error) { return nil, nil }
func (r *MockWorkflowRepository) SaveExecution(exec *model.WorkflowExecution) error       { return nil }
func (r *MockWorkflowRepository) GetExecution(id string) (*model.WorkflowExecution, error) { return nil, nil }
func (r *MockWorkflowRepository) ListExecutions(workflowID string, status string, page, size int) ([]*model.WorkflowExecution, int64, error) {
	return []*model.WorkflowExecution{}, 0, nil
}

// MockDocumentRepository Mock 文档仓库
type MockDocumentRepository struct{}

func (r *MockDocumentRepository) Create(doc *model.Document) error             { return nil }
func (r *MockDocumentRepository) GetByID(id string) (*model.Document, error)   { return nil, nil }
func (r *MockDocumentRepository) List(page, size int) ([]*model.Document, int64, error) {
	return []*model.Document{}, 0, nil
}
func (r *MockDocumentRepository) Delete(id string) error                    { return nil }

// 确保实现了 service 接口
var _ service.AgentRepository = (*MockAgentRepository)(nil)
var _ service.WorkflowRepository = (*MockWorkflowRepository)(nil)
var _ service.DocumentRepository = (*MockDocumentRepository)(nil)
