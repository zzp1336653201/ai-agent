package repository

import (
	"context"
	"fmt"

	"sirenagent/internal/model"

	"gorm.io/gorm"
)

// WorkflowRepo GORM 实现的工作流仓库
// 同时实现 service.WorkflowRepository 和 core.WorkflowStore 两个接口
type WorkflowRepo struct {
	db *gorm.DB
}

func NewWorkflowRepo(db *gorm.DB) *WorkflowRepo {
	return &WorkflowRepo{db: db}
}

// ==================== 工作流 CRUD ====================

func (r *WorkflowRepo) Create(wf *model.Workflow) error {
	return r.db.Create(wf).Error
}

func (r *WorkflowRepo) GetByID(id string) (*model.Workflow, error) {
	var wf model.Workflow
	err := r.db.Where("id = ?", id).First(&wf).Error
	if err != nil {
		return nil, fmt.Errorf("工作流不存在: %w", err)
	}
	return &wf, nil
}

func (r *WorkflowRepo) List(status string, page, size int) ([]*model.Workflow, int64, error) {
	var wfs []*model.Workflow
	var total int64

	query := r.db.Model(&model.Workflow{})
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * size
	if err := query.Offset(offset).Limit(size).Order("created_at DESC").Find(&wfs).Error; err != nil {
		return nil, 0, err
	}
	return wfs, total, nil
}

func (r *WorkflowRepo) Update(wf *model.Workflow) error {
	return r.db.Save(wf).Error
}

func (r *WorkflowRepo) Delete(id string) error {
	// 级联删除节点和边
	r.db.Where("workflow_id = ?", id).Delete(&model.WorkflowNode{})
	r.db.Where("workflow_id = ?", id).Delete(&model.WorkflowEdge{})
	return r.db.Where("id = ?", id).Delete(&model.Workflow{}).Error
}

// GetWorkflowForExecution 加载工作流（含节点和边）用于执行
func (r *WorkflowRepo) GetWorkflowForExecution(ctx context.Context, id string) (*model.Workflow, error) {
	var wf model.Workflow
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&wf).Error
	if err != nil {
		return nil, fmt.Errorf("工作流不存在: %w", err)
	}
	return &wf, nil
}

// ==================== WorkflowStore 接口实现 ====================

func (r *WorkflowRepo) GetWorkflow(ctx context.Context, id string) (*model.Workflow, error) {
	return r.GetWorkflowForExecution(ctx, id)
}

func (r *WorkflowRepo) SaveExecution(exec *model.WorkflowExecution) error {
	return r.db.Create(exec).Error
}

func (r *WorkflowRepo) UpdateExecution(exec *model.WorkflowExecution) error {
	return r.db.Save(exec).Error
}

// ==================== 执行记录查询 ====================

func (r *WorkflowRepo) ListExecutions(workflowID string, status string, page, size int) ([]*model.WorkflowExecution, int64, error) {
	var execs []*model.WorkflowExecution
	var total int64

	query := r.db.Model(&model.WorkflowExecution{}).Where("workflow_id = ?", workflowID)
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * size
	if err := query.Offset(offset).Limit(size).Order("started_at DESC").Find(&execs).Error; err != nil {
		return nil, 0, err
	}
	return execs, total, nil
}

// ==================== 工作流节点/边管理 ====================

// SaveNodes 批量保存工作流节点（先删除旧节点再批量插入）
func (r *WorkflowRepo) SaveNodes(workflowID string, nodes []*model.WorkflowNode) error {
	if len(nodes) == 0 {
		return nil
	}
	// 先删后插，保持幂等
	r.db.Where("workflow_id = ?", workflowID).Delete(&model.WorkflowNode{})
	return r.db.CreateInBatches(nodes, 50).Error
}

// SaveEdges 批量保存工作流边（先删除旧边再批量插入）
func (r *WorkflowRepo) SaveEdges(workflowID string, edges []*model.WorkflowEdge) error {
	if len(edges) == 0 {
		return nil
	}
	r.db.Where("workflow_id = ?", workflowID).Delete(&model.WorkflowEdge{})
	return r.db.CreateInBatches(edges, 50).Error
}

// GetNodes 获取工作流的所有节点
func (r *WorkflowRepo) GetNodes(workflowID string) ([]*model.WorkflowNode, error) {
	var nodes []*model.WorkflowNode
	err := r.db.Where("workflow_id = ?", workflowID).Order("sort_order ASC").Find(&nodes).Error
	return nodes, err
}

// GetEdges 获取工作流的所有边
func (r *WorkflowRepo) GetEdges(workflowID string) ([]*model.WorkflowEdge, error) {
	var edges []*model.WorkflowEdge
	err := r.db.Where("workflow_id = ?", workflowID).Find(&edges).Error
	return edges, err
}
