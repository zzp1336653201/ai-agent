package repository

import (
	"sirenagent/internal/model"
)

// ==================== Mock Repository（开发阶段用）====================
// 生产环境应替换为 GORM + PostgreSQL 实现

type MockAgentRepository struct{}

func (r *MockAgentRepository) Create(agent *model.Agent) error { return nil }
func (r *MockAgentRepository) GetByID(id string) (*model.Agent, error) { return &model.Agent{ID: id}, nil }
func (r *MockAgentRepository) List(status string, page, size int) ([]*model.Agent, int64, error) {
	return []*model.Agent{}, 0, nil
}
func (r *MockAgentRepository) Update(agent *model.Agent) error { return nil }
func (r *MockAgentRepository) Delete(id string) error { return nil }

// ============

type MockWorkflowRepository struct{}

func (r *MockWorkflowRepository) Create(wf *model.Workflow) error { return nil }
func (r *MockWorkflowRepository) GetByID(id string) (*model.Workflow, error) {
	return &model.Workflow{ID: id}, nil
}
func (r *MockWorkflowRepository) List(status string, page, size int) ([]*model.Workflow, int64, error) {
	return []*model.Workflow{}, 0, nil
}
func (r *MockWorkflowRepository) Update(wf *model.Workflow) error { return nil }
func (r *MockWorkflowRepository) Delete(id string) error { return nil }
func (r *MockWorkflowRepository) GetWorkflowForExecution(ctx interface{}, id string) (*model.Workflow, error) {
	return &model.Workflow{ID: id}, nil
}
func (r *MockWorkflowRepository) SaveExecution(exec *model.WorkflowExecution) error { return nil }
func (r *MockWorkflowRepository) UpdateExecution(exec *model.WorkflowExecution) error { return nil }
func (r *MockWorkflowRepository) ListExecutions(workflowID, status string, page, size int) ([]*model.WorkflowExecution, int64, error) {
	return []*model.WorkflowExecution{}, 0, nil
}

// ============

type MockDocumentRepository struct{}

func (r *MockDocumentRepository) Create(doc *model.Document) error { return nil }
func (r *MockDocumentRepository) GetByID(id string) (*model.Document, error) {
	return &model.Document{ID: id}, nil
}
func (r *MockDocumentRepository) List(page, size int) ([]*model.Document, int64, error) {
	return []*model.Document{}, 0, nil
}
func (r *MockDocumentRepository) Delete(id string) error { return nil }
