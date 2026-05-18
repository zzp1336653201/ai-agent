package repository

import (
	"fmt"

	"sirenagent/internal/model"

	"gorm.io/gorm"
)

// AutoMigrate 自动迁移所有模型到数据库
func AutoMigrate(db *gorm.DB) error {
	err := db.AutoMigrate(
		&model.Agent{},
		&model.Conversation{},
		&model.Message{},
		&model.Memory{},
		&model.Document{},
		&model.Workflow{},
		&model.WorkflowNode{},
		&model.WorkflowEdge{},
		&model.WorkflowExecution{},
		&model.SocialAccount{},
		&model.SocialPost{},
		&model.PromptTemplate{},
		&model.ToolDefinition{},
	)
	if err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}
	fmt.Println("[Migration] 数据库表迁移完成")
	return nil
}
