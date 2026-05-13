package llm

// NewLLMProvider 工厂方法：根据配置创建对应的 LLM 提供者
func NewLLMProvider(provider, endpoint, apiKey, model string) LLMProvider {
	switch provider {
	case "openai", "doubao":
		return NewOpenAIProvider(endpoint, apiKey, model)
	default: // ollama
		return NewOllamaProvider(endpoint)
	}
}
