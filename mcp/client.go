package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Provider AI提供商类型
type Provider string

const (
	ProviderDeepSeek Provider = "deepseek"
	ProviderQwen     Provider = "qwen"
	ProviderCustom   Provider = "custom"
)

// Client AI API配置
type Client struct {
	Provider    Provider
	APIKey      string
	BaseURL     string
	Model       string
	Temperature float64 // AI模型的temperature参数，默认0.3
	Timeout     time.Duration
	UseFullURL  bool // 是否使用完整URL（不添加/chat/completions）
	UseStream   bool // 是否优先使用流式响应
}

func New() *Client {
	// 默认配置
	return &Client{
		Provider:    ProviderDeepSeek,
		BaseURL:     "https://api.deepseek.com/v1",
		Model:       "deepseek-chat",
		Temperature: 0.3, // 降低temperature以减少LLM抖动，提高决策稳定性
		Timeout:     180 * time.Second, // 180秒超时，AI需要分析大量数据和复杂提示词
		UseStream:   true,
	}
}

// SetDeepSeekAPIKey 设置DeepSeek API密钥
// customURL 为空时使用默认URL，customModel 为空时使用默认模型
func (client *Client) SetDeepSeekAPIKey(apiKey string, customURL string, customModel string) {
	client.Provider = ProviderDeepSeek
	client.APIKey = apiKey
	if customURL != "" {
		client.BaseURL = customURL
		log.Printf("🔧 [MCP] DeepSeek 使用自定义 BaseURL: %s", customURL)
	} else {
		client.BaseURL = "https://api.deepseek.com/v1"
		log.Printf("🔧 [MCP] DeepSeek 使用默认 BaseURL: %s", client.BaseURL)
	}
	if customModel != "" {
		client.Model = customModel
		log.Printf("🔧 [MCP] DeepSeek 使用自定义 Model: %s", customModel)
	} else {
		client.Model = "deepseek-chat"
		log.Printf("🔧 [MCP] DeepSeek 使用默认 Model: %s", client.Model)
	}
	// 打印 API Key 的前后各4位用于验证
	if len(apiKey) > 8 {
		log.Printf("🔧 [MCP] DeepSeek API Key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
	}
}

// SetQwenAPIKey 设置阿里云Qwen API密钥
// customURL 为空时使用默认URL，customModel 为空时使用默认模型
func (client *Client) SetQwenAPIKey(apiKey string, customURL string, customModel string) {
	client.Provider = ProviderQwen
	client.APIKey = apiKey
	if customURL != "" {
		client.BaseURL = customURL
		log.Printf("🔧 [MCP] Qwen 使用自定义 BaseURL: %s", customURL)
	} else {
		client.BaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		log.Printf("🔧 [MCP] Qwen 使用默认 BaseURL: %s", client.BaseURL)
	}
	if customModel != "" {
		client.Model = customModel
		log.Printf("🔧 [MCP] Qwen 使用自定义 Model: %s", customModel)
	} else {
		client.Model = "qwen-plus" // 可选: qwen-turbo, qwen-plus, qwen-max
		log.Printf("🔧 [MCP] Qwen 使用默认 Model: %s", client.Model)
	}
	// 打印 API Key 的前后各4位用于验证
	if len(apiKey) > 8 {
		log.Printf("🔧 [MCP] Qwen API Key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
	}
}

// SetCustomAPI 设置自定义OpenAI兼容API
func (client *Client) SetCustomAPI(apiURL, apiKey, modelName string) {
	client.Provider = ProviderCustom
	client.APIKey = apiKey

	// 检查URL是否以#结尾，如果是则使用完整URL（不添加/chat/completions）
	if strings.HasSuffix(apiURL, "#") {
		client.BaseURL = strings.TrimSuffix(apiURL, "#")
		client.UseFullURL = true
	} else {
		client.BaseURL = apiURL
		client.UseFullURL = false
	}

	client.Model = modelName
	client.Timeout = 180 * time.Second // 180秒超时，AI需要分析大量数据和复杂提示词
}

// SetUseStream 设置是否使用流式响应
func (client *Client) SetUseStream(enable bool) {
	client.UseStream = enable
	log.Printf("🔧 [MCP] UseStream set to %v", enable)
}

// SetClient 设置完整的AI配置（高级用户）
func (client *Client) SetClient(Client Client) {
	if Client.Timeout == 0 {
		Client.Timeout = 30 * time.Second
	}
	client = &Client
}

// CallWithMessages 使用 system + user prompt 调用AI API（推荐）
func (client *Client) CallWithMessages(systemPrompt, userPrompt string) (string, error) {
	if client.APIKey == "" {
		return "", fmt.Errorf("AI API密钥未设置，请先调用 SetDeepSeekAPIKey() 或 SetQwenAPIKey()")
	}

	// 若启用流式响应，则使用流式调用（对大 prompt 更稳健）
	if client.UseStream {
		return client.CallWithMessagesStream(systemPrompt, userPrompt, nil)
	}

	// 重试配置
	maxRetries := 5
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		result, err := client.callOnce(systemPrompt, userPrompt)
		if err == nil {
			if attempt > 1 {
				fmt.Printf("✓ AI API重试成功 (第%d次尝试)\n", attempt)
			}
			return result, nil
		}

		// 记录错误信息（每次失败都记录）
		if attempt == 1 {
			log.Printf("❌ [MCP] AI API调用失败 (第1次尝试): %v", err)
			fmt.Printf("⚠️  AI API调用失败: %v\n", err)
		} else {
			log.Printf("❌ [MCP] AI API调用失败 (第%d次尝试): %v", attempt, err)
			fmt.Printf("⚠️  AI API调用失败，正在重试 (%d/%d): %v\n", attempt, maxRetries, err)
		}

		lastErr = err
		// 如果不是网络错误，不重试
		if !isRetryableError(err) {
			log.Printf("❌ [MCP] 错误不可重试，停止重试: %v", err)
			return "", err
		}

		// 重试前等待
		if attempt < maxRetries {
			waitTime := time.Duration(attempt) * 2 * time.Second
			fmt.Printf("⏳ 等待%v后重试...\n", waitTime)
			time.Sleep(waitTime)
		}
	}

	return "", fmt.Errorf("重试%d次后仍然失败: %w", maxRetries, lastErr)
}

// callOnce 单次调用AI API（内部使用）
func (client *Client) callOnce(systemPrompt, userPrompt string) (string, error) {
	// 打印当前 AI 配置
	log.Printf("📡 [MCP] AI 请求配置:")
	log.Printf("   Provider: %s", client.Provider)
	log.Printf("   BaseURL: %s", client.BaseURL)
	log.Printf("   Model: %s", client.Model)
	log.Printf("   UseFullURL: %v", client.UseFullURL)
	if len(client.APIKey) > 8 {
		log.Printf("   API Key: %s...%s", client.APIKey[:4], client.APIKey[len(client.APIKey)-4:])
	}

	// 构建 messages 数组
	messages := []map[string]string{}

	// 如果有 system prompt，添加 system message
	if systemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	// 添加 user message
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userPrompt,
	})

	// 构建请求体
	requestBody := map[string]interface{}{
		"model":       client.Model,
		"messages":    messages,
		"temperature": client.Temperature,  // 使用配置的temperature值，默认0.3
		"max_tokens":  6000, // 增加到10000，确保所有币种都能完整分析
	}

	// 注意：response_format 参数仅 OpenAI 支持，DeepSeek/Qwen 不支持
	// 我们通过强化 prompt 和后处理来确保 JSON 格式正确

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	// 创建HTTP请求
	var url string
	if client.UseFullURL {
		// 使用完整URL，不添加/chat/completions
		url = client.BaseURL
	} else {
		// 默认行为：添加/chat/completions
		url = fmt.Sprintf("%s/chat/completions", client.BaseURL)
	}
	log.Printf("📡 [MCP] 请求 URL: %s", url)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// 根据不同的Provider设置认证方式
	switch client.Provider {
	case ProviderDeepSeek:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", client.APIKey))
	case ProviderQwen:
		// 阿里云Qwen使用API-Key认证
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", client.APIKey))
		// 注意：如果使用的不是兼容模式，可能需要不同的认证方式
	default:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", client.APIKey))
	}

	// 发送请求
	// 创建带超时控制的HTTP客户端，包含更详细的超时设置
	httpClient := &http.Client{
		Timeout: client.Timeout,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: client.Timeout, // 响应头超时与总超时一致
			IdleConnTimeout:       90 * time.Second,
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// 记录详细的错误信息，帮助诊断问题
		log.Printf("❌ [MCP] HTTP请求失败: %v", err)
		return "", fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// 记录详细响应头与 body，便于排查 5xx/522 等网关或上游错误
		log.Printf("❌ [MCP] API返回错误 (status %d). Response headers: %v", resp.StatusCode, resp.Header)
		// 尝试提取常见 trace id 以便上游支持定位
		if trace := resp.Header.Get("x-ds-trace-id"); trace != "" {
			log.Printf("    x-ds-trace-id: %s", trace)
		}
		if reqid := resp.Header.Get("x-request-id"); reqid != "" {
			log.Printf("    x-request-id: %s", reqid)
		}
		if cfid := resp.Header.Get("x-amz-cf-id"); cfid != "" {
			log.Printf("    x-amz-cf-id: %s", cfid)
		}
		log.Printf("    response body: %s", string(body))
		return "", fmt.Errorf("API返回错误 (status %d): %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		// 输出 body 帮助定位解析失败的具体返回内容
		log.Printf("❌ [MCP] 解析响应失败: %v; response body: %s", err, string(body))
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	if len(result.Choices) == 0 {
		log.Printf("❌ [MCP] API返回空响应, response headers: %v", resp.Header)
		return "", fmt.Errorf("API返回空响应")
	}

	return result.Choices[0].Message.Content, nil
}

// isRetryableError 判断错误是否可重试
func isRetryableError(err error) bool {
	errStr := err.Error()
	// 网络错误、超时、EOF等可以重试
	retryableErrors := []string{
		"EOF",
		"timeout",
		"Timeout",
		"deadline exceeded",
		"context deadline",
		"Client.Timeout",
		"connection reset",
		"connection refused",
		"temporary failure",
		"no such host",
		"stream error",   // HTTP/2 stream 错误
		"INTERNAL_ERROR", // 服务端内部错误
	}
	for _, retryable := range retryableErrors {
		if strings.Contains(errStr, retryable) {
			return true
		}
	}
	return false
}

// StreamCallback 流式回调函数类型
type StreamCallback func(chunk string) error

// timeoutReader 带超时的读取器
type timeoutReader struct {
	reader  io.ReadCloser
	timeout time.Duration
}

func (tr *timeoutReader) Read(p []byte) (n int, err error) {
	// 使用带超时的读取，避免AI思考时长时间无数据导致的假超时
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)

	go func() {
		n, err := tr.reader.Read(p)
		ch <- result{n, err}
	}()

	select {
	case res := <-ch:
		return res.n, res.err
	case <-time.After(tr.timeout):
		return 0, fmt.Errorf("读取超时: %v", tr.timeout)
	}
}

func (tr *timeoutReader) Close() error {
	return tr.reader.Close()
}

// CallWithMessagesStream 流式调用AI API，实时推送内容到回调函数
func (client *Client) CallWithMessagesStream(systemPrompt, userPrompt string, callback StreamCallback) (string, error) {
	// 重试配置 - 针对HTTP/2流错误等网络问题
	maxRetries := 5 // 增加到5次，和非流式调用保持一致
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		result, err := client.callWithMessagesStreamOnce(systemPrompt, userPrompt, callback)
		if err == nil {
			if attempt > 1 {
				log.Printf("✓ [MCP] 流式API重试成功 (第%d次尝试)", attempt)
			}
			return result, nil
		}

		// 记录错误信息
		if attempt == 1 {
			log.Printf("❌ [MCP] 流式API调用失败 (第1次尝试): %v", err)
		} else {
			log.Printf("❌ [MCP] 流式API调用失败 (第%d次尝试): %v", attempt, err)
		}

		lastErr = err

		// 检查是否可重试
		if !isRetryableError(err) {
			log.Printf("❌ [MCP] 流错误不可重试，停止重试: %v", err)
			return "", err
		}

		// 重试前等待，指数退避
		if attempt < maxRetries {
			waitTime := time.Duration(attempt*attempt) * time.Second // 1s, 4s, 9s...
			log.Printf("⏳ [MCP] 等待%v后重试流式调用 (%d/%d)...", waitTime, attempt+1, maxRetries)
			time.Sleep(waitTime)
		}
	}

	return "", fmt.Errorf("流式调用重试%d次后仍然失败: %w", maxRetries, lastErr)
}

// callWithMessagesStreamOnce 执行一次流式调用（内部函数）
func (client *Client) callWithMessagesStreamOnce(systemPrompt, userPrompt string, callback StreamCallback) (string, error) {
	if client.APIKey == "" {
		return "", fmt.Errorf("AI API密钥未设置，请先调用 SetDeepSeekAPIKey() 或 SetQwenAPIKey()")
	}

	// 构建 messages 数组
	messages := []map[string]string{}

	// 如果有 system prompt，添加 system message
	if systemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	// 添加 user message
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userPrompt,
	})

	// 构建请求体（启用流式）
	requestBody := map[string]interface{}{
		"model":       client.Model,
		"messages":    messages,
		"temperature": client.Temperature, // 使用配置的temperature值，默认0.3
		// 限制最大生成长度，防止思维链过长导致超时和费用浪费
		"max_tokens": 6000,
		"stream":     true, // 启用流式响应
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	// 创建HTTP请求
	var url string
	if client.UseFullURL {
		url = client.BaseURL
	} else {
		url = fmt.Sprintf("%s/chat/completions", client.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// 根据不同的Provider设置认证方式
	switch client.Provider {
	case ProviderDeepSeek:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", client.APIKey))
	case ProviderQwen:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", client.APIKey))
	default:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", client.APIKey))
	}

	// 发送请求 - 使用更短的超时避免长时间等待
	httpClient := &http.Client{
		Timeout: client.Timeout,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       60 * time.Second,
			DisableKeepAlives:     false, // 允许keep-alive但处理连接问题
		},
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("❌ [MCP] API返回错误 (status %d). Response headers: %v", resp.StatusCode, resp.Header)
		if trace := resp.Header.Get("x-ds-trace-id"); trace != "" {
			log.Printf("    x-ds-trace-id: %s", trace)
		}
		if reqid := resp.Header.Get("x-request-id"); reqid != "" {
			log.Printf("    x-request-id: %s", reqid)
		}
		if cfid := resp.Header.Get("x-amz-cf-id"); cfid != "" {
			log.Printf("    x-amz-cf-id: %s", cfid)
		}
		log.Printf("    response body: %s", string(body))
		return "", fmt.Errorf("API返回错误 (status %d): %s", resp.StatusCode, string(body))
	}

	// 读取流式响应
	var fullContent strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // 1MB buffer

	// 使用更长的读取超时，避免AI思考时的中断
	resp.Body = &timeoutReader{reader: resp.Body, timeout: 60 * time.Second}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// SSE 格式: "data: {...}"
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			// 解析 JSON
			var streamResp struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
				previewLen := 100
				if len(data) < previewLen {
					previewLen = len(data)
				}
				log.Printf("⚠️ [MCP] 解析SSE数据失败，跳过: %v, 数据: %s", err, data[:previewLen])
				continue // 跳过解析错误的行
			}

			if len(streamResp.Choices) > 0 {
				chunk := streamResp.Choices[0].Delta.Content
				if chunk != "" {
					fullContent.WriteString(chunk)
					// 调用回调函数推送内容
					if callback != nil {
						if err := callback(chunk); err != nil {
							return fullContent.String(), err
						}
					}
				}

				// 检查是否完成
				if streamResp.Choices[0].FinishReason != "" {
					break
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("❌ [MCP] 流式响应扫描器错误: %v", err)
		return fullContent.String(), fmt.Errorf("读取流式响应失败: %w", err)
	}

	// 检查响应是否为空
	if fullContent.Len() == 0 {
		log.Printf("⚠️ [MCP] 流式响应为空，可能是API服务问题")
		return "", fmt.Errorf("API返回空响应")
	}

	log.Printf("✓ [MCP] 流式响应完成，总长度: %d字符", fullContent.Len())

	// 记录完整响应用于调试
	fullResponse := fullContent.String()
	if len(fullResponse) < 100 {
		log.Printf("⚠️ [MCP] AI响应过短，可能不完整: %q", fullResponse)
		if strings.TrimSpace(fullResponse) == "=== AI思维链分析 ===" {
			return "", fmt.Errorf("AI响应不完整：只返回了标题，没有实际内容")
		}
	} else {
		log.Printf("✓ [MCP] 收到AI完整响应 (长度: %d字符)", len(fullResponse))
		previewLen := 200
		if len(fullResponse) < previewLen {
			previewLen = len(fullResponse)
		}
		log.Printf("📝 [MCP] AI响应预览: %q", fullResponse[:previewLen])
	}

	return fullResponse, nil
}
