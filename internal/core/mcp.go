package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ==================== MCP 协议支持 ====================
// MCP (Model Context Protocol) — 让 Agent 能对接外部工具服务
// 当前实现：标准 MCP 协议 over stdio（JSON-RPC 2.0 + Content-Length 头）

// StdioMCPClient 基于 stdio 的 MCP 客户端
// 启动外部进程并通过 stdin/stdout 以 JSON-RPC 2.0 格式通信
type StdioMCPClient struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *bufio.Reader
	mu      sync.Mutex
	nextID  int
	tools   []MCPToolInfo
}

// MCPToolInfo MCP 工具信息
type MCPToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// NewStdioMCPClient 创建 stdio MCP 客户端并初始化
func NewStdioMCPClient(name, command string, args []string, env map[string]string) (*StdioMCPClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)

	// 设置环境变量
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

	// 启动进程
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("[MCP:%s] 启动进程失败: %w", name, err)
	}

	client := &StdioMCPClient{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReaderSize(stdout, 64*1024), // 64KB buffer
		nextID: 1,
	}

	// 初始化时获取工具列表
	tools, err := client.listTools()
	if err != nil {
		// 获取工具列表失败不是致命错误，记录日志
		fmt.Printf("[MCP:%s] 获取工具列表失败: %v\n", name, err)
	} else {
		client.tools = tools
		fmt.Printf("[MCP:%s] 已连接，发现 %d 个工具\n", name, len(tools))
		for _, t := range tools {
			fmt.Printf("  - %s: %s\n", t.Name, truncateString(t.Description, 60))
		}
	}

	return client, nil
}

// Close 关闭 MCP 客户端
func (c *StdioMCPClient) Close() error {
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return nil
}

// ListTools 获取已缓存的工具列表
func (c *StdioMCPClient) ListTools() []MCPToolInfo {
	return c.tools
}

// Name 返回客户端名称
func (c *StdioMCPClient) Name() string {
	return c.name
}

// CallTool 调用远程工具
func (c *StdioMCPClient) CallTool(name string, params map[string]interface{}) (string, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()

	req := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
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

	// 检查错误
	if result.Error != nil {
		return "", fmt.Errorf("[MCP:%s] 工具 %s 返回错误: %s", c.name, name, result.Error.Message)
	}

	// 提取内容
	var texts []string
	if result.Result != nil {
		for _, block := range result.Result.Content {
			texts = append(texts, block.Text)
		}
	}

	return strings.Join(texts, "\n"), nil
}

// listTools 获取远程工具列表
func (c *StdioMCPClient) listTools() ([]MCPToolInfo, error) {
	req := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "tools/list",
		Params:  map[string]interface{}{},
	}

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

// sendRequest 发送 JSON-RPC 请求并读取响应
func (c *StdioMCPClient) sendRequest(req mcpJSONRPCRequest) (json.RawMessage, error) {
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	// 写入 Content-Length 头 + 空行 + JSON body（标准 MCP stdio 协议）
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(reqData))
	msg := header + string(reqData)

	c.mu.Lock()
	if _, err := c.stdin.Write([]byte(msg)); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("写入 stdin 失败: %w", err)
	}
	c.mu.Unlock()

	return c.readResponse()
}

// readResponse 读取 JSON-RPC 响应（标准 MCP stdio 协议：Content-Length 头 + JSON body）
func (c *StdioMCPClient) readResponse() (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	const timeout = 30 * time.Second
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

		// 去掉 \r\n 或 \n 后缀
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// 空行表示 Header 结束
			break
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

	// 精确读取 ContentLength 字节
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, fmt.Errorf("读取响应body失败: %w", err)
	}

	return body, nil
}

// ==================== JSON-RPC 数据结构 ====================

// mcpJSONRPCRequest JSON-RPC 2.0 请求
type mcpJSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// mcpListToolsResult 工具列表响应
type mcpListToolsResult struct {
	Result *struct {
		Tools []MCPToolInfo `json:"tools"`
	} `json:"result,omitempty"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// mcpCallToolResult 工具调用响应
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

// ==================== 工具桥接 ====================

// AsMCPTool 将 MCP 远程工具包装为本地 Tool 接口（桥接模式）
func AsMCPTool(client *StdioMCPClient, info MCPToolInfo) Tool {
	return &mcpBridgeTool{client: client, info: info}
}

type mcpBridgeTool struct {
	client *StdioMCPClient
	info   MCPToolInfo
}

func (t *mcpBridgeTool) Name() string        { return t.info.Name }
func (t *mcpBridgeTool) Description() string { return t.info.Description }
func (t *mcpBridgeTool) Parameters() map[string]interface{} {
	if t.info.InputSchema == nil {
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}
	return t.info.InputSchema
}
func (t *mcpBridgeTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	content, err := t.client.CallTool(t.info.Name, params)
	if err != nil {
		return NewToolError(err), nil
	}
	return NewToolResult(content), nil
}

// ==================== 辅助函数 ====================

func formatEnv(env map[string]string) []string {
	var result []string
	for k, v := range env {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
