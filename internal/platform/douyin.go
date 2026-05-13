package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ==================== 抖音平台适配器 ====================
// 对接抖音开放平台 API
// 参考: https://open.douyin.com/

type DouyinAdapter struct {
	clientID     string
	clientSecret string
	redirectURI  string
	client       *http.Client
}

func NewDouyinAdapter(clientID, clientSecret, redirectURI string) *DouyinAdapter {
	return &DouyinAdapter{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		client:       &http.Client{Timeout: 30 * time.Second},
	}
}

func (d *DouyinAdapter) Name() string { return "douyin" }

// GetAuthURL 获取 OAuth 授权链接
func (d *DouyinAdapter) GetAuthURL(state string) string {
	return fmt.Sprintf(
		"https://open.douyin.com/platform/oauth/connect/?client_id=%s&redirect_uri=%s&response_type=code&scope=video.create,video.data&state=%s",
		d.clientID, d.redirectURI, state,
	)
}

// ExchangeToken 用授权码换取 Access Token
func (d *DouyinAdapter) ExchangeToken(code string) (*TokenInfo, error) {
	url := "https://open.douyin.com/oauth/access_token/"
	reqBody := map[string]string{
		"client_id":     d.clientID,
		"client_secret": d.clientSecret,
		"code":          code,
		"grant_type":    "authorization_code",
	}
	jsonData, _ := json.Marshal(reqBody)

	resp, err := d.client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		OpenID       string `json:"open_id"`
		Error        struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		} `json:"error"`
	}

	json.Unmarshal(body, &result)
	if result.Error.Code != 0 {
		return nil, fmt.Errorf("抖音 API 错误 [%d]: %s", result.Error.Code, result.Error.Msg)
	}

	return &TokenInfo{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
		OpenID:       result.OpenID,
	}, nil
}

// RefreshToken 刷新 Token
func (d *DouyinAdapter) RefreshToken(refreshToken string) (*TokenInfo, error) {
	url := "https://open.douying.com/oauth/refresh_token/"
	reqBody := map[string]string{
		"client_id":     d.clientID,
		"client_secret": d.clientSecret,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	// 类似 ExchangeToken 的实现...
	_ = url
	_ = reqBody

	return nil, fmt.Errorf("待实现")
}

// Publish 发布视频到抖音
func (d *DouyinAdapter) Publish(ctx context.Context, req *PublishRequest, token *TokenInfo) (*PublishResult, error) {
	// 抖音视频发布流程:
	// 1. 上传视频文件到 CDN → 获得 video_id
	// 2. 创建发布任务（含 video_id、标题、标签等）
	// 3. 轮询审核状态

	// Step 1: 上传视频
	videoID, err := d.uploadVideo(ctx, req.MediaURLs[0], token)
	if err != nil {
		return nil, fmt.Errorf("视频上传失败: %w", err)
	}

	// Step 2: 创建发布任务
	publishURL := "https://open.douyin.com/video/create/"
	taskBody := map[string]interface{}{
		"video_id":   videoID,
		"text":       req.Content,
		"micro_app_info": map[string]interface{}{
			"app_name": "SirenAgent",
		},
	}
	// 添加标签
	if len(req.Tags) > 0 {
		taskBody["tags"] = req.Tags
	}
	jsonData, _ := json.Marshal(taskBody)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", publishURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("access-token", token.AccessToken)

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var createResult struct {
		TaskID struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
		Error  struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		} `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&createResult)

	if createResult.Error.Code != 0 {
		return nil, fmt.Errorf("创建发布任务失败 [%d]: %s", createResult.Error.Code, createResult.Error.Msg)
	}

	return &PublishResult{
		PostID:      createResult.TaskID.TaskID,
		URL:         fmt.Sprintf("https://www.douyin.com/task/%s", createResult.TaskID.TaskID),
		Status:      "review", // 抖音需要审核
		RawData:     map[string]interface{}{"task_id": createResult.TaskID.TaskID},
	}, nil
}

// GetPost 查询帖子状态
func (d *DouyinAdapter) GetPost(ctx context.Context, postID string, token *TokenInfo) (*PostInfo, error) {
	// TODO: 实现帖子详情查询
	return &PostInfo{
		PostID: postID,
		Status: "review",
	}, nil
}

// ListPosts 列出用户的帖子
func (d *DouyinAdapter) ListPosts(ctx context.Context, userID string, page, pageSize int, token *TokenInfo) ([]*PostInfo, int64, error) {
	// TODO: 实现列表查询
	return []*PostInfo{}, 0, nil
}

// CreateComment 发表评论
func (d *DouyinAdapter) CreateComment(ctx context.Context, postID, content string, token *TokenInfo) (string, error) {
	// TODO: 实现评论功能
	return "", fmt.Errorf("待实现")
}

// ReplyComment 回复评论
func (d *DouyinAdapter) ReplyComment(ctx context.Context, commentID, content string, token *TokenInfo) (string, error) {
	// TODO: 实现回复评论功能
	return "", fmt.Errorf("待实现")
}

// uploadVideo 视频上传（简化版）
func (d *DouyinAdapter) uploadVideo(ctx context.Context, mediaURL string, token *TokenInfo) (string, error) {
	// 实际流程：先获取上传 URL，再分片上传，最后确认完成
	// 这里返回模拟的 video_id
	return fmt.Sprintf("mock_video_%d", time.Now().Unix()), nil
}
