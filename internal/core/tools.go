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

	backends := []string{"duckduckgo", "duckduckgo_lite", "bing_html", "baidu_html", "bing_direct"}

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

	// 所有后端都失败时，返回提示让 Agent 尝试用 http_request 直接搜索
	return NewToolResult(fmt.Sprintf(
		"搜索失败(最后错误: %v)。\n请使用 http_request 工具直接搜索:\n"+
			"- GET https://cn.bing.com/search?q=%s (国内可用)\n"+
			"- GET https://www.baidu.com/s?wd=%s (百度)",
		lastErr, url.QueryEscape(query), url.QueryEscape(query),
	)), nil
}

func (t *WebSearchTool) searchBackend(ctx context.Context, backend string, query string, count int) (string, error) {
	switch backend {
	case "duckduckgo":
		return t.searchDuckDuckGo(ctx, query)
	case "duckduckgo_lite":
		return t.searchDuckDuckGoLite(ctx, query, count)
	case "bing_html":
		return t.searchBingHTML(ctx, query, count)
	case "bing_direct":
		return t.searchBingDirect(ctx, query, count)
	case "baidu_html":
		return t.searchBaiduHTML(ctx, query, count)
	default:
		return "", fmt.Errorf("未知搜索后端: %s", backend)
	}
}

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
		Abstract       string `json:"Abstract"`
		AbstractText   string `json:"AbstractText"`
		Heading        string `json:"Heading"`
		RelatedTopics  []struct {
			Text    string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
		Answer     string `json:"Answer"`
		AnswerType string `json:"AnswerType"`
	}
	json.Unmarshal(body, &result)

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

// searchDuckDuckGoLite 通过 DuckDuckGo Lite HTML 页面搜索（结构极简稳定）
func (t *WebSearchTool) searchDuckDuckGoLite(ctx context.Context, query string, count int) (string, error) {
	searchURL := fmt.Sprintf("https://lite.duckduckgo.com/lite/?q=%s", url.QueryEscape(query))
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
	html := string(body)

	type item struct{ title, url, desc string }
	var results []item

	rows := strings.Split(html, `<tr class="result">`)
	for _, row := range rows[1:] {
		aHref := `<a href="`
		hStart := strings.Index(row, aHref)
		if hStart == -1 {
			continue
		}
		hStart += len(aHref)
		hEnd := strings.Index(row[hStart:], `"`)
		if hEnd == -1 {
			continue
		}
		u := row[hStart : hStart+hEnd]
		if u == "" {
			continue
		}

		gt := strings.Index(row[hStart-20:], ">")
		if gt == -1 {
			continue
		}
		tStart := hStart - 20 + gt + 1
		cA := strings.Index(row[tStart:], "</a>")
		if cA == -1 {
			continue
		}
		title := strings.TrimSpace(stripHTMLTags(row[tStart : tStart+cA]))
		if title == "" {
			continue
		}

		desc := ""
		snip := `class="result-snippet"`
		sStart := strings.Index(row, snip)
		if sStart != -1 {
			sGT := strings.Index(row[sStart:], ">")
			if sGT != -1 {
				cStart := sStart + sGT + 1
				tdEnd := strings.Index(row[cStart:], "</td>")
				if tdEnd != -1 && tdEnd < 500 {
					desc = strings.TrimSpace(stripHTMLTags(row[cStart : cStart+tdEnd]))
					if len([]rune(desc)) > 200 {
						desc = string([]rune(desc)[:200]) + "..."
					}
				}
			}
		}
		results = append(results, item{title, u, desc})
		if len(results) >= count {
			break
		}
	}
	if len(results) == 0 {
		return "", fmt.Errorf("DuckDuckGo Lite 未返回可解析的结果")
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("关于「%s」的搜索结果（共%d条）：\n", query, len(results)))
	for i, r := range results {
		parts = append(parts, fmt.Sprintf("%d. [%s](%s)", i+1, r.title, r.url))
		if r.desc != "" {
			parts = append(parts, fmt.Sprintf("   %s", r.desc))
		}
		parts = append(parts, "")
	}
	return strings.Join(parts, "\n"), nil
}

// searchBingDirect 通过 HTTP 直连 cn.bing.com 获取搜索结果（最终兜底）
func (t *WebSearchTool) searchBingDirect(ctx context.Context, query string, count int) (string, error) {
	searchURL := fmt.Sprintf("https://cn.bing.com/search?q=%s&count=%d",
		url.QueryEscape(query), count)
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	text := stripHTMLTags(string(body))

	// 按行取出有意义的结果片段
	var results []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len([]rune(line)) < 15 || line == "" {
			continue
		}
		results = append(results, line)
	}
	if len(results) > 50 {
		results = results[:50]
	}

	if len(results) == 0 {
		return "", fmt.Errorf("未能从 Bing 搜索结果中提取文本")
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("关于「%s」的搜索结果：\n", query))
	for i, r := range results {
		parts = append(parts, fmt.Sprintf("%d. %s", i+1, r))
	}
	return strings.Join(parts, "\n"), nil
}

// ==================== Bing 搜索解析 ====================

// bingResult 用于 Bing/百度搜索结果的内部类型
type bingResult struct {
	title string
	url   string
	desc  string
}

func (t *WebSearchTool) searchBingHTML(ctx context.Context, query string, count int) (string, error) {
	searchURL := fmt.Sprintf(
		"https://www.bing.com/search?q=%s&count=%d&ensearch=0",
		url.QueryEscape(query), count,
	)
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	var results []bingResult
	classPatterns := []string{`class="b_algo"`, `class="b_caption"`, `class="b_algo_sr"`}
	for _, cp := range classPatterns {
		results = parseBingByClass(html, cp, count)
		if len(results) > 0 {
			break
		}
	}
	if len(results) == 0 {
		results = parseBingByH2(html, count)
	}
	if len(results) == 0 {
		results = parseBingByAnyLink(html, count)
	}
	if len(results) == 0 {
		return "", fmt.Errorf("Bing 未返回可解析的结果")
	}

	results = filterBingResults(results)
	if len(results) > count {
		results = results[:count]
	}
	if len(results) == 0 {
		return "", fmt.Errorf("Bing 没有返回有效搜索结果")
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("关于「%s」的搜索结果（共%d条）：\n", query, len(results)))
	for i, r := range results {
		parts = append(parts, fmt.Sprintf("%d. [%s](%s)", i+1, r.title, r.url))
		if r.desc != "" {
			parts = append(parts, fmt.Sprintf("   %s", r.desc))
		}
		parts = append(parts, "")
	}
	return strings.Join(parts, "\n"), nil
}

func parseBingByClass(html, classPattern string, count int) []bingResult {
	var results []bingResult
	sections := strings.Split(html, classPattern)
	for _, section := range sections[1:] {
		r := extractLinkFromSection(section)
		if r != nil {
			results = append(results, *r)
			if len(results) >= count {
				break
			}
		}
	}
	return results
}

func parseBingByH2(html string, count int) []bingResult {
	var results []bingResult
	h2s := strings.Split(html, "<h2")
	for _, h2 := range h2s[1:] {
		h2End := strings.Index(h2, "</h2>")
		if h2End == -1 {
			continue
		}
		r := extractLinkFromSection(h2[:h2End])
		if r != nil {
			results = append(results, *r)
			if len(results) >= count {
				break
			}
		}
	}
	return results
}

func parseBingByAnyLink(html string, count int) []bingResult {
	var results []bingResult
	seen := make(map[string]bool)
	remaining := html
	for {
		aTag := `<a href="http`
		idx := strings.Index(remaining, aTag)
		if idx == -1 {
			break
		}
		start := idx + len(aTag) - 4
		end := strings.Index(remaining[start:], `"`)
		if end == -1 {
			break
		}
		u := remaining[start : start+end]
		if strings.Contains(u, "bing.com") || strings.Contains(u, "microsoft.com") || seen[u] {
			remaining = remaining[start+end:]
			continue
		}
		seen[u] = true

		before := remaining[:idx]
		lastGT := strings.LastIndex(before, ">")
		title := u
		if lastGT != -1 && idx-lastGT < 200 {
			candidate := strings.TrimSpace(stripHTMLTags(before[lastGT+1:]))
			if len([]rune(candidate)) > 1 && len([]rune(candidate)) < 100 {
				title = candidate
			}
		}
		results = append(results, bingResult{title: title, url: u})
		if len(results) >= count {
			break
		}
		remaining = remaining[start+end:]
	}
	return results
}

func extractLinkFromSection(section string) *bingResult {
	aHref := `<a href="`
	hrefStart := strings.Index(section, aHref)
	if hrefStart == -1 {
		return nil
	}
	hrefStart += len(aHref)
	if !strings.HasPrefix(section[hrefStart:], "http") {
		return nil
	}
	hrefEnd := strings.Index(section[hrefStart:], `"`)
	if hrefEnd == -1 {
		return nil
	}
	rawURL := section[hrefStart : hrefStart+hrefEnd]

	gtPos := strings.Index(section[hrefStart-50:hrefStart+200], ">")
	if gtPos == -1 {
		return nil
	}
	titleStart := hrefStart - 50 + gtPos + 1
	aClose := strings.Index(section[titleStart:], "</a>")
	if aClose == -1 {
		return nil
	}
	title := strings.TrimSpace(stripHTMLTags(section[titleStart : titleStart+aClose]))
	if title == "" || len([]rune(title)) > 150 {
		return nil
	}

	desc := extractDescription(section)
	return &bingResult{title: title, url: rawURL, desc: desc}
}

func extractDescription(section string) string {
	searchArea := section
	if len(searchArea) > 600 {
		searchArea = searchArea[:600]
	}
	for _, delim := range []string{"<p>", "</p>", "<span>", "</span>"} {
		s := strings.Index(searchArea, delim)
		if s == -1 {
			continue
		}
		s += len(delim)
		e := strings.Index(searchArea[s:], "<")
		if e == -1 || e > 300 {
			continue
		}
		c := strings.TrimSpace(stripHTMLTags(searchArea[s : s+e]))
		if len([]rune(c)) > 20 {
			if len([]rune(c)) > 200 {
				c = string([]rune(c)[:200]) + "..."
			}
			return c
		}
	}
	return ""
}

func filterBingResults(results []bingResult) []bingResult {
	noiseKeywords := []string{"在新选项卡中打开链接", "Open link in", "时间不限", "english", "deutsch"}
	var filtered []bingResult
	for _, r := range results {
		noisy := false
		for _, nk := range noiseKeywords {
			if strings.Contains(r.title, nk) {
				noisy = true
				break
			}
		}
		if !noisy && len([]rune(r.title)) > 1 {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// stripHTMLTags 去除 HTML 标签，保留文本
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
	cleaned := result.String()
	replacements := map[string]string{
		"&nbsp;": " ", "&amp;": "&", "&lt;": "<", "&gt;": ">",
		"&quot;": `"`, "&#39;": "'", "&#x27;": "'",
	}
	for old, new := range replacements {
		cleaned = strings.ReplaceAll(cleaned, old, new)
	}
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return strings.TrimSpace(cleaned)
}

// ==================== 百度搜索 ====================

func (t *WebSearchTool) searchBaiduHTML(ctx context.Context, query string, count int) (string, error) {
	searchURL := fmt.Sprintf(
		"https://www.baidu.com/s?wd=%s&rn=%d",
		url.QueryEscape(query), count,
	)
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	var results []bingResult

	sections := strings.Split(html, `class="result c-container`)
	for _, section := range sections[1:] {
		tStart := strings.Index(section, `class="t"`)
		if tStart == -1 {
			continue
		}
		tagA := `<a `
		aStart := strings.Index(section[tStart:], tagA)
		if aStart == -1 {
			continue
		}
		aStart += tStart

		hrefTag := `href="`
		hrefStart := strings.Index(section[aStart:], hrefTag)
		if hrefStart == -1 {
			continue
		}
		hrefStart += aStart + len(hrefTag)
		hrefEnd := strings.Index(section[hrefStart:], `"`)
		if hrefEnd == -1 {
			continue
		}
		u := section[hrefStart : hrefStart+hrefEnd]

		gtPos := strings.Index(section[aStart:], ">")
		if gtPos == -1 {
			continue
		}
		gtPos += aStart + 1
		aClose := strings.Index(section[gtPos:], "</a>")
		if aClose == -1 {
			continue
		}
		title := strings.TrimSpace(stripHTMLTags(section[gtPos : gtPos+aClose]))
		if title == "" {
			continue
		}

		desc := extractBaiduDescription(section)
		results = append(results, bingResult{title: title, url: u, desc: desc})
	}

	if len(results) == 0 {
		return "", fmt.Errorf("百度未返回可解析的结果")
	}
	if len(results) > count {
		results = results[:count]
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("关于「%s」的搜索结果（共%d条）：\n", query, len(results)))
	for i, r := range results {
		parts = append(parts, fmt.Sprintf("%d. [%s](%s)", i+1, r.title, r.url))
		if r.desc != "" {
			parts = append(parts, fmt.Sprintf("   %s", r.desc))
		}
		parts = append(parts, "")
	}
	return strings.Join(parts, "\n"), nil
}

func extractBaiduDescription(section string) string {
	patterns := []string{`class="c-abstract"`, `class="content-right`}
	for _, pattern := range patterns {
		absStart := strings.Index(section, pattern)
		if absStart == -1 {
			continue
		}
		gtStart := strings.Index(section[absStart:], ">")
		if gtStart == -1 {
			continue
		}
		contentStart := absStart + gtStart + 1
		for _, closer := range []string{"</div>", "</span>"} {
			absEnd := strings.Index(section[contentStart:], closer)
			if absEnd != -1 {
				desc := strings.TrimSpace(stripHTMLTags(section[contentStart : contentStart+absEnd]))
				if len([]rune(desc)) > 200 {
					desc = string([]rune(desc)[:200]) + "..."
				}
				return desc
			}
		}
	}
	return ""
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

	collection := ""
	if c, ok := params["collection"].(string); ok && c != "" {
		collection = c
	} else {
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
	socialService SocialMediaService
}

func NewSocialPublishTool(svc SocialMediaService) *SocialPublishTool {
	return &SocialPublishTool{socialService: svc}
}

func (t *SocialPublishTool) Name() string        { return "social_publish" }
func (t *SocialPublishTool) Description() string {
	return "高风险操作：将内容发布到社交媒体平台（抖音/小红书/视频号）。仅在用户明确要求发布时使用，且必须先获得用户确认。"
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

// HTTPRequestTool 通用 HTTP 请求工具
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
				"description": "HTTP 方法，默认 GET",
				"enum":        []string{"GET", "POST", "PUT", "DELETE"},
				"default":     "GET",
			},
			"headers": map[string]interface{}{
				"type":        "object",
				"description": "请求头键值对",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "请求体（仅POST/PUT时使用）",
			},
		},
		"required": []string{"url"},
	}
}

func (t *HTTPRequestTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	rawURL, _ := params["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return NewToolResult(fmt.Sprintf("请求失败：URL「%s」需要以 http:// 或 https:// 开头", rawURL)), nil
	}

	method, _ := params["method"].(string)
	if method == "" {
		method = "GET"
	}
	method = strings.ToUpper(method)

	var bodyReader io.Reader
	if bodyStr, ok := params["body"].(string); ok && method != "GET" {
		bodyReader = strings.NewReader(bodyStr)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	if h, ok := params["headers"].(map[string]interface{}); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	result := map[string]interface{}{
		"status_code": resp.StatusCode,
		"body":        string(respBody),
	}
	resultJSON, _ := json.Marshal(result)
	return NewToolResult(string(resultJSON)), nil
}

// ==================== 计算器工具 ====================

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
func (t *CalculatorTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	return &ToolResult{
		Content: "计算功能待实现，请接入 govaluate 库",
	}, nil
}

// ==================== 文件读写工具 ====================

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
				"description": "文件路径",
			},
		},
		"required": []string{"path"},
	}
}
func (t *FileReadTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	return &ToolResult{Content: "文件读取功能待实现，请接入 os 包实现"}, nil
}

// ==================== 时间日期工具 ====================

type GetCurrentDateTimeTool struct{}

func NewGetCurrentDateTimeTool() *GetCurrentDateTimeTool { return &GetCurrentDateTimeTool{} }
func (t *GetCurrentDateTimeTool) Name() string { return "get_current_datetime" }
func (t *GetCurrentDateTimeTool) Description() string {
	return "获取当前日期和时间。当用户询问「现在几点」「今天几号」「星期几」时使用。无需网络，即时返回。"
}
func (t *GetCurrentDateTimeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"timezone": map[string]interface{}{
				"type":        "string",
				"description": "时区，默认 Asia/Shanghai",
				"enum":        []string{"Asia/Shanghai", "UTC", "America/New_York"},
				"default":     "Asia/Shanghai",
			},
			"format": map[string]interface{}{
				"type":        "string",
				"description": "输出格式，默认 full",
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
		loc = time.UTC
	}
	now := time.Now().In(loc)
	weekdayCN := [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	format, _ := params["format"].(string)
	var content string
	switch format {
	case "date":
		content = now.Format("2006年01月02日")
	case "time":
		content = now.Format("15:04:05")
	case "weekday":
		content = weekdayCN[now.Weekday()]
	default:
		content = fmt.Sprintf("当前时间（%s）：%s %s %s", timezone,
			now.Format("2006年01月02日"), weekdayCN[now.Weekday()], now.Format("15:04:05"))
	}
	return NewToolResult(content), nil
}

// ==================== 知识保存工具 ====================

type KnowledgeSaveHandler func(ctx context.Context, title, content, category, agentID string) error

type KnowledgeSaveTool struct {
	engine *AgentEngine
	saver  KnowledgeSaveHandler
}

func NewKnowledgeSaveTool(engine *AgentEngine, saver KnowledgeSaveHandler) *KnowledgeSaveTool {
	return &KnowledgeSaveTool{engine: engine, saver: saver}
}
func (t *KnowledgeSaveTool) Name() string { return "knowledge_save" }
func (t *KnowledgeSaveTool) Description() string {
	return "将有用的信息保存到知识库向量数据库中，供后续检索使用。当你从网页、搜索结果或其他来源获取到有价值的内容时使用。"
}
func (t *KnowledgeSaveTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{
				"type":        "string",
				"description": "知识标题，简短概括内容主题",
				"minLength":   2, "maxLength": 200,
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "知识正文内容",
				"minLength":   10, "maxLength": 50000,
			},
			"category": map[string]interface{}{
				"type":    "string",
				"description": "知识分类",
				"enum":    []string{"tech", "product", "faq", "policy", "manual", "other"},
				"default": "other",
			},
			"tags": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
				"description": "标签列表，方便分类检索",
				"maxItems": 10,
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
	agentID, _ := ctx.Value(ContextKeyKnowledgeBaseID).(string)
	chunks := smartChunkContent(content)
	metadatas := make([]map[string]interface{}, len(chunks))
	for i := range chunks {
		metadatas[i] = map[string]interface{}{
			"title": title, "chunk_index": i, "type": "agent_saved",
			"category": category, "tags": tags, "agent_id": agentID,
		}
	}
	collection := KnowledgeBaseCollection(agentID)
	if t.engine.vectorDB != nil {
		docID := fmt.Sprintf("save_%d", time.Now().UnixNano())
		if err := t.engine.vectorDB.Insert(ctx, docID, chunks, metadatas, collection); err != nil {
			return nil, fmt.Errorf("向量入库失败: %w", err)
		}
	} else {
		return NewToolResult("保存失败：向量数据库未初始化"), nil
	}
	if t.saver != nil {
		aID := agentID
		if aID == "" {
			aID = "agent_saved"
		}
		if err := t.saver(ctx, title, content, category, aID); err != nil {
			fmt.Printf("[KnowledgeSave] 文档元数据保存失败: %v\n", err)
		}
	}
	return NewToolResult(fmt.Sprintf("✅ 知识已保存\n标题: %s\n分类: %s\n内容长度: %d字\n分块数: %d",
		title, category, len([]rune(content)), len(chunks))), nil
}

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
	Platform  string   `json:"platform"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags"`
	MediaURLs []string `json:"media_urls,omitempty"`
}
