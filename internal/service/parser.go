package service

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// ==================== HTML 解析器 ====================

// ParseHTMLResult HTML 解析结果
type ParseHTMLResult struct {
	Title    string   // 页面标题
	Content  string   // 清洗后的纯文本
	Chunks   []string // 按语义切分的文本块
	ChunkInfo []ChunkInfo
}

type ChunkInfo struct {
	Index     int
	Heading   string // 该块所属的标题
	CharCount int
}

// ParseHTML 解析 HTML 字符串，提取标题和正文
func ParseHTML(htmlContent string) *ParseHTMLResult {
	result := &ParseHTMLResult{}

	// 1. 提取 <title>
	result.Title = extractTitle(htmlContent)

	// 2. 提取 <body> 内容（没有则用全文）
	body := extractBody(htmlContent)

	// 3. 清理 HTML 标签，保留标题层级标记
	cleanText := cleanHTML(body)

	// 4. 智能分块
	result.Content = cleanText
	chunks, chunkInfos := smartChunk(cleanText)
	result.Chunks = chunks
	result.ChunkInfo = chunkInfos

	return result
}

// FetchAndParseURL 从 URL 抓取并解析 HTML
func FetchAndParseURL(rawURL string, timeout time.Duration) (*ParseHTMLResult, error) {
	// 验证 URL
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return nil, fmt.Errorf("无效的 URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("不支持的协议: %s，仅支持 http/https", parsedURL.Scheme)
	}

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SirenAgent/1.0; KnowledgeCrawler)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("服务器返回异常状态码: %d", resp.StatusCode)
	}

	// 检测编码并读取
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}

	htmlContent := string(bodyBytes)

	// 尝试从 Content-Type 或 HTML meta 检测编码
	contentType := resp.Header.Get("Content-Type")
	encoding := detectCharset(htmlContent, contentType)
	if encoding != "" && !strings.EqualFold(encoding, "utf-8") && !strings.EqualFold(encoding, "utf8") {
		// 非 UTF-8 编码做简单替换（完整的编码转换需要第三方库）
		fmt.Printf("[Parser] 非 UTF-8 编码: %s，可能存在乱码\n", encoding)
	}

	return ParseHTML(htmlContent), nil
}

// ==================== 内部函数 ====================

// extractTitle 提取 <title> 标签内容
func extractTitle(html string) string {
	start := strings.Index(strings.ToLower(html), "<title")
	if start == -1 {
		return ""
	}
	// 跳过 <title ...>
	closeTag := strings.Index(html[start:], ">")
	if closeTag == -1 {
		return ""
	}
	contentStart := start + closeTag + 1
	end := strings.Index(strings.ToLower(html[contentStart:]), "</title>")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(html[contentStart : contentStart+end])
}

// extractBody 提取 <body> 标签内容
func extractBody(html string) string {
	lower := strings.ToLower(html)
	bodyStart := strings.Index(lower, "<body")
	if bodyStart == -1 {
		return html // 没有 body 则返回全文
	}
	closeTag := strings.Index(html[bodyStart:], ">")
	if closeTag == -1 {
		return html
	}
	contentStart := bodyStart + closeTag + 1
	bodyEnd := strings.Index(lower[contentStart:], "</body>")
	if bodyEnd == -1 {
		return html[contentStart:]
	}
	return html[contentStart : contentStart+bodyEnd]
}

// cleanHTML 清洗 HTML，保留标题结构，返回纯文本
func cleanHTML(html string) string {
	// 1. 移除 <script>...</script>
	html = removeTagBlocks(html, "script")
	// 2. 移除 <style>...</style>
	html = removeTagBlocks(html, "style")
	// 3. 移除 <nav>...</nav>（导航栏）
	html = removeTagBlocks(html, "nav")
	// 4. 移除注释 <!-- -->
	html = removeCommentBlocks(html)

	// 5. 将标题标签替换为 Markdown 标题标记
	replacements := []struct {
		from, to string
	}{
		// 标题标签 → Markdown 标题
		{`<h1`, `\n# `},
		{`<h2`, `\n## `},
		{`<h3`, `\n### `},
		{`<h4`, `\n#### `},
		{`</h1>`, `\n`},
		{`</h2>`, `\n`},
		{`</h3>`, `\n`},
		{`</h4>`, `\n`},
		// 段落和换行
		{`<p`, `\n`},
		{`</p>`, `\n`},
		{`<br`, `\n`},
		{`<li`, `\n- `},
		{`</li>`, ``},
		{`</ul>`, `\n`},
		{`</ol>`, `\n`},
		{`<tr`, `\n`},
		{`</tr>`, `|`},
		{`<td`, ``},
		{`</td>`, `|`},
		{`<th`, ``},
		{`</th>`, `|`},
		{`</table>`, `\n`},
		{`<blockquote`, `\n> `},
		{`</blockquote>`, `\n`},
		{`<div`, `\n`},
		{`</div>`, `\n`},
		{`<span`, ``},
		{`</span>`, ``},
		{`<strong>`, `**`},
		{`</strong>`, `**`},
		{`<b>`, `**`},
		{`</b>`, `**`},
		{`<em>`, `*`},
		{`</em>`, `*`},
		{`<i>`, `*`},
		{`</i>`, `*`},
		{`<code>`, "`"},
		{`</code>`, "`"},
		{`<pre>`, "\n```\n"},
		{`</pre>`, "\n```\n"},
		{`<a href="`, `[`},
		{`<a `, `[`},
		{`</a>`, `)`},
		{`<img`, `[图片: `},
		{`/>`, ``},
	}

	for _, r := range replacements {
		html = strings.ReplaceAll(html, r.from, r.to)
	}

	// 6. 移除所有剩余的 HTML 标签
	html = stripRemainingTags(html)

	// 7. 处理特殊字符
	html = decodeHTMLEntities(html)

	// 8. 替换链接格式：从 [text](href) 中提取
	html = normalizeLinks(html)

	// 9. 合并多余空行
	html = compactBlankLines(html)

	return strings.TrimSpace(html)
}

// removeTagBlocks 移除指定标签及其内容
func removeTagBlocks(html, tag string) string {
	var result strings.Builder
	lower := strings.ToLower(html)
	i := 0
	for i < len(html) {
		openStart := strings.Index(lower[i:], "<"+tag)
		if openStart == -1 {
			result.WriteString(html[i:])
			break
		}
		result.WriteString(html[i : i+openStart])
		// 找到 > 结束开放标签
		closeBracket := strings.Index(html[i+openStart:], ">")
		if closeBracket == -1 {
			result.WriteString(html[i+openStart:])
			break
		}
		// 找 </tag>
		closeTag := "</" + tag + ">"
		closeIdx := strings.Index(lower[i+openStart+closeBracket+1:], closeTag)
		if closeIdx == -1 {
			result.WriteString(html[i+openStart : i+openStart+closeBracket+1])
			i = i + openStart + closeBracket + 1
			continue
		}
		i = i + openStart + closeBracket + 1 + closeIdx + len(closeTag)
	}
	return result.String()
}

// removeCommentBlocks 移除 HTML 注释
func removeCommentBlocks(html string) string {
	var result strings.Builder
	i := 0
	for i < len(html) {
		start := strings.Index(html[i:], "<!--")
		if start == -1 {
			result.WriteString(html[i:])
			break
		}
		result.WriteString(html[i : i+start])
		end := strings.Index(html[i+start:], "-->")
		if end == -1 {
			break
		}
		i = i + start + end + 3
	}
	return result.String()
}

// stripRemainingTags 去除所有剩余的 HTML/XML 标签
func stripRemainingTags(s string) string {
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
	return result.String()
}

// decodeHTMLEntities 解码常见 HTML 实体
func decodeHTMLEntities(s string) string {
	entities := map[string]string{
		"&amp;":  "&",
		"&lt;":   "<",
		"&gt;":   ">",
		"&quot;": `"`,
		"&#39;":  "'",
		"&#x27;": "'",
		"&#x2F;": "/",
		"&#60;":  "<",
		"&#62;":  ">",
		"&nbsp;": " ",
		"&mdash;": "—",
		"&ndash;": "–",
		"&ldquo;": "\"",
		"&rdquo;": "\"",
		"&lsquo;": "'",
		"&rsquo;": "'",
		"&laquo;": "«",
		"&raquo;": "»",
	}
	for k, v := range entities {
		s = strings.ReplaceAll(s, k, v)
	}
	// 处理十进制/十六进制实体 &#123; / &#x1F;
	// 简单情况，完整方案需要正则
	return s
}

// normalizeLinks 将 [text](href) 格式简化为 text (href)
func normalizeLinks(s string) string {
	// 简单的替换，完整的链接提取需要正则
	s = strings.ReplaceAll(s, "[", "")
	s = strings.ReplaceAll(s, "](http", " (http")
	s = strings.ReplaceAll(s, "](/", " (https://")
	return s
}

// compactBlankLines 将超过2个连续换行压缩为2个
func compactBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	blankCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blankCount++
			if blankCount <= 2 {
				result = append(result, "")
			}
		} else {
			blankCount = 0
			// 将 `\n` 文本字面替换为真正的换行
			line = strings.ReplaceAll(line, "\\n", "\n")
			result = append(result, trimmed)
		}
	}
	return strings.Join(result, "\n")
}

// detectCharset 从 HTML 或 Content-Type 中检测字符编码
func detectCharset(html, contentType string) string {
	// 从 Content-Type 检测
	ct := strings.ToLower(contentType)
	if idx := strings.Index(ct, "charset="); idx != -1 {
		return strings.TrimSpace(ct[idx+8:])
	}

	// 从 HTML meta 检测
	lower := strings.ToLower(html)
	patterns := []string{`charset="`, `charset='`, `charset=`}
	for _, p := range patterns {
		idx := strings.Index(lower, p)
		if idx != -1 {
			start := idx + len(p)
			end := strings.IndexAny(html[start:], `"' />`)
			if end > 0 {
				return strings.TrimSpace(html[start : start+end])
			}
		}
	}
	return ""
}

// ==================== 智能分块策略 ====================

// smartChunk 智能分块：按标题/段落切分，保留结构信息
func smartChunk(text string) ([]string, []ChunkInfo) {
	type section struct {
		heading string
		content string
		level   int
	}

	const maxChunkSize = 800
	const minChunkSize = 100

	lines := strings.Split(text, "\n")
	var sections []section
	var current section
	current.level = 99 // 初始低优先级

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// 检测标题
		level, heading := detectHeading(trimmed)
		if level > 0 {
			// 保存上一个 section
			if strings.TrimSpace(current.content) != "" {
				sections = append(sections, current)
			}
			current = section{
				heading: heading,
				level:   level,
			}
			continue
		}

		// 普通内容行
		if current.content != "" {
			current.content += "\n"
		}
		current.content += trimmed
	}

	// 保存最后一个 section
	if strings.TrimSpace(current.content) != "" {
		sections = append(sections, current)
	}

	// 如果没有检测到标题，按段落分块
	if len(sections) == 0 {
		return chunkByParagraph(text, maxChunkSize)
	}

	// 按 section 分块：合并小 section，拆分大 section
	var chunks []string
	var chunkInfos []ChunkInfo

	var buffer string
	var bufferHeading string
	bufferSize := 0
	chunkIndex := 0

	flushBuffer := func() {
		if strings.TrimSpace(buffer) == "" {
			return
		}
		chunks = append(chunks, strings.TrimSpace(buffer))
		chunkInfos = append(chunkInfos, ChunkInfo{
			Index:     chunkIndex,
			Heading:   bufferHeading,
			CharCount: len([]rune(strings.TrimSpace(buffer))),
		})
		chunkIndex++
		buffer = ""
		bufferSize = 0
		bufferHeading = ""
	}

	for _, sec := range sections {
		secContent := sec.heading + "\n" + sec.content
		secLen := len([]rune(secContent))

		if secLen >= maxChunkSize && buffer == "" {
			// 大 section，单独分块
			subChunks := splitLongText(secContent, maxChunkSize)
			for _, sub := range subChunks {
				chunks = append(chunks, strings.TrimSpace(sub))
				chunkInfos = append(chunkInfos, ChunkInfo{
					Index:     chunkIndex,
					Heading:   sec.heading,
					CharCount: len([]rune(strings.TrimSpace(sub))),
				})
				chunkIndex++
			}
			continue
		}

		if bufferSize+secLen > maxChunkSize && bufferSize >= minChunkSize {
			flushBuffer()
		}

		if buffer != "" {
			buffer += "\n"
		}
		buffer += secContent
		bufferSize += secLen
		if bufferHeading == "" {
			bufferHeading = sec.heading
		}
	}
	flushBuffer()

	return chunks, chunkInfos
}

// detectHeading 检测是否为标题行，返回级别和标题文本
func detectHeading(line string) (level int, heading string) {
	// Markdown 标题: # ## ###
	for i := 1; i <= 4; i++ {
		prefix := strings.Repeat("#", i) + " "
		if strings.HasPrefix(line, prefix) {
			return i, strings.TrimSpace(line[i+1:])
		}
	}
	return 0, ""
}

// chunkByParagraph 按段落分块（保底策略）
func chunkByParagraph(text string, maxSize int) ([]string, []ChunkInfo) {
	paragraphs := strings.Split(text, "\n\n")
	var chunks []string
	var chunkInfos []ChunkInfo

	var buffer string
	bufferSize := 0
	idx := 0

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		pLen := len([]rune(p))

		if bufferSize+pLen > maxSize && bufferSize > 0 {
			chunks = append(chunks, strings.TrimSpace(buffer))
			chunkInfos = append(chunkInfos, ChunkInfo{Index: idx, CharCount: bufferSize})
			idx++
			buffer = ""
			bufferSize = 0
		}

		if buffer != "" {
			buffer += "\n\n"
		}
		buffer += p
		bufferSize += pLen
	}

	if strings.TrimSpace(buffer) != "" {
		chunks = append(chunks, strings.TrimSpace(buffer))
		chunkInfos = append(chunkInfos, ChunkInfo{Index: idx, CharCount: bufferSize})
	}

	return chunks, chunkInfos
}

// splitLongText 将长文本拆分为多个块
func splitLongText(text string, maxSize int) []string {
	runes := []rune(text)
	if len(runes) <= maxSize {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < len(runes) {
		end := start + maxSize
		if end > len(runes) {
			end = len(runes)
		}
		// 尽量在句子边界断开
		if end < len(runes) {
			// 往前找句号、换行等
			for j := end; j > start+100; j-- {
				c := runes[j]
				if c == '.' || c == '。' || c == '!' || c == '！' || c == '\n' || c == '?' || c == '？' {
					end = j + 1
					break
				}
			}
		}
		chunks = append(chunks, string(runes[start:end]))
		start = end
	}
	return chunks
}

// ==================== 通用工具函数 ====================

// IsHTMLLike 判断内容是否像 HTML
func IsHTMLLike(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "<!doctype html") ||
		strings.Contains(lower, "<html") ||
		(strings.Contains(lower, "<body") && strings.Contains(lower, "</body>"))
}

// IsURL 判断字符串是否像 URL
func IsURL(s string) bool {
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return false
	}
	// 基本验证
	runes := []rune(s)
	for _, r := range runes {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return strings.Count(s, ".") > 0
}

// TruncateContent 截断内容用于日志
func TruncateContent(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
