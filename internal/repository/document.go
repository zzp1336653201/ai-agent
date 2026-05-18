package repository

import (
	"fmt"

	"sirenagent/internal/model"

	"gorm.io/gorm"
)

// DocumentRepo GORM 实现的文档仓库
type DocumentRepo struct {
	db *gorm.DB
}

func NewDocumentRepo(db *gorm.DB) *DocumentRepo {
	return &DocumentRepo{db: db}
}

func (r *DocumentRepo) Create(doc *model.Document) error {
	return r.db.Create(doc).Error
}

func (r *DocumentRepo) GetByID(id string) (*model.Document, error) {
	var doc model.Document
	err := r.db.Where("id = ?", id).First(&doc).Error
	if err != nil {
		return nil, fmt.Errorf("文档不存在: %w", err)
	}
	return &doc, nil
}

func (r *DocumentRepo) List(page, size int) ([]*model.Document, int64, error) {
	var docs []*model.Document
	var total int64

	if err := r.db.Model(&model.Document{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * size
	if err := r.db.Offset(offset).Limit(size).Order("created_at DESC").Find(&docs).Error; err != nil {
		return nil, 0, err
	}
	return docs, total, nil
}

func (r *DocumentRepo) ListByAgent(agentID string, page, size int) ([]*model.Document, int64, error) {
	var docs []*model.Document
	var total int64

	query := r.db.Model(&model.Document{}).Where("agent_id = ?", agentID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * size
	if err := query.Offset(offset).Limit(size).Order("created_at DESC").Find(&docs).Error; err != nil {
		return nil, 0, err
	}
	return docs, total, nil
}

func (r *DocumentRepo) ListByCategory(category string, page, size int) ([]*model.Document, int64, error) {
	var docs []*model.Document
	var total int64

	query := r.db.Model(&model.Document{}).Where("category = ?", category)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * size
	if err := query.Offset(offset).Limit(size).Order("created_at DESC").Find(&docs).Error; err != nil {
		return nil, 0, err
	}
	return docs, total, nil
}

func (r *DocumentRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&model.Document{}).Error
}
