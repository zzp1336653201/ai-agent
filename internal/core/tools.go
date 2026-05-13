package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ==================== 内置工具集 ====================
// 这些工具展示了 Agent 能力边界，面试时重点讲解

// WebSearchTool 网络搜索工具
type WebSearchTool struct {
	client *http.Client
}

func NewWebSearchTool() *WebSearchTool {
	return &WebSearchTool{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *WebSearchTool) Name() string { return "web_search" }
func (t *WebSearchTool) Description() string { return "在互联网上搜索信息，获取实时数据、新闻、知识等。当用户的问题需要最新信息或你不确定答案时使用此工具。" }
func (t *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "搜索关键词",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "返回结果数量，默认5",
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	query, _ := params["query"].(string)
	_ = 5 // 默认搜索数量
	if c, ok := params["count"].(float64); ok {
		_ = int(c)
	}

	// 调用 DuckDuckGo Instant Answer API（免费无需 API Key）
	searchURL := fmt.Sprintf(
		"https://api.duckduckgo.com/?q=%s&format=json&no_html=1",
		url.QueryEscape(query),
	)
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Abstract     string `json:"Abstract"`
		AbstractText string `json:"AbstractText"`
		Heading      string `json:"Heading"`
	}
	json.Unmarshal(body, &result)

	content := result.AbstractText
	if content == "" {
		content = result.Abstract
	}
	if content == "" {
		content = fmt.Sprintf("未找到关于「%s」的搜索结果", query)
	} else if result.Heading != "" {
		content = fmt.Sprintf("[%s]\n%s", result.Heading, content)
	}

	return NewToolResult(content), nil
}

// ==================== RAG 知识库检索工具 ====================

// RAGSearchTool RAG 检索工具 - 让 Agent 能查询知识库
type RAGSearchTool struct {
	engine *AgentEngine
}

func NewRAGSearchTool(engine *AgentEngine) *RAGSearchTool {
	return &RAGSearchTool{engine: engine}
}

func (t *RAGSearchTool) Name() string        { return "rag_search" }
func (t *RAGSearchTool) Description() string {
	return "在企业/项目知识库中检索相关文档片段。当用户询问产品信息、技术文档、FAQ等问题时使用。"
}
func (t *RAGSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "检索问题或关键词",
			},
			"top_k": map[string]interface{}{
				"type":        "integer",
				"description": "返回最相关的文档数量，默认3",
			},
		},
		"required": []string{"query"},
	}
}

func (t *RAGSearchTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	query, _ := params["query"].(string)
	topK := 3
	if k, ok := params["top_k"].(float64); ok {
		topK = int(k)
	}

	results, err := t.engine.vectorDB.Search(ctx, query, topK, "agent_knowledge")
	if err != nil {
		return nil, fmt.Errorf("向量检索失败: %w", err)
	}

	var parts []string
	for i, r := range results {
		part := fmt.Sprintf("%d. [相似度:%.2f] %s", i+1, r.Score, r.Content)
		parts = append(parts, part)
	}

	content := strings.Join(parts, "\n\n")
	if len(results) == 0 {
		content = "知识库中未找到相关内容"
	}

	return NewToolResult(content), nil
}

// ==================== 社交媒体发布工具 ====================

// SocialPublishTool 社交媒体内容发布工具
type SocialPublishTool struct {
	socialService SocialMediaService // 接口，实际对接各平台
}

func NewSocialPublishTool(svc SocialMediaService) *SocialPublishTool {
	return &SocialPublishTool{socialService: svc}
}

func (t *SocialPublishTool) Name() string        { return "social_publish" }
func (t *SocialPublishTool) Description() string {
	return "将生成的内容发布到社交媒体平台（抖音/小红书/视频号）。参数包括平台类型、标题、内容和标签。"
}
func (t *SocialPublishTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"platform": map[string]interface{}{
				"type":        "string",
				"description": "目标平台: douyin | xiaohongshu | video_channel",
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "帖子标题（可选）",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "帖子正文内容",
			},
			"tags": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
				"description": "话题标签列表",
			},
		},
		"required": []string{"platform", "content"},
	}
}

func (t *SocialPublishTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	platform, _ := params["platform"].(string)
	content, _ := params["content"].(string)
	title, _ := params["title"].(string)

	tags := make([]string, 0)
	if rawTags, ok := params["tags"].([]interface{}); ok {
		for _, tag := range rawTags {
			if s, ok := tag.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	req := &PublishRequest{
		Platform: platform,
		Title:    title,
		Content:  content,
		Tags:     tags,
	}

	postID, err := t.socialService.Publish(ctx, req)
	if err != nil {
		return NewToolError(err), nil
	}

	return NewToolResult(fmt.Sprintf("发布成功！平台: %s, 帖子ID: %s", platform, postID)), nil
}

// ==================== HTTP 请求工具 ====================

// HTTPRequestTool 通用 HTTP 请求工具 — Agent 对接任意 Web API
type HTTPRequestTool struct {
	client *http.Client
}

func NewHTTPRequestTool() *HTTPRequestTool {
	return &HTTPRequestTool{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *HTTPRequestTool) Name() string        { return "http_request" }
func (t *HTTPRequestTool) Description() string {
	return "发送 HTTP 请求到指定 URL。用于调用外部 RESTful API 或获取网页数据。支持 GET/POST 方法。"
}
func (t *HTTPRequestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "请求 URL",
			},
			"method": map[string]interface{}{
				"type":        "string",
				"description": "HTTP 方法: GET | POST | PUT | DELETE",
			},
			"headers": map[string]interface{}{
				"type":        "object",
				"description": "请求头键值对",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "请求体（POST 时使用）",
			},
		},
		"required": []string{"url"},
	}
}

func (t *HTTPRequestTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	rawURL, _ := params["url"].(string)
	method, _ := params["method"].(string)
	if method == "" {
		method = "GET"
	}
	method = strings.ToUpper(method)

	headers := make(map[string]string)
	if h, ok := params["headers"].(map[string]interface{}); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
	}

	var bodyReader io.Reader
	if bodyStr, ok := params["body"].(string); ok && method != "GET" {
		bodyReader = strings.NewReader(bodyStr)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	result := map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(respBody),
	}
	resultJSON, _ := json.Marshal(result)

	return NewToolResult(string(resultJSON)), nil
}

// ==================== 计算器工具 ====================

// CalculatorTool 数学计算工具 — Agent 进行数值计算
type CalculatorTool struct{}

func NewCalculatorTool() *CalculatorTool { return &CalculatorTool{} }
func (t *CalculatorTool) Name() string   { return "calculator" }
func (t *CalculatorTool) Description() string {
	return "进行数学计算、数据统计、格式转换等操作。当需要精确计算数字时使用。"
}
func (t *CalculatorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"expression": map[string]interface{}{
				"type":        "string",
				"description": "数学表达式或计算指令，如 '1+2*3' 或 '计算 15% of 200'",
			},
		},
		"required": []string{"expression"},
	}
}

// CalculatorTool 的 Execute 实现省略，实际可接入 govaluate 库做表达式求值
func (t *CalculatorTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	return &ToolResult{
		Content: "计算功能待实现，请接入 govaluate 库",
	}, nil
}

// ==================== 文件读写工具 ====================

// FileReadTool 文件读取工具 — Agent 处理本地文件
type FileReadTool struct{}

func NewFileReadTool() *FileReadTool { return &FileReadTool{} }
func (t *FileReadTool) Name() string   { return "file_read" }
func (t *FileReadTool) Description() string {
	return "读取本地文件的内容。用于处理配置文件、日志文件等。"
}
func (t *FileReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "文件路径",
			},
			"encoding": map[string]interface{}{
				"type":        "string",
				"description": "文件编码，默认 utf-8",
			},
		},
		"required": []string{"path"},
	}
}

func (t *FileReadTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	return &ToolResult{
		Content: "文件读取功能待实现，请接入 os 包实现",
	}, nil
}

// ==================== 网络搜索工具 ====================

// WebSearchTool 网络搜索工具 — Agent 联网获取实时信息
type WebSearchTool struct {
	httpClient interface{} // 可替换为具体 HTTP 客户端
}

func NewWebSearchTool() *WebSearchTool { return &WebSearchTool{} }
func (t *WebSearchTool) Name() string   { return "web_search" }
func (t *WebSearchTool) Description() string {
	return "联网搜索互联网信息，获取实时数据、新闻、天气、时间等。当用户询问需要最新信息时使用此工具。"
}
func (t *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "搜索关键词或问题",
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	query, _ := params["query"].(string)
	if query == "" {
		query = "current time"
	}

	// 使用 DuckDuckGo 免费搜索 API
	searchURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1", url.QueryEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(searchURL)
	if err != nil {
		return &ToolResult{Content: fmt.Sprintf("搜索失败: %v", err)}, nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Abstract       string `json:"Abstract"`
		AbstractText   string `json:"AbstractText"`
		AbstractSource string `json:"AbstractSource"`
		AbstractURL    string `json:"AbstractURL"`
		Heading         string `json:"Heading"`
	}
	json.Unmarshal(body, &result)

	if result.Abstract != "" {
		content := result.Abstract
		if result.AbstractURL != "" {
			content += "\n\n来源: " + result.AbstractSource + " (" + result.AbstractURL + ")"
		}
		return &ToolResult{Content: content}, nil
	}

	return &ToolResult{Content: "抱歉，没有找到相关结果。请换个关键词试试。"}, nil
}

// SocialMediaService 社交媒体服务接口
type SocialMediaService interface {
	Publish(ctx context.Context, req *PublishRequest) (postID string, err error)
	GetPostStatus(ctx context.Context, postID string) (status string, err error)
}

// PublishRequest 发布请求
type PublishRequest struct {
	Platform string   `json:"platform"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Tags     []string `json:"tags"`
	MediaURLs []string `json:"media_urls,omitempty"`
}
