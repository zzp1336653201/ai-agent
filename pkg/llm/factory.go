package llm

// NewLLMProvider 工厂方法：根据配置创建对应的 LLM 提供者
func NewLLMProvider(provider, endpoint, apiKey string) LLMProvider {
	switch provider {
	case "openai", "doubao":
		return NewOpenAIProvider(endpoint, apiKey)
	default: // ollama
		return NewOllamaProvider(endpoint)
	}
}
