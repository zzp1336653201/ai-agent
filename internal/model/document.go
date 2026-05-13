package model

import (
	"time"

	"github.com/google/uuid"
)

type Document struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Type      string    `json:"type"` // pdf, docx, md, txt
	FilePath  string    `json:"file_path"`
	Metadata  string    `json:"metadata"` // JSON 格式的元数据
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewDocument(title, content, docType, filePath string) *Document {
	return &Document{
		ID:        uuid.New().String(),
		Title:     title,
		Content:   content,
		Type:      docType,
		FilePath:  filePath,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}
