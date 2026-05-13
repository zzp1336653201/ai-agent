package platform

import (
	"sirenagent/internal/config"
)

// NewPlatformAdapters 根据配置创建所有平台适配器
func NewPlatformAdapters(cfg *config.SocialPlatformsConfig) map[string]PlatformAdapter {
	adapters := make(map[string]PlatformAdapter)

	if cfg.Douyin.Enabled {
		adapters["douyin"] = NewDouyinAdapter(cfg.Douyin.AppID, cfg.Douyin.Secret, "")
	}

	if cfg.Xiaohongshu.Enabled {
		adapters["xiaohongshu"] = NewXiaohongshuAdapter(cfg.Xiaohongshu.AppID, cfg.Xiaohongshu.Secret)
	}

	if cfg.VideoChannel.Enabled {
		adapters["video_channel"] = NewVideoChannelAdapter(cfg.VideoChannel.AppID, cfg.VideoChannel.Secret)
	}

	return adapters
}
