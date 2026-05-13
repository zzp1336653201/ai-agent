package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OllamaProvider Ollama 本地模型实现
type OllamaProvider struct {
	endpoint string
	client   *http.Client
}

// OllamaGenerateRequest Ollama 生成请求
type ollamaGenerateRequest struct {
	Model     string `json:"model"`
	Prompt    string `json:"prompt"`
	System    string `json:"system,omitempty"`
	Stream    bool   `json:"stream"`
	Raw       bool   `json:"raw,omitempty"`
	Format    string `json:"format,omitempty"`
	Tools     []ollamaToolDefinition `json:"tools,omitempty"`
}

type ollamaToolDefinition struct {
	Type     string              `json:"type"` // function
	Function ollamaFunctionDef   `json:"function"`
}

type ollamaFunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// OllamaGenerateResponse Ollama 生成响应（非流式）
type ollamaGenerateResponse struct {
	Response   string            `json:"response"`
	Done       bool              `json:"done"`
	PromptEvalCount int          `json:"prompt_eval_count"`
	EvalCount  int               `json:"eval_count"`
	Message    *ollamaMessage    `json:"message,omitempty"`
}

type ollamaMessage struct {
	Role      string             `json:"role"`
	Content   string             `json:"content"`
	ToolCalls []ollamaToolCall   `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	ID        *ollamaToolCallID  `json:"id,omitempty"`
	Function  *ollamaFuncCall    `json:"function,omitempty"`
}

type ollamaToolCallID struct {
	ID string `json:"id"`
}

type ollamaFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// NewOllamaProvider 创建 Ollama 提供者
func NewOllamaProvider(endpoint string) *OllamaProvider {
	return &OllamaProvider{
		endpoint: endpoint,
		client:   &http.Client{},
	}
}

func (o *OllamaProvider) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	model := req.Model
	if model == "" {
		model = "llama3.2"
	}

	body := ollamaGenerateRequest{
		Model:  model,
		Prompt: req.Prompt,
		System: req.System,
		Stream: false,
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/api/generate", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("调用 Ollama 失败: %w", err)
	}
	defer resp.Body.Close()

	var result ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	response := &GenerateResponse{
		Content:      result.Response,
		FinishReason: "stop",
		TokenUsage: TokenUsage{
			PromptTokens:     result.PromptEvalCount,
			CompletionTokens: result.EvalCount,
			TotalTokens:      result.PromptEvalCount + result.EvalCount,
		},
	}

	return response, nil
}

func (o *OllamaProvider) GenerateStream(ctx context.Context, req *GenerateRequest) (<-chan StreamChunk, error) {
	model := req.Model
	if model == "" {
		model = "llama3.2"
	}

	body := ollamaGenerateRequest{
		Model:  model,
		Prompt: req.Prompt,
		System: req.System,
		Stream: true,
	}

	jsonData, _ := json.Marshal(body)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/api/generate", bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 100)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		for {
			var chunk ollamaGenerateResponse
			if err := decoder.Decode(&chunk); err != nil {
				if err == io.EOF {
					ch <- StreamChunk{Done: true}
				} else {
					ch <- StreamChunk{Content: "", Done: true}
				}
				return
			}
			if chunk.Done {
				ch <- StreamChunk{Done: true}
				return
			}
			ch <- StreamChunk{Content: chunk.Response}
		}
	}()

	return ch, nil
}

func (o *OllamaProvider) Chat(ctx context.Context, messages []*Message) (*ChatResponse, error) {
	model := "llama3.2"

	body := map[string]interface{}{
		"model":    model,
		"messages": convertMessages(messages),
		"stream":   false,
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/api/chat", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Message *ollamaMessage `json:"message"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		EvalCount  int         `json:"eval_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	msg := &Message{Role: "assistant", Content: result.Message.Content}
	if len(result.Message.ToolCalls) > 0 {
		toolCalls := make([]ToolCall, 0, len(result.Message.ToolCalls))
		for _, ollamaTC := range result.Message.ToolCalls {
			tc := ToolCall{}
			if ollamaTC.ID != nil {
				tc.ID = ollamaTC.ID.ID
			}
			if ollamaTC.Function != nil {
				tc.Function = &FunctionCall{
					Name:      ollamaTC.Function.Name,
					Arguments: ollamaTC.Function.Arguments,
				}
			}
			toolCalls = append(toolCalls, tc)
		}
		msg.ToolCalls = toolCalls
	}

	return &ChatResponse{
		Message: msg,
		TokenUsage: TokenUsage{
			PromptTokens:     result.PromptEvalCount,
			CompletionTokens: result.EvalCount,
			TotalTokens:      result.PromptEvalCount + result.EvalCount,
		},
	}, nil
}

func (o *OllamaProvider) ChatStream(ctx context.Context, messages []*Message) (<-chan StreamChunk, error) {
	body := map[string]interface{}{
		"model":    "llama3.2",
		"messages": convertMessages(messages),
		"stream":   true,
	}

	jsonData, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/api/chat", bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 100)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		for {
			var chunk struct {
				Message *struct {
					Content string `json:"content"`
				} `json:"message"`
				Done bool `json:"done"`
			}
			if err := decoder.Decode(&chunk); err != nil || chunk.Done {
				ch <- StreamChunk{Done: true}
				return
			}
			if chunk.Message != nil && chunk.Message.Content != "" {
				ch <- StreamChunk{Content: chunk.Message.Content}
			}
		}
	}()

	return ch, nil
}

func convertMessages(messages []*Message) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		result = append(result, map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		})
	}
	return result
}
