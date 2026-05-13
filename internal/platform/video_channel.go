package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ==================== 微信视频号平台适配器 ====================
// 对接微信开放平台视频号 API
// 参考: https://developers.weixin.qq.com/doc/offiaccount/Channels/Channel.html

type VideoChannelAdapter struct {
	appID     string
	appSecret string
	client    *http.Client
}

func NewVideoChannelAdapter(appID, appSecret string) *VideoChannelAdapter {
	return &VideoChannelAdapter{
		appID:     appID,
		appSecret: appSecret,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (v *VideoChannelAdapter) Name() string { return "video_channel" }

func (v *VideoChannelAdapter) GetAuthURL(state string) string {
	return fmt.Sprintf(
		"https://open.weixin.qq.com/connect/oauth2/authorize?appid=%s&redirect_uri=%s&response_type=code&scope=snsapi_userinfo&state=%s#wechat_redirect",
		v.appID, "", state,
	)
}

// ExchangeToken 用授权码换取 Token（同时获取 Access Token）
func (v *VideoChannelAdapter) ExchangeToken(code string) (*TokenInfo, error) {
	url := fmt.Sprintf(
		"https://api.weixin.qq.com/sns/oauth2/access_token?appid=%s&secret=%s&code=%s&grant_type=authorization_code",
		v.appID, v.appSecret, code,
	)

	resp, err := v.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		OpenID       string `json:"openid"`
		UnionID      string `json:"unionid"`
		ErrCode      int    `json:"errcode"`
		ErrMsg       string `json:"errmsg"`
	}
	json.Unmarshal(body, &result)

	if result.ErrCode != 0 {
		return nil, fmt.Errorf("微信 API 错误 [%d]: %s", result.ErrCode, result.ErrMsg)
	}

	return &TokenInfo{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
		OpenID:       result.OpenID,
	}, nil
}

func (v *VideoChannelAdapter) RefreshToken(refreshToken string) (*TokenInfo, error) {
	return nil, fmt.Errorf("待实现")
}

// Publish 发布内容到视频号
func (v *VideoChannelAdapter) Publish(ctx context.Context, req *PublishRequest, token *TokenInfo) (*PublishResult, error) {
	// 视频号发布流程：
	// 1. 获取 component_access_token（或使用用户 token）
	// 2. 上传素材 → 获取 media_id
	// 3. 发布草稿
	// 4. 提交审核

	mediaURL := ""
	if len(req.MediaURLs) > 0 {
		mediaURL = req.MediaURLs[0]
	}

	// Step 1: 获取 access_token
	tokenURL := fmt.Sprintf(
		"https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s",
		v.appID, v.appSecret,
	)

	tokenResp, err := v.client.Get(tokenURL)
	if err != nil {
		return nil, fmt.Errorf("获取 access_token 失败: %w", err)
	}
	defer tokenResp.Body.Close()

	var tokenResult struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
	}
	json.NewDecoder(tokenResp.Body).Decode(&tokenResult)
	if tokenResult.ErrCode != 0 {
		return nil, fmt.Errorf("获取 token 失败")
	}

	accessToken := tokenResult.AccessToken

	// Step 2 & 3: 创建发布任务
	publishURL := "https://api.weixin.qq.com/cgi-bin/freepublish/submit"
	taskBody := map[string]interface{}{
		"media_id": mediaURL, // 实际需要先上传获得 media_id
		"desc":     req.Content + "\n" + joinStrings(req.Topics, " "),
	}
	jsonData, _ := json.Marshal(taskBody)

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s?access_token=%s", publishURL, accessToken),
		bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("发布请求失败: %w", err)
	}
	defer resp.Body.Close()

	var pubResult struct {
		PublishID string `json:"publish_id"`
		MsgID     string `json:"msg_id"`
		ErrCode   int    `json:"errcode"`
	}
	json.NewDecoder(resp.Body).Decode(&pubResult)

	if pubResult.ErrCode != 0 {
		return nil, fmt.Errorf("视频号发布失败 [%d]", pubResult.ErrCode)
	}

	return &PublishResult{
		PostID:      pubResult.PublishID,
		URL:         fmt.Sprintf("https://channels.weixin.qq.com/pages/%s/%s", v.appID, pubResult.PublishID),
		Status:      "review", // 视频号需审核
		RawData:     map[string]interface{}{"msg_id": pubResult.MsgID},
	}, nil
}

func (v *VideoChannelAdapter) GetPost(ctx context.Context, postID string, token *TokenInfo) (*PostInfo, error) {
	return &PostInfo{PostID: postID}, nil
}

func (v *VideoChannelAdapter) ListPosts(ctx context.Context, userID string, page, pageSize int, token *TokenInfo) ([]*PostInfo, int64, error) {
	return []*PostInfo{}, 0, nil
}

func (v *VideoChannelAdapter) CreateComment(ctx context.Context, postID, content string, token *TokenInfo) (string, error) { return "", nil }
func (v *VideoChannelAdapter) ReplyComment(ctx context.Context, commentID, content string, token *TokenInfo) (string, error) { return "", nil }

// ==================== 辅助函数 ====================

func isVideoFile(url string) bool {
	videoExts := []string{".mp4", ".mov", ".avi", ".mkv", ".webm"}
	for _, ext := range videoExts {
		if strings.Contains(url, ext) { return true }
	}
	return false
}

func calcExpireTime(expiresInSec int) time.Time {
	return time.Now().Add(time.Duration(expiresInSec) * time.Second - 5*time.Minute)
}

func isTokenExpired(expireAt time.Time) bool {
	return !expireAt.IsZero() && time.Now().After(expireAt.Add(-5*time.Minute))
}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 { result += sep }
		result += s
	}
	return result
}
