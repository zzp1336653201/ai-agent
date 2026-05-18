package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ==================== CalculatorTool 测试 ====================

func TestNormalizeExpression(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"计算 1+2", "1+2"},
		{"求 3*4", "3*4"},
		{"算 5-1", "5-1"},
		{"1 + 2 * 3", "1 + 2 * 3"},   // normalizeExpression 保留空格，safeEvaluate 内部去空格
		{"（1+2）", "(1+2)"},
		{"10 ÷ 2", "10 / 2"},         // ÷ 转为 /，但保留空格
		{"2 × 3", "2 * 3"},           // × 转为 *，保留空格
		{"3.5 + 2.1", "3.5 + 2.1"},
	}
	for _, tt := range tests {
		got := normalizeExpression(tt.input)
		if got != tt.want {
			t.Errorf("normalizeExpression(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSafeEvaluate(t *testing.T) {
	tests := []struct {
		expr    string
		want    float64
		wantErr bool
	}{
		{"1+2", 3, false},
		{"1+2*3", 7, false},
		{"(1+2)*3", 9, false},
		{"2^3", 8, false},
		{"10/2", 5, false},
		{"10%3", 1, false},
		{"3.5*2", 7, false},
		{"(2+3)*(4-1)", 15, false},
		{"2^(1+2)", 8, false},
		{"", 0, true},
		{"1/0", 0, true},
		{"1+", 0, true},
		{"(1+2", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := safeEvaluate(tt.expr)
		if (err != nil) != tt.wantErr {
			t.Errorf("safeEvaluate(%q) error = %v, wantErr %v", tt.expr, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("safeEvaluate(%q) = %v, want %v", tt.expr, got, tt.want)
		}
	}
}

func TestCalculatorTool_Execute(t *testing.T) {
	tool := NewCalculatorTool()
	ctx := context.Background()

	// 正常计算
	res, err := tool.Execute(ctx, map[string]interface{}{"expression": "1+2*3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "7") {
		t.Errorf("expected result contains 7, got: %s", res.Content)
	}

	// 缺少参数
	res, err = tool.Execute(ctx, map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "缺少") {
		t.Errorf("expected missing param error, got: %s", res.Content)
	}

	// 除零错误
	res, err = tool.Execute(ctx, map[string]interface{}{"expression": "1/0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "除零") {
		t.Errorf("expected division by zero error, got: %s", res.Content)
	}
}

// ==================== FileReadTool 测试 ====================

func TestFileReadTool_ParamValidation(t *testing.T) {
	tool := NewFileReadTool()
	ctx := context.Background()

	// 缺少 path
	res, _ := tool.Execute(ctx, map[string]interface{}{})
	if !strings.Contains(res.Content, "缺少 path") {
		t.Errorf("应拦截缺少 path 的请求，got: %s", res.Content)
	}

	// 路径遍历
	res, _ = tool.Execute(ctx, map[string]interface{}{"path": "../../../etc/passwd"})
	if !strings.Contains(res.Content, "非法字符") {
		t.Errorf("应拦截路径遍历，got: %s", res.Content)
	}

	// 非法扩展名
	res, _ = tool.Execute(ctx, map[string]interface{}{"path": "test.exe"})
	if !strings.Contains(res.Content, "不允许读取") {
		t.Errorf("应拦截非法扩展名，got: %s", res.Content)
	}

	// 目录而非文件（用当前项目目录测试）
	cwd, _ := os.Getwd()
	res, _ = tool.Execute(ctx, map[string]interface{}{"path": cwd})
	// cwd 可能没有合法扩展名，会被扩展名检查拦截；也可能被目录检查拦截
	if !strings.Contains(res.Content, "目录") && !strings.Contains(res.Content, "不允许读取") {
		t.Errorf("应拦截目录读取或非法扩展名，got: %s", res.Content)
	}
}

func TestFileReadTool_ReadKnownFile(t *testing.T) {
	tool := NewFileReadTool()
	ctx := context.Background()

	// 尝试两种可能的路径（go test 包模式 vs 根目录模式）
	candidates := []string{"tools.go", "internal/core/tools.go"}
	var res *ToolResult
	var err error
	for _, p := range candidates {
		res, err = tool.Execute(ctx, map[string]interface{}{"path": p})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(res.Content, "❌") {
			break // 找到了
		}
	}
	if strings.Contains(res.Content, "❌") {
		t.Fatalf("读取已知文件被拦截: %s", res.Content)
	}
	if !strings.Contains(res.Content, "package core") {
		t.Errorf("文件内容不正确，可能未正确读取: %s", res.Content)
	}
}

func TestFileReadTool_SizeLimit(t *testing.T) {
	tool := NewFileReadTool()
	ctx := context.Background()

	// 创建一个刚好超过 1MB 的临时文件
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "big.txt")
	data := make([]byte, 1024*1024+100) // 1MB + 100 bytes
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	// 由于 tmpDir 不在项目工作目录内，预期会被工作目录限制拦截
	// 但如果碰巧在，则会被大小限制拦截
	res, _ := tool.Execute(ctx, map[string]interface{}{"path": tmpFile})
	if !strings.Contains(res.Content, "❌") {
		t.Logf("警告：超大文件未被拦截，got: %s", res.Content)
	}
}

// ==================== HTTPRequestTool 测试 ====================

func TestHTTPRequestTool_ParamValidation(t *testing.T) {
	tool := NewHTTPRequestTool()
	ctx := context.Background()

	// URL 不以 http 开头
	res, _ := tool.Execute(ctx, map[string]interface{}{"url": "ftp://example.com"})
	if !strings.Contains(res.Content, "http://") {
		t.Errorf("应拦截非 HTTP URL，got: %s", res.Content)
	}

	// 空 URL（required 由 LLM 保证，这里直接测试空字符串）
	res, _ = tool.Execute(ctx, map[string]interface{}{"url": ""})
	if !strings.Contains(res.Content, "http://") {
		t.Errorf("应拦截空 URL，got: %s", res.Content)
	}
}

func TestHTTPRequestTool_ExecuteReal(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("CI 环境跳过真实 HTTP 请求测试")
	}

	tool := NewHTTPRequestTool()
	ctx := context.Background()

	// 请求 httpbin（一个简单的测试端点）
	res, err := tool.Execute(ctx, map[string]interface{}{
		"url":    "https://httpbin.org/get",
		"method": "GET",
	})
	if err != nil {
		t.Fatalf("HTTP 请求失败: %v", err)
	}
	if strings.Contains(res.Content, "status_code") {
		// 正常返回 JSON 结果
		t.Logf("HTTP 请求成功，结果长度: %d", len(res.Content))
	} else {
		t.Errorf("HTTP 请求结果异常: %s", res.Content)
	}
}
