package main

import (
	"fmt"
	"log"
	"nofx/mcp"
)

func main() {
	// 创建MCP客户端
	client := mcp.New()
	client.SetDeepSeekAPIKey("your-api-key", "https://api.just2chat.cn", "deepseek-chat")

	// 简单的测试提示词
	systemPrompt := "你是一个AI助手，请简要回答问题。"
	userPrompt := "请说 'Hello World' 然后停止。"

	fmt.Println("🔍 测试AI API调用...")
	response, err := client.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		log.Printf("❌ API调用失败: %v", err)
		return
	}

	fmt.Printf("✓ 收到响应 (长度: %d): %q\n", len(response), response)
}


