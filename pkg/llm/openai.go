package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OpenAIProvider OpenAI / 豆包 API 兼容实现
type OpenAIProvider struct {
	endpoint string
	apiKey   string
	client   *http.Client
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []*Message      `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	Stream      bool            `json:"stream"`
}

type openAIChatResponse struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage   `json:"usage"`
}

type openAIChoice struct {
	Index   int       `json:"index"`
	Message *Message  `json:"message"`
	FinishReason string `json:"finish_reason"`
	Delta   *struct {
		Content string     `json:"content"`
	} `json:"delta,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// NewOpenAIProvider 创建 OpenAI 兼容提供者
func NewOpenAIProvider(endpoint, apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		endpoint: endpoint,
		apiKey:   apiKey,
		client:   &http.Client{},
	}
}

func (o *OpenAIProvider) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResponse, error) {
	messages := []*Message{
		{Role: "system", Content: req.System},
		{Role: "user", Content: req.Prompt},
	}
	if req.Messages != nil {
		messages = req.Messages
	}

	return o.doChat(ctx, &GenerateRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Messages:    messages,
	}, false)
}

func (o *OpenAIProvider) GenerateStream(ctx context.Context, req *GenerateRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 100)
	resp, err := o.doStreamRequest(ctx, req)
	if err != nil {
		close(ch)
		return ch, err
	}

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		for {
			var streamResp struct {
			 Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content string `json:"content"`
				 } `json:"delta"`
				FinishReason string `json:"finish_reason"`
			 } `json:"choices"`
			}
			if err := decoder.Decode(&streamResp); err != nil || len(streamResp.Choices) == 0 {
				if err == io.EOF || (err == nil && len(streamResp.Choices) == 0) {
					ch <- StreamChunk{Done: true}
				}
				return
			}
			choice := streamResp.Choices[0]
			if choice.FinishReason != "" {
				ch <- StreamChunk{Done: true}
				return
			}
			if choice.Delta.Content != "" {
				ch <- StreamChunk{Content: choice.Delta.Content}
			}
		}
	}()

	return ch, nil
}

func (o *OpenAIProvider) Chat(ctx context.Context, messages []*Message) (*ChatResponse, error) {
	resp, err := o.doChat(ctx, &GenerateRequest{
		Model:    "gpt-3.5-turbo",
		Messages: messages,
	}, false)
	if err != nil {
		return nil, err
	}

	return &ChatResponse{
		Message: &Message{
			Role:       "assistant",
			Content:    resp.Content,
			ToolCalls:  resp.ToolCalls,
		},
		TokenUsage: resp.TokenUsage,
	}, nil
}

func (o *OpenAIProvider) ChatStream(ctx context.Context, messages []*Message) (<-chan StreamChunk, error) {
	req := &GenerateRequest{
		Model:    "gpt-3.5-turbo",
		Messages: messages,
	}
	return o.GenerateStream(ctx, req)
}

// doChat 执行非流式对话请求
func (o *OpenAIProvider) doChat(ctx context.Context, req *GenerateRequest, stream bool) (*GenerateResponse, error) {
	model := req.Model
	if model == "" {
		model = "gpt-3.5-turbo"
	}

	body := openAIChatRequest{
		Model:       model,
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Tools:       req.Tools,
		Stream:      stream,
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	defer resp.Body.Close()

	var result openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("LLM 返回空结果")
	}

	choice := result.Choices[0]
	return &GenerateResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		TokenUsage: TokenUsage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		},
		ToolCalls: choice.Message.ToolCalls,
	}, nil
}

// doStreamRequest 发起流式请求
func (o *OpenAIProvider) doStreamRequest(ctx context.Context, req *GenerateRequest) (*http.Response, error) {
	model := req.Model
	if model == "" {
		model = "gpt-3.5-turbo"
	}

	body := openAIChatRequest{
		Model:       model,
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      true,
	}

	jsonData, _ := json.Marshal(body)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/v1/chat/completions", bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("流式调用失败: %w", err)
	}

	return resp, nil
}
