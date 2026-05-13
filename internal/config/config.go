package config

import "github.com/spf13/viper"

// Config 全局配置结构
type Config struct {
	Server        ServerConfig       `mapstructure:"server"`
	LLM           LLMConfig          `mapstructure:"llm"`
	VectorDB      VectorDBConfig     `mapstructure:"vector_db"`
	Database      DatabaseConfig     `mapstructure:"database"`
	Redis         RedisConfig        `mapstructure:"redis"`
	Agent         AgentConfig        `mapstructure:"agent"`
	SocialPlatforms SocialPlatformsConfig `mapstructure:"social_platforms"`
	MCP           MCPConfig          `mapstructure:"mcp"`
}

type ServerConfig struct {
	Port int `mapstructure:"port"`
}

type LLMConfig struct {
	Provider    string  `mapstructure:"provider"`    // ollama | openai | doubao
	Endpoint    string  `mapstructure:"endpoint"`
	Model       string  `mapstructure:"model"`
	APIKey      string  `mapstructure:"api_key"`
	MaxTokens   int     `mapstructure:"max_tokens"`
	Temperature float64 `mapstructure:"temperature"`
}

type VectorDBConfig struct {
	Provider       string `mapstructure:"provider"`
	Endpoint       string `mapstructure:"endpoint"`
	Collection     string `mapstructure:"collection"`
	EmbeddingModel string `mapstructure:"embedding_model"`
}

type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	DBName   string `mapstructure:"dbname"`
	SSLMode  string `mapstructure:"sslmode"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type AgentConfig struct {
	MaxIterations int `mapstructure:"max_iterations"`
	MemoryTTL     int `mapstructure:"memory_ttl"`
	ToolsTimeout  int `mapstructure:"tools_timeout"`
}

type SocialPlatformsConfig struct {
	Douyin      PlatformConfig `mapstructure:"douyin"`
	Xiaohongshu PlatformConfig `mapstructure:"xiaohongshu"`
	VideoChannel PlatformConfig `mapstructure:"video_channel"`
}

type PlatformConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	AppID   string `mapstructure:"app_id"`
	Secret  string `mapstructure:"app_secret"`
}

type MCPConfig struct {
	Enabled bool              `mapstructure:"enabled"`
	Servers []MCPServerConfig `mapstructure:"servers"`
}

type MCPServerConfig struct {
	Name    string            `mapstructure:"name"`
	Command string            `mapstructure:"command"`
	Args    []string          `mapstructure:"args"`
	Env     map[string]string `mapstructure:"env"`
}

// Load 加载配置文件
func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./configs")

	// 默认配置
	setDefaults()

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func setDefaults() {
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("llm.provider", "ollama")
	viper.SetDefault("llm.endpoint", "http://localhost:11434")
	viper.SetDefault("llm.model", "llama3.2")
	viper.SetDefault("llm.max_tokens", 2048)
	viper.SetDefault("llm.temperature", 0.7)
	viper.SetDefault("vector_db.provider", "chromadb")
	viper.SetDefault("vector_db.endpoint", "http://localhost:8000")
	viper.SetDefault("vector_db.collection", "agent_knowledge")
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.user", "postgres")
	viper.SetDefault("database.password", "postgres")
	viper.SetDefault("database.dbname", "sirenagent")
	viper.SetDefault("redis.addr", "localhost:6379")
	viper.SetDefault("agent.max_iterations", 10)
	viper.SetDefault("agent.memory_ttl", 3600)
	viper.SetDefault("agent.tools_timeout", 30)
}
