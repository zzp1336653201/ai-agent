package repository

import (
	"fmt"

	"sirenagent/internal/model"

	"gorm.io/gorm"
)

// AgentRepo GORM 实现的 Agent 仓库
type AgentRepo struct {
	db *gorm.DB
}

func NewAgentRepo(db *gorm.DB) *AgentRepo {
	return &AgentRepo{db: db}
}

func (r *AgentRepo) Create(agent *model.Agent) error {
	return r.db.Create(agent).Error
}

func (r *AgentRepo) GetByID(id string) (*model.Agent, error) {
	var agent model.Agent
	err := r.db.Where("id = ?", id).First(&agent).Error
	if err != nil {
		return nil, fmt.Errorf("智能体不存在: %w", err)
	}
	return &agent, nil
}

func (r *AgentRepo) List(status string, page, size int) ([]*model.Agent, int64, error) {
	var agents []*model.Agent
	var total int64

	query := r.db.Model(&model.Agent{})
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * size
	if err := query.Offset(offset).Limit(size).Order("created_at DESC").Find(&agents).Error; err != nil {
		return nil, 0, err
	}
	return agents, total, nil
}

func (r *AgentRepo) Update(agent *model.Agent) error {
	return r.db.Save(agent).Error
}

func (r *AgentRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&model.Agent{}).Error
}
