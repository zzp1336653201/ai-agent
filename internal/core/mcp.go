package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ==================== MCP (Model Context Protocol) ====================
// 支持两种传输模式：
//   1. Stdio 模式：通过子进程 stdin/stdout 通信（默认）
//   2. HTTP 模式：通过 HTTP POST 发送 JSON-RPC（通过 --port 启动）

// MCPToolInfo MCP 工具信息
type MCPToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// ==================== Stdio MCP 客户端 ====================

// StdioMCPClient 基于 stdio 的 MCP 客户端
type StdioMCPClient struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *bufio.Reader
	mu      sync.Mutex
	nextID  int
	tools   []MCPToolInfo
}

func (c *StdioMCPClient) Name() string           { return c.name }
func (c *StdioMCPClient) ListTools() []MCPToolInfo { return c.tools }

// ==================== HTTP MCP 客户端 ====================

// HTTPMCPClient 通过 HTTP 连接的 MCP 客户端
// Playwright MCP 的 HTTP 模式：npx @playwright/mcp@latest --port 8931
// 客户端通过 POST http://localhost:8931/mcp 发送 JSON-RPC 请求
type HTTPMCPClient struct {
	name      string
	url       string
	client    *http.Client
	mu        sync.Mutex
	nextID    int
	tools     []MCPToolInfo
	sessionID string // MCP Streamable HTTP 会话 ID
}

func NewHTTPMCPClient(name, url string) *HTTPMCPClient {
	return &HTTPMCPClient{
		name:   name,
		url:    strings.TrimRight(url, "/") + "/mcp",
		client: &http.Client{Timeout: 60 * time.Second},
		nextID: 1,
	}
}

func (c *HTTPMCPClient) Name() string                     { return c.name }
func (c *HTTPMCPClient) ListTools() []MCPToolInfo           { return c.tools }

// Initialize 执行 MCP 初始化握手 + 获取工具列表
func (c *HTTPMCPClient) Initialize(ctx context.Context) error {
	// Step 1: initialize
	initReq := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "sirenagent",
				"version": "1.0.0",
			},
		},
	}

	if _, err := c.doRequest(initReq); err != nil {
		return fmt.Errorf("initialize 失败: %w", err)
	}

	// Step 2: initialized 通知
	notif := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	if _, err := c.doRequest(notif); err != nil {
		// 通知失败可忽略
	}

	// Step 3: 获取工具列表
	listReq := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/list",
		Params:  map[string]interface{}{},
	}

	respBody, err := c.doRequest(listReq)
	if err != nil {
		return fmt.Errorf("tools/list 失败: %w", err)
	}

	var result mcpListToolsResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("解析工具列表失败: %w", err)
	}
	if result.Error != nil {
		return fmt.Errorf("获取工具列表错误: %s", result.Error.Message)
	}
	if result.Result == nil {
		return fmt.Errorf("工具列表为空")
	}

	c.tools = result.Result.Tools
	fmt.Printf("[MCP:%s] 已连接，发现 %d 个工具\n", c.name, len(c.tools))
	for _, t := range c.tools {
		fmt.Printf("  - %s: %s\n", t.Name, truncateString(t.Description, 60))
	}
	return nil
}

// CallTool 调用远程工具
func (c *HTTPMCPClient) CallTool(name string, params map[string]interface{}) (string, error) {
	req := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      name,
			"arguments": params,
		},
	}

	respBody, err := c.doRequest(req)
	if err != nil {
		return "", fmt.Errorf("[MCP:%s] 调用工具 %s 失败: %w", c.name, name, err)
	}

	var result mcpCallToolResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("[MCP:%s] 解析结果失败: %w", c.name, err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("[MCP:%s] 工具 %s 错误: %s", c.name, name, result.Error.Message)
	}

	var texts []string
	if result.Result != nil {
		for _, block := range result.Result.Content {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

// doRequest 发送 JSON-RPC 请求（HTTP POST）
func (c *HTTPMCPClient) doRequest(req mcpJSONRPCRequest) (json.RawMessage, error) {
	c.mu.Lock()
	if req.ID == 0 {
		req.ID = c.nextID
		c.nextID++
	}
	// 带上已持有的 session ID
	currentSession := c.sessionID
	c.mu.Unlock()

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", c.url, bytes.NewReader(reqData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if currentSession != "" {
		httpReq.Header.Set("Mcp-Session-Id", currentSession)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 提取 session ID（服务器在首次初始化时返回）
	if sessionID := resp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		c.mu.Lock()
		if c.sessionID == "" {
			c.sessionID = sessionID
		}
		c.mu.Unlock()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return body, nil
}

// ==================== Stdio MCP 客户端保留（兼容）====================

// NewStdioMCPClient 创建 stdio MCP 客户端
func NewStdioMCPClient(name, command string, args []string, env map[string]string) (*StdioMCPClient, error) {
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), formatEnv(env)...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("[MCP:%s] 创建 stdin 管道失败: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("[MCP:%s] 创建 stdout 管道失败: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("[MCP:%s] 启动进程失败: %w", name, err)
	}

	client := &StdioMCPClient{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReaderSize(stdout, 64*1024),
		nextID: 1,
	}

	// 异步初始化
	go func() {
		if err := client.initialize(); err != nil {
			fmt.Printf("[MCP:%s] 初始化失败: %v\n", name, err)
			return
		}
		tools, err := client.listTools()
		if err != nil {
			fmt.Printf("[MCP:%s] 获取工具列表失败: %v\n", name, err)
			return
		}
		client.tools = tools
		fmt.Printf("[MCP:%s] 已连接，发现 %d 个工具\n", name, len(tools))
		for _, t := range tools {
			fmt.Printf("  - %s: %s\n", t.Name, truncateString(t.Description, 60))
		}
	}()

	return client, nil
}

func (c *StdioMCPClient) Close() error {
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return nil
}

func (c *StdioMCPClient) CallTool(name string, params map[string]interface{}) (string, error) {
	req := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      name,
			"arguments": params,
		},
	}
	respBody, err := c.sendRequest(req)
	if err != nil {
		return "", fmt.Errorf("[MCP:%s] 调用工具 %s 失败: %w", c.name, name, err)
	}
	var result mcpCallToolResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("[MCP:%s] 解析结果失败: %w", c.name, err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("[MCP:%s] 工具 %s 错误: %s", c.name, name, result.Error.Message)
	}
	var texts []string
	if result.Result != nil {
		for _, block := range result.Result.Content {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func (c *StdioMCPClient) initialize() error {
	initReq := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name": "sirenagent", "version": "1.0.0",
			},
		},
	}
	_, err := c.sendRequest(initReq)
	if err != nil {
		return err
	}
	notif := mcpJSONRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"}
	nd, _ := json.Marshal(notif)
	h := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(nd))
	c.stdin.Write([]byte(h + string(nd)))
	return nil
}

func (c *StdioMCPClient) listTools() ([]MCPToolInfo, error) {
	req := mcpJSONRPCRequest{JSONRPC: "2.0", Method: "tools/list", Params: map[string]interface{}{}}
	respBody, err := c.sendRequest(req)
	if err != nil {
		return nil, err
	}
	var result mcpListToolsResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析工具列表失败: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("获取工具列表错误: %s", result.Error.Message)
	}
	if result.Result == nil {
		return nil, fmt.Errorf("工具列表为空")
	}
	return result.Result.Tools, nil
}

func (c *StdioMCPClient) sendRequest(req mcpJSONRPCRequest) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if req.ID == 0 {
		req.ID = c.nextID
		c.nextID++
	}
	reqData, _ := json.Marshal(req)
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(reqData))
	if _, err := c.stdin.Write([]byte(header + string(reqData))); err != nil {
		return nil, fmt.Errorf("写入 stdin 失败: %w", err)
	}
	return c.readResponseLocked()
}

func (c *StdioMCPClient) readResponseLocked() (json.RawMessage, error) {
	timeout := 30 * time.Second
	deadline := time.Now().Add(timeout)
	var contentLength int
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("读取响应超时 (%v)", timeout)
		}
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("读取响应头失败: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if contentLength > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "Content-Length:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				contentLength, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
			}
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("无效的 Content-Length: %d", contentLength)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, fmt.Errorf("读取响应body失败: %w", err)
	}
	return body, nil
}

// ==================== 工具桥接 ====================

// AsMCPTool 将 Stdio MCP 客户端工具包装为本地 Tool
func AsMCPTool(client *StdioMCPClient, info MCPToolInfo) Tool {
	return &mcpBridgeTool{stdioClient: client, info: info}
}

// AsHTTPMCPTool 将 HTTP MCP 客户端工具包装为本地 Tool
func AsHTTPMCPTool(client *HTTPMCPClient, info MCPToolInfo) Tool {
	return &mcpBridgeTool{httpClient: client, info: info}
}

type mcpBridgeTool struct {
	stdioClient *StdioMCPClient
	httpClient  *HTTPMCPClient
	info        MCPToolInfo
}

func (t *mcpBridgeTool) Name() string        { return t.info.Name }
func (t *mcpBridgeTool) Description() string { return t.info.Description }
func (t *mcpBridgeTool) Parameters() map[string]interface{} {
	if t.info.InputSchema == nil {
		return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
	}
	return t.info.InputSchema
}
func (t *mcpBridgeTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	if t.httpClient != nil {
		content, err := t.httpClient.CallTool(t.info.Name, params)
		if err != nil {
			return NewToolError(err), nil
		}
		return NewToolResult(content), nil
	}
	if t.stdioClient != nil {
		content, err := t.stdioClient.CallTool(t.info.Name, params)
		if err != nil {
			return NewToolError(err), nil
		}
		return NewToolResult(content), nil
	}
	return NewToolError(fmt.Errorf("未初始化的 MCP 桥接工具")), nil
}

// ==================== 辅助 ====================

func formatEnv(env map[string]string) []string {
	var r []string
	for k, v := range env {
		r = append(r, fmt.Sprintf("%s=%s", k, v))
	}
	return r
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ==================== JSON-RPC 数据结构 ====================

type mcpJSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type mcpListToolsResult struct {
	Result *struct {
		Tools []MCPToolInfo `json:"tools"`
	} `json:"result,omitempty"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type mcpCallToolResult struct {
	Result *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result,omitempty"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}
