package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// LLMProvider LLM 接口抽象（策略模式，支持多模型切换）
type LLMProvider interface {
	// Generate 生成文本（非流式）
	Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error)
	// GenerateStream 流式生成
	GenerateStream(ctx context.Context, req *GenerateRequest) (<-chan StreamChunk, error)
	// Chat 对话式调用（支持多轮上下文）
	Chat(ctx context.Context, messages []*Message) (*ChatResponse, error)
	// ChatStream 流式对话
	ChatStream(ctx context.Context, messages []*Message) (<-chan StreamChunk, error)
}

// GenerateRequest 统一生成请求
type GenerateRequest struct {
	Prompt      string            `json:"prompt"`
	Model       string            `json:"model"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Temperature float64           `json:"temperature,omitempty"`
	System      string            `json:"system,omitempty"`
	Tools       []ToolDefinition  `json:"tools,omitempty"`
	Messages    []*Message        `json:"messages,omitempty"` // Chat 模式使用
}

// GenerateResponse 统一生成响应
type GenerateResponse struct {
	Content    string         `json:"content"`
	FinishReason string       `json:"finish_reason"`
	TokenUsage TokenUsage     `json:"token_usage"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
}

// Message 对话消息
type Message struct {
	Role       string          `json:"role"` // system|user|assistant|tool
	Content    string          `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// ToolDefinition 工具定义（给 LLM 看的）
type ToolDefinition struct {
	Type     string             `json:"type"` // function
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition 函数定义
type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"` // JSON Schema
}

// ToolCall 工具调用结果
type ToolCall struct {
	ID       string          `json:"id"`
	Function *FunctionCall    `json:"function"`
}

// FunctionCall 具体函数调用
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ChatResponse 对话响应
type ChatResponse struct {
	Message    *Message   `json:"message"`
	TokenUsage TokenUsage `json:"token_usage"`
}

// TokenUsage Token 使用统计
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamChunk 流式响应块
type StreamChunk struct {
	Content    string     `json:"content"`
	Done       bool       `json:"done"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}
