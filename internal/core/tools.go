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
func (t *WebSearchTool) Description() string {
	return "在互联网上搜索实时信息。当你需要最新新闻、实时数据、不确定的知识点时使用。不适合查询当前时间（请用get_current_datetime）。"
}
func (t *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "搜索关键词，越具体越精确，建议2-10个中文词",
				"minLength":   1,
				"maxLength":   200,
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "返回结果数量（1-20条），默认5条",
				"minimum":     1,
				"maximum":     20,
				"default":     5,
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	query, _ := params["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return NewToolResult("搜索失败：query参数不能为空，请输入有效的搜索关键词"), nil
	}

	count := 5
	if c, ok := params["count"].(float64); ok {
		count = int(c)
	}
	if count < 1 {
		count = 1
	}
	if count > 20 {
		count = 20
	}

	// 尝试多个搜索后端，一个失败则自动切换
	backends := []string{"duckduckgo", "bing_html"}

	var lastErr error
	for _, backend := range backends {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		content, err := t.searchBackend(ctx, backend, query, count)
		if err == nil {
			return NewToolResult(content), nil
		}
		lastErr = err
		fmt.Printf("[WebSearch] 后端 %s 搜索失败: %v，尝试下一个...\n", backend, err)
	}

	return NewToolResult(fmt.Sprintf("搜索失败: %v。请尝试使用其他工具，如 get_current_datetime（查时间）或 http_request（直接调用API）", lastErr)), nil
}

// searchBackend 在指定后端执行搜索
func (t *WebSearchTool) searchBackend(ctx context.Context, backend string, query string, count int) (string, error) {
	switch backend {
	case "duckduckgo":
		return t.searchDuckDuckGo(ctx, query)
	case "bing_html":
		return t.searchBingHTML(ctx, query, count)
	default:
		return "", fmt.Errorf("未知搜索后端: %s", backend)
	}
}

// searchDuckDuckGo 调用 DuckDuckGo Instant Answer API
func (t *WebSearchTool) searchDuckDuckGo(ctx context.Context, query string) (string, error) {
	searchURL := fmt.Sprintf(
		"https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1",
		url.QueryEscape(query),
	)
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SirenAgent/1.0)")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Abstract     string   `json:"Abstract"`
		AbstractText string   `json:"AbstractText"`
		Heading      string   `json:"Heading"`
		RelatedTopics []struct {
			Text    string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
		Answer        string `json:"Answer"`
		AnswerType    string `json:"AnswerType"`
	}
	json.Unmarshal(body, &result)

	// 优先使用即时答案（适合时间、计算等精确查询）
	if result.Answer != "" {
		return fmt.Sprintf("[%s]\n%s", result.Heading, result.Answer), nil
	}

	content := result.AbstractText
	if content == "" {
		content = result.Abstract
	}
	if content == "" {
		return "", fmt.Errorf("没有找到结果")
	}
	if result.Heading != "" {
		content = fmt.Sprintf("[%s]\n%s", result.Heading, content)
	}
	return content, nil
}

// searchBingHTML 通过 Bing 搜索（无需 API Key，解析 HTML）
func (t *WebSearchTool) searchBingHTML(ctx context.Context, query string, count int) (string, error) {
	searchURL := fmt.Sprintf(
		"https://www.bing.com/search?q=%s&count=%d",
		url.QueryEscape(query), count,
	)
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// 简单解析：提取 <h2><a href="...">标题</a></h2> 结构 (Bing)
	var results []string
	lines := strings.Split(html, "\n")
	inH2 := false
	for _, line := range lines {
		if strings.Contains(line, "<h2>") || strings.Contains(line, "<h2 ") {
			inH2 = true
		}
		if inH2 {
			// 提取 <a> 标签
			aStart := strings.Index(line, "<a ")
			if aStart != -1 {
				// 提取 href
				hrefStart := strings.Index(line[aStart:], "href=\"")
				if hrefStart != -1 {
					hrefStart += aStart + 6
					hrefEnd := strings.Index(line[hrefStart:], "\"")
					if hrefEnd != -1 {
						urlStr := line[hrefStart : hrefStart+hrefEnd]
						// 提取标题文字
						textStart := strings.Index(line, ">")
						textEnd := strings.LastIndex(line, "</a>")
						if textStart != -1 && textEnd != -1 && textEnd > textStart {
							title := strings.TrimSpace(line[textStart+1 : textEnd])
							title = stripHTMLTags(title)
							if title != "" {
								results = append(results, fmt.Sprintf("- [%s](%s)", title, urlStr))
							}
						}
					}
				}
				inH2 = false
			}
		}
	}

	if len(results) == 0 {
		return "", fmt.Errorf("Bing 未返回可解析的结果")
	}

	content := fmt.Sprintf("关于「%s」的搜索结果：\n", query)
	content += strings.Join(results, "\n")
	return content, nil
}

// stripHTMLTags 简单去除 HTML 标签
func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, c := range s {
		if c == '<' {
			inTag = true
			continue
		}
		if c == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(c)
		}
	}
	// 清理 &nbsp; 等
	cleaned := strings.ReplaceAll(result.String(), "&nbsp;", " ")
	cleaned = strings.ReplaceAll(cleaned, "&amp;", "&")
	cleaned = strings.ReplaceAll(cleaned, "&lt;", "<")
	cleaned = strings.ReplaceAll(cleaned, "&gt;", ">")
	return strings.TrimSpace(cleaned)
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
	return "在专属知识库中检索相关文档片段。当用户询问产品信息、技术文档、FAQ、内部资料时使用。不适合搜索互联网实时信息（请用web_search）。"
}
func (t *RAGSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "检索关键词或问题，建议从用户的问题中提取最核心的3-8个关键词",
				"minLength":   1,
				"maxLength":   200,
			},
			"top_k": map[string]interface{}{
				"type":        "integer",
				"description": "返回最相关的文档块数量（1-10条），默认3条。技术问答可设大一些",
				"minimum":     1,
				"maximum":     10,
				"default":     3,
			},
			"collection": map[string]interface{}{
				"type":        "string",
				"description": "向量集合名称（可选）。不传则自动使用当前Agent的知识库，一般不需要传此参数",
			},
		},
		"required": []string{"query"},
	}
}

func (t *RAGSearchTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	query, _ := params["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return NewToolResult("检索失败：query参数不能为空，请输入要查询的关键词"), nil
	}

	topK := 3
	if k, ok := params["top_k"].(float64); ok {
		topK = int(k)
	}
	if topK < 1 {
		topK = 1
	}
	if topK > 10 {
		topK = 10
	}

	// 确定集合名称 — 优先级：参数 > Context > 全局默认
	collection := ""
	if c, ok := params["collection"].(string); ok && c != "" {
		collection = c
	} else {
		// 从 Context 读取当前 Agent 的知识库ID
		if kbID, ok := ctx.Value(ContextKeyKnowledgeBaseID).(string); ok && kbID != "" {
			collection = KnowledgeBaseCollection(kbID)
		}
	}
	if collection == "" {
		collection = "agent_knowledge"
	}

	fmt.Printf("[RAGSearch] query=%q, topK=%d, collection=%s\n", query, topK, collection)

	results, err := t.engine.vectorDB.Search(ctx, query, topK, collection)
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
	return "⚠️ 高风险操作：将内容发布到社交媒体平台（抖音/小红书/视频号）。仅在用户明确要求发布时使用，且必须先获得用户确认。"
}
func (t *SocialPublishTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"platform": map[string]interface{}{
				"type":        "string",
				"description": "目标平台，可选值：douyin（抖音）, xiaohongshu（小红书）, video_channel（微信视频号）",
				"enum":        []string{"douyin", "xiaohongshu", "video_channel"},
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "帖子标题（可选），最长100字",
				"maxLength":   100,
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "帖子正文内容，最少10个字，最长2000字",
				"minLength":   10,
				"maxLength":   2000,
			},
			"tags": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "话题标签列表，每个标签不加#号，建议2-5个标签",
				"maxItems":    10,
			},
		},
		"required": []string{"platform", "content"},
	}
}

func (t *SocialPublishTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	platform, _ := params["platform"].(string)
	content, _ := params["content"].(string)
	title, _ := params["title"].(string)

	// Poka-yoke: 平台名自动转小写去空格
	platform = strings.TrimSpace(strings.ToLower(platform))
	validPlatforms := map[string]bool{"douyin": true, "xiaohongshu": true, "video_channel": true}
	if !validPlatforms[platform] {
		return NewToolResult(fmt.Sprintf(
			"发布失败：无效的平台「%s」\n有效平台: douyin（抖音）, xiaohongshu（小红书）, video_channel（微信视频号）",
			platform,
		)), nil
	}

	content = strings.TrimSpace(content)
	if len([]rune(content)) < 10 {
		return NewToolResult(fmt.Sprintf("发布失败：内容过短（%d字），最少需要10个字", len([]rune(content)))), nil
	}
	if len([]rune(content)) > 2000 {
		return NewToolResult(fmt.Sprintf("发布失败：内容过长（%d字），最多2000字", len([]rune(content)))), nil
	}

	tags := make([]string, 0)
	if rawTags, ok := params["tags"].([]interface{}); ok {
		for _, tag := range rawTags {
			if s, ok := tag.(string); ok {
				s = strings.TrimSpace(s)
				// Poka-yoke: 自动去掉用户可能误输入的 # 号
				s = strings.TrimPrefix(s, "#")
				if s != "" {
					tags = append(tags, s)
				}
			}
		}
	}
	if len(tags) > 10 {
		tags = tags[:10]
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
	return "发送 HTTP 请求调用外部 RESTful API 或获取网页数据。支持 GET/POST/PUT/DELETE。注意：不要用于内部敏感接口。"
}
func (t *HTTPRequestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "请求 URL，必须以 http:// 或 https:// 开头",
				"pattern":     "^https?://",
			},
			"method": map[string]interface{}{
				"type":        "string",
				"description": "HTTP 方法，默认 GET。查询用GET，创建用POST，更新用PUT，删除用DELETE",
				"enum":        []string{"GET", "POST", "PUT", "DELETE"},
				"default":     "GET",
			},
			"headers": map[string]interface{}{
				"type":        "object",
				"description": "请求头键值对，如 {\"Authorization\": \"Bearer xxx\", \"Content-Type\": \"application/json\"}",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "请求体（仅POST/PUT时使用）。JSON格式请确保是合法的JSON字符串",
			},
		},
		"required": []string{"url"},
	}
}

func (t *HTTPRequestTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	rawURL, _ := params["url"].(string)
	rawURL = strings.TrimSpace(rawURL)

	// Poka-yoke: URL 校验
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return NewToolResult(fmt.Sprintf("请求失败：URL「%s」需要以 http:// 或 https:// 开头，请检查URL", rawURL)), nil
	}

	method, _ := params["method"].(string)
	if method == "" {
		method = "GET"
	}
	method = strings.ToUpper(method)
	validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true}
	if !validMethods[method] {
		method = "GET"
	}

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
	return "读取指定文本文件的内容。可用于读取配置文件、日志文件、代码文件等。注意：只能读取文本文件，不能读取二进制文件。"
}
func (t *FileReadTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "文件路径，使用相对于项目的路径（如 config.yaml）或绝对路径。不要使用 ~ 开头的路径",
			},
			"encoding": map[string]interface{}{
				"type":        "string",
				"description": "文件编码，默认 utf-8。可选：utf-8, gbk, latin1",
				"enum":        []string{"utf-8", "gbk", "latin1"},
				"default":     "utf-8",
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

// ==================== 时间日期工具（本地无API依赖） ====================

// GetCurrentDateTimeTool 获取当前时间日期 — 无需外部 API
type GetCurrentDateTimeTool struct{}

func NewGetCurrentDateTimeTool() *GetCurrentDateTimeTool { return &GetCurrentDateTimeTool{} }

func (t *GetCurrentDateTimeTool) Name() string { return "get_current_datetime" }
func (t *GetCurrentDateTimeTool) Description() string {
	return "获取当前日期和时间。当用户询问「现在几点」「今天几号」「星期几」时使用。无需网络，即时返回。不能用于查询历史日期或未来日期。"
}
func (t *GetCurrentDateTimeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"timezone": map[string]interface{}{
				"type":        "string",
				"description": "时区，默认 Asia/Shanghai（北京时间）。可选值：Asia/Shanghai, UTC, America/New_York",
				"enum":        []string{"Asia/Shanghai", "UTC", "America/New_York"},
				"default":     "Asia/Shanghai",
			},
			"format": map[string]interface{}{
				"type":        "string",
				"description": "输出格式，默认 full。可选值：full（完整日期+时间）, date（仅日期）, time（仅时间）, weekday（仅星期几）",
				"enum":        []string{"full", "date", "time", "weekday"},
				"default":     "full",
			},
		},
	}
}

func (t *GetCurrentDateTimeTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	timezone := "Asia/Shanghai"
	if tz, ok := params["timezone"].(string); ok && tz != "" {
		timezone = tz
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		// 回退到 UTC
		loc = time.UTC
	}

	now := time.Now().In(loc)
	weekdayCN := [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	weekdayEN := [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

	format, _ := params["format"].(string)
	content := ""

	switch format {
	case "date":
		content = now.Format("2006年01月02日")
	case "time":
		content = now.Format("15:04:05")
	case "weekday":
		content = fmt.Sprintf("%s（%s）", weekdayCN[now.Weekday()], weekdayEN[now.Weekday()])
	default:
		content = fmt.Sprintf(
			"当前时间（%s）：%s %s %s",
			timezone,
			now.Format("2006年01月02日"),
			weekdayCN[now.Weekday()],
			now.Format("15:04:05"),
		)
	}

	return NewToolResult(content), nil
}

// ==================== 知识保存工具 ====================

// KnowledgeSaveHandler 保存文档元数据的回调函数
type KnowledgeSaveHandler func(ctx context.Context, title, content, category, agentID string) error

// KnowledgeSaveTool 知识保存工具 - 让Agent可以在对话中自主保存信息到知识库向量数据库
type KnowledgeSaveTool struct {
	engine *AgentEngine
	saver  KnowledgeSaveHandler
}

func NewKnowledgeSaveTool(engine *AgentEngine, saver KnowledgeSaveHandler) *KnowledgeSaveTool {
	return &KnowledgeSaveTool{
		engine: engine,
		saver:  saver,
	}
}

func (t *KnowledgeSaveTool) Name() string { return "knowledge_save" }
func (t *KnowledgeSaveTool) Description() string {
	return "将有用的信息保存到知识库向量数据库中，供后续检索使用。" +
		"当你从网页、搜索结果或其他来源获取到有价值的内容时，" +
		"或者用户希望记住某些信息时使用。保存后下次检索知识库就能找到。"
}
func (t *KnowledgeSaveTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"type":        "string",
				"description": "知识标题，简短概括内容主题，建议10-30字",
				"minLength":   2,
				"maxLength":   200,
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "知识正文内容，要保存的具体信息。建议整理成清晰的结构化文本，避免原始噪音",
				"minLength":   10,
				"maxLength":   50000,
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "知识分类，可选值：tech（技术）, product（产品）, faq（常见问题）, policy（政策）, manual（手册）, other（其他）",
				"enum":        []string{"tech", "product", "faq", "policy", "manual", "other"},
				"default":     "other",
			},
			"tags": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "标签列表，方便分类检索，建议2-5个关键词",
				"maxItems":    10,
			},
		},
		"required": []string{"title", "content"},
	}
}

func (t *KnowledgeSaveTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	title, _ := params["title"].(string)
	content, _ := params["content"].(string)
	content = strings.TrimSpace(content)
	if len([]rune(content)) < 10 {
		return NewToolResult("保存失败：内容太短，最少需要10个字"), nil
	}

	category, _ := params["category"].(string)
	if category == "" {
		category = "other"
	}

	tags := make([]string, 0)
	if rawTags, ok := params["tags"].([]interface{}); ok {
		for _, tag := range rawTags {
			if s, ok := tag.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					tags = append(tags, s)
				}
			}
		}
	}

	// 从 Context 获取当前 Agent ID（如果有）
	agentID, _ := ctx.Value(ContextKeyKnowledgeBaseID).(string)

	// 1. 简单分块（按段落/换行）
	chunks := smartChunkContent(content)

	// 2. 构建元数据
	metadatas := make([]map[string]interface{}, len(chunks))
	for i := range chunks {
		metadatas[i] = map[string]interface{}{
			"title":       title,
			"chunk_index": i,
			"type":        "agent_saved",
			"category":    category,
			"tags":        tags,
			"agent_id":    agentID,
		}
	}

	// 3. 确定向量集合
	collection := KnowledgeBaseCollection(agentID)

	// 4. 写入向量数据库
	if t.engine.vectorDB != nil {
		docID := fmt.Sprintf("save_%d", time.Now().UnixNano())
		if err := t.engine.vectorDB.Insert(ctx, docID, chunks, metadatas, collection); err != nil {
			return nil, fmt.Errorf("向量入库失败: %w", err)
		}
		fmt.Printf("[KnowledgeSave] 已保存: title=%q, chunks=%d, collection=%s\n", title, len(chunks), collection)
	} else {
		return NewToolResult("保存失败：向量数据库未初始化"), nil
	}

	// 5. 可选：保存文档元数据（如配置了 saver）
	if t.saver != nil {
		agentIDForMeta := agentID
		if agentIDForMeta == "" {
			agentIDForMeta = "agent_saved"
		}
		if err := t.saver(ctx, title, content, category, agentIDForMeta); err != nil {
			fmt.Printf("[KnowledgeSave] 文档元数据保存失败: %v\n", err)
		}
	}

	tagStr := strings.Join(tags, ", ")
	if tagStr != "" {
		tagStr = "，标签: " + tagStr
	}
	return NewToolResult(fmt.Sprintf("✅ 知识已保存\n标题: %s\n分类: %s\n内容长度: %d字\n分块数: %d%s",
		title, category, len([]rune(content)), len(chunks), tagStr)), nil
}

// smartChunkContent 简单按换行分段，每段不超过约500字
func smartChunkContent(content string) []string {
	paragraphs := strings.Split(content, "\n")
	var chunks []string
	var current strings.Builder

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if current.Len() > 0 && len([]rune(current.String()+p)) > 500 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(p)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	if len(chunks) == 0 {
		chunks = append(chunks, content)
	}
	return chunks
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
