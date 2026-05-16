# ai-agent
初创的ai智能体项目

搭建后粗略可以调用Tools

模块	状态	详细
ReAct 推理引擎	✅	Thought→Action→Observation 循环，带 Function Calling + 文本回退解析
内置工具 (6个)	✅	web_search, rag_search, social_publish, http_request, calculator, file_read, get_current_datetime
MCP 协议	✅	Stdio + HTTP 双传输模式，已配 Playwright 浏览器工具
多 Agent 编排	✅	Orchestrator 模式，主 Agent 分配子任务给子 Agent
DAG 工作流	✅	7 种节点类型（start/end/llm/tool/condition/parallel/http）
RAG 知识库	✅	ChromaDB / Pgvector 双实现，文档分块+向量化入库
记忆管理	✅	短期内存 + 长期关键词检索
社交媒体	✅	抖音/小红书/视频号适配器
LLM 多供应商	✅	Ollama/OpenAI/豆包
