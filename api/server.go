package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"nofx/auth"
	"nofx/backtest"
	"nofx/config"
	"nofx/decision"
	"nofx/logger"
	"nofx/manager"
	"nofx/market"
	"nofx/mcp"
	"nofx/review"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Server HTTP API服务器
type Server struct {
	router        *gin.Engine
	traderManager *manager.TraderManager
	database      *config.Database
	port          int
	// SSE 流管理
	streamChannels map[string]chan string // trader_id -> channel
	streamMutex    sync.RWMutex
}

// NewServer 创建API服务器
func NewServer(traderManager *manager.TraderManager, database *config.Database, port int) *Server {
	// 设置为Release模式（减少日志输出）
	gin.SetMode(gin.ReleaseMode)

	router := gin.Default()

	// 启用CORS
	router.Use(corsMiddleware())

	s := &Server{
		router:         router,
		traderManager:  traderManager,
		database:       database,
		port:           port,
		streamChannels: make(map[string]chan string),
	}

	// 设置路由
	s.setupRoutes()

	return s
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

		c.Next()
	}
}

// setupRoutes 设置路由
func (s *Server) setupRoutes() {
	// API路由组
	api := s.router.Group("/api")
	{
		// 健康检查
		api.Any("/health", s.handleHealth)

		// 认证相关路由（无需认证）
		api.POST("/register", s.handleRegister)
		api.POST("/login", s.handleLogin)
		api.POST("/verify-otp", s.handleVerifyOTP)
		api.POST("/complete-registration", s.handleCompleteRegistration)

		// 系统支持的模型和交易所（无需认证）
		api.GET("/supported-models", s.handleGetSupportedModels)
		api.GET("/supported-exchanges", s.handleGetSupportedExchanges)

		// 系统配置（无需认证）
		api.GET("/config", s.handleGetSystemConfig)

		// 系统提示词模板管理（无需认证）
		api.GET("/prompt-templates", s.handleGetPromptTemplates)
		api.GET("/prompt-templates/:name", s.handleGetPromptTemplate)

		// 需要认证的路由
		protected := api.Group("/", s.authMiddleware())
		{
			// AI交易员管理
			protected.GET("/traders", s.handleTraderList)
			protected.GET("/traders/:id/config", s.handleGetTraderConfig)
			protected.POST("/traders", s.handleCreateTrader)
			protected.PUT("/traders/:id", s.handleUpdateTrader)
			protected.DELETE("/traders/:id", s.handleDeleteTrader)
			protected.POST("/traders/:id/start", s.handleStartTrader)
			protected.POST("/traders/:id/stop", s.handleStopTrader)
			protected.PUT("/traders/:id/prompt", s.handleUpdateTraderPrompt)

			// AI模型配置
			protected.GET("/models", s.handleGetModelConfigs)
			protected.PUT("/models", s.handleUpdateModelConfigs)

			// 交易所配置
			protected.GET("/exchanges", s.handleGetExchangeConfigs)
			protected.PUT("/exchanges", s.handleUpdateExchangeConfigs)

			// 用户信号源配置
			protected.GET("/user/signal-sources", s.handleGetUserSignalSource)
			protected.POST("/user/signal-sources", s.handleSaveUserSignalSource)

			// 竞赛总览
			protected.GET("/competition", s.handleCompetition)

			// 指定trader的数据（使用query参数 ?trader_id=xxx）
			protected.GET("/status", s.handleStatus)
			protected.GET("/account", s.handleAccount)
			protected.GET("/positions", s.handlePositions)
			protected.GET("/pending-orders", s.handlePendingOrders)
			protected.GET("/decisions", s.handleDecisions)
			protected.GET("/decisions/latest", s.handleLatestDecisions)
			protected.GET("/statistics", s.handleStatistics)
			protected.GET("/equity-history", s.handleEquityHistory)
			protected.GET("/performance", s.handlePerformance)
			protected.GET("/cycle-check", s.handleCycleCheck)
			protected.GET("/close-reviews", s.handleListCloseReviews)
			protected.GET("/trades/:trade_id/close-review", s.handleGetCloseReview)
			protected.POST("/trades/:trade_id/close-review", s.handleCreateCloseReview)
			protected.POST("/review-loss-trades", s.handleReviewLossTrades)
			protected.POST("/trades/:trade_id/review", s.handleReviewSingleTrade)

			// K线数据
			protected.GET("/klines", s.handleKlines)

			// 回测（基于规则引擎的离线分析，用于评估硬规则和模块化提示词的匹配度）
			protected.POST("/backtest", s.handleBacktest)
			protected.GET("/backtest/status", s.handleBacktestStatus)

			// AI 实时思考流（SSE）
			protected.GET("/ai/stream", s.handleAIStream)
			protected.POST("/ai/decision/stream", s.handleAIDecisionStream)
		}
	}
}

// handleHealth 健康检查
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   c.Request.Context().Value("time"),
	})
}

// handleGetSystemConfig 获取系统配置（客户端需要知道的配置）
func (s *Server) handleGetSystemConfig(c *gin.Context) {
	// 获取默认币种
	defaultCoinsStr, _ := s.database.GetSystemConfig("default_coins")
	var defaultCoins []string
	if defaultCoinsStr != "" {
		json.Unmarshal([]byte(defaultCoinsStr), &defaultCoins)
	}
	if len(defaultCoins) == 0 {
		// 使用硬编码的默认币种
		defaultCoins = []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "XRPUSDT", "DOGEUSDT", "ADAUSDT", "HYPEUSDT"}
	}

	// 获取杠杆配置
	btcEthLeverageStr, _ := s.database.GetSystemConfig("btc_eth_leverage")
	altcoinLeverageStr, _ := s.database.GetSystemConfig("altcoin_leverage")

	btcEthLeverage := 5
	if val, err := strconv.Atoi(btcEthLeverageStr); err == nil && val > 0 {
		btcEthLeverage = val
	}

	altcoinLeverage := 5
	if val, err := strconv.Atoi(altcoinLeverageStr); err == nil && val > 0 {
		altcoinLeverage = val
	}

	c.JSON(http.StatusOK, gin.H{
		"admin_mode":       auth.IsAdminMode(),
		"default_coins":    defaultCoins,
		"btc_eth_leverage": btcEthLeverage,
		"altcoin_leverage": altcoinLeverage,
	})
}

// BacktestRequest 简化版回测请求
type BacktestRequest struct {
	Symbols      []string `json:"symbols"`
	Start        string   `json:"start"` // "2006-01-02 15:04:05"
	End          string   `json:"end"`   // "2006-01-02 15:04:05"
	IntervalMins int      `json:"interval_minutes"`
}

// handleBacktest 提交一个新的回测任务（异步），返回 job_id，前端可轮询进度
func (s *Server) handleBacktest(c *gin.Context) {
	var req BacktestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// 解析时间
	start := time.Now().AddDate(0, 0, -7) // 默认最近7天
	end := time.Now()
	var err error

	if req.Start != "" {
		start, err = time.Parse("2006-01-02 15:04:05", req.Start)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start time format"})
			return
		}
	}
	if req.End != "" {
		end, err = time.Parse("2006-01-02 15:04:05", req.End)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end time format"})
			return
		}
	}

	interval := 3
	if req.IntervalMins > 0 {
		interval = req.IntervalMins
	}

	btParams := backtest.Params{
		Symbols:      req.Symbols,
		StartTime:    start,
		EndTime:      end,
		ScanInterval: time.Duration(interval) * time.Minute,
	}

	job := backtest.StartJob(btParams)

	c.JSON(http.StatusOK, gin.H{
		"job_id":        job.ID,
		"status":        job.Status,
		"total_cycles":  job.TotalCycles,
		"current_cycle": job.CurrentCycle,
		"started_at":    job.StartedAt,
	})
}

// handleBacktestStatus 查询回测任务进度/结果
func (s *Server) handleBacktestStatus(c *gin.Context) {
	jobID := c.Query("job_id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_id is required"})
		return
	}

	job, ok := backtest.GetJob(jobID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	// 构造简洁响应，包含进度和结果摘要
	resp := gin.H{
		"job_id":        job.ID,
		"status":        job.Status,
		"error":         job.Error,
		"total_cycles":  job.TotalCycles,
		"current_cycle": job.CurrentCycle,
		"params":        job.Params,
		"started_at":    job.StartedAt,
		"finished_at":   job.FinishedAt,
	}

	if job.Result != nil && job.Result.Statistics != nil {
		stats := job.Result.Statistics
		resp["statistics"] = stats
		resp["wait_rate"] = stats.WaitRate()
		resp["open_rate"] = stats.OpenRate()
		resp["top_waitReasons"] = stats.TopWaitReasons(10)
		resp["top_ruleFails"] = stats.TopRuleFailures(10)
	}

	c.JSON(http.StatusOK, resp)
}

// getTraderFromQuery 从query参数获取trader
func (s *Server) getTraderFromQuery(c *gin.Context) (*manager.TraderManager, string, error) {
	userID := c.GetString("user_id")
	traderID := c.Query("trader_id")

	// 确保用户的交易员已加载到内存中
	err := s.traderManager.LoadUserTraders(s.database, userID)
	if err != nil {
		log.Printf("⚠️ 加载用户 %s 的交易员失败: %v", userID, err)
	}

	if traderID == "" {
		// 如果没有指定trader_id，返回该用户的第一个trader
		ids := s.traderManager.GetTraderIDs()
		if len(ids) == 0 {
			return nil, "", fmt.Errorf("没有可用的trader")
		}

		// 获取用户的交易员列表，优先返回用户自己的交易员
		userTraders, err := s.database.GetTraders(userID)
		if err == nil && len(userTraders) > 0 {
			traderID = userTraders[0].ID
		} else {
			traderID = ids[0]
		}
	}

	return s.traderManager, traderID, nil
}

// AI交易员管理相关结构体
type CreateTraderRequest struct {
	Name                 string  `json:"name" binding:"required"`
	AIModelID            string  `json:"ai_model_id" binding:"required"`
	ExchangeID           string  `json:"exchange_id" binding:"required"`
	InitialBalance       float64 `json:"initial_balance"`
	BTCETHLeverage       int     `json:"btc_eth_leverage"`
	AltcoinLeverage      int     `json:"altcoin_leverage"`
	TradingSymbols       string  `json:"trading_symbols"`
	CustomPrompt         string  `json:"custom_prompt"`
	OverrideBasePrompt   bool    `json:"override_base_prompt"`
	SystemPromptTemplate string  `json:"system_prompt_template"` // 系统提示词模板名称
	IsCrossMargin        *bool   `json:"is_cross_margin"`        // 指针类型，nil表示使用默认值true
	UseCoinPool          bool    `json:"use_coin_pool"`
	UseOITop             bool    `json:"use_oi_top"`
}

type ModelConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Enabled      bool   `json:"enabled"`
	APIKey       string `json:"apiKey,omitempty"`
	CustomAPIURL string `json:"customApiUrl,omitempty"`
}

type ExchangeConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "cex" or "dex"
	Enabled   bool   `json:"enabled"`
	APIKey    string `json:"apiKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	Testnet   bool   `json:"testnet,omitempty"`
}

type UpdateModelConfigRequest struct {
	Models map[string]struct {
		Enabled         bool   `json:"enabled"`
		APIKey          string `json:"api_key"`
		CustomAPIURL    string `json:"custom_api_url"`
		CustomModelName string `json:"custom_model_name"`
	} `json:"models"`
}

type UpdateExchangeConfigRequest struct {
	Exchanges map[string]struct {
		Enabled               bool   `json:"enabled"`
		APIKey                string `json:"api_key"`
		SecretKey             string `json:"secret_key"`
		Testnet               bool   `json:"testnet"`
		HyperliquidWalletAddr string `json:"hyperliquid_wallet_addr"`
		AsterUser             string `json:"aster_user"`
		AsterSigner           string `json:"aster_signer"`
		AsterPrivateKey       string `json:"aster_private_key"`
	} `json:"exchanges"`
}

// handleCreateTrader 创建新的AI交易员
func (s *Server) handleCreateTrader(c *gin.Context) {
	userID := c.GetString("user_id")
	var req CreateTraderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 校验杠杆值（放宽限制以支持低频交易策略）
	if req.BTCETHLeverage < 0 || req.BTCETHLeverage > 125 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "主流币杠杆必须在0-125倍之间"})
		return
	}
	if req.AltcoinLeverage < 0 || req.AltcoinLeverage > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "山寨币杠杆必须在0-100倍之间"})
		return
	}

	// 校验交易币种格式
	if req.TradingSymbols != "" {
		symbols := strings.Split(req.TradingSymbols, ",")
		for _, symbol := range symbols {
			symbol = strings.TrimSpace(symbol)
			if symbol != "" && !strings.HasSuffix(strings.ToUpper(symbol), "USDT") {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("无效的币种格式: %s，必须以USDT结尾", symbol)})
				return
			}
		}
	}

	// 生成交易员ID
	traderID := fmt.Sprintf("%s_%s_%d", req.ExchangeID, req.AIModelID, time.Now().Unix())

	// 设置默认值
	isCrossMargin := true // 默认为全仓模式
	if req.IsCrossMargin != nil {
		isCrossMargin = *req.IsCrossMargin
	}

	// 设置杠杆默认值（从系统配置获取）
	btcEthLeverage := 5
	altcoinLeverage := 5
	if req.BTCETHLeverage > 0 {
		btcEthLeverage = req.BTCETHLeverage
	} else {
		// 从系统配置获取默认值
		if btcEthLeverageStr, _ := s.database.GetSystemConfig("btc_eth_leverage"); btcEthLeverageStr != "" {
			if val, err := strconv.Atoi(btcEthLeverageStr); err == nil && val > 0 {
				btcEthLeverage = val
			}
		}
	}
	if req.AltcoinLeverage > 0 {
		altcoinLeverage = req.AltcoinLeverage
	} else {
		// 从系统配置获取默认值
		if altcoinLeverageStr, _ := s.database.GetSystemConfig("altcoin_leverage"); altcoinLeverageStr != "" {
			if val, err := strconv.Atoi(altcoinLeverageStr); err == nil && val > 0 {
				altcoinLeverage = val
			}
		}
	}

	// 设置系统提示词模板默认值
	systemPromptTemplate := "default"
	if req.SystemPromptTemplate != "" {
		systemPromptTemplate = req.SystemPromptTemplate
	}

	// 创建交易员配置（数据库实体）
	trader := &config.TraderRecord{
		ID:                   traderID,
		UserID:               userID,
		Name:                 req.Name,
		AIModelID:            req.AIModelID,
		ExchangeID:           req.ExchangeID,
		InitialBalance:       req.InitialBalance,
		BTCETHLeverage:       btcEthLeverage,
		AltcoinLeverage:      altcoinLeverage,
		TradingSymbols:       req.TradingSymbols,
		UseCoinPool:          req.UseCoinPool,
		UseOITop:             req.UseOITop,
		CustomPrompt:         req.CustomPrompt,
		OverrideBasePrompt:   req.OverrideBasePrompt,
		SystemPromptTemplate: systemPromptTemplate,
		IsCrossMargin:        isCrossMargin,
		ScanIntervalMinutes:  3, // 默认3分钟
		IsRunning:            false,
	}

	// 保存到数据库
	err := s.database.CreateTrader(trader)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("创建交易员失败: %v", err)})
		return
	}

	// 立即将新交易员加载到TraderManager中
	err = s.traderManager.LoadUserTraders(s.database, userID)
	if err != nil {
		log.Printf("⚠️ 加载用户交易员到内存失败: %v", err)
		// 这里不返回错误，因为交易员已经成功创建到数据库
	}

	log.Printf("✓ 创建交易员成功: %s (模型: %s, 交易所: %s)", req.Name, req.AIModelID, req.ExchangeID)

	c.JSON(http.StatusCreated, gin.H{
		"trader_id":   traderID,
		"trader_name": req.Name,
		"ai_model":    req.AIModelID,
		"is_running":  false,
	})
}

// UpdateTraderRequest 更新交易员请求
type UpdateTraderRequest struct {
	Name               string  `json:"name" binding:"required"`
	AIModelID          string  `json:"ai_model_id" binding:"required"`
	ExchangeID         string  `json:"exchange_id" binding:"required"`
	InitialBalance     float64 `json:"initial_balance"`
	BTCETHLeverage     int     `json:"btc_eth_leverage"`
	AltcoinLeverage    int     `json:"altcoin_leverage"`
	TradingSymbols     string  `json:"trading_symbols"`
	CustomPrompt       string  `json:"custom_prompt"`
	OverrideBasePrompt bool    `json:"override_base_prompt"`
	IsCrossMargin      *bool   `json:"is_cross_margin"`
}

// handleUpdateTrader 更新交易员配置
func (s *Server) handleUpdateTrader(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	var req UpdateTraderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 检查交易员是否存在且属于当前用户
	traders, err := s.database.GetTraders(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取交易员列表失败"})
		return
	}

	var existingTrader *config.TraderRecord
	for _, trader := range traders {
		if trader.ID == traderID {
			existingTrader = trader
			break
		}
	}

	if existingTrader == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "交易员不存在"})
		return
	}

	// 设置默认值
	isCrossMargin := existingTrader.IsCrossMargin // 保持原值
	if req.IsCrossMargin != nil {
		isCrossMargin = *req.IsCrossMargin
	}

	// 设置杠杆默认值
	btcEthLeverage := req.BTCETHLeverage
	altcoinLeverage := req.AltcoinLeverage
	if btcEthLeverage <= 0 {
		btcEthLeverage = existingTrader.BTCETHLeverage // 保持原值
	}
	if altcoinLeverage <= 0 {
		altcoinLeverage = existingTrader.AltcoinLeverage // 保持原值
	}

	// 更新交易员配置
	trader := &config.TraderRecord{
		ID:                  traderID,
		UserID:              userID,
		Name:                req.Name,
		AIModelID:           req.AIModelID,
		ExchangeID:          req.ExchangeID,
		InitialBalance:      req.InitialBalance,
		BTCETHLeverage:      btcEthLeverage,
		AltcoinLeverage:     altcoinLeverage,
		TradingSymbols:      req.TradingSymbols,
		CustomPrompt:        req.CustomPrompt,
		OverrideBasePrompt:  req.OverrideBasePrompt,
		IsCrossMargin:       isCrossMargin,
		ScanIntervalMinutes: existingTrader.ScanIntervalMinutes, // 保持原值
		IsRunning:           existingTrader.IsRunning,           // 保持原值
	}

	// 更新数据库
	err = s.database.UpdateTrader(trader)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("更新交易员失败: %v", err)})
		return
	}

	// 重新加载交易员到内存
	err = s.traderManager.LoadUserTraders(s.database, userID)
	if err != nil {
		log.Printf("⚠️ 重新加载用户交易员到内存失败: %v", err)
	}

	log.Printf("✓ 更新交易员成功: %s (模型: %s, 交易所: %s)", req.Name, req.AIModelID, req.ExchangeID)

	c.JSON(http.StatusOK, gin.H{
		"trader_id":   traderID,
		"trader_name": req.Name,
		"ai_model":    req.AIModelID,
		"message":     "交易员更新成功",
	})
}

// handleDeleteTrader 删除交易员
func (s *Server) handleDeleteTrader(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	// 从数据库删除
	err := s.database.DeleteTrader(userID, traderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("删除交易员失败: %v", err)})
		return
	}

	// 如果交易员正在运行，先停止它
	if trader, err := s.traderManager.GetTrader(traderID); err == nil {
		status := trader.GetStatus()
		if isRunning, ok := status["is_running"].(bool); ok && isRunning {
			trader.Stop()
			log.Printf("⏹  已停止运行中的交易员: %s", traderID)
		}
	}

	log.Printf("✓ 交易员已删除: %s", traderID)
	c.JSON(http.StatusOK, gin.H{"message": "交易员已删除"})
}

// handleStartTrader 启动交易员
func (s *Server) handleStartTrader(c *gin.Context) {
	traderID := c.Param("id")

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "交易员不存在"})
		return
	}

	// 检查交易员是否已经在运行
	status := trader.GetStatus()
	if isRunning, ok := status["is_running"].(bool); ok && isRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": "交易员已在运行中"})
		return
	}

	// 启动交易员
	go func() {
		log.Printf("▶️  启动交易员 %s (%s)", traderID, trader.GetName())
		if err := trader.Run(); err != nil {
			log.Printf("❌ 交易员 %s 运行错误: %v", trader.GetName(), err)
		}
	}()

	// 更新数据库中的运行状态
	userID := c.GetString("user_id")
	err = s.database.UpdateTraderStatus(userID, traderID, true)
	if err != nil {
		log.Printf("⚠️  更新交易员状态失败: %v", err)
	}

	log.Printf("✓ 交易员 %s 已启动", trader.GetName())
	c.JSON(http.StatusOK, gin.H{"message": "交易员已启动"})
}

// handleStopTrader 停止交易员
func (s *Server) handleStopTrader(c *gin.Context) {
	traderID := c.Param("id")

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "交易员不存在"})
		return
	}

	// 检查交易员是否正在运行
	status := trader.GetStatus()
	if isRunning, ok := status["is_running"].(bool); ok && !isRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": "交易员已停止"})
		return
	}

	// 停止交易员
	trader.Stop()

	// 更新数据库中的运行状态
	userID := c.GetString("user_id")
	err = s.database.UpdateTraderStatus(userID, traderID, false)
	if err != nil {
		log.Printf("⚠️  更新交易员状态失败: %v", err)
	}

	log.Printf("⏹  交易员 %s 已停止", trader.GetName())
	c.JSON(http.StatusOK, gin.H{"message": "交易员已停止"})
}

// handleUpdateTraderPrompt 更新交易员自定义Prompt
func (s *Server) handleUpdateTraderPrompt(c *gin.Context) {
	traderID := c.Param("id")
	userID := c.GetString("user_id")

	var req struct {
		CustomPrompt       string `json:"custom_prompt"`
		OverrideBasePrompt bool   `json:"override_base_prompt"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 更新数据库
	err := s.database.UpdateTraderCustomPrompt(userID, traderID, req.CustomPrompt, req.OverrideBasePrompt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("更新自定义prompt失败: %v", err)})
		return
	}

	// 如果trader在内存中，更新其custom prompt和override设置
	trader, err := s.traderManager.GetTrader(traderID)
	if err == nil {
		trader.SetCustomPrompt(req.CustomPrompt)
		trader.SetOverrideBasePrompt(req.OverrideBasePrompt)
		log.Printf("✓ 已更新交易员 %s 的自定义prompt (覆盖基础=%v)", trader.GetName(), req.OverrideBasePrompt)
	}

	c.JSON(http.StatusOK, gin.H{"message": "自定义prompt已更新"})
}

// handleGetModelConfigs 获取AI模型配置
func (s *Server) handleGetModelConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	log.Printf("🔍 查询用户 %s 的AI模型配置", userID)
	models, err := s.database.GetAIModels(userID)
	if err != nil {
		log.Printf("❌ 获取AI模型配置失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取AI模型配置失败: %v", err)})
		return
	}
	log.Printf("✅ 找到 %d 个AI模型配置", len(models))

	c.JSON(http.StatusOK, models)
}

// handleUpdateModelConfigs 更新AI模型配置
func (s *Server) handleUpdateModelConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	var req UpdateModelConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 更新每个模型的配置
	for modelID, modelData := range req.Models {
		err := s.database.UpdateAIModel(userID, modelID, modelData.Enabled, modelData.APIKey, modelData.CustomAPIURL, modelData.CustomModelName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("更新模型 %s 失败: %v", modelID, err)})
			return
		}
	}

	// 重新加载该用户的所有交易员，使新配置立即生效
	err := s.traderManager.LoadUserTraders(s.database, userID)
	if err != nil {
		log.Printf("⚠️ 重新加载用户交易员到内存失败: %v", err)
		// 这里不返回错误，因为模型配置已经成功更新到数据库
	}

	log.Printf("✓ AI模型配置已更新: %+v", req.Models)
	c.JSON(http.StatusOK, gin.H{"message": "模型配置已更新"})
}

// handleGetExchangeConfigs 获取交易所配置
func (s *Server) handleGetExchangeConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	log.Printf("🔍 查询用户 %s 的交易所配置", userID)
	exchanges, err := s.database.GetExchanges(userID)
	if err != nil {
		log.Printf("❌ 获取交易所配置失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取交易所配置失败: %v", err)})
		return
	}
	log.Printf("✅ 找到 %d 个交易所配置", len(exchanges))

	c.JSON(http.StatusOK, exchanges)
}

// handleUpdateExchangeConfigs 更新交易所配置
func (s *Server) handleUpdateExchangeConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	var req UpdateExchangeConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 更新每个交易所的配置
	for exchangeID, exchangeData := range req.Exchanges {
		err := s.database.UpdateExchange(userID, exchangeID, exchangeData.Enabled, exchangeData.APIKey, exchangeData.SecretKey, exchangeData.Testnet, exchangeData.HyperliquidWalletAddr, exchangeData.AsterUser, exchangeData.AsterSigner, exchangeData.AsterPrivateKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("更新交易所 %s 失败: %v", exchangeID, err)})
			return
		}
	}

	// 重新加载该用户的所有交易员，使新配置立即生效
	err := s.traderManager.LoadUserTraders(s.database, userID)
	if err != nil {
		log.Printf("⚠️ 重新加载用户交易员到内存失败: %v", err)
		// 这里不返回错误，因为交易所配置已经成功更新到数据库
	}

	log.Printf("✓ 交易所配置已更新: %+v", req.Exchanges)
	c.JSON(http.StatusOK, gin.H{"message": "交易所配置已更新"})
}

// handleGetUserSignalSource 获取用户信号源配置
func (s *Server) handleGetUserSignalSource(c *gin.Context) {
	userID := c.GetString("user_id")
	source, err := s.database.GetUserSignalSource(userID)
	if err != nil {
		// 如果配置不存在，返回空配置而不是404错误
		c.JSON(http.StatusOK, gin.H{
			"coin_pool_url": "",
			"oi_top_url":    "",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"coin_pool_url": source.CoinPoolURL,
		"oi_top_url":    source.OITopURL,
	})
}

// handleSaveUserSignalSource 保存用户信号源配置
func (s *Server) handleSaveUserSignalSource(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		CoinPoolURL string `json:"coin_pool_url"`
		OITopURL    string `json:"oi_top_url"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := s.database.CreateUserSignalSource(userID, req.CoinPoolURL, req.OITopURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("保存用户信号源配置失败: %v", err)})
		return
	}

	log.Printf("✓ 用户信号源配置已保存: user=%s, coin_pool=%s, oi_top=%s", userID, req.CoinPoolURL, req.OITopURL)
	c.JSON(http.StatusOK, gin.H{"message": "用户信号源配置已保存"})
}

// handleTraderList trader列表
func (s *Server) handleTraderList(c *gin.Context) {
	userID := c.GetString("user_id")
	traders, err := s.database.GetTraders(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取交易员列表失败: %v", err)})
		return
	}

	result := make([]map[string]interface{}, 0, len(traders))
	for _, trader := range traders {
		// 获取实时运行状态
		isRunning := trader.IsRunning
		if at, err := s.traderManager.GetTrader(trader.ID); err == nil {
			status := at.GetStatus()
			if running, ok := status["is_running"].(bool); ok {
				isRunning = running
			}
		}

		// 解析候选币种列表
		var candidateCoins []string
		if trader.TradingSymbols != "" {
			symbols := strings.Split(trader.TradingSymbols, ",")
			for _, symbol := range symbols {
				symbol = strings.TrimSpace(symbol)
				if symbol != "" {
					candidateCoins = append(candidateCoins, symbol)
				}
			}
		}
		// 如果没有配置币种，使用默认币种
		if len(candidateCoins) == 0 {
			defaultCoinsStr, _ := s.database.GetSystemConfig("default_coins")
			if defaultCoinsStr != "" {
				json.Unmarshal([]byte(defaultCoinsStr), &candidateCoins)
			}
			if len(candidateCoins) == 0 {
				// 使用硬编码的默认币种
				candidateCoins = []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "XRPUSDT", "DOGEUSDT", "ADAUSDT", "HYPEUSDT"}
			}
		}

		// 返回完整的 AIModelID（如 "admin_deepseek"），不要截断
		// 前端需要完整 ID 来验证模型是否存在（与 handleGetTraderConfig 保持一致）
		result = append(result, map[string]interface{}{
			"trader_id":       trader.ID,
			"trader_name":     trader.Name,
			"ai_model":        trader.AIModelID, // 使用完整 ID
			"exchange_id":     trader.ExchangeID,
			"is_running":      isRunning,
			"initial_balance": trader.InitialBalance,
			"candidate_coins": candidateCoins, // 添加候选币种列表
		})
	}

	c.JSON(http.StatusOK, result)
}

// handleGetTraderConfig 获取交易员详细配置
func (s *Server) handleGetTraderConfig(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	if traderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "交易员ID不能为空"})
		return
	}

	traderConfig, _, _, err := s.database.GetTraderConfig(userID, traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("获取交易员配置失败: %v", err)})
		return
	}

	// 获取实时运行状态
	isRunning := traderConfig.IsRunning
	if at, err := s.traderManager.GetTrader(traderID); err == nil {
		status := at.GetStatus()
		if running, ok := status["is_running"].(bool); ok {
			isRunning = running
		}
	}

	// 返回完整的 AIModelID（如 "admin_deepseek"），不要截断
	// 前端需要完整 ID 来验证模型是否存在
	result := map[string]interface{}{
		"trader_id":              traderConfig.ID,
		"trader_name":            traderConfig.Name,
		"ai_model":               traderConfig.AIModelID, // 使用完整 ID
		"exchange_id":            traderConfig.ExchangeID,
		"initial_balance":        traderConfig.InitialBalance,
		"btc_eth_leverage":       traderConfig.BTCETHLeverage,
		"altcoin_leverage":       traderConfig.AltcoinLeverage,
		"trading_symbols":        traderConfig.TradingSymbols,
		"custom_prompt":          traderConfig.CustomPrompt,
		"override_base_prompt":   traderConfig.OverrideBasePrompt,
		"system_prompt_template": traderConfig.SystemPromptTemplate, // 添加此字段
		"is_cross_margin":        traderConfig.IsCrossMargin,
		"use_coin_pool":          traderConfig.UseCoinPool,
		"use_oi_top":             traderConfig.UseOITop,
		"is_running":             isRunning,
	}

	c.JSON(http.StatusOK, result)
}

// handleStatus 系统状态
func (s *Server) handleStatus(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	status := trader.GetStatus()
	c.JSON(http.StatusOK, status)
}

// handleAccount 账户信息
func (s *Server) handleAccount(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	log.Printf("📊 收到账户信息请求 [%s]", trader.GetName())
	account, err := trader.GetAccountInfo()
	if err != nil {
		log.Printf("❌ 获取账户信息失败 [%s]: %v", trader.GetName(), err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取账户信息失败: %v", err),
		})
		return
	}

	log.Printf("✓ 返回账户信息 [%s]: 净值=%.2f, 可用=%.2f, 盈亏=%.2f (%.2f%%)",
		trader.GetName(),
		account["total_equity"],
		account["available_balance"],
		account["total_pnl"],
		account["total_pnl_pct"])
	c.JSON(http.StatusOK, account)
}

// handlePositions 持仓列表
func (s *Server) handlePositions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	positions, err := trader.GetPositions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取持仓列表失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, positions)
}

// handlePendingOrders 获取待成交的限价单列表
func (s *Server) handlePendingOrders(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	pendingOrders := trader.GetPendingOrders()

	c.JSON(http.StatusOK, gin.H{
		"pending_orders": pendingOrders,
		"count":          len(pendingOrders),
	})
}

// handleDecisions 决策日志列表
func (s *Server) handleDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 获取所有历史决策记录（无限制）
	records, err := trader.GetDecisionLogger().GetLatestRecords(10000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取决策日志失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, records)
}

// handleLatestDecisions 最新决策日志（最近100条，最新的在前）
func (s *Server) handleLatestDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 获取最近100条决策记录（从5条改为100条）
	records, err := trader.GetDecisionLogger().GetLatestRecords(100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取决策日志失败: %v", err),
		})
		return
	}

	// 反转数组，让最新的在前面（用于列表显示）
	// GetLatestRecords返回的是从旧到新（用于图表），这里需要从新到旧
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}

	c.JSON(http.StatusOK, records)
}

// handleStatistics 统计信息
func (s *Server) handleStatistics(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	stats, err := trader.GetDecisionLogger().GetStatistics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取统计信息失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleCompetition 竞赛总览（对比所有trader）
func (s *Server) handleCompetition(c *gin.Context) {
	userID := c.GetString("user_id")

	// 确保用户的交易员已加载到内存中
	err := s.traderManager.LoadUserTraders(s.database, userID)
	if err != nil {
		log.Printf("⚠️ 加载用户 %s 的交易员失败: %v", userID, err)
	}

	competition, err := s.traderManager.GetCompetitionData()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取竞赛数据失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, competition)
}

// handleEquityHistory 收益率历史数据
func (s *Server) handleEquityHistory(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 获取尽可能多的历史数据（几天的数据）
	// 每3分钟一个周期：10000条 = 约20天的数据
	records, err := trader.GetDecisionLogger().GetLatestRecords(10000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取历史数据失败: %v", err),
		})
		return
	}

	// 构建收益率历史数据点
	type EquityPoint struct {
		Timestamp        string  `json:"timestamp"`
		TotalEquity      float64 `json:"total_equity"`      // 账户净值（wallet + unrealized）
		AvailableBalance float64 `json:"available_balance"` // 可用余额
		TotalPnL         float64 `json:"total_pnl"`         // 总盈亏（相对初始余额）
		TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
		PositionCount    int     `json:"position_count"`    // 持仓数量
		MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
		CycleNumber      int     `json:"cycle_number"`
	}

	// 从AutoTrader获取初始余额（用于计算盈亏百分比）
	initialBalance := 0.0
	if status := trader.GetStatus(); status != nil {
		if ib, ok := status["initial_balance"].(float64); ok && ib > 0 {
			initialBalance = ib
		}
	}

	// 如果无法从status获取，且有历史记录，则从第一条记录获取
	if initialBalance == 0 && len(records) > 0 {
		// 第一条记录的equity作为初始余额
		initialBalance = records[0].AccountState.TotalBalance
	}

	// 如果还是无法获取，返回错误
	if initialBalance == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "无法获取初始余额",
		})
		return
	}

	var history []EquityPoint
	for _, record := range records {
		// TotalBalance字段实际存储的是TotalEquity
		totalEquity := record.AccountState.TotalBalance
		// TotalUnrealizedProfit字段实际存储的是TotalPnL（相对初始余额）
		totalPnL := record.AccountState.TotalUnrealizedProfit

		// 计算盈亏百分比
		totalPnLPct := 0.0
		if initialBalance > 0 {
			totalPnLPct = (totalPnL / initialBalance) * 100
		}

		history = append(history, EquityPoint{
			Timestamp:        record.Timestamp.Format("2006-01-02 15:04:05"),
			TotalEquity:      totalEquity,
			AvailableBalance: record.AccountState.AvailableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			PositionCount:    record.AccountState.PositionCount,
			MarginUsedPct:    record.AccountState.MarginUsedPct,
			CycleNumber:      record.CycleNumber,
		})
	}

	c.JSON(http.StatusOK, history)
}

// handlePerformance AI历史表现分析（用于展示AI学习和反思）
func (s *Server) handlePerformance(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 分析全部历史交易表现（用于AI学习与数据面板展示）
	// 传入0表示获取全部记录，不限制周期数量
	performance, err := trader.GetDecisionLogger().AnalyzePerformance(0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("分析历史表现失败: %v", err),
		})
		return
	}

	// 过滤：只显示2024年11月20日之后的交易
	filterDate := time.Date(2025, 11, 20, 0, 0, 0, 0, time.UTC)
	filteredTrades := []logger.TradeOutcome{}
	for _, trade := range performance.RecentTrades {
		if trade.CloseTime.After(filterDate) || trade.CloseTime.Equal(filterDate) {
			filteredTrades = append(filteredTrades, trade)
		}
	}

	// 重新计算统计指标
	performance.RecentTrades = filteredTrades
	performance.TotalTrades = len(filteredTrades)
	performance.WinningTrades = 0
	performance.LosingTrades = 0
	totalWinAmount := 0.0
	totalLossAmount := 0.0

	// 重新计算币种统计
	performance.SymbolStats = make(map[string]*logger.SymbolPerformance)
	bestPnL := -999999.0
	worstPnL := 999999.0
	bestSymbol := ""
	worstSymbol := ""

	for _, trade := range filteredTrades {
		// 分类交易
		if trade.PnL > 0 {
			performance.WinningTrades++
			totalWinAmount += trade.PnL
		} else if trade.PnL < 0 {
			performance.LosingTrades++
			totalLossAmount += trade.PnL
		}

		// 更新币种统计
		if _, exists := performance.SymbolStats[trade.Symbol]; !exists {
			performance.SymbolStats[trade.Symbol] = &logger.SymbolPerformance{
				Symbol: trade.Symbol,
			}
		}
		stats := performance.SymbolStats[trade.Symbol]
		stats.TotalTrades++
		stats.TotalPnL += trade.PnL
		if trade.PnL > 0 {
			stats.WinningTrades++
		} else if trade.PnL < 0 {
			stats.LosingTrades++
		}

		// 更新最佳/最差币种
		if stats.TotalPnL > bestPnL {
			bestPnL = stats.TotalPnL
			bestSymbol = trade.Symbol
		}
		if stats.TotalPnL < worstPnL {
			worstPnL = stats.TotalPnL
			worstSymbol = trade.Symbol
		}
	}

	// 计算平均盈亏
	if performance.WinningTrades > 0 {
		performance.AvgWin = totalWinAmount / float64(performance.WinningTrades)
	}
	if performance.LosingTrades > 0 {
		performance.AvgLoss = totalLossAmount / float64(performance.LosingTrades)
	}

	// 计算胜率
	if performance.TotalTrades > 0 {
		performance.WinRate = (float64(performance.WinningTrades) / float64(performance.TotalTrades)) * 100
	}

	// 计算盈亏比
	// totalLossAmount 是负数，所以取负号得到绝对值
	if totalLossAmount != 0 {
		performance.ProfitFactor = totalWinAmount / (-totalLossAmount)
	} else if totalWinAmount > 0 {
		performance.ProfitFactor = 999.0
	}

	// 计算各币种胜率和平均盈亏
	for _, stats := range performance.SymbolStats {
		if stats.TotalTrades > 0 {
			stats.WinRate = (float64(stats.WinningTrades) / float64(stats.TotalTrades)) * 100
			stats.AvgPnL = stats.TotalPnL / float64(stats.TotalTrades)
		}
	}

	performance.BestSymbol = bestSymbol
	performance.WorstSymbol = worstSymbol

	c.JSON(http.StatusOK, performance)
}

// authMiddleware JWT认证中间件
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 如果是管理员模式，直接使用admin用户
		if auth.IsAdminMode() {
			c.Set("user_id", "admin")
			c.Set("email", "admin@localhost")
			c.Next()
			return
		}

		var token string
		
		// 优先从 Authorization header 获取
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			// 检查Bearer token格式
			tokenParts := strings.Split(authHeader, " ")
			if len(tokenParts) == 2 && tokenParts[0] == "Bearer" {
				token = tokenParts[1]
			}
		}
		
		// 如果 header 中没有，尝试从 query 参数获取（用于 SSE/EventSource）
		if token == "" {
			token = c.Query("token")
		}

		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少Authorization头或token参数"})
			c.Abort()
			return
		}

		// 验证JWT token
		claims, err := auth.ValidateJWT(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效的token: " + err.Error()})
			c.Abort()
			return
		}

		// 将用户信息存储到上下文中
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Next()
	}
}

// handleRegister 处理用户注册请求
func (s *Server) handleRegister(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=6"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 检查邮箱是否已存在
	_, err := s.database.GetUserByEmail(req.Email)
	if err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "邮箱已被注册"})
		return
	}

	// 生成密码哈希
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "密码处理失败"})
		return
	}

	// 生成OTP密钥
	otpSecret, err := auth.GenerateOTPSecret()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "OTP密钥生成失败"})
		return
	}

	// 创建用户（未验证OTP状态）
	userID := uuid.New().String()
	user := &config.User{
		ID:           userID,
		Email:        req.Email,
		PasswordHash: passwordHash,
		OTPSecret:    otpSecret,
		OTPVerified:  false,
	}

	err = s.database.CreateUser(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建用户失败: " + err.Error()})
		return
	}

	// 返回OTP设置信息
	qrCodeURL := auth.GetOTPQRCodeURL(otpSecret, req.Email)
	c.JSON(http.StatusOK, gin.H{
		"user_id":     userID,
		"email":       req.Email,
		"otp_secret":  otpSecret,
		"qr_code_url": qrCodeURL,
		"message":     "请使用Google Authenticator扫描二维码并验证OTP",
	})
}

// handleCompleteRegistration 完成注册（验证OTP）
func (s *Server) handleCompleteRegistration(c *gin.Context) {
	var req struct {
		UserID  string `json:"user_id" binding:"required"`
		OTPCode string `json:"otp_code" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 获取用户信息
	user, err := s.database.GetUserByID(req.UserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}

	// 验证OTP
	if !auth.VerifyOTP(user.OTPSecret, req.OTPCode) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "OTP验证码错误"})
		return
	}

	// 更新用户OTP验证状态
	err = s.database.UpdateUserOTPVerified(req.UserID, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新用户状态失败"})
		return
	}

	// 生成JWT token
	token, err := auth.GenerateJWT(user.ID, user.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成token失败"})
		return
	}

	// 初始化用户的默认模型和交易所配置
	err = s.initUserDefaultConfigs(user.ID)
	if err != nil {
		log.Printf("初始化用户默认配置失败: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"token":   token,
		"user_id": user.ID,
		"email":   user.Email,
		"message": "注册完成",
	})
}

// handleLogin 处理用户登录请求
func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 获取用户信息
	user, err := s.database.GetUserByEmail(req.Email)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
		return
	}

	// 验证密码
	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
		return
	}

	// 检查OTP是否已验证
	if !user.OTPVerified {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":              "账户未完成OTP设置",
			"user_id":            user.ID,
			"requires_otp_setup": true,
		})
		return
	}

	// 返回需要OTP验证的状态
	c.JSON(http.StatusOK, gin.H{
		"user_id":      user.ID,
		"email":        user.Email,
		"message":      "请输入Google Authenticator验证码",
		"requires_otp": true,
	})
}

// handleVerifyOTP 验证OTP并完成登录
func (s *Server) handleVerifyOTP(c *gin.Context) {
	var req struct {
		UserID  string `json:"user_id" binding:"required"`
		OTPCode string `json:"otp_code" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 获取用户信息
	user, err := s.database.GetUserByID(req.UserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}

	// 验证OTP
	if !auth.VerifyOTP(user.OTPSecret, req.OTPCode) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "验证码错误"})
		return
	}

	// 生成JWT token
	token, err := auth.GenerateJWT(user.ID, user.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成token失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":   token,
		"user_id": user.ID,
		"email":   user.Email,
		"message": "登录成功",
	})
}

// initUserDefaultConfigs 为新用户初始化默认的模型和交易所配置
func (s *Server) initUserDefaultConfigs(userID string) error {
	// 注释掉自动创建默认配置，让用户手动添加
	// 这样新用户注册后不会自动有配置项
	log.Printf("用户 %s 注册完成，等待手动配置AI模型和交易所", userID)
	return nil
}

// handleGetSupportedModels 获取系统支持的AI模型列表
func (s *Server) handleGetSupportedModels(c *gin.Context) {
	// 返回系统支持的AI模型（从default用户获取）
	models, err := s.database.GetAIModels("default")
	if err != nil {
		log.Printf("❌ 获取支持的AI模型失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取支持的AI模型失败"})
		return
	}

	c.JSON(http.StatusOK, models)
}

// handleGetSupportedExchanges 获取系统支持的交易所列表
func (s *Server) handleGetSupportedExchanges(c *gin.Context) {
	// 返回系统支持的交易所（从default用户获取）
	exchanges, err := s.database.GetExchanges("default")
	if err != nil {
		log.Printf("❌ 获取支持的交易所失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取支持的交易所失败"})
		return
	}

	c.JSON(http.StatusOK, exchanges)
}

// Start 启动服务器
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("🌐 API服务器启动在 http://localhost%s", addr)
	log.Printf("📊 API文档:")
	log.Printf("  • GET  /api/health           - 健康检查")
	log.Printf("  • GET  /api/traders          - AI交易员列表")
	log.Printf("  • POST /api/traders          - 创建新的AI交易员")
	log.Printf("  • DELETE /api/traders/:id    - 删除AI交易员")
	log.Printf("  • POST /api/traders/:id/start - 启动AI交易员")
	log.Printf("  • POST /api/traders/:id/stop  - 停止AI交易员")
	log.Printf("  • GET  /api/models           - 获取AI模型配置")
	log.Printf("  • PUT  /api/models           - 更新AI模型配置")
	log.Printf("  • GET  /api/exchanges        - 获取交易所配置")
	log.Printf("  • PUT  /api/exchanges        - 更新交易所配置")
	log.Printf("  • GET  /api/status?trader_id=xxx     - 指定trader的系统状态")
	log.Printf("  • GET  /api/account?trader_id=xxx    - 指定trader的账户信息")
	log.Printf("  • GET  /api/positions?trader_id=xxx  - 指定trader的持仓列表")
	log.Printf("  • GET  /api/pending-orders?trader_id=xxx - 指定trader的待成交限价单")
	log.Printf("  • GET  /api/decisions?trader_id=xxx  - 指定trader的决策日志")
	log.Printf("  • GET  /api/decisions/latest?trader_id=xxx - 指定trader的最新决策")
	log.Printf("  • GET  /api/statistics?trader_id=xxx - 指定trader的统计信息")
	log.Printf("  • GET  /api/equity-history?trader_id=xxx - 指定trader的收益率历史数据")
	log.Printf("  • GET  /api/performance?trader_id=xxx - 指定trader的AI学习表现分析")
	log.Println()

	return s.router.Run(addr)
}

// handleGetPromptTemplates 获取所有系统提示词模板列表
func (s *Server) handleGetPromptTemplates(c *gin.Context) {
	// 导入 decision 包
	templates := decision.GetAllPromptTemplates()

	// 转换为响应格式
	response := make([]map[string]interface{}, 0, len(templates))
	for _, tmpl := range templates {
		response = append(response, map[string]interface{}{
			"name": tmpl.Name,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"templates": response,
	})
}

// handleGetPromptTemplate 获取指定名称的提示词模板内容
func (s *Server) handleGetPromptTemplate(c *gin.Context) {
	templateName := c.Param("name")

	template, err := decision.GetPromptTemplate(templateName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("模板不存在: %s", templateName)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"name":    template.Name,
		"content": template.Content,
	})
}

// handleKlines 获取K线数据
func (s *Server) handleKlines(c *gin.Context) {
	// 获取参数
	symbol := c.Query("symbol")
	interval := c.Query("interval")
	limitStr := c.Query("limit")

	// 参数验证
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol参数必填"})
		return
	}

	// 默认值
	if interval == "" {
		interval = "4h"
	}

	limit := 100
	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// 调用 market 包获取K线数据
	klines, err := market.GetKlines(symbol, interval, limit)
	if err != nil {
		log.Printf("获取K线数据失败: symbol=%s interval=%s limit=%d error=%v", symbol, interval, limit, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取K线数据失败: %v", err)})
		return
	}

	// 计算技术指标
	ema20Values := make([]float64, len(klines))
	ema50Values := make([]float64, len(klines))
	ema200Values := make([]float64, len(klines))
	macdValues := make([]float64, len(klines))
	rsiValues := make([]float64, len(klines))
	bollingerUpper := make([]float64, len(klines))
	bollingerMiddle := make([]float64, len(klines))
	bollingerLower := make([]float64, len(klines))

	// 逐个K线计算指标（只要有足够数据就计算）
	for i := range klines {
		if i < 19 {
			// 数据不足，填充0或null
			ema20Values[i] = 0
			ema50Values[i] = 0
			ema200Values[i] = 0
			macdValues[i] = 0
			rsiValues[i] = 0
			bollingerUpper[i] = 0
			bollingerMiddle[i] = 0
			bollingerLower[i] = 0
			continue
		}

		// 取到当前位置的切片来计算
		sliceForCalc := klines[:i+1]

		// EMA20
		if i >= 19 {
			ema20Values[i] = market.CalculateEMA(sliceForCalc, 20)
		}

		// EMA50
		if i >= 49 {
			ema50Values[i] = market.CalculateEMA(sliceForCalc, 50)
		} else {
			ema50Values[i] = 0
		}

		// EMA200
		if i >= 199 {
			ema200Values[i] = market.CalculateEMA(sliceForCalc, 200)
		} else {
			ema200Values[i] = 0
		}

		// MACD
		if i >= 25 {
			macdValues[i] = market.CalculateMACD(sliceForCalc)
		}

		// RSI
		if i >= 13 {
			rsiValues[i] = market.CalculateRSI(sliceForCalc, 14)
		}

		// 布林带
		if i >= 19 {
			bb := market.CalculateBollinger(sliceForCalc, 20, 2.0)
			if bb != nil {
				bollingerUpper[i] = bb.Upper
				bollingerMiddle[i] = bb.Middle
				bollingerLower[i] = bb.Lower
			}
		}
	}

	// 转换为前端需要的格式（包含指标）
	type KlineResponse struct {
		Time            int64   `json:"time"` // Unix时间戳（秒）
		Open            float64 `json:"open"`
		High            float64 `json:"high"`
		Low             float64 `json:"low"`
		Close           float64 `json:"close"`
		Volume          float64 `json:"volume"`
		EMA20           float64 `json:"ema20,omitempty"`
		EMA50           float64 `json:"ema50,omitempty"`
		EMA200          float64 `json:"ema200,omitempty"`
		MACD            float64 `json:"macd,omitempty"`
		RSI             float64 `json:"rsi,omitempty"`
		BollingerUpper  float64 `json:"bb_upper,omitempty"`
		BollingerMiddle float64 `json:"bb_middle,omitempty"`
		BollingerLower  float64 `json:"bb_lower,omitempty"`
	}

	response := make([]KlineResponse, len(klines))
	for i, k := range klines {
		response[i] = KlineResponse{
			Time:            k.OpenTime / 1000, // 转换为秒
			Open:            k.Open,
			High:            k.High,
			Low:             k.Low,
			Close:           k.Close,
			Volume:          k.Volume,
			EMA20:           ema20Values[i],
			EMA50:           ema50Values[i],
			EMA200:          ema200Values[i],
			MACD:            macdValues[i],
			RSI:             rsiValues[i],
			BollingerUpper:  bollingerUpper[i],
			BollingerMiddle: bollingerMiddle[i],
			BollingerLower:  bollingerLower[i],
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"symbol":   symbol,
		"interval": interval,
		"klines":   response,
	})
}

// handleCycleCheck 返回最近N个决策周期（用于cycle check视图）
func (s *Server) handleCycleCheck(c *gin.Context) {
	traderMgr, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	traderInstance, err := traderMgr.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("获取trader失败: %v", err)})
		return
	}

	limit := parseLimit(c.Query("limit"), 15)

	records, err := traderInstance.GetDecisionLogger().GetLatestRecords(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取决策记录失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"trader_id": traderID,
		"limit":     limit,
		"records":   records,
	})
}

// handleListCloseReviews 返回某个trader的close review摘要列表
func (s *Server) handleListCloseReviews(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	limit := parseLimit(c.Query("limit"), 50)

	reviews, err := s.database.ListCloseReviews(traderID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("查询close review失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"trader_id": traderID,
		"items":     reviews,
	})
}

// handleGetCloseReview 返回某个trade的close review详情
func (s *Server) handleGetCloseReview(c *gin.Context) {
	tradeID := c.Param("trade_id")
	if tradeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trade_id不能为空"})
		return
	}

	summary, err := s.database.GetCloseReview(tradeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到对应的close review"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("查询close review失败: %v", err)})
		return
	}

	var detail *review.CloseReviewFile
	if summary != nil && summary.FilePath != "" {
		payload, loadErr := review.LoadCloseReview(summary.FilePath)
		if loadErr != nil {
			log.Printf("⚠️ 读取close review文件失败 %s: %v", summary.FilePath, loadErr)
		} else {
			detail = payload
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"summary": summary,
		"detail":  detail,
	})
}

// handleCreateCloseReview 保存一条close review记录
func (s *Server) handleCreateCloseReview(c *gin.Context) {
	traderMgr, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 确认trader存在（用于权限校验）
	if _, err := traderMgr.GetTrader(traderID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("获取trader失败: %v", err)})
		return
	}

	tradeID := c.Param("trade_id")
	if tradeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trade_id不能为空"})
		return
	}

	var payload review.CloseReviewFile
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("请求体解析失败: %v", err)})
		return
	}

	if payload.TradeSnapshot.TradeID == "" {
		payload.TradeSnapshot.TradeID = tradeID
	}

	if payload.TradeSnapshot.TradeID != tradeID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trade_id 与 payload 不一致"})
		return
	}

	// 确保review段落包含trade标识
	payload.Review.TradeID = payload.TradeSnapshot.TradeID
	if payload.Review.Symbol == "" {
		payload.Review.Symbol = payload.TradeSnapshot.Symbol
	}
	if payload.Review.Side == "" {
		payload.Review.Side = payload.TradeSnapshot.Side
	}

	filePath, err := review.SaveCloseReview(review.DefaultBaseDir, traderID, &payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("保存Close Review失败: %v", err)})
		return
	}

	summary := &config.CloseReviewSummary{
		TradeID:                   payload.TradeSnapshot.TradeID,
		TraderID:                  traderID,
		Symbol:                    payload.TradeSnapshot.Symbol,
		Side:                      payload.TradeSnapshot.Side,
		PnL:                       payload.TradeSnapshot.PnL,
		PnLPct:                    payload.TradeSnapshot.PnLPct,
		HoldingMinutes:            payload.TradeSnapshot.HoldingMinutes,
		RiskScore:                 payload.Review.RiskScore,
		ExecutionScore:            payload.Review.ExecutionScore,
		SignalScore:               payload.Review.SignalScore,
		Summary:                   payload.Review.Summary,
		WhatWentWell:              payload.Review.WhatWentWell,
		Improvements:              payload.Review.Improvements,
		RootCause:                 payload.Review.RootCause,
		ExtremeInterventionReview: payload.Review.ExtremeInterventionReview,
		ActionItems:               payload.Review.ActionItems,
		Confidence:                payload.Review.Confidence,
		Reasoning:                 payload.Review.Reasoning,
		FilePath:                  filePath,
		CreatedAt:                 payload.Timestamp,
	}

	if err := s.database.UpsertCloseReview(summary); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("写入数据库失败: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"summary": summary,
	})
}

func parseLimit(raw string, defaultVal int) int {
	if raw == "" {
		return defaultVal
	}

	val, err := strconv.Atoi(raw)
	if err != nil || val <= 0 {
		return defaultVal
	}
	return val
}

// handleReviewLossTrades 批量复盘亏损订单
func (s *Server) handleReviewLossTrades(c *gin.Context) {
	traderMgr, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := traderMgr.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("获取trader失败: %v", err)})
		return
	}

	// 获取参数
	limit := parseLimit(c.Query("limit"), 100) // 默认分析最近100个周期的记录
	force := c.Query("force") == "true"        // 是否强制重新复盘已有复盘的交易

	// 获取决策日志记录器
	decisionLogger := trader.GetDecisionLogger()
	if decisionLogger == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法获取决策日志记录器"})
		return
	}

	// 提取亏损交易
	lossTrades, err := review.ExtractLossTrades(decisionLogger, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("提取亏损交易失败: %v", err)})
		return
	}

	if len(lossTrades) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message":     "未找到亏损交易",
			"reviewed":    0,
			"failed":      0,
			"skipped":     0,
			"total_found": 0,
		})
		return
	}

	// 获取trader的MCP客户端（用于调用AI）
	// 这里需要从trader获取AI客户端，但trader接口可能没有暴露
	// 我们需要通过其他方式获取，或者创建一个新的客户端
	// 暂时先返回错误，提示需要配置
	mcpClient := mcp.New()
	// TODO: 从trader配置中获取AI模型配置并设置到mcpClient
	// 这里需要根据实际情况调整

	// 创建复盘生成器
	reviewGen := review.NewReviewGenerator(mcpClient)

	// 批量生成复盘
	reviewed := 0
	failed := 0
	skipped := 0
	var errors []string

	for _, trade := range lossTrades {
		// 检查是否已有复盘
		if !force {
			existing, err := s.database.GetCloseReview(trade.TradeID)
			if err == nil && existing != nil {
				skipped++
				continue
			}
		}

		// 生成复盘
		reviewFile, err := reviewGen.GenerateReview(trade, decisionLogger)
		if err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("%s: %v", trade.TradeID, err))
			log.Printf("❌ 生成复盘失败 %s: %v", trade.TradeID, err)
			continue
		}

		// 保存复盘
		filePath, err := review.SaveCloseReview(review.DefaultBaseDir, traderID, reviewFile)
		if err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("%s: 保存失败 %v", trade.TradeID, err))
			log.Printf("❌ 保存复盘失败 %s: %v", trade.TradeID, err)
			continue
		}

		// 保存到数据库
		summary := &config.CloseReviewSummary{
			TradeID:                   reviewFile.TradeSnapshot.TradeID,
			TraderID:                  traderID,
			Symbol:                    reviewFile.TradeSnapshot.Symbol,
			Side:                      reviewFile.TradeSnapshot.Side,
			PnL:                       reviewFile.TradeSnapshot.PnL,
			PnLPct:                    reviewFile.TradeSnapshot.PnLPct,
			HoldingMinutes:            reviewFile.TradeSnapshot.HoldingMinutes,
			RiskScore:                 reviewFile.Review.RiskScore,
			ExecutionScore:            reviewFile.Review.ExecutionScore,
			SignalScore:               reviewFile.Review.SignalScore,
			Summary:                   reviewFile.Review.Summary,
			WhatWentWell:              reviewFile.Review.WhatWentWell,
			Improvements:              reviewFile.Review.Improvements,
			RootCause:                 reviewFile.Review.RootCause,
			ExtremeInterventionReview: reviewFile.Review.ExtremeInterventionReview,
			ActionItems:               reviewFile.Review.ActionItems,
			Confidence:                reviewFile.Review.Confidence,
			Reasoning:                 reviewFile.Review.Reasoning,
			FilePath:                  filePath,
			CreatedAt:                 reviewFile.Timestamp,
		}

		if err := s.database.UpsertCloseReview(summary); err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("%s: 写入数据库失败 %v", trade.TradeID, err))
			log.Printf("❌ 写入数据库失败 %s: %v", trade.TradeID, err)
			continue
		}

		reviewed++
		log.Printf("✓ 成功生成并保存复盘: %s", trade.TradeID)
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     fmt.Sprintf("批量复盘完成: 成功 %d, 失败 %d, 跳过 %d", reviewed, failed, skipped),
		"reviewed":    reviewed,
		"failed":      failed,
		"skipped":     skipped,
		"total_found": len(lossTrades),
		"errors":      errors,
	})
}

// handleReviewSingleTrade 为单个交易生成复盘
func (s *Server) handleReviewSingleTrade(c *gin.Context) {
	userID := c.GetString("user_id")
	traderMgr, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tradeID := c.Param("trade_id")
	if tradeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trade_id不能为空"})
		return
	}

	// 尝试从TraderManager获取交易员（如果正在运行）
	var decisionLogger *logger.DecisionLogger
	trader, err := traderMgr.GetTrader(traderID)
	if err == nil && trader != nil {
		// 交易员正在运行，使用其决策日志记录器
		decisionLogger = trader.GetDecisionLogger()
		if decisionLogger == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "无法获取决策日志记录器"})
			return
		}
	} else {
		// 交易员未运行，直接从文件系统创建决策日志记录器
		logDir := fmt.Sprintf("decision_logs/%s", traderID)
		decisionLogger = logger.NewDecisionLogger(logDir)
		log.Printf("ℹ️  交易员 %s 未运行，从文件系统读取决策日志: %s", traderID, logDir)
	}

	// 从数据库获取trader配置（包含AI模型配置）
	_, aiModelCfg, _, err := s.database.GetTraderConfig(userID, traderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("获取trader配置失败: %v", err)})
		return
	}

	// 创建并配置MCP客户端
	mcpClient := mcp.New()
	if aiModelCfg.Provider == "qwen" {
		mcpClient.SetQwenAPIKey(aiModelCfg.APIKey, aiModelCfg.CustomAPIURL, aiModelCfg.CustomModelName)
	} else if aiModelCfg.Provider == "deepseek" {
		mcpClient.SetDeepSeekAPIKey(aiModelCfg.APIKey, aiModelCfg.CustomAPIURL, aiModelCfg.CustomModelName)
	} else if aiModelCfg.Provider == "custom" {
		mcpClient.SetCustomAPI(aiModelCfg.CustomAPIURL, aiModelCfg.APIKey, aiModelCfg.CustomModelName)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("不支持的AI模型: %s", aiModelCfg.Provider)})
		return
	}

	// 创建复盘生成器
	reviewGen := review.NewReviewGenerator(mcpClient)

	// 首先尝试从亏损交易列表中查找（更快）
	lossTrades, err := review.ExtractLossTrades(decisionLogger, 1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("提取亏损交易失败: %v", err)})
		return
	}

	// 查找指定的交易
	var targetTrade *review.TradeInfo
	for i := range lossTrades {
		if lossTrades[i].TradeID == tradeID {
			targetTrade = &lossTrades[i]
			break
		}
	}

	// 如果从亏损交易列表中找不到，尝试直接从决策日志中查找（不限制是否亏损）
	if targetTrade == nil {
		log.Printf("ℹ️  从亏损交易列表中未找到 %s，尝试直接从决策日志中查找", tradeID)
		// 增加查找的记录数量，确保能找到
		foundTrade, findErr := review.FindTradeByID(decisionLogger, tradeID, 5000)
		if findErr != nil {
			log.Printf("❌ 查找交易失败: %v", findErr)
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("未找到交易 %s: %v", tradeID, findErr)})
			return
		}
		targetTrade = foundTrade
		log.Printf("✓ 从决策日志中找到了交易 %s", tradeID)
	}

	// 检查是否已有复盘
	force := c.Query("force") == "true"
	if !force {
		existing, err := s.database.GetCloseReview(tradeID)
		if err == nil && existing != nil {
			c.JSON(http.StatusOK, gin.H{
				"message": "该交易已有复盘，使用force=true可强制重新生成",
				"summary": existing,
			})
			return
		}
	}

	// 生成复盘
	reviewFile, err := reviewGen.GenerateReview(*targetTrade, decisionLogger)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("生成复盘失败: %v", err)})
		return
	}

	// 保存复盘
	filePath, err := review.SaveCloseReview(review.DefaultBaseDir, traderID, reviewFile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("保存复盘失败: %v", err)})
		return
	}

	// 保存到数据库
	summary := &config.CloseReviewSummary{
		TradeID:                   reviewFile.TradeSnapshot.TradeID,
		TraderID:                  traderID,
		Symbol:                    reviewFile.TradeSnapshot.Symbol,
		Side:                      reviewFile.TradeSnapshot.Side,
		PnL:                       reviewFile.TradeSnapshot.PnL,
		PnLPct:                    reviewFile.TradeSnapshot.PnLPct,
		HoldingMinutes:            reviewFile.TradeSnapshot.HoldingMinutes,
		RiskScore:                 reviewFile.Review.RiskScore,
		ExecutionScore:            reviewFile.Review.ExecutionScore,
		SignalScore:               reviewFile.Review.SignalScore,
		Summary:                   reviewFile.Review.Summary,
		WhatWentWell:              reviewFile.Review.WhatWentWell,
		Improvements:              reviewFile.Review.Improvements,
		RootCause:                 reviewFile.Review.RootCause,
		ExtremeInterventionReview: reviewFile.Review.ExtremeInterventionReview,
		ActionItems:               reviewFile.Review.ActionItems,
		Confidence:                reviewFile.Review.Confidence,
		Reasoning:                 reviewFile.Review.Reasoning,
		FilePath:                  filePath,
		CreatedAt:                 reviewFile.Timestamp,
	}

	if err := s.database.UpsertCloseReview(summary); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("写入数据库失败: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "复盘生成成功",
		"summary":   summary,
		"file_path": filePath,
	})
}

// handleAIStream SSE 流式推送 AI 思考过程
func (s *Server) handleAIStream(c *gin.Context) {
	// 认证已在中间件中处理
	traderID := c.Query("trader_id")
	if traderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trader_id 参数必填"})
		return
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	// 创建 channel 用于推送数据
	ch := make(chan string, 100)
	s.streamMutex.Lock()
	s.streamChannels[traderID] = ch
	s.streamMutex.Unlock()

	// 注册流式回调到 decision 包
	streamCallback := func(chunk string) error {
		// 推送数据到 channel
		select {
		case ch <- chunk:
		default:
			// channel 已满，跳过
		}
		return nil
	}
	decision.RegisterStreamCallback(traderID, streamCallback)

	// 清理函数：客户端断开时移除 channel 和回调
	defer func() {
		s.streamMutex.Lock()
		delete(s.streamChannels, traderID)
		close(ch)
		s.streamMutex.Unlock()
		decision.UnregisterStreamCallback(traderID)
	}()

	// 发送初始连接消息（使用默认事件，前端 onmessage 可以接收）
	c.SSEvent("", gin.H{
		"type":    "connected",
		"message": "已连接到 AI 思考流，等待下一次决策...",
	})
	c.Writer.Flush()

	// 监听 channel 并推送数据
	for {
		select {
		case data, ok := <-ch:
			if !ok {
				// channel 已关闭
				c.SSEvent("", gin.H{
					"type":    "closed",
					"message": "连接已关闭",
				})
				c.Writer.Flush()
				return
			}
			// 推送数据（使用默认事件）
			c.SSEvent("", gin.H{
				"type": "partial_cot",
				"data": data,
			})
			c.Writer.Flush()

		case <-c.Request.Context().Done():
			// 客户端断开连接
			return

		case <-time.After(30 * time.Second):
			// 发送心跳保持连接
			c.SSEvent("", gin.H{
				"type": "heartbeat",
			})
			c.Writer.Flush()
		}
	}
}

// pushToStream 推送数据到指定 trader 的流
func (s *Server) pushToStream(traderID string, data string) {
	s.streamMutex.RLock()
	ch, exists := s.streamChannels[traderID]
	s.streamMutex.RUnlock()

	if exists {
		select {
		case ch <- data:
		default:
			// channel 已满，跳过
		}
	}
}

// handleAIDecisionStream 手动触发一次决策并流式返回
func (s *Server) handleAIDecisionStream(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	// 发送初始消息
	c.SSEvent("message", gin.H{
		"type":    "start",
		"message": "开始 AI 决策分析...",
	})
	c.Writer.Flush()

	// 获取 trader 的上下文信息（需要从 trader 获取）
	// 这里需要调用 trader 的方法来构建决策上下文
	// 由于 trader 接口可能没有直接暴露构建上下文的方法，我们需要通过反射或添加新方法
	// 为了简化，我们先尝试获取账户和持仓信息来构建上下文

	account, err := trader.GetAccountInfo()
	if err != nil {
		c.SSEvent("message", gin.H{
			"type":    "error",
			"message": fmt.Sprintf("获取账户信息失败: %v", err),
		})
		c.Writer.Flush()
		return
	}

	positions, err := trader.GetPositions()
	if err != nil {
		c.SSEvent("message", gin.H{
			"type":    "error",
			"message": fmt.Sprintf("获取持仓信息失败: %v", err),
		})
		c.Writer.Flush()
		return
	}

	// 构建决策上下文（简化版，实际需要更多信息）
	ctx := &decision.Context{
		CurrentTime:    time.Now().Format("2006-01-02 15:04:05"),
		RuntimeMinutes: 0, // 需要从 trader 获取
		CallCount:      0, // 需要从 trader 获取
		Account: decision.AccountInfo{
			TotalEquity:      account["total_equity"].(float64),
			AvailableBalance: account["available_balance"].(float64),
			TotalPnL:         account["total_pnl"].(float64),
			TotalPnLPct:      account["total_pnl_pct"].(float64),
			MarginUsed:       account["margin_used"].(float64),
			MarginUsedPct:    account["margin_used_pct"].(float64),
			PositionCount:    int(account["position_count"].(float64)),
		},
		Positions:     make([]decision.PositionInfo, 0),
		PendingOrders: make([]decision.PendingOrderInfo, 0),
		CandidateCoins: make([]decision.CandidateCoin, 0),
	}

	// 转换持仓信息
	for _, pos := range positions {
		ctx.Positions = append(ctx.Positions, decision.PositionInfo{
			Symbol:           pos["symbol"].(string),
			Side:             pos["side"].(string),
			EntryPrice:       pos["entry_price"].(float64),
			MarkPrice:        pos["mark_price"].(float64),
			Quantity:         pos["quantity"].(float64),
			Leverage:         int(pos["leverage"].(float64)),
			UnrealizedPnL:    pos["unrealized_pnl"].(float64),
			UnrealizedPnLPct: pos["unrealized_pnl_pct"].(float64),
			LiquidationPrice: pos["liquidation_price"].(float64),
			MarginUsed:       pos["margin_used"].(float64),
		})
	}

	// 获取 MCP 客户端（需要从 trader 获取）
	// 这里假设 trader 有 GetMCPClient 方法，如果没有需要添加
	// 为了简化，我们先使用流式回调来推送内容

	// 创建流式回调
	var fullResponse strings.Builder
	_ = func(chunk string) error {
		fullResponse.WriteString(chunk)
		// 推送增量内容
		c.SSEvent("message", gin.H{
			"type": "partial_cot",
			"data": chunk,
		})
		c.Writer.Flush()
		return nil
	}

	// 获取 trader 的 MCP 客户端（需要添加方法或通过接口获取）
	// 这里先注释，需要根据实际 trader 接口实现
	/*
	mcpClient := trader.GetMCPClient()
	if mcpClient == nil {
		c.SSEvent("message", gin.H{
			"type":    "error",
			"message": "无法获取 AI 客户端",
		})
		c.Writer.Flush()
		return
	}

	// 调用流式决策
	decisionResult, err := decision.GetFullDecisionStream(ctx, mcpClient, "", false, "", streamCallback)
	if err != nil {
		c.SSEvent("message", gin.H{
			"type":    "error",
			"message": fmt.Sprintf("决策失败: %v", err),
		})
		c.Writer.Flush()
		return
	}

	// 发送最终决策
	c.SSEvent("message", gin.H{
		"type":     "final_decision",
		"decision":  decisionResult,
		"cot_trace": decisionResult.CoTTrace,
	})
	c.Writer.Flush()
	*/

	// 临时实现：发送提示信息
	c.SSEvent("message", gin.H{
		"type":    "info",
		"message": "流式决策功能需要 trader 接口支持 GetMCPClient 方法",
	})
	c.Writer.Flush()
}
