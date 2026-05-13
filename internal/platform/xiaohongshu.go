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

// ==================== 小红书平台适配器 ====================
// 对接小红书开放平台 API
// 参考: https://open.xiaohongshu.com/

type XiaohongshuAdapter struct {
	appID     string
	appSecret string
	client    *http.Client
}

func NewXiaohongshuAdapter(appID, appSecret string) *XiaohongshuAdapter {
	return &XiaohongshuAdapter{
		appID:     appID,
		appSecret: appSecret,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (x *XiaohongshuAdapter) Name() string { return "xiaohongshu" }

func (x *XiaohongshuAdapter) GetAuthURL(state string) string {
	return fmt.Sprintf(
		"https://www.xiaohongshu.com/platform/authorize?app_id=%s&redirect_uri=%s&response_type=code&state=%s",
		x.appID, "", state,
	)
}

func (x *XiaohongshuAdapter) ExchangeToken(code string) (*TokenInfo, error) {
	url := "https://api.xiaohongshu.com/oauth2/access_token"
	reqBody := map[string]string{
		"app_id":      x.appID,
		"secret":      x.appSecret,
		"code":        code,
		"grant_type":  "authorization_code",
	}
	jsonData, _ := json.Marshal(reqBody)

	resp, err := x.client.Post(url, "application/json", bytes.NewBuffer(jsonData))
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
		return nil, fmt.Errorf("小红书 API 错误 [%d]: %s", result.Error.Code, result.Error.Msg)
	}

	return &TokenInfo{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
		OpenID:       result.OpenID,
	}, nil
}

func (x *XiaohongshuAdapter) RefreshToken(refreshToken string) (*TokenInfo, error) {
	// TODO: 实现刷新逻辑
	return nil, fmt.Errorf("待实现")
}

// Publish 发布笔记到小红书（图文或视频）
func (x *XiaohongshuAdapter) Publish(ctx context.Context, req *PublishRequest, token *TokenInfo) (*PublishResult, error) {
	// 小红书发布流程：
	// 图文笔记：上传图片 → 创建笔记
	// 视频笔记：上传视频 → 创建笔记

	var mediaType string
	if len(req.MediaURLs) > 0 {
		mediaURL := req.MediaURLs[0]
		if isVideoFile(mediaURL) {
			mediaType = "video"
		} else {
			mediaType = "image"
		}
	}

	noteBody := map[string]interface{}{
		"type":      mediaType,
		"title":     req.Title,
		"content":   req.Content,
		"topics":    req.Topics,
	}

	// 添加媒体文件 ID
	// 实际需要先调用上传接口获取 media_id
	// noteBody["media_ids"] = []string{uploadedMediaID}

	jsonData, _ := json.Marshal(noteBody)

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.xiaohongshu.com/sns/v1/note", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := x.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("发布请求失败: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		NoteID string `json:"note_id"`
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	return &PublishResult{
		PostID:      result.NoteID,
		URL:         result.URL,
		Status:      result.Status,
		RawData:     map[string]interface{}{"note_id": result.NoteID},
	}, nil
}

func (x *XiaohongshuAdapter) GetPost(ctx context.Context, postID string, token *TokenInfo) (*PostInfo, error) {
	return &PostInfo{PostID: postID}, nil
}

func (x *XiaohongshuAdapter) ListPosts(ctx context.Context, userID string, page, pageSize int, token *TokenInfo) ([]*PostInfo, int64, error) {
	return []*PostInfo{}, 0, nil
}

func (x *XiaohongshuAdapter) CreateComment(ctx context.Context, postID, content string, token *TokenInfo) (string, error) { return "", nil }
func (x *XiaohongshuAdapter) ReplyComment(ctx context.Context, commentID, content string, token *TokenInfo) (string, error) { return "", nil }
