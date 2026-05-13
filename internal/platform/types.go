package platform

import (
	"context"
	"fmt"
	"time"

	"sirenagent/internal/model"
)

// ==================== 社交媒体平台抽象层 ====================
// 对应职位要求：对接社交媒体平台接口（抖音/小红书/视频号）
// 设计模式：策略模式 + 工厂模式，方便扩展新平台

// PlatformAdapter 平台适配器接口 — 统一各平台的差异
type PlatformAdapter interface {
	Name() string
	// 认证相关
	GetAuthURL(state string) string
	ExchangeToken(code string) (*TokenInfo, error)
	RefreshToken(refreshToken string) (*TokenInfo, error)
	// 内容发布
	Publish(ctx context.Context, req *PublishRequest, token *TokenInfo) (*PublishResult, error)
	// 内容查询
	GetPost(ctx context.Context, postID string, token *TokenInfo) (*PostInfo, error)
	ListPosts(ctx context.Context, userID string, page, pageSize int, token *TokenInfo) ([]*PostInfo, int64, error)
	// 互动操作（评论/回复）
	CreateComment(ctx context.Context, postID, content string, token *TokenInfo) (string, error)
	ReplyComment(ctx context.Context, commentID, content string, token *TokenInfo) (string, error)
}

// TokenInfo 平台授权令牌信息
type TokenInfo struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in"`
	Scope        []string  `json:"scope"`
	OpenID       string    `json:"open_id"`
}

func (t *TokenInfo) IsExpired() bool {
	return time.Now().Unix() >= t.ExpiresIn
}

// PublishRequest 发布请求（跨平台统一格式）
type PublishRequest struct {
	Title      string   `json:"title"`                // 标题（可选）
	Content    string   `json:"content"`              // 正文内容
	MediaURLs  []string `json:"media_urls,omitempty"` // 媒体文件 URL 列表
	Tags       []string `json:"tags,omitempty"`       // 标签/话题
	CoverImage string   `json:"cover_image,omitempty"` // 封面图（视频用）
	Visibility string   `json:"visibility,omitempty"` // 可见性: public|friends|private
	PublishAt  *time.Time `json:"publish_at,omitempty"` // 定时发布时间
	Topics     []string `json:"topics,omitempty"`      // 话题
}

// PublishResult 发布结果
type PublishResult struct {
	PostID      string            `json:"post_id"`
	URL         string            `json:"url"`          // 帖子链接
	Status      string            `json:"status"`       // pending/review/published/rejected
	PlatformMsg string            `json:"platform_msg"`
	RawData     map[string]interface{} `json:"raw_data"`
}

// PostInfo 帖子详情
type PostInfo struct {
	PostID      string    `json:"post_id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Status      string    `json:"status"`
	ViewCount   int64     `json:"view_count"`
	LikeCount   int64     `json:"like_count"`
	CommentCount int64    `json:"comment_count"`
	ShareCount  int64     `json:"share_count"`
	CreatedAt   time.Time `json:"created_at"`
	RawData     map[string]interface{} `json:"raw_data"`
}

// CommentInfo 评论信息
type CommentInfo struct {
	CommentID   string    `json:"comment_id"`
	Content     string    `json:"content"`
	UserNick    string    `json:"user_nick"`
	UserAvatar  string    `json:"user_avatar"`
	LikeCount   int64     `json:"like_count"`
	ReplyCount  int64     `json:"reply_count"`
	CreatedAt   time.Time `json:"created_at"`
}

// ==================== 平台管理服务 ====================

// PlatformManager 平台管理器 — 统一管理所有平台的账号和操作
type PlatformManager struct {
	adapters    map[string]PlatformAdapter
	accountRepo AccountRepository
	postRepo    PostRepository
}

type AccountRepository interface {
	Save(account *model.SocialAccount) error
	GetByUserAndPlatform(userID, platform string) (*model.SocialAccount, error)
	ListByUser(userID string) ([]*model.SocialAccount, error)
	UpdateToken(id, accessToken, refreshToken string, expireAt time.Time) error
}

type PostRepository interface {
	Save(post *model.SocialPost) error
	GetByID(id string) (*model.SocialPost, error)
	ListByUser(userID string, status string, page, size int) ([]*model.SocialPost, int64, error)
	UpdateStatus(id, status, errorMsg, postID string) error
}

func NewPlatformManager(adapters map[string]PlatformAdapter, accountRepo AccountRepository, postRepo PostRepository) *PlatformManager {
	return &PlatformManager{
		adapters:    adapters,
		accountRepo: accountRepo,
		postRepo:    postRepo,
	}
}

// PublishToPlatform 发布内容到指定平台
func (m *PlatformManager) PublishToPlatform(
	ctx context.Context,
	userID, platform string,
	req *PublishRequest,
) (*PublishResult, error) {
	adapter, ok := m.adapters[platform]
	if !ok {
		return nil, fmt.Errorf("不支持的平台: %s", platform)
	}

	// 获取用户在该平台的授权账号
	account, err := m.accountRepo.GetByUserAndPlatform(userID, platform)
	if err != nil {
		return nil, fmt.Errorf("未找到 %s 平台授权: %w", platform, err)
	}

	token := &TokenInfo{
		AccessToken:  account.AccessToken,
		RefreshToken: account.RefreshToken,
	}

	// 检查 token 是否过期并刷新
	if isTokenExpired(account.ExpireAt) && token.RefreshToken != "" {
		newToken, err := adapter.RefreshToken(token.RefreshToken)
		if err != nil {
			return nil, fmt.Errorf("token 刷新失败: %w", err)
		}
		m.accountRepo.UpdateToken(account.ID, newToken.AccessToken, newToken.RefreshToken, calcExpireTime(newToken.ExpiresIn))
		token = newToken
	}

	// 执行发布
	result, err := adapter.Publish(ctx, req, token)
	if err != nil {
		return nil, fmt.Errorf("发布失败: %w", err)
	}

	// 保存发布记录
	post := &model.SocialPost{
		UserID:     userID,
		Platform:   platform,
		Title:      req.Title,
		Content:    req.Content,
		Status:     result.Status,
		PostID:     result.PostID,
		PublishAt:  req.PublishAt,
	}
	m.postRepo.Save(post)

	return result, nil
}

// ListPlatforms 返回支持的平台列表
func (m *PlatformManager) ListPlatforms() []string {
	names := make([]string, 0, len(m.adapters))
	for name := range m.adapters {
		names = append(names, name)
	}
	return names
}

