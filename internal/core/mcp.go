package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/rpc"
)

// ==================== MCP 协议支持 ====================
// MCP (Model Context Protocol) — 让 Agent 能对接外部工具服务
// 对应职位要求：MCP 协议、工具调用

// MCPServer MCP 服务端 — 暴露工具给外部 Agent 调用
type MCPServer struct {
	name   string
	tools  map[string]Tool
	engine *AgentEngine
}

func NewMCPServer(name string, engine *AgentEngine) *MCPServer {
	return &MCPServer{
		name:  name,
		tools: make(map[string]Tool),
		engine: engine,
	}
}

// RegisterTool 注册 MCP 工具
func (s *MCPServer) RegisterTool(tool Tool) {
	s.tools[tool.Name()] = tool
}

// ListTools 列出所有可用工具（MCP 协议方法）
type ListToolsRequest struct{}
type ListToolsResponse struct {
	Tools []MCPToolInfo `json:"tools"`
}
type MCPToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

func (s *MCPServer) ListTools(_ *ListToolsRequest, reply *ListToolsResponse) error {
	tools := make([]MCPToolInfo, 0, len(s.tools))
	for _, tool := range s.tools {
		tools = append(tools, MCPToolInfo{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.Parameters(),
		})
	}
	reply.Tools = tools
	return nil
}

// CallTool 调用工具（MCP 协议方法）
type CallToolRequest struct {
	Arguments map[string]interface{} `json:"arguments"` // 包含 name 和参数
	Name      string                 `json:"name"`
}
type CallToolResponse struct {
	Content []MCPContentBlock `json:"content"`
	IsError bool              `json:"isError"`
}
type MCPContentBlock struct {
	Type string `json:"type"` // text | image | resource
	Text string `json:"text"`
}

func (s *MCPServer) CallTool(req *CallToolRequest, reply *CallToolResponse) error {
	tool, exists := s.tools[req.Name]
	if !exists {
		reply.Content = []MCPContentBlock{{Type: "text", Text: fmt.Errorf("工具 %s 不存在", req.Name).Error()}}
		reply.IsError = true
		return nil
	}

	result, err := tool.Execute(context.Background(), req.Arguments)
	if err != nil {
		reply.Content = []MCPContentBlock{{Type: "text", Text: err.Error()}}
		reply.IsError = true
		return nil
	}

	reply.Content = []MCPContentBlock{
		{Type: "text", Text: result.Content},
	}
	if result.Data != nil {
		dataJSON, _ := json.Marshal(result.Data)
		reply.Content = append(reply.Content, MCPContentBlock{Type: "text", Text: string(dataJSON)})
	}
	return nil
}

// StartRPCServer 启动 RPC 服务（简化版，实际应使用 JSON-RPC over stdio/SSE）
func (s *MCPServer) StartRPCServer(address string) error {
	server := rpc.NewServer()
	err := server.RegisterName("MCP", s)
	if err != nil {
		return fmt.Errorf("注册 MCP 服务失败: %w", err)
	}
	// server.Accept(listener) // 实际启动监听
	_ = address
	return fmt.Errorf("RPC server 启动待实现")
}

// ==================== MCP 客户端（连接外部 MCP 服务）====================

// MCPClient MCP 客户端 — 让本 Agent 能调用外部 MCP 工具
type MCPClient struct {
	name    string
	address string
	client  *rpc.Client
	tools   []MCPToolInfo
}

func NewMCPClient(name, address string) (*MCPClient, error) {
	client, err := rpc.DialHTTP("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("连接 MCP 服务失败: &w", err)
	}

	mcp := &MCPClient{
		name:    name,
		address: address,
		client:  client,
	}

	// 获取远程工具列表
	var reply ListToolsResponse
	if err := mcp.client.Call("MCP.ListTools", &ListToolsRequest{}, &reply); err == nil {
		mcp.tools = reply.Tools
	}

	return mcp, nil
}

func (c *MCPClient) GetTools() []MCPToolInfo { return c.tools }
func (c *MCPClient) Name() string            { return c.name }

func (c *MCPClient) CallTool(name string, params map[string]interface{}) (*CallToolResponse, error) {
	req := &CallToolRequest{Name: name, Arguments: params}
	var reply CallToolResponse
	if err := c.client.Call("MCP.CallTool", req, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// AsMCPTool 将 MCP 远程工具包装为本地 Tool 接口（桥接模式）
func AsMCPTool(client *MCPClient, info MCPToolInfo) Tool {
	return &mcpBridgeTool{client: client, info: info}
}

type mcpBridgeTool struct {
	client *MCPClient
	info   MCPToolInfo
}

func (t *mcpBridgeTool) Name() string         { return t.info.Name }
func (t *mcpBridgeTool) Description() string  { return t.info.Description }
func (t *mcpBridgeTool) Parameters() map[string]interface{} { return t.info.InputSchema }

func (t *mcpBridgeTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	resp, err := t.client.CallTool(t.info.Name, params)
	if err != nil {
		return NewToolError(err), nil
	}
	if resp.IsError {
		return NewToolError(fmt.Errorf(resp.Content[0].Text)), nil
	}
	content := ""
	for _, block := range resp.Content {
		content += block.Text + "\n"
	}
	return NewToolResult(content), nil
}
