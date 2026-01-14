package trader

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"nofx/config"
	"nofx/decision"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ===== M2.2: 限价订单生命周期管理 =====

// LimitOrderExecutionReport 限价订单执行报告
type LimitOrderExecutionReport struct {
	OrderID        int64   `json:"order_id"`
	Symbol         string  `json:"symbol"`
	Side           string  `json:"side"` // "BUY" or "SELL"
	AttemptIndex   int     `json:"attempt_index"`
	LimitPrice     float64 `json:"limit_price"`
	PricingReason  string  `json:"pricing_reason"`
	Quantity       float64 `json:"quantity"`
	FilledQuantity float64 `json:"filled_quantity"`
	AvgFillPrice   float64 `json:"avg_fill_price"`
	Status         string  `json:"status"` // FILLED, PARTIAL, CANCELLED, EXPIRED, TIMEOUT, RETRIES_EXHAUSTED
	StartTime      int64   `json:"start_time"`
	EndTime        int64   `json:"end_time"`
	DurationMs     int64   `json:"duration_ms"`
	Error          string  `json:"error,omitempty"`
}

// PositionTarget 用来记住这个持仓当初AI给的三个止盈点位，以及当前走到哪一段了
type PositionTarget struct {
	TP1       float64 `json:"tp1"`
	TP2       float64 `json:"tp2"`
	TP3       float64 `json:"tp3"`
	Stage     int     `json:"stage"`      // 0=还没到tp1, 1=到过tp1, 2=到过tp2, 3=到过tp3
	CurrentSL float64 `json:"current_sl"` // 当前已生效的止损价（开仓时=初始止损）
}

// PendingOrder 待成交的限价单
type PendingOrder struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long"/"short"
	LimitPrice       float64 `json:"limit_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	OrderID          int64   `json:"order_id"`
	TP1              float64 `json:"tp1"`
	TP2              float64 `json:"tp2"`
	TP3              float64 `json:"tp3"`
	StopLoss         float64 `json:"stop_loss"`
	TakeProfit       float64 `json:"take_profit"`
	CreateTime       int64   `json:"create_time"` // 创建时间戳（毫秒）
	Confidence       int     `json:"confidence"`
	Reasoning        string  `json:"reasoning"`
	Thesis           string  `json:"thesis"`            // 入场逻辑的一句话总结
	CancelConditions string  `json:"cancel_conditions"` // 撤单条件
}

// AutoTraderConfig 自动交易配置（简化版 - AI全权决策）
type AutoTraderConfig struct {
	// Trader标识
	ID      string // Trader唯一标识（用于日志目录等）
	Name    string // Trader显示名称
	AIModel string // AI模型: "qwen" 或 "deepseek"

	// 交易平台选择
	Exchange   string // "binance", "hyperliquid" 或 "aster"
	TraderMode string // "paper" 或 "binance"，默认 "binance"

	// M2.2: 限价订单生命周期管理配置
	LimitOrderWaitSeconds    int  `json:"limit_order_wait_seconds"`     // 等待成交超时时间(秒)
	LimitOrderMaxRetries     int  `json:"limit_order_max_retries"`      // 最大重试次数
	LimitOrderPollIntervalMs int  `json:"limit_order_poll_interval_ms"` // 轮询间隔(毫秒)
	CancelOnPartialFill      bool `json:"cancel_on_partial_fill"`       // 是否在部分成交时取消剩余
	PostOnlyWhenLimitOnly    bool `json:"post_only_when_limit_only"`    // limit_only模式时是否使用post-only

	// 币安API配置
	BinanceAPIKey    string
	BinanceSecretKey string

	// 止损触发类型配置
	StopLossWorkingType string // "CONTRACT_PRICE"(Last) 或 "MARK_PRICE"
	EnablePriceProtect  bool   // 是否启用priceProtect

	// Hyperliquid配置
	HyperliquidPrivateKey string
	HyperliquidWalletAddr string
	HyperliquidTestnet    bool

	// Aster配置
	AsterUser       string // Aster主钱包地址
	AsterSigner     string // Aster API钱包地址
	AsterPrivateKey string // Aster API钱包私钥

	CoinPoolAPIURL string

	// AI配置
	UseQwen     bool
	DeepSeekKey string
	QwenKey     string

	// 自定义AI API配置
	CustomAPIURL    string
	CustomAPIKey    string
	CustomModelName string

	// 扫描配置
	ScanInterval time.Duration // 扫描间隔（建议3分钟）

	// 账户配置
	InitialBalance float64 // 初始金额（用于计算盈亏，需手动设置）

	// 杠杆配置
	BTCETHLeverage  int // BTC和ETH的杠杆倍数
	AltcoinLeverage int // 山寨币的杠杆倍数

	// 风险控制（仅作为提示，AI可自主决定）
	MaxDailyLoss    float64       // 最大日亏损百分比（提示）
	MaxDrawdown     float64       // 最大回撤百分比（提示）
	StopTradingTime time.Duration // 触发风控后暂停时长

	// 仓位模式
	IsCrossMargin bool // true=全仓模式, false=逐仓模式

	// 币种配置
	DefaultCoins []string // 默认币种列表（从数据库获取）
	TradingCoins []string // 实际交易币种列表

	// 系统提示词模板
	SystemPromptTemplate string // 系统提示词模板名称（如 "default", "aggressive"）
}

// AutoTrader 自动交易器
type AutoTrader struct {
	id                    string // Trader唯一标识
	name                  string // Trader显示名称
	aiModel               string // AI模型名称
	exchange              string // 交易平台名称
	config                AutoTraderConfig
	globalConfig          *config.Config // 全局配置（包含分层风控配置）
	trader                Trader         // 使用Trader接口（支持多平台）
	mcpClient             *mcp.Client
	decisionLogger        *logger.DecisionLogger // 决策日志记录器
	initialBalance        float64
	dailyPnL              float64
	customPrompt          string   // 自定义交易策略prompt
	overrideBasePrompt    bool     // 是否覆盖基础prompt
	systemPromptTemplate  string   // 系统提示词模板名称
	defaultCoins          []string // 默认币种列表（从数据库获取）
	tradingCoins          []string // 实际交易币种列表
	lastResetTime         time.Time
	stopUntil             time.Time
	isRunning             bool
	stopChan              chan struct{}    // 停止信号通道
	startTime             time.Time        // 系统启动时间
	callCount             int              // AI调用次数
	positionFirstSeenTime map[string]int64 // 持仓首次出现时间 (symbol_side -> timestamp毫秒)

	// 记住这个持仓当初AI给的TP1/TP2/TP3
	positionTargets map[string]*PositionTarget // key: "BTCUSDT_long" / "ETHUSDT_short"

	// 记住最新的持仓快照（用于检测交易所自动平仓）
	positionMemory  map[string]decision.PositionInfo
	autoCloseEvents []logger.DecisionAction

	// 记住所有待成交的限价单
	pendingOrders map[string]*PendingOrder // key: "BTCUSDT_long" / "ETHUSDT_short"

	// 记录每个币种当日已开单数（市价+限价）
	dailyPairTrades     map[string]int // key: "BTCUSDT", value: 今日开单次数
	dailyTradesResetDay string         // 上次重置日期（YYYY-MM-DD）

	// 上一周期的AI思维链（用于提供给下一周期参考）
	lastCoTTrace string

	// 冷却状态管理 (symbol_direction -> cooldown_until_ms)
	cooldownStates map[string]int64

	// 止损历史记录 (symbol_direction -> []stopLossTime_ms)
	stopLossHistory map[string][]int64
}

// NewAutoTrader 创建自动交易器
func NewAutoTrader(config AutoTraderConfig, globalConfig *config.Config) (*AutoTrader, error) {
	// 设置默认值
	if config.ID == "" {
		config.ID = "default_trader"
	}
	if config.Name == "" {
		config.Name = "Default Trader"
	}
	if config.AIModel == "" {
		if config.UseQwen {
			config.AIModel = "qwen"
		} else {
			config.AIModel = "deepseek"
		}
	}
	if config.TraderMode == "" {
		config.TraderMode = "binance" // 默认使用真实交易所
	}

	mcpClient := mcp.New()

	// 初始化AI
	if config.AIModel == "custom" {
		// 使用自定义API
		mcpClient.SetCustomAPI(config.CustomAPIURL, config.CustomAPIKey, config.CustomModelName)
		log.Printf("🤖 [%s] 使用自定义AI API: %s (模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
	} else if config.UseQwen || config.AIModel == "qwen" {
		// 使用Qwen (支持自定义URL和Model)
		mcpClient.SetQwenAPIKey(config.QwenKey, config.CustomAPIURL, config.CustomModelName)
		if config.CustomAPIURL != "" || config.CustomModelName != "" {
			log.Printf("🤖 [%s] 使用阿里云Qwen AI (自定义URL: %s, 模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
		} else {
			log.Printf("🤖 [%s] 使用阿里云Qwen AI", config.Name)
		}
	} else {
		// 默认使用DeepSeek (支持自定义URL和Model)
		mcpClient.SetDeepSeekAPIKey(config.DeepSeekKey, config.CustomAPIURL, config.CustomModelName)
		if config.CustomAPIURL != "" || config.CustomModelName != "" {
			log.Printf("🤖 [%s] 使用DeepSeek AI (自定义URL: %s, 模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
		} else {
			log.Printf("🤖 [%s] 使用DeepSeek AI", config.Name)
		}
	}

	// 初始化币种池API
	if config.CoinPoolAPIURL != "" {
		pool.SetCoinPoolAPI(config.CoinPoolAPIURL)
	}

	// 设置默认交易平台
	if config.Exchange == "" {
		config.Exchange = "binance"
	}

	// 根据配置创建对应的交易器
	var trader Trader
	var err error

	// 记录仓位模式（通用）
	marginModeStr := "全仓"
	if !config.IsCrossMargin {
		marginModeStr = "逐仓"
	}
	log.Printf("📊 [%s] 仓位模式: %s", config.Name, marginModeStr)

	// 根据 trader_mode 创建交易器
	if config.TraderMode == "paper" {
		log.Printf("📝 [%s] 使用纸交易模式 (不连接真实交易所)", config.Name)
		trader = NewPaperTrader()
	} else {
		// 真实交易模式
		switch config.Exchange {
		case "binance":
			log.Printf("🏦 [%s] 使用币安合约交易", config.Name)
			// 使用配置的止损工作类型，默认MARK_PRICE更抗插针
			stopLossWorkingType := config.StopLossWorkingType
			if stopLossWorkingType == "" {
				stopLossWorkingType = "MARK_PRICE" // 默认值
			}
			trader = NewFuturesTraderWithConfig(config.BinanceAPIKey, config.BinanceSecretKey,
				stopLossWorkingType, config.EnablePriceProtect)
		case "hyperliquid":
			log.Printf("🏦 [%s] 使用Hyperliquid交易", config.Name)
			trader, err = NewHyperliquidTrader(config.HyperliquidPrivateKey, config.HyperliquidWalletAddr, config.HyperliquidTestnet)
			if err != nil {
				return nil, fmt.Errorf("初始化Hyperliquid交易器失败: %w", err)
			}
		case "aster":
			log.Printf("🏦 [%s] 使用Aster交易", config.Name)
			trader, err = NewAsterTrader(config.AsterUser, config.AsterSigner, config.AsterPrivateKey)
			if err != nil {
				return nil, fmt.Errorf("初始化Aster交易器失败: %w", err)
			}
		default:
			return nil, fmt.Errorf("不支持的交易平台: %s", config.Exchange)
		}
	}

	// 验证初始金额配置
	if config.InitialBalance <= 0 {
		return nil, fmt.Errorf("初始金额必须大于0，请在配置中设置InitialBalance")
	}

	// 初始化决策日志记录器（使用trader ID创建独立目录）
	logDir := fmt.Sprintf("decision_logs/%s", config.ID)
	decisionLogger := logger.NewDecisionLogger(logDir)

	// 设置默认系统提示词模板
	systemPromptTemplate := config.SystemPromptTemplate
	if systemPromptTemplate == "" {
		// feature/partial-close-dynamic-tpsl 分支默认使用 adaptive（支持动态止盈止损）
		systemPromptTemplate = "adaptive"
	}

	return &AutoTrader{
		id:                    config.ID,
		name:                  config.Name,
		aiModel:               config.AIModel,
		exchange:              config.Exchange,
		config:                config,
		globalConfig:          globalConfig,
		trader:                trader,
		mcpClient:             mcpClient,
		decisionLogger:        decisionLogger,
		initialBalance:        config.InitialBalance,
		systemPromptTemplate:  systemPromptTemplate,
		defaultCoins:          config.DefaultCoins,
		tradingCoins:          config.TradingCoins,
		lastResetTime:         time.Now(),
		startTime:             time.Now(),
		callCount:             0,
		isRunning:             false,
		positionFirstSeenTime: make(map[string]int64),
		positionTargets:       make(map[string]*PositionTarget),
		positionMemory:        make(map[string]decision.PositionInfo),
		autoCloseEvents:       make([]logger.DecisionAction, 0),
		pendingOrders:         make(map[string]*PendingOrder),
		dailyPairTrades:       make(map[string]int),
		dailyTradesResetDay:   time.Now().Format("2006-01-02"),
		cooldownStates:        make(map[string]int64),
		stopLossHistory:       make(map[string][]int64),
	}, nil
}

// loadDailyPairTrades 从磁盘加载每日开单计数（如果存在且为今天则恢复）
func (at *AutoTrader) loadDailyPairTrades() {
	// compute log dir (decision logger stores logs under decision_logs/<trader_id>)
	logDir := filepath.Join("decision_logs", at.id)
	path := filepath.Join(logDir, "daily_pair_trades.json")
	data, err := ioutil.ReadFile(path)
	if err != nil {
		// 文件不存在或无法读取，保持当前内存计数
		return
	}

	var payload struct {
		Date   string         `json:"date"`
		Trades map[string]int `json:"trades"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
	today := time.Now().Format("2006-01-02")
	if payload.Date == today && payload.Trades != nil {
		at.dailyPairTrades = payload.Trades
		at.dailyTradesResetDay = payload.Date
	} else {
		// 不是今天的数据，忽略并重置
		at.dailyPairTrades = make(map[string]int)
		at.dailyTradesResetDay = today
	}
}

// saveDailyPairTrades 将当前每日开单计数写盘（覆盖）
func (at *AutoTrader) saveDailyPairTrades() {
	if at.decisionLogger == nil {
		// still compute logDir based on trader id to allow persistence
	}
	logDir := filepath.Join("decision_logs", at.id)
	path := filepath.Join(logDir, "daily_pair_trades.json")
	payload := struct {
		Date   string         `json:"date"`
		Trades map[string]int `json:"trades"`
	}{
		Date:   at.dailyTradesResetDay,
		Trades: at.dailyPairTrades,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	// 尝试创建目录（已由 DecisionLogger 创建过，但以防万一）
	_ = os.MkdirAll(logDir, 0755)
	_ = ioutil.WriteFile(path, data, 0644)
}

// incrementDailyPairTrades 单币种计数增加并持久化
func (at *AutoTrader) incrementDailyPairTrades(symbol string) {
	if symbol == "" {
		return
	}
	at.dailyPairTrades[symbol]++
	log.Printf("  📊 %s 今日已开 %d 单", symbol, at.dailyPairTrades[symbol])
	at.saveDailyPairTrades()
}

// decrementDailyPairTrades 单币种计数减少并持久化（不低于0）
func (at *AutoTrader) decrementDailyPairTrades(symbol string) {
	if symbol == "" {
		return
	}
	if at.dailyPairTrades[symbol] > 0 {
		at.dailyPairTrades[symbol]--
		log.Printf("  📊 %s 今日开单计数 -1，当前为 %d 单", symbol, at.dailyPairTrades[symbol])
		at.saveDailyPairTrades()
	}
}

// Run 运行自动交易主循环
func (at *AutoTrader) Run() error {
	at.isRunning = true
	at.stopChan = make(chan struct{})
	log.Println("🚀 AI驱动自动交易系统启动")
	log.Printf("💰 初始余额: %.2f USDT", at.initialBalance)
	log.Printf("⚙️  扫描间隔: %v", at.config.ScanInterval)
	log.Println("🤖 AI将全权决定杠杆、仓位大小、止损止盈等参数")

	// 每3分钟扫描一次市场
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	// 首次立即执行
	if err := at.runCycle(); err != nil {
		log.Printf("❌ 执行失败: %v", err)
	}

	for at.isRunning {
		select {
		case <-ticker.C:
			if err := at.runCycle(); err != nil {
				log.Printf("❌ 执行失败: %v", err)
			}
		case <-at.stopChan:
			log.Println("⏹ 收到停止信号，正在退出...")
			return nil
		}
	}

	return nil
}

// Stop 停止自动交易
func (at *AutoTrader) Stop() {
	at.isRunning = false
	if at.stopChan != nil {
		close(at.stopChan)
	}
	log.Println("⏹ 自动交易系统停止")
}

// runCycle 运行一个交易周期（使用AI全权决策）
func (at *AutoTrader) runCycle() error {
	at.callCount++

	separator := strings.Repeat("=", 70)
	log.Printf("\n%s", separator)
	log.Printf("⏰ %s - AI决策周期 #%d", time.Now().Format("2006-01-02 15:04:05"), at.callCount)
	log.Printf("%s", separator)

	// 创建决策记录
	record := &logger.DecisionRecord{
		ExecutionLog: []string{},
		Success:      true,
	}

	// 1. 检查是否需要停止交易
	if time.Now().Before(at.stopUntil) {
		remaining := at.stopUntil.Sub(time.Now())
		log.Printf("⏸ 风险控制：暂停交易中，剩余 %.0f 分钟", remaining.Minutes())
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("风险控制暂停中，剩余 %.0f 分钟", remaining.Minutes())
		at.decisionLogger.LogDecision(record)
		return nil
	}

	// 2. 重置日盈亏和每日开单计数（每天重置）
	currentDay := time.Now().Format("2006-01-02")
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.dailyPnL = 0
		at.lastResetTime = time.Now()
		log.Println("📅 日盈亏已重置")
	}
	// 重置每日开单计数
	if at.dailyTradesResetDay != currentDay {
		at.dailyPairTrades = make(map[string]int)
		at.dailyTradesResetDay = currentDay
		log.Println("📅 每日开单计数已重置")
	}

	// 3. 收集交易上下文
	ctx, err := at.buildTradingContext()
	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Errorf("构建交易上下文失败: %v", err).Error()
		at.decisionLogger.LogDecision(record)
		return fmt.Errorf("构建交易上下文失败: %w", err)
	}

	// 保存账户状态快照
	record.AccountState = logger.AccountSnapshot{
		TotalBalance:          ctx.Account.TotalEquity,
		AvailableBalance:      ctx.Account.AvailableBalance,
		TotalUnrealizedProfit: ctx.Account.TotalPnL,
		PositionCount:         ctx.Account.PositionCount,
		MarginUsedPct:         ctx.Account.MarginUsedPct,
	}

	// 保存持仓快照
	for _, pos := range ctx.Positions {
		record.Positions = append(record.Positions, logger.PositionSnapshot{
			Symbol:           pos.Symbol,
			Side:             pos.Side,
			PositionAmt:      pos.Quantity,
			EntryPrice:       pos.EntryPrice,
			MarkPrice:        pos.MarkPrice,
			UnrealizedProfit: pos.UnrealizedPnL,
			Leverage:         float64(pos.Leverage),
			LiquidationPrice: pos.LiquidationPrice,
		})
	}

	// 保存候选币种列表
	for _, coin := range ctx.CandidateCoins {
		record.CandidateCoins = append(record.CandidateCoins, coin.Symbol)
	}

	log.Printf("📊 账户净值: %.2f USDT | 可用: %.2f USDT | 持仓: %d",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)

	// 3.4. 写入上一轮检测到的自动平仓事件（如止损被打）
	if autoEvents := at.drainAutoCloseEvents(); len(autoEvents) > 0 {
		for _, evt := range autoEvents {
			record.Decisions = append(record.Decisions, evt)
			reason := "auto_close"
			if evt.WasStopLoss {
				reason = "stop_loss"
			}
			record.ExecutionLog = append(record.ExecutionLog,
				fmt.Sprintf("⚠️ 检测到 %s %s 被交易所自动平仓 (%s)", evt.Symbol, evt.Action, reason))
		}
	}

	// 3.5. 同步限价单状态（检测已成交的限价单并清理）
	log.Println("🔍 同步限价单状态...")
	if err := at.syncPendingOrders(); err != nil {
		log.Printf("⚠️ 同步限价单失败: %v", err)
	}

	// 3.6. 自动检测TP触及并抬止损（代码层自动执行，不需要AI介入）
	log.Println("🔍 检查持仓TP触及情况...")
	if err := at.autoCheckAndUpdateStopLoss(); err != nil {
		log.Printf("⚠️ 自动抬止损检查失败: %v", err)
	}

	// 4. PreLLM Gate：检查冷却状态和极端波动
	log.Println("🚪 执行PreLLM门控检查...")
	skipLLM, allowedSymbols, cooldownSymbols, extremeSymbols := at.preLLMGate(ctx.CandidateCoins)

	record.CooldownSkipLLM = skipLLM
	record.CooldownSymbols = cooldownSymbols
	record.ExtremeSymbols = extremeSymbols

	var decisionResp *decision.FullDecision

	// 如果需要跳过LLM，直接生成决策
	if skipLLM {
		log.Printf("⏭️ 跳过LLM调用，直接生成决策")
		allCooldownDecisions := append(
			at.generateCooldownDecisions(cooldownSymbols, "cooldown中"),
			at.generateCooldownDecisions(extremeSymbols, "极端波动中")...,
		)

		// 模拟FullDecision响应
		decisionResp = &decision.FullDecision{
			SystemPrompt: "PreLLM Gate: Skipped due to cooldown/extreme volatility",
			UserPrompt:   "N/A",
			CoTTrace:     "跳过LLM调用，系统自动生成保守决策",
			Decisions:    allCooldownDecisions,
			Timestamp:    time.Now(),
		}
		err = nil
	} else {
		// 5. 调用AI获取完整决策（只对允许的symbols）
		log.Printf("🤖 正在请求AI分析并决策... [模板: %s] [允许交易: %v]", at.systemPromptTemplate, allowedSymbols)

		// 动态拼接当前持仓的TP1/TP2/TP3到本轮自定义prompt里
		dynamicPrompt := at.buildDynamicPrompt(ctx)
		finalPrompt := at.customPrompt
		if dynamicPrompt != "" {
			if finalPrompt != "" {
				finalPrompt = finalPrompt + "\n\n" + dynamicPrompt
			} else {
				finalPrompt = dynamicPrompt
			}
		}

		decisionResp, err = decision.GetFullDecisionWithCustomPromptAndTraderID(ctx, at.mcpClient, finalPrompt, at.overrideBasePrompt, at.systemPromptTemplate, at.id, at.globalConfig)

		// 如果LLM调用成功，合并冷却symbol的决策
		if err == nil && decisionResp != nil {
			cooldownDecisions := at.generateCooldownDecisions(cooldownSymbols, "cooldown中")
			extremeDecisions := at.generateCooldownDecisions(extremeSymbols, "极端波动中")
			decisionResp.Decisions = append(decisionResp.Decisions, cooldownDecisions...)
			decisionResp.Decisions = append(decisionResp.Decisions, extremeDecisions...)
		}
	}

	// 即使有错误，也保存思维链、决策和输入prompt（用于debug）
	if decisionResp != nil {
		record.SystemPrompt = decisionResp.SystemPrompt // 保存系统提示词
		record.InputPrompt = decisionResp.UserPrompt
		record.CoTTrace = decisionResp.CoTTrace

		// 保存当前思维链供下一周期参考
		if decisionResp.CoTTrace != "" {
			at.lastCoTTrace = decisionResp.CoTTrace
		}

		if len(decisionResp.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(decisionResp.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		}
	}

	if err != nil {
		// 检查是否是DecisionError
		if decisionErr, ok := err.(*decision.DecisionError); ok {
			if decisionErr.Type == decision.DECISION_VALIDATION_REJECTED {
				// 对于验证拒绝，不return error，而是记录为warning状态
				record.Success = true // 分析成功，只是被风控拦截
				record.Status = "warning"
				record.ErrorType = string(decisionErr.Type)
				record.ErrorSeverity = "warning"
				record.ErrorMessage = decisionErr.Message

				// 提取验证错误详情
				if causeErr := decisionErr.Cause; causeErr != nil {
					record.ValidationErrors = []logger.ValidationError{
						{
							Symbol: "", // TODO: 从错误信息中提取symbol
							Action: "", // TODO: 从错误信息中提取action
							Reason: causeErr.Error(),
						},
					}
				}

				log.Printf("⚠️ AI决策被风控拦截: %s", decisionErr.Message)
			} else {
				// 其他DecisionError类型仍按error处理
				record.Success = false
				record.Status = "error"
				record.ErrorType = string(decisionErr.Type)
				record.ErrorSeverity = "error"
				record.ErrorMessage = fmt.Sprintf("获取AI决策失败: %v", err)
			}
		} else {
			// 普通错误
			record.Success = false
			record.Status = "error"
			record.ErrorSeverity = "error"
			record.ErrorMessage = fmt.Sprintf("获取AI决策失败: %v", err)
		}

		// 打印系统提示词和AI思维链（即使有错误，也要输出以便调试）
		if decisionResp != nil {
			if decisionResp.SystemPrompt != "" {
				eqSeparator := strings.Repeat("=", 70)
				log.Printf("\n%s", eqSeparator)
				log.Printf("📋 系统提示词 [模板: %s] (%s情况)", at.systemPromptTemplate, record.Status)
				log.Println(eqSeparator)
				log.Println(decisionResp.SystemPrompt)
				log.Printf("%s\n", eqSeparator)
			}

			if decisionResp.CoTTrace != "" {
				dashSeparator := strings.Repeat("-", 70)
				log.Printf("\n%s", dashSeparator)
				log.Printf("💭 AI思维链分析（%s情况）:", record.Status)
				log.Println(dashSeparator)
				log.Println(decisionResp.CoTTrace)
				log.Printf("%s\n", dashSeparator)
			}
		}

		at.decisionLogger.LogDecision(record)

		// 对于DECISION_VALIDATION_REJECTED，不return error，继续执行
		if record.Status == "warning" && record.ErrorType == "DECISION_VALIDATION_REJECTED" {
			log.Printf("✅ 继续执行流程（AI分析成功，仅决策被风控拦截）")
		} else {
			return fmt.Errorf("获取AI决策失败: %w", err)
		}
	}

	log.Println()

	// 8. 对决策排序：确保先平仓后开仓（防止仓位叠加超限）
	sortedDecisions := sortDecisionsByPriority(decisionResp.Decisions)

	log.Println("🔄 执行顺序（已优化）: 先平仓→后开仓")
	for i, d := range sortedDecisions {
		log.Printf("  [%d] %s %s", i+1, d.Symbol, d.Action)
	}
	log.Println()

	// 执行决策并记录结果
	for _, d := range sortedDecisions {
		actionRecord := logger.DecisionAction{
			Action:    d.Action,
			Symbol:    d.Symbol,
			Quantity:  0,
			Leverage:  d.Leverage,
			Price:     0,
			Timestamp: time.Now(),
			Success:   false,
		}

		if err := at.executeDecisionWithRecord(&d, &actionRecord); err != nil {
			log.Printf("❌ 执行决策失败 (%s %s): %v", d.Symbol, d.Action, err)
			actionRecord.Error = err.Error()
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("❌ %s %s 失败: %v", d.Symbol, d.Action, err))
		} else {
			actionRecord.Success = true
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("✓ %s %s 成功", d.Symbol, d.Action))
			// 成功执行后短暂延迟
			time.Sleep(1 * time.Second)
		}

		record.Decisions = append(record.Decisions, actionRecord)
	}

	// 9. 保存决策记录
	if err := at.decisionLogger.LogDecision(record); err != nil {
		log.Printf("⚠ 保存决策记录失败: %v", err)
	}

	return nil
}

// syncPendingOrders 同步限价单状态，检测已成交的限价单并自动设置止盈止损
func (at *AutoTrader) syncPendingOrders() error {
	if len(at.pendingOrders) == 0 {
		return nil
	}

	// 检查每个pending order是否还在交易所的挂单列表中
	for posKey, pendingOrder := range at.pendingOrders {
		// 获取该币种的未成交订单
		openOrders, err := at.trader.GetOpenOrders(pendingOrder.Symbol)
		if err != nil {
			log.Printf("  ⚠️ 获取 %s 未成交订单失败: %v", pendingOrder.Symbol, err)
			continue
		}

		// 检查该订单ID是否还存在
		orderExists := false
		for _, order := range openOrders {
			if orderID, ok := order["orderId"].(int64); ok && orderID == pendingOrder.OrderID {
				orderExists = true
				break
			}
		}

		// 如果订单不存在，说明已成交或已取消
		if !orderExists {
			// 检查是否真的成交了（通过检查持仓）
			positions, err := at.trader.GetPositions()
			if err != nil {
				log.Printf("  ⚠️ 获取持仓失败: %v", err)
				// 即使获取持仓失败，也删除pending order（可能是已取消）
				delete(at.pendingOrders, posKey)
				delete(at.positionFirstSeenTime, posKey)
				continue
			}

			// 检查是否有对应的持仓
			hasPosition := false
			for _, pos := range positions {
				symbol, _ := pos["symbol"].(string)
				side, _ := pos["side"].(string)
				if symbol == pendingOrder.Symbol && strings.ToLower(side) == pendingOrder.Side {
					hasPosition = true

					// 获取持仓数量
					qty, _ := pos["positionAmt"].(float64)
					if qty < 0 {
						qty = -qty
					}

					// 限价单成交后，自动设置止盈止损
					log.Printf("  ✓ 限价单已成交: %s %s (订单ID: %d), 自动设置止盈止损",
						pendingOrder.Symbol, pendingOrder.Side, pendingOrder.OrderID)

					// 设置止损
					if pendingOrder.StopLoss > 0 {
						if err := at.trader.SetStopLoss(pendingOrder.Symbol, strings.ToUpper(pendingOrder.Side), qty, pendingOrder.StopLoss); err != nil {
							log.Printf("  ⚠️ 限价单成交后设置止损失败: %v", err)
						} else {
							log.Printf("  ✓ 止损已设置: %.4f", pendingOrder.StopLoss)
						}
					}

					// 设置止盈（TP3）
					if pendingOrder.TakeProfit > 0 {
						if err := at.trader.SetTakeProfit(pendingOrder.Symbol, strings.ToUpper(pendingOrder.Side), qty, pendingOrder.TakeProfit); err != nil {
							log.Printf("  ⚠️ 限价单成交后设置止盈失败: %v", err)
						} else {
							log.Printf("  ✓ 止盈已设置: %.4f", pendingOrder.TakeProfit)
						}
					}

					// 记录AI给的三个止盈点位（与市价单相同）
					at.positionTargets[posKey] = &PositionTarget{
						TP1:       pendingOrder.TP1,
						TP2:       pendingOrder.TP2,
						TP3:       pendingOrder.TP3,
						Stage:     0,
						CurrentSL: pendingOrder.StopLoss,
					}

					// 记录开仓时间
					at.positionFirstSeenTime[posKey] = pendingOrder.CreateTime

					break
				}
			}

			if hasPosition {
				log.Printf("  ✓ 限价单已成交并完成止盈止损设置: %s %s (订单ID: %d)",
					pendingOrder.Symbol, pendingOrder.Side, pendingOrder.OrderID)
			} else {
				log.Printf("  ✓ 限价单已取消: %s %s (订单ID: %d), 从待处理列表中移除",
					pendingOrder.Symbol, pendingOrder.Side, pendingOrder.OrderID)
			}

			// 从待处理列表中移除
			delete(at.pendingOrders, posKey)
			if !hasPosition {
				delete(at.positionFirstSeenTime, posKey)
			}
		}
	}

	return nil
}

func (at *AutoTrader) autoCheckAndUpdateStopLoss() error {
	// 获取当前所有持仓
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	if len(positions) == 0 {
		return nil
	}

	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		entry, _ := pos["entryPrice"].(float64)
		qty, _ := pos["positionAmt"].(float64)
		if qty < 0 {
			qty = -qty
		}

		// 构造position key
		sideKey := strings.ToLower(side)
		posKey := fmt.Sprintf("%s_%s", symbol, sideKey)

		// 检查是否有TP记录
		tgt, ok := at.positionTargets[posKey]
		if !ok || tgt == nil {
			continue
		}

		// 获取当前市价
		mkt, err := market.Get(symbol)
		if err != nil {
			log.Printf("  ⚠️ %s 获取市价失败: %v", symbol, err)
			continue
		}
		currentPrice := mkt.CurrentPrice

		// 计算新的止损和阶段
		newSL, newStage := computeTrailingSL(entry, strings.ToUpper(side), tgt, currentPrice)

		// 如果没有变化，跳过
		if newSL <= 0 || newSL == tgt.CurrentSL || newStage <= tgt.Stage {
			continue
		}

		// 安全间隔检查
		const minGapRatio = 0.0005 // 0.05%

		switch strings.ToUpper(side) {
		case "LONG":
			// 多单止损必须在市价下方
			maxSL := currentPrice * (1 - minGapRatio)
			if newSL >= maxSL {
				log.Printf("  ⚠️ %s LONG 计算出的新止损 %.4f 过于接近市价 %.4f，调整为 %.4f",
					symbol, newSL, currentPrice, maxSL)
				newSL = maxSL
			}
			if newSL <= tgt.CurrentSL {
				continue
			}

		case "SHORT":
			// 空单止损必须在市价上方
			minSL := currentPrice * (1 + minGapRatio)
			if newSL <= minSL {
				log.Printf("  ⚠️ %s SHORT 计算出的新止损 %.4f 过于接近市价 %.4f，调整为 %.4f",
					symbol, newSL, currentPrice, minSL)
				newSL = minSL
			}
			if newSL >= tgt.CurrentSL {
				continue
			}
		}

		// 执行分批止盈（根据阶段变化）
		var partialCloseQty float64
		var partialCloseRatio string
		partialCloseSuccess := false // 标记分批平仓是否成功

		if newStage > tgt.Stage {
			switch newStage {
			case 1: // 到达 TP1：平掉 1/4 仓位
				partialCloseQty = qty * (1.0 / 4.0)
				partialCloseRatio = "1/4"
			case 2: // 到达 TP2：再平 1/3（剩余仓位的 1/3）
				partialCloseQty = qty * (1.0 / 3.0)
				partialCloseRatio = "1/3 剩余"
			case 3: // 到达 TP3：交易所的止盈单会自动平掉全部
				// 不需要手动平仓，TP3止盈单会自动触发
				log.Printf("  🎯 %s %s 到达TP3，等待止盈单自动平仓", symbol, strings.ToUpper(side))
				partialCloseSuccess = true // TP3 不需要平仓，直接标记为成功
			}

			// 执行分批平仓（TP1和TP2时）
			if partialCloseQty > 0 && newStage < 3 {
				log.Printf("  💰 分批止盈: %s %s | Stage=%d→%d | 平仓 %s (数量: %.4f)",
					symbol, strings.ToUpper(side), tgt.Stage, newStage, partialCloseRatio, partialCloseQty)

				var closeErr error
				switch strings.ToUpper(side) {
				case "LONG":
					_, closeErr = at.trader.CloseLong(symbol, partialCloseQty)
				case "SHORT":
					_, closeErr = at.trader.CloseShort(symbol, partialCloseQty)
				}

				if closeErr != nil {
					log.Printf("  ❌ %s 分批平仓失败: %v，Stage 不会更新，下次仍会重试", symbol, closeErr)
					// 平仓失败，不更新 Stage，下次检查时仍会重试
					partialCloseSuccess = false
				} else {
					log.Printf("  ✅ %s %s 成功平仓 %s，剩余仓位继续持有", symbol, strings.ToUpper(side), partialCloseRatio)
					partialCloseSuccess = true

					// 更新当前持仓数量（用于后续止损设置）
					// 重新获取最新持仓数量
					updatedPositions, err := at.trader.GetPositions()
					if err == nil {
						for _, updatedPos := range updatedPositions {
							if updatedPos["symbol"] == symbol && updatedPos["side"] == side {
								qty, _ = updatedPos["positionAmt"].(float64)
								if qty < 0 {
									qty = -qty
								}
								log.Printf("  📊 %s 更新后的仓位数量: %.4f", symbol, qty)
								break
							}
						}
					}
				}
			}
		}

		// 执行抬止损（无论平仓成功与否，都尝试抬止损）
		log.Printf("  📈 自动抬止损: %s %s | 阶段 %d→%d | 止损 %.4f→%.4f",
			symbol, strings.ToUpper(side), tgt.Stage, newStage, tgt.CurrentSL, newSL)

		slUpdateSuccess := false
		if err := at.trader.SetStopLoss(symbol, strings.ToUpper(side), qty, newSL); err != nil {
			log.Printf("  ❌ %s 设置止损失败: %v", symbol, err)
			// 抬止损失败，但继续执行 Stage 更新逻辑（如果平仓成功）
		} else {
			slUpdateSuccess = true
		}

		// 更新内存记录
		// 关键修复：只有平仓成功（或TP3不需要平仓）时，才更新 Stage
		// 这样可以防止平仓失败后，Stage 被错误更新，导致下次检查时无法重试
		// 注意：即使抬止损失败，只要平仓成功，也要更新 Stage，避免重复平仓
		if newStage > tgt.Stage {
			// 对于 TP1 和 TP2，必须平仓成功才更新 Stage
			// 对于 TP3，不需要平仓，直接更新 Stage
			if newStage < 3 {
				// TP1 或 TP2：只有平仓成功才更新 Stage
				if partialCloseSuccess {
					tgt.Stage = newStage
					log.Printf("  ✅ %s %s Stage 已更新为 %d（平仓成功）", symbol, strings.ToUpper(side), tgt.Stage)
					// 如果抬止损失败，记录警告但继续
					if !slUpdateSuccess {
						log.Printf("  ⚠️ %s %s 抬止损失败，但 Stage 已更新，避免重复平仓", symbol, strings.ToUpper(side))
					}
				} else {
					log.Printf("  ⚠️ %s %s Stage 保持为 %d（平仓失败，下次重试）", symbol, strings.ToUpper(side), tgt.Stage)
				}
			} else {
				// TP3：不需要平仓，直接更新 Stage
				tgt.Stage = newStage
				log.Printf("  ✅ %s %s Stage 已更新为 %d（到达TP3）", symbol, strings.ToUpper(side), tgt.Stage)
			}
		}

		// 更新止损价（只有抬止损成功时才更新）
		if slUpdateSuccess {
			tgt.CurrentSL = newSL
			log.Printf("  ✅ %s %s 止损已自动抬升至 %.4f (Stage=%d)", symbol, strings.ToUpper(side), newSL, tgt.Stage)
		} else {
			log.Printf("  ⚠️ %s %s 止损抬升失败，当前止损仍为 %.4f (Stage=%d)", symbol, strings.ToUpper(side), tgt.CurrentSL, tgt.Stage)
		}
	}

	return nil
}

// buildDynamicPrompt 把当前持仓的 tp1/tp2/tp3 拼成一段，喂回给AI，让它知道什么时候该发 update_stop_loss
func (at *AutoTrader) buildDynamicPrompt(ctx *decision.Context) string {
	var sb strings.Builder

	// 1. 添加上一周期的思维链（如果存在）
	if at.lastCoTTrace != "" {
		sb.WriteString("# 📝 上一周期的思维链（供参考，保持决策连贯性）\n")
		sb.WriteString("```\n")
		sb.WriteString(at.lastCoTTrace)
		sb.WriteString("\n```\n\n")
		sb.WriteString("注意：以上是上一周期的分析思路，请参考但不要盲目跟随。如果市场情况发生变化，应该及时调整策略。\n\n")
	}

	// 2. 添加当前持仓止盈结构（如果有持仓）
	if len(ctx.Positions) > 0 {
		sb.WriteString("# 当前持仓止盈结构（系统自动分批止盈+抬止损）\n")
		sb.WriteString("# TP1: 自动平仓 1/4 + 抬止损到开仓价\n")
		sb.WriteString("# TP2: 自动平仓 1/3剩余 + 抬止损到 (entry+TP1)/2\n")
		sb.WriteString("# TP3: 止盈单自动平掉全部剩余仓位\n")
		for _, pos := range ctx.Positions {
			sideKey := strings.ToLower(pos.Side) // long / short
			key := fmt.Sprintf("%s_%s", pos.Symbol, sideKey)
			if target, ok := at.positionTargets[key]; ok && target != nil {
				sb.WriteString(fmt.Sprintf("- %s %s | entry=%.4f | tp1=%.4f | tp2=%.4f | tp3=%.4f | stage=%d\n",
					pos.Symbol, strings.ToUpper(pos.Side),
					pos.EntryPrice, target.TP1, target.TP2, target.TP3, target.Stage))
			} else {
				sb.WriteString(fmt.Sprintf("- %s %s | entry=%.4f | 未记录tp1/tp2/tp3，请按系统规则（1h/4h斐波那契+4h/15m区间核对）自行补全；到达tp1/tp2仅返回update_stop_loss。\n",
					pos.Symbol, strings.ToUpper(pos.Side), pos.EntryPrice))
			}
		}
	}

	return sb.String()
}

// buildTradingContext 构建交易上下文
func (at *AutoTrader) buildTradingContext() (*decision.Context, error) {
	// 1. 获取账户信息
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取账户余额失败: %w", err)
	}

	// 获取账户字段
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Total Equity = 钱包余额 + 未实现盈亏
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 2. 获取持仓信息
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	var positionInfos []decision.PositionInfo
	totalMarginUsed := 0.0

	// 当前持仓的key集合（用于清理已平仓的记录）
	currentPositionKeys := make(map[string]bool)

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // 空仓数量为负，转为正数
		}

		// 跳过已平仓的持仓（quantity = 0），防止"幽灵持仓"传递给AI
		if quantity == 0 {
			continue
		}

		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		// 计算占用保证金（估算）
		leverage := 10 // 默认值，实际应该从持仓信息获取
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)

		// 计算盈亏百分比（基于实际盈亏和保证金）
		pnlPct := 0.0
		if marginUsed > 0 {
			pnlPct = (unrealizedPnl / marginUsed) * 100
		}
		totalMarginUsed += marginUsed

		// 跟踪持仓首次出现时间
		posKey := symbol + "_" + side
		currentPositionKeys[posKey] = true
		if _, exists := at.positionFirstSeenTime[posKey]; !exists {
			// 新持仓，记录当前时间
			at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
		}
		updateTime := at.positionFirstSeenTime[posKey]

		posInfo := decision.PositionInfo{
			Symbol:           symbol,
			Side:             side,
			EntryPrice:       entryPrice,
			MarkPrice:        markPrice,
			Quantity:         quantity,
			Leverage:         leverage,
			UnrealizedPnL:    unrealizedPnl,
			UnrealizedPnLPct: pnlPct,
			LiquidationPrice: liquidationPrice,
			MarginUsed:       marginUsed,
			UpdateTime:       updateTime,
		}

		positionInfos = append(positionInfos, posInfo)
		at.positionMemory[posKey] = posInfo
	}

	// 清理已平仓的持仓记录，并撤销孤儿委托单
	for key := range at.positionFirstSeenTime {
		if !currentPositionKeys[key] {
			// 仓位消失了（可能被止损/止盈触发，或被強平）
			// 提取币种名称（key 格式：BTCUSDT_long 或 SOLUSDT_short）
			parts := strings.Split(key, "_")
			if len(parts) == 2 {
				symbol := parts[0]
				log.Printf("⚠️ 检测到仓位消失: %s → 自动撤销委托单", symbol)

				// 撤销该币种的所有委托单（清理孤儿止损/止盈單）
				if err := at.trader.CancelAllOrders(symbol); err != nil {
					log.Printf("  ⚠️ 撤销 %s 委托单失败: %v", symbol, err)
				} else {
					log.Printf("  ✓ 已撤销 %s 的所有委托单", symbol)
				}
			}

			// 记录自动平仓事件
			at.recordAutoClosedPosition(key)

			delete(at.positionFirstSeenTime, key)
			// 同步清理该持仓的TP记忆
			delete(at.positionTargets, key)
			delete(at.positionMemory, key)
		}
	}

	// 3. 获取交易员的候选币种池
	candidateCoins, err := at.getCandidateCoins()
	if err != nil {
		return nil, fmt.Errorf("获取候选币种失败: %w", err)
	}

	// 4. 计算总盈亏
	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 5. 分析历史表现
	performance, err := at.decisionLogger.AnalyzePerformance(100)
	if err != nil {
		log.Printf("⚠️  分析历史表现失败: %v", err)
		performance = nil
	}

	// 6. 构建限价单信息
	pendingOrderInfos := make([]decision.PendingOrderInfo, 0)
	for _, order := range at.pendingOrders {
		// 计算挂单时长
		durationMs := time.Now().UnixMilli() - order.CreateTime
		durationMin := int(durationMs / (1000 * 60))

		pendingOrderInfos = append(pendingOrderInfos, decision.PendingOrderInfo{
			Symbol:           order.Symbol,
			Side:             order.Side,
			LimitPrice:       order.LimitPrice,
			Quantity:         order.Quantity,
			Leverage:         order.Leverage,
			OrderID:          order.OrderID,
			TP1:              order.TP1,
			TP2:              order.TP2,
			TP3:              order.TP3,
			StopLoss:         order.StopLoss,
			TakeProfit:       order.TakeProfit,
			CreateTime:       order.CreateTime,
			DurationMin:      durationMin,
			Confidence:       order.Confidence,
			Reasoning:        order.Reasoning,
			Thesis:           order.Thesis,
			CancelConditions: order.CancelConditions,
		})
	}

	// 7. 构建上下文
	ctx := &decision.Context{
		CurrentTime:     time.Now().Format("2006-01-02 15:04:05"),
		RuntimeMinutes:  int(time.Since(at.startTime).Minutes()),
		CallCount:       at.callCount,
		BTCETHLeverage:  at.config.BTCETHLeverage,
		AltcoinLeverage: at.config.AltcoinLeverage,
		Account: decision.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:            positionInfos,
		PendingOrders:        pendingOrderInfos,
		CandidateCoins:       candidateCoins,
		DailyPairTrades:      at.dailyPairTrades,
		Performance:          performance,
		RiskManagementConfig: &at.globalConfig.RiskManagement,
	}

	return ctx, nil
}

// recordAutoClosedPosition 在检测到交易所自动平仓后，补写一条 close_long/close_short 记录
func (at *AutoTrader) recordAutoClosedPosition(posKey string) {
	info, ok := at.positionMemory[posKey]
	if !ok || info.Symbol == "" {
		return
	}

	symbol := info.Symbol
	side := strings.ToLower(info.Side)
	if side != "long" && side != "short" {
		parts := strings.Split(posKey, "_")
		if len(parts) == 2 {
			side = strings.ToLower(parts[1])
		}
	}

	action := "close_long"
	if side == "short" {
		action = "close_short"
	}

	target := at.positionTargets[posKey]

	marketPrice := 0.0
	if mkt, err := market.Get(symbol); err == nil && mkt.CurrentPrice > 0 {
		marketPrice = mkt.CurrentPrice
	}

	closePrice := marketPrice
	if closePrice == 0 {
		closePrice = info.MarkPrice
	}
	if closePrice == 0 {
		closePrice = info.EntryPrice
	}
	if closePrice == 0 && target != nil && target.CurrentSL > 0 {
		closePrice = target.CurrentSL
	}

	wasStopLoss := false
	if target != nil {
		const minRelDist = 0.003 // 0.3% 容错
		distanceToSL := math.MaxFloat64
		distanceToTP := math.MaxFloat64

		if target.CurrentSL > 0 && closePrice > 0 {
			distanceToSL = math.Abs(closePrice-target.CurrentSL) / target.CurrentSL
		}
		if target.TP3 > 0 && closePrice > 0 {
			distanceToTP = math.Abs(closePrice-target.TP3) / target.TP3
		}

		if distanceToSL <= distanceToTP || distanceToSL <= minRelDist {
			wasStopLoss = true
			if target.CurrentSL > 0 {
				closePrice = target.CurrentSL
			}
		} else if target.TP3 > 0 {
			closePrice = target.TP3
		}
	} else {
		const tolerance = 0.001 // 0.1% 容错
		if side == "long" {
			wasStopLoss = closePrice <= info.EntryPrice*(1+tolerance)
		} else if side == "short" {
			wasStopLoss = closePrice >= info.EntryPrice*(1-tolerance)
		}
	}

	event := logger.DecisionAction{
		Action:      action,
		Symbol:      symbol,
		Quantity:    info.Quantity,
		Leverage:    info.Leverage,
		Price:       closePrice,
		Timestamp:   time.Now(),
		Success:     true,
		WasStopLoss: wasStopLoss,
	}

	reason := "止盈/自动平仓"
	if wasStopLoss {
		reason = "止损触发"
		// 更新cooldown状态
		at.updateCooldownState(symbol, side)
	}
	log.Printf("⚠️ 检测到 %s %s 被交易所自动平仓（%s），价格 %.4f，数量 %.4f", symbol, strings.ToUpper(side), reason, closePrice, info.Quantity)

	at.autoCloseEvents = append(at.autoCloseEvents, event)
	delete(at.positionMemory, posKey)
}

// updateCooldownState 更新冷却状态（止损触发时调用）
func (at *AutoTrader) updateCooldownState(symbol, side string) {
	key := fmt.Sprintf("%s_%s", symbol, side)
	now := time.Now().UnixMilli()

	// 记录止损历史
	if at.stopLossHistory[key] == nil {
		at.stopLossHistory[key] = make([]int64, 0)
	}
	at.stopLossHistory[key] = append(at.stopLossHistory[key], now)

	// 检查12小时内的止损次数
	twelveHoursAgo := now - (12 * 60 * 60 * 1000) // 12小时前
	stopLossCount := 0
	for _, stopLossTime := range at.stopLossHistory[key] {
		if stopLossTime >= twelveHoursAgo {
			stopLossCount++
		}
	}

	// 计算冷却时间
	var cooldownMinutes int
	if stopLossCount >= 2 {
		// 12小时内第二次止损：4小时冷却
		cooldownMinutes = 4 * 60
		log.Printf("🚫 %s %s 12小时内第%d次止损，进入4小时冷却", symbol, strings.ToUpper(side), stopLossCount)
	} else {
		// 首次止损：60分钟冷却
		cooldownMinutes = 60
		log.Printf("🚫 %s %s 首次止损，进入60分钟冷却", symbol, strings.ToUpper(side))
	}

	// 设置冷却到期时间
	at.cooldownStates[key] = now + int64(cooldownMinutes*60*1000)
}

// isInCooldown 检查symbol+direction是否处于冷却状态
func (at *AutoTrader) isInCooldown(symbol, side string) bool {
	key := fmt.Sprintf("%s_%s", symbol, side)
	cooldownUntil, exists := at.cooldownStates[key]
	if !exists {
		return false
	}

	now := time.Now().UnixMilli()
	return now < cooldownUntil
}

// getRemainingCooldownMinutes 获取剩余冷却时间（分钟）
func (at *AutoTrader) getRemainingCooldownMinutes(symbol, side string) int {
	key := fmt.Sprintf("%s_%s", symbol, side)
	cooldownUntil, exists := at.cooldownStates[key]
	if !exists {
		return 0
	}

	now := time.Now().UnixMilli()
	if now >= cooldownUntil {
		return 0
	}

	return int((cooldownUntil - now) / (60 * 1000))
}

// drainAutoCloseEvents 取出累积的自动平仓事件
func (at *AutoTrader) drainAutoCloseEvents() []logger.DecisionAction {
	if len(at.autoCloseEvents) == 0 {
		return nil
	}

	events := at.autoCloseEvents
	at.autoCloseEvents = nil
	return events
}

// executeDecisionWithRecord 执行AI决策并记录详细信息
// preLLMGate LLM前置门控（检查冷却状态和极端波动）
func (at *AutoTrader) preLLMGate(candidateCoins []decision.CandidateCoin) (skipLLM bool, allowedSymbols []string, cooldownSymbols []string, extremeSymbols []string) {
	// 提取symbol列表
	symbols := make([]string, len(candidateCoins))
	for i, coin := range candidateCoins {
		symbols[i] = coin.Symbol
	}
	allInCooldown := true
	cooldownSymbols = make([]string, 0)
	extremeSymbols = make([]string, 0)
	allowedSymbols = make([]string, 0)

	// 检查每个symbol的状态
	for _, symbol := range symbols {
		hasCooldown := false
		hasExtreme := false

		// 检查多空两个方向的冷却状态
		for _, side := range []string{"long", "short"} {
			if at.isInCooldown(symbol, side) {
				hasCooldown = true
				remaining := at.getRemainingCooldownMinutes(symbol, side)
				log.Printf("⏰ %s %s 冷却中，剩余%d分钟", symbol, strings.ToUpper(side), remaining)
				break // 只要一个方向在冷却，整个symbol就跳过
			}
		}

		// 检查极端波动
		if marketData, err := market.Get(symbol); err == nil {
			if marketData.RiskMetrics != nil && marketData.RiskMetrics.VolatilityLevel == "extreme" {
				hasExtreme = true
				log.Printf("🌪️ %s 极端波动(extreme)，跳过LLM调用", symbol)
			}
		}

		if hasCooldown {
			cooldownSymbols = append(cooldownSymbols, symbol)
		} else if hasExtreme {
			extremeSymbols = append(extremeSymbols, symbol)
		} else {
			allowedSymbols = append(allowedSymbols, symbol)
			allInCooldown = false
		}
	}

	// 如果所有symbol都在冷却中，直接跳过LLM
	skipLLM = allInCooldown && len(cooldownSymbols) > 0

	if skipLLM {
		log.Printf("🚫 所有symbol都在冷却中，跳过本轮LLM调用")
	} else if len(allowedSymbols) == 0 {
		log.Printf("🚫 没有允许交易的symbol，跳过LLM调用")
		skipLLM = true
	}

	return skipLLM, allowedSymbols, cooldownSymbols, extremeSymbols
}

// generateCooldownDecisions 为冷却中的symbol生成wait/hold决策
func (at *AutoTrader) generateCooldownDecisions(symbols []string, reason string) []decision.Decision {
	decisions := make([]decision.Decision, 0, len(symbols))

	for _, symbol := range symbols {
		// 检查是否有持仓需要管理
		positions, err := at.trader.GetPositions()
		if err != nil {
			log.Printf("⚠️ 获取持仓失败: %v", err)
			continue
		}

		hasPosition := false
		hasPendingOrders := false

		// 检查是否有持仓
		for _, pos := range positions {
			posSymbol, _ := pos["symbol"].(string)
			if posSymbol == symbol {
				hasPosition = true
				break
			}
		}

		// 检查是否有挂单
		for _, pending := range at.pendingOrders {
			if pending.Symbol == symbol {
				hasPendingOrders = true
				break
			}
		}

		action := "wait"
		if hasPosition || hasPendingOrders {
			action = "hold"
		}

		decision := decision.Decision{
			Symbol:    symbol,
			Action:    action,
			Reasoning: fmt.Sprintf("%s，禁止开仓，等待冷却结束", reason),
		}

		decisions = append(decisions, decision)
		log.Printf("🧊 %s 生成%s决策: %s", symbol, action, reason)
	}

	return decisions
}

// validateCooldownEnforcer 冷却强制执行器（双保险）
func (at *AutoTrader) validateCooldownEnforcer(decision *decision.Decision) (bool, string) {
	// 只对开仓动作进行验证
	isOpenAction := decision.Action == "open_long" || decision.Action == "open_short" ||
		decision.Action == "limit_open_long" || decision.Action == "limit_open_short"

	if !isOpenAction {
		return true, "" // 非开仓动作，直接允许
	}

	// 检查多空两个方向的冷却状态
	for _, side := range []string{"long", "short"} {
		if at.isInCooldown(decision.Symbol, side) {
			remaining := at.getRemainingCooldownMinutes(decision.Symbol, side)
			return false, fmt.Sprintf("%s %s 冷却中，剩余%d分钟，禁止开仓",
				decision.Symbol, strings.ToUpper(side), remaining)
		}
	}

	return true, ""
}

// sanitizeDecision 决策输出一致性修复器
func sanitizeDecision(decision *decision.Decision) (bool, string, []string) {
	var fixes []string
	var rejections []string

	// 1. 强制补齐version（默认v1）- 如果需要的话
	// 暂时跳过，因为Decision结构体中没有version字段

	// 2. 对开仓动作：强制take_profit = tp3
	isOpenAction := decision.Action == "open_long" || decision.Action == "open_short" ||
		decision.Action == "limit_open_long" || decision.Action == "limit_open_short"

	if isOpenAction {
		if decision.TP3 != 0 && decision.TakeProfit != decision.TP3 {
			// 自动修正take_profit为tp3
			oldTP := decision.TakeProfit
			decision.TakeProfit = decision.TP3
			fixes = append(fixes, fmt.Sprintf("修正take_profit: %.4f → %.4f (tp3)", oldTP, decision.TakeProfit))
		}

		// 3. 对开仓reasoning：强制检查grade=X score=YY前缀
		grade, score, err := parseGradeAndScoreFromReasoning(decision.Reasoning)
		if err != nil {
			rejections = append(rejections, fmt.Sprintf("缺少grade/score前缀: %v", err))
		} else {
			// 4. 对B级：强制只能limit_open_*
			if grade == "B" {
				if decision.Action == "open_long" || decision.Action == "open_short" {
					rejections = append(rejections, fmt.Sprintf("B级决策只能使用限价开仓: grade=%s score=%d", grade, score))
				} else {
					fixes = append(fixes, fmt.Sprintf("B级限价开仓验证通过: grade=%s score=%d", grade, score))
				}
			}
		}
	}

	// 如果有拒绝原因，返回false
	if len(rejections) > 0 {
		return false, strings.Join(rejections, "; "), fixes
	}

	return true, "", fixes
}

// validateVolatilityCircuitBreaker 高波动熔断验证器
func (at *AutoTrader) validateVolatilityCircuitBreaker(decision *decision.Decision) (bool, string) {
	// 只对开仓动作进行验证
	isOpenAction := decision.Action == "open_long" || decision.Action == "open_short" ||
		decision.Action == "limit_open_long" || decision.Action == "limit_open_short"

	if !isOpenAction {
		return true, "" // 非开仓动作，直接允许
	}

	// 获取市场数据
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return false, fmt.Sprintf("获取市场数据失败: %v", err)
	}

	if marketData.RiskMetrics == nil {
		return false, "风险指标数据缺失"
	}

	volatilityLevel := marketData.RiskMetrics.VolatilityLevel

	switch volatilityLevel {
	case "extreme":
		// 极端波动：禁止任何开仓
		spreadBps := 0.0
		if marketData.Microstructure != nil {
			spreadBps = marketData.Microstructure.SpreadBps
		}
		return false, fmt.Sprintf("高波动熔断(extreme): 禁止开仓 vol_percentile=%.1f%% spread=%.2fbps",
			marketData.VolumePercentile15m, spreadBps)

	case "high":
		// 高波动：只允许限价开仓，禁止市价开仓
		if decision.Action == "open_long" || decision.Action == "open_short" {
			spreadBps := 0.0
			if marketData.Microstructure != nil {
				spreadBps = marketData.Microstructure.SpreadBps
			}
			return false, fmt.Sprintf("高波动熔断(high): 只允许限价开仓 vol_percentile=%.1f%% spread=%.2fbps",
				marketData.VolumePercentile15m, spreadBps)
		}

	case "medium", "low":
		// 中等/低波动：完全允许
	}

	return true, ""
}

// validateExecutionMode 执行模式强制验证器
func (at *AutoTrader) validateExecutionMode(decision *decision.Decision) (bool, string) {
	// 只对开仓动作进行验证
	isOpenAction := decision.Action == "open_long" || decision.Action == "open_short"
	if !isOpenAction {
		return true, "" // 非开仓动作，直接允许
	}

	// 获取市场数据
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return false, fmt.Sprintf("获取市场数据失败: %v", err)
	}

	if marketData.Execution == nil {
		return false, "execution gate数据缺失"
	}

	// 计算计划的notional（保证金 × 杠杆）
	plannedNotional := decision.PositionSizeUSD * float64(decision.Leverage)

	// 重新评估execution mode（包含计划仓位信息）
	micro := marketData.Microstructure
	newExecutionGate := market.EvaluateExecutionGate(micro, plannedNotional)

	// 根据execution mode强制执行门禁
	switch newExecutionGate.Mode {
	case "no_trade":
		return false, fmt.Sprintf("execution.mode拦截(no_trade): %s", newExecutionGate.Reason)
	case "limit_only":
		// 如果是市价开仓，拒绝；如果是限价开仓，允许
		if decision.Action == "open_long" || decision.Action == "open_short" {
			return false, fmt.Sprintf("execution.mode拦截(limit_only): %s - 必须使用limit_open_*", newExecutionGate.Reason)
		}
	case "limit_preferred":
		// 可以记录警告，但不强制拦截
		log.Printf("⚠️ execution.mode警告(limit_preferred): %s - 建议使用limit_open_*", newExecutionGate.Reason)
	case "market_ok":
		// 完全允许
	}

	return true, ""
}

// validateHedgeAntiHedge 对冲模式反自对冲验证器
func (at *AutoTrader) validateHedgeAntiHedge(decision *decision.Decision) (bool, string) {
	// 只对开仓动作进行验证
	isOpenAction := decision.Action == "open_long" || decision.Action == "open_short" ||
		decision.Action == "limit_open_long" || decision.Action == "limit_open_short"

	if !isOpenAction {
		return true, "" // 非开仓动作，直接允许
	}

	// 获取当前持仓
	positions, err := at.trader.GetPositions()
	if err != nil {
		return false, fmt.Sprintf("获取持仓失败: %v", err)
	}

	// 检查是否存在相反方向的持仓
	var existingSide string
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		if sym == decision.Symbol {
			existingSide = strings.ToLower(side)
			break
		}
	}

	// 如果没有持仓，直接允许开仓
	if existingSide == "" {
		return true, ""
	}

	// 检查是否有相反方向的开仓意图
	var isOppositeDirection bool
	if existingSide == "long" && (decision.Action == "open_short" || decision.Action == "limit_open_short") {
		isOppositeDirection = true
	} else if existingSide == "short" && (decision.Action == "open_long" || decision.Action == "limit_open_long") {
		isOppositeDirection = true
	}

	// 如果不是相反方向，直接允许
	if !isOppositeDirection {
		return true, ""
	}

	// 解析grade和score
	grade, score, err := parseGradeAndScoreFromReasoning(decision.Reasoning)
	if err != nil {
		return false, fmt.Sprintf("解析grade/score失败: %v", err)
	}

	// 检查是否满足白名单条件：反转D + grade=S + score≥88 + 结构反转成立
	hasReversalKeyword := strings.Contains(strings.ToLower(decision.Reasoning), "反转") ||
		strings.Contains(strings.ToLower(decision.Reasoning), "reversal") ||
		strings.Contains(strings.ToLower(decision.Reasoning), "bos") ||
		strings.Contains(strings.ToLower(decision.Reasoning), "choch")

	hasStructureEvidence := strings.Contains(strings.ToLower(decision.Reasoning), "4h") ||
		strings.Contains(strings.ToLower(decision.Reasoning), "1h") ||
		strings.Contains(strings.ToLower(decision.Reasoning), "结构") ||
		strings.Contains(strings.ToLower(decision.Reasoning), "structure")

	if grade == "S" && score >= 88 && hasReversalKeyword && hasStructureEvidence {
		log.Printf("✅ 允许反向开仓 (%s): 满足白名单条件 grade=%s score=%d + 反转关键词 + 结构证据",
			decision.Symbol, grade, score)
		return true, ""
	}

	// 不满足条件，拒绝并建议改为hold
	var reasons []string
	if grade != "S" {
		reasons = append(reasons, fmt.Sprintf("grade=%s(需要S)", grade))
	}
	if score < 88 {
		reasons = append(reasons, fmt.Sprintf("score=%d(需要≥88)", score))
	}
	if !hasReversalKeyword {
		reasons = append(reasons, "缺少反转关键词")
	}
	if !hasStructureEvidence {
		reasons = append(reasons, "缺少结构证据")
	}

	return false, fmt.Sprintf("对冲反自对冲拦截: %s已有%s持仓，反向开仓(%s)未满足白名单条件: %s",
		decision.Symbol, existingSide, decision.Action, strings.Join(reasons, ", "))
}

// parseGradeAndScoreFromReasoning 解析决策reasoning中的grade和score
func parseGradeAndScoreFromReasoning(reasoning string) (grade string, score int, err error) {
	if reasoning == "" {
		return "", 0, fmt.Errorf("reasoning为空，无法解析grade/score")
	}

	// 查找grade= 和 score= 模式
	gradePattern := `grade=([SA-F])`
	scorePattern := `score=(\d{1,3})`

	// 使用正则表达式解析grade
	gradeRegex := regexp.MustCompile(gradePattern)
	gradeMatches := gradeRegex.FindStringSubmatch(reasoning)
	if len(gradeMatches) < 2 {
		return "", 0, fmt.Errorf("reasoning中未找到有效的grade=X格式")
	}
	grade = gradeMatches[1]

	// 使用正则表达式解析score
	scoreRegex := regexp.MustCompile(scorePattern)
	scoreMatches := scoreRegex.FindStringSubmatch(reasoning)
	if len(scoreMatches) < 2 {
		return "", 0, fmt.Errorf("reasoning中未找到有效的score=YY格式")
	}

	score, err = strconv.Atoi(scoreMatches[1])
	if err != nil {
		return "", 0, fmt.Errorf("score格式错误: %v", err)
	}

	return grade, score, nil
}

func (at *AutoTrader) executeDecisionWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// CooldownEnforcer 双保险（优先级最高）
	if allowed, reason := at.validateCooldownEnforcer(decision); !allowed {
		log.Printf("🚫 冷却强制拦截: %s", reason)
		// 将决策改为hold，并记录拦截原因
		decision.Action = "hold"
		actionRecord.Action = "hold"
		actionRecord.Error = fmt.Sprintf("冷却强制拦截: %s", reason)
		return nil
	}

	// 决策一致性修复
	if allowed, rejectReason, fixes := sanitizeDecision(decision); !allowed {
		log.Printf("🚫 决策一致性拒绝: %s", rejectReason)
		// 记录拒绝原因和可能的修复建议
		decision.Action = "hold"
		actionRecord.Action = "hold"
		actionRecord.Error = fmt.Sprintf("决策一致性拒绝: %s", rejectReason)
		if len(fixes) > 0 {
			log.Printf("💡 修复建议: %s", strings.Join(fixes, "; "))
		}
		return nil
	} else if len(fixes) > 0 {
		// 记录自动修复的内容
		log.Printf("🔧 自动修复决策: %s", strings.Join(fixes, "; "))
	}

	// 高波动熔断验证
	if allowed, reason := at.validateVolatilityCircuitBreaker(decision); !allowed {
		log.Printf("🚫 %s", reason)
		// 将决策改为hold，并记录拦截原因
		decision.Action = "hold"
		actionRecord.Action = "hold"
		actionRecord.Error = reason
		return nil // 不执行原决策，但不返回错误
	}

	// Execution Mode强制验证
	if allowed, reason := at.validateExecutionMode(decision); !allowed {
		log.Printf("🚫 %s", reason)
		// 将决策改为hold，并记录拦截原因
		decision.Action = "hold"
		actionRecord.Action = "hold"
		actionRecord.Error = reason
		return nil // 不执行原决策，但不返回错误
	}

	// 对冲模式反自对冲验证
	if allowed, reason := at.validateHedgeAntiHedge(decision); !allowed {
		log.Printf("🚫 %s", reason)
		// 将决策改为hold，并记录拦截原因
		decision.Action = "hold"
		actionRecord.Action = "hold"
		actionRecord.Error = reason
		return nil // 不执行原决策，但不返回错误
	}

	switch decision.Action {
	case "open_long":
		return at.executeOpenLongWithRecord(decision, actionRecord)
	case "open_short":
		return at.executeOpenShortWithRecord(decision, actionRecord)
	case "close_long":
		return at.executeCloseLongWithRecord(decision, actionRecord)
	case "close_short":
		return at.executeCloseShortWithRecord(decision, actionRecord)
	case "partial_close_long":
		return at.executePartialCloseLongWithRecord(decision, actionRecord)
	case "partial_close_short":
		return at.executePartialCloseShortWithRecord(decision, actionRecord)
	case "update_stop_loss":
		return at.executeUpdateStopLossWithRecord(decision, actionRecord)
	case "update_take_profit":
		return at.executeUpdateTakeProfitWithRecord(decision, actionRecord)
	case "limit_open_long":
		return at.executeLimitOpenLongWithRecord(decision, actionRecord)
	case "limit_open_short":
		return at.executeLimitOpenShortWithRecord(decision, actionRecord)
	case "cancel_limit_order":
		return at.executeCancelLimitOrderWithRecord(decision, actionRecord)
	case "hold", "wait":
		// 无需执行，仅记录
		return nil
	default:
		return fmt.Errorf("未知的action: %s", decision.Action)
	}
}

func (at *AutoTrader) executeUpdateTakeProfitWithRecord(dec *decision.Decision, actionRecord *logger.DecisionAction) error {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	var (
		side string
		qty  float64
		ok   bool
	)

	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		if sym != dec.Symbol {
			continue
		}

		s, _ := pos["side"].(string)
		side = strings.ToUpper(s)
		q, _ := pos["positionAmt"].(float64)
		if q < 0 {
			q = -q
		}
		qty = q
		ok = true
		break
	}

	if !ok {
		return fmt.Errorf("当前没有 %s 的持仓，不能 update_take_profit", dec.Symbol)
	}

	if dec.NewTakeProfit <= 0 {
		return fmt.Errorf("update_take_profit 需要有效的新止盈价")
	}

	if err := at.trader.SetTakeProfit(dec.Symbol, side, qty, dec.NewTakeProfit); err != nil {
		return fmt.Errorf("设置止盈失败: %w", err)
	}

	actionRecord.Quantity = qty
	actionRecord.Price = dec.NewTakeProfit

	log.Printf("  ✓ %s %s 止盈已更新为 %.4f", dec.Symbol, side, dec.NewTakeProfit)
	return nil
}

// computeTrailingSL 按 tp1/tp2/tp3 + entry + 当前价格，算出新的止损和阶段
// side: "LONG" / "SHORT"
func computeTrailingSL(entry float64, side string, tgt *PositionTarget, lastPrice float64) (float64, int) {
	if tgt == nil {
		return 0, 0
	}

	newSL := tgt.CurrentSL
	newStage := tgt.Stage

	switch strings.ToUpper(side) {
	case "LONG":
		// 多单：价格向上走，达到 tp1/tp2/tp3 时逐级上抬止损
		// 按照 adaptive.txt 提示词规则：
		// TP1 → 抬到开仓价（保本）
		// TP2 → 抬到 (entry + TP1) / 2
		// TP3 → 抬到 (TP1 + TP2) / 2
		if lastPrice >= tgt.TP1 && tgt.Stage < 1 {
			target := entry // 到达TP1：保本
			if target > newSL {
				newSL = target
				newStage = 1
			}
		}
		if lastPrice >= tgt.TP2 && tgt.Stage < 2 {
			target := (entry + tgt.TP1) / 2 // 到达TP2：entry和TP1中点
			if target > newSL {
				newSL = target
				newStage = 2
			}
		}
		if lastPrice >= tgt.TP3 && tgt.Stage < 3 {
			target := (tgt.TP1 + tgt.TP2) / 2 // 到达TP3：TP1和TP2中点
			if target > newSL {
				newSL = target
				newStage = 3
			}
		}

	case "SHORT":
		// 空单：价格向下走，达到 tp1/tp2/tp3 时逐级下移止损
		// 按照 adaptive.txt 提示词规则（方向相反）：
		// TP1 → 抬到开仓价（保本）
		// TP2 → 抬到 (entry + TP1) / 2
		// TP3 → 抬到 (TP1 + TP2) / 2
		if lastPrice <= tgt.TP1 && tgt.Stage < 1 {
			target := entry // 到达TP1：保本
			if newSL == 0 || target < newSL {
				newSL = target
				newStage = 1
			}
		}
		if lastPrice <= tgt.TP2 && tgt.Stage < 2 {
			target := (entry + tgt.TP1) / 2 // 到达TP2：entry和TP1中点
			if newSL == 0 || target < newSL {
				newSL = target
				newStage = 2
			}
		}
		if lastPrice <= tgt.TP3 && tgt.Stage < 3 {
			target := (tgt.TP1 + tgt.TP2) / 2 // 到达TP3：TP1和TP2中点
			if newSL == 0 || target < newSL {
				newSL = target
				newStage = 3
			}
		}
	}

	return newSL, newStage
}

// executeUpdateStopLossWithRecord 调整已有仓位的止损
// executeUpdateStopLossWithRecord 调整已有仓位的止损（AI 只负责发信号，价格由策略代码按 tp1/tp2/tp3 计算）
func (at *AutoTrader) executeUpdateStopLossWithRecord(dec *decision.Decision, actionRecord *logger.DecisionAction) error {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}

	var (
		side   string
		qty    float64
		entry  float64
		hasPos bool
	)

	// 找到当前 symbol 的持仓
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		if sym != dec.Symbol {
			continue
		}

		s, _ := pos["side"].(string)
		side = strings.ToUpper(s)

		q, _ := pos["positionAmt"].(float64)
		if q < 0 {
			q = -q
		}
		qty = q

		entry, _ = pos["entryPrice"].(float64)

		hasPos = true
		break
	}

	if !hasPos {
		return fmt.Errorf("当前没有 %s 的持仓，不能 update_stop_loss", dec.Symbol)
	}

	// 没有记录 tp1/tp2/tp3 的话，就直接忽略这次抬止损（也可以选择退回 AI 的 new_stop_loss，看你喜好）
	sideKey := strings.ToLower(side) // LONG/SHORT -> long/short
	key := fmt.Sprintf("%s_%s", dec.Symbol, sideKey)
	tgt, ok := at.positionTargets[key]
	if !ok || tgt == nil {
		log.Printf("  ⚠ %s %s 没有记录 tp1/tp2/tp3，忽略本次 update_stop_loss 信号", dec.Symbol, side)
		return nil
	}

	// 获取当前市价
	mkt, err := market.Get(dec.Symbol)
	if err != nil {
		return fmt.Errorf("获取行情失败: %w", err)
	}
	lastPrice := mkt.CurrentPrice

	var newSL float64
	var newStage int
	var slSource string

	// A) 优先检查AI提供的结构化止损
	if dec.NewStopLoss > 0 {
		aiSL := dec.NewStopLoss
		valid := true
		var reason string

		// 1. 方向合法性校验
		switch side {
		case "LONG":
			if aiSL <= tgt.CurrentSL {
				valid = false
				reason = fmt.Sprintf("LONG新止损%.4f <= 当前止损%.4f", aiSL, tgt.CurrentSL)
			} else if aiSL >= lastPrice {
				valid = false
				reason = fmt.Sprintf("LONG新止损%.4f >= 当前价%.4f", aiSL, lastPrice)
			}
		case "SHORT":
			if aiSL >= tgt.CurrentSL {
				valid = false
				reason = fmt.Sprintf("SHORT新止损%.4f >= 当前止损%.4f", aiSL, tgt.CurrentSL)
			} else if aiSL <= lastPrice {
				valid = false
				reason = fmt.Sprintf("SHORT新止损%.4f <= 当前价%.4f", aiSL, lastPrice)
			}
		}

		// 2. 噪声保护校验（用ATR推最小安全距离）
		if valid && mkt.RiskMetrics != nil {
			atrPct := mkt.RiskMetrics.ATR14PercentOfPrice
			minGapPct := math.Max(0.15*atrPct, 0.002) // min(0.15*ATR%, 0.20%)
			minGap := lastPrice * minGapPct

			switch side {
			case "LONG":
				if lastPrice-aiSL < minGap {
					valid = false
					reason = fmt.Sprintf("LONG止损距离过近: %.4f < %.4f(%.2f%%)", lastPrice-aiSL, minGap, minGapPct*100)
				}
			case "SHORT":
				if aiSL-lastPrice < minGap {
					valid = false
					reason = fmt.Sprintf("SHORT止损距离过近: %.4f < %.4f(%.2f%%)", aiSL-lastPrice, minGap, minGapPct*100)
				}
			}
		}

		// 3. 爆仓前纪律校验
		if valid {
			stopDistancePct := math.Abs(entry-aiSL) / entry
			maxStopPct := 0.85 / float64(dec.Leverage) // 与RiskManagement一致
			if stopDistancePct >= maxStopPct {
				valid = false
				reason = fmt.Sprintf("止损距离%.2f%% >= 最大允许%.2f%%(杠杆%d)", stopDistancePct*100, maxStopPct*100, dec.Leverage)
			}
		}

		if valid {
			newSL = aiSL
			slSource = "structure"
			log.Printf("  ✅ %s %s 采用AI结构止损: %.4f (通过全部校验)", dec.Symbol, side, newSL)
		} else {
			log.Printf("  ❌ %s %s AI结构止损%.4f被拒绝: %s, fallback到公式计算", dec.Symbol, side, aiSL, reason)
		}
	}

	// 如果AI结构止损无效或未提供，回退到TP阶段公式
	if newSL == 0 {
		newSL, newStage = computeTrailingSL(entry, side, tgt, lastPrice)
		slSource = "formula"
	}

	// 如果算出来和当前止损一样或没有提升，就不下单
	if newSL <= 0 || newSL == tgt.CurrentSL {
		log.Printf("  ℹ %s %s 本次未产生更优的止损（当前SL=%.4f，newSL=%.4f，stage=%d）",
			dec.Symbol, side, tgt.CurrentSL, newSL, tgt.Stage)
		return nil
	}

	// 再做一层方向校验 + 距离校验，防止直接触发 -2021
	const minGapRatio = 0.0005 // 0.05% 安全间隔，可按你习惯调整

	switch side {
	case "LONG":
		// 多单止损必须在市价下方，且要高于当前止损（只抬不放）
		maxSL := lastPrice * (1 - minGapRatio)
		if newSL >= maxSL {
			log.Printf("  ⚠ 计算出的新止损 %.4f 距离多单市价 %.4f 过近或在上方，调整为 %.4f 避免立即触发",
				newSL, lastPrice, maxSL)
			newSL = maxSL
		}
		if newSL <= tgt.CurrentSL {
			log.Printf("  ⚠ %s LONG 新止损 %.4f 不优于当前止损 %.4f，忽略本次抬升",
				dec.Symbol, newSL, tgt.CurrentSL)
			return nil
		}

	case "SHORT":
		// 空单止损必须在市价上方，且要低于当前止损（只往有利方向移动）
		minSL := lastPrice * (1 + minGapRatio)
		if newSL <= minSL {
			log.Printf("  ⚠ 计算出的新止损 %.4f 距离空单市价 %.4f 过近或在下方，调整为 %.4f 避免立即触发",
				newSL, lastPrice, minSL)
			newSL = minSL
		}
		if tgt.CurrentSL != 0 && newSL >= tgt.CurrentSL {
			log.Printf("  ⚠ %s SHORT 新止损 %.4f 不优于当前止损 %.4f，忽略本次抬升",
				dec.Symbol, newSL, tgt.CurrentSL)
			return nil
		}
	}

	// 真正下改单
	if err := at.trader.SetStopLoss(dec.Symbol, side, qty, newSL); err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}

	actionRecord.Quantity = qty
	actionRecord.Price = newSL
	actionRecord.StopLossSource = slSource

	// 更新内存中的止损和阶段
	tgt.CurrentSL = newSL
	if newStage > tgt.Stage {
		tgt.Stage = newStage
	}

	log.Printf("  ✓ %s %s 止损已按tp分段规则抬到 %.4f (stage=%d)", dec.Symbol, side, newSL, tgt.Stage)
	return nil
}

// executeOpenLongWithRecord 执行开多仓并记录详细信息
func (at *AutoTrader) executeOpenLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📈 开多仓: %s", decision.Symbol)

	// 执行门禁检查和执行方式确定
	if err := at.checkExecutionGate(decision, actionRecord); err != nil {
		return err
	}

	// ✅ 只有不是补仓时才检查有没有同方向仓位
	if !decision.IsAddOn {
		positions, err := at.trader.GetPositions()
		if err == nil {
			// 检查总持仓数量是否已达上限（3个）
			if len(positions) >= 3 {
				return fmt.Errorf("❌ 总持仓数已达上限（3个），拒绝开新仓。当前持仓：%d", len(positions))
			}

			// 检查同币种同方向是否已有持仓
			for _, pos := range positions {
				if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
					return fmt.Errorf("❌ %s 已有多仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_long 决策", decision.Symbol)
				}
			}
		}
	}

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}

	// 计算数量
	margin := decision.PositionSizeUSD
	quantity := (margin * float64(decision.Leverage)) / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// 设置仓位模式
	if err := at.trader.SetMarginMode(decision.Symbol, at.config.IsCrossMargin); err != nil {
		log.Printf("  ⚠️ 设置仓位模式失败: %v", err)
		// 继续执行，不影响交易
	}

	// 开仓
	order, err := at.trader.OpenLong(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 增加每日开单计数并持久化
	at.incrementDailyPairTrades(decision.Symbol)

	// 设置止损止盈（注意：只挂最终止盈TP3，即 decision.TakeProfit 应当等于 TP3）
	if err := at.trader.SetStopLoss(decision.Symbol, "LONG", quantity, decision.StopLoss); err != nil {
		log.Printf("  ⚠ 设置止损失败: %v", err)
	}
	if err := at.trader.SetTakeProfit(decision.Symbol, "LONG", quantity, decision.TakeProfit); err != nil {
		log.Printf("  ⚠ 设置止盈失败: %v", err)
	}

	// 记录AI给的三个止盈点位
	at.positionTargets[posKey] = &PositionTarget{
		TP1:       decision.TP1,
		TP2:       decision.TP2,
		TP3:       decision.TP3,
		Stage:     0,
		CurrentSL: decision.StopLoss,
	}

	return nil
}

// executeOpenShortWithRecord 执行开空仓并记录详细信息
func (at *AutoTrader) executeOpenShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📉 开空仓: %s", decision.Symbol)

	// 执行门禁检查和执行方式确定
	if err := at.checkExecutionGate(decision, actionRecord); err != nil {
		return err
	}

	// ✅ 只有不是补仓时才检查有没有同方向仓位
	if !decision.IsAddOn {
		positions, err := at.trader.GetPositions()
		if err == nil {
			// 检查总持仓数量是否已达上限（3个）
			if len(positions) >= 3 {
				return fmt.Errorf("❌ 总持仓数已达上限（3个），拒绝开新仓。当前持仓：%d", len(positions))
			}

			// 检查同币种同方向是否已有持仓
			for _, pos := range positions {
				if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
					return fmt.Errorf("❌ %s 已有空仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_short 决策", decision.Symbol)
				}
			}
		}
	}

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}

	// 计算数量
	margin := decision.PositionSizeUSD
	quantity := (margin * float64(decision.Leverage)) / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// 设置仓位模式
	if err := at.trader.SetMarginMode(decision.Symbol, at.config.IsCrossMargin); err != nil {
		log.Printf("  ⚠️ 设置仓位模式失败: %v", err)
		// 继续执行，不影响交易
	}

	// 开仓
	order, err := at.trader.OpenShort(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// 增加每日开单计数并持久化
	at.incrementDailyPairTrades(decision.Symbol)

	// 设置止损止盈（注意：只挂最终止盈TP3，即 decision.TakeProfit 应当等于 TP3）
	if err := at.trader.SetStopLoss(decision.Symbol, "SHORT", quantity, decision.StopLoss); err != nil {
		log.Printf("  ⚠ 设置止损失败: %v", err)
	}
	if err := at.trader.SetTakeProfit(decision.Symbol, "SHORT", quantity, decision.TakeProfit); err != nil {
		log.Printf("  ⚠ 设置止盈失败: %v", err)
	}

	// 记录AI给的三个止盈点位
	at.positionTargets[posKey] = &PositionTarget{
		TP1:       decision.TP1,
		TP2:       decision.TP2,
		TP3:       decision.TP3,
		Stage:     0,
		CurrentSL: decision.StopLoss,
	}

	return nil
}

// executeCloseLongWithRecord 执行平多仓并记录详细信息
func (at *AutoTrader) executeCloseLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🔄 平多仓: %s", decision.Symbol)

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 检查是否系统已自动处理了 TP1/TP2 的分批止盈
	posKey := decision.Symbol + "_long"
	if tgt, ok := at.positionTargets[posKey]; ok && tgt.Stage > 0 {
		// 如果提供了 close_quantity 或 close_ratio，说明是部分平仓，应该使用 partial_close_long
		if decision.CloseQuantity > 0 || decision.CloseRatio > 0 {
			log.Printf("  ⚠️ %s 检测到使用 close_long 进行部分平仓，建议使用 partial_close_long 以区分全平和部分平仓", decision.Symbol)
		}
		// 如果已经到达 TP1/TP2，系统应该已经自动平过了，记录警告
		if tgt.Stage >= 1 {
			log.Printf("  ⚠️ %s 已到达 TP%d，系统已自动执行分批止盈，请确认是否需要再次平仓", decision.Symbol, tgt.Stage)
		}
	}

	// 计算本次应平仓数量：
	// 1) 若显式给出 close_quantity，则直接使用；
	// 2) 否则若给出 close_ratio，则按当前仓位 * 比例计算；
	// 3) 若两者都未提供或无效，则回退为“全平”（quantity=0 语义保持不变）。
	var closeQty float64

	// 获取当前多仓数量
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}
	var currentQty float64
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		if sym == decision.Symbol && strings.ToLower(side) == "long" {
			q, _ := pos["positionAmt"].(float64)
			if q < 0 {
				q = -q
			}
			currentQty = q
			break
		}
	}

	// 优先使用 close_quantity
	if decision.CloseQuantity > 0 {
		closeQty = decision.CloseQuantity
	} else if decision.CloseRatio > 0 {
		// 使用比例计算数量
		if decision.CloseRatio > 1 {
			// 容错：如果AI给的是百分比（例如 33），转换为 0.33
			closeQty = currentQty * (decision.CloseRatio / 100.0)
		} else {
			closeQty = currentQty * decision.CloseRatio
		}
	}

	// 容错保护：若计算出的数量过小或大于等于当前仓位，则退回全平语义
	if closeQty <= 0 || currentQty == 0 {
		log.Printf("  ℹ %s 未提供有效的部分平仓数量/比例，按全平处理", decision.Symbol)
		closeQty = 0 // 0 = 全部平仓
	} else if closeQty >= currentQty {
		log.Printf("  ℹ %s 部分平仓数量>=当前仓位(%.4f>=%.4f)，按全平处理", decision.Symbol, closeQty, currentQty)
		closeQty = 0
	} else {
		log.Printf("  📐 %s 计算得到部分平仓数量: %.4f / 当前仓位: %.4f", decision.Symbol, closeQty, currentQty)
	}

	// 平仓（quantity=0 仍然代表“全平”，保持原有语义）
	order, err := at.trader.CloseLong(decision.Symbol, closeQty)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	// 仅当被视为“全平”时，才清理该持仓的tp记忆
	if closeQty == 0 {
		delete(at.positionTargets, decision.Symbol+"_long")
		delete(at.positionFirstSeenTime, decision.Symbol+"_long")
		delete(at.positionMemory, decision.Symbol+"_long")
		log.Printf("  ✓ 全平成功，已清理 TP 记忆")
	} else {
		log.Printf("  ✓ 部分平仓成功: %s 多单 平掉 %.4f，剩余仓位继续跟踪 TP 结构", decision.Symbol, closeQty)
	}

	return nil
}

// executeCloseShortWithRecord 执行平空仓并记录详细信息
func (at *AutoTrader) executeCloseShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🔄 平空仓: %s", decision.Symbol)

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 检查是否系统已自动处理了 TP1/TP2 的分批止盈
	posKey := decision.Symbol + "_short"
	if tgt, ok := at.positionTargets[posKey]; ok && tgt.Stage > 0 {
		// 如果提供了 close_quantity 或 close_ratio，说明是部分平仓，应该使用 partial_close_short
		if decision.CloseQuantity > 0 || decision.CloseRatio > 0 {
			log.Printf("  ⚠️ %s 检测到使用 close_short 进行部分平仓，建议使用 partial_close_short 以区分全平和部分平仓", decision.Symbol)
		}
		// 如果已经到达 TP1/TP2，系统应该已经自动平过了，记录警告
		if tgt.Stage >= 1 {
			log.Printf("  ⚠️ %s 已到达 TP%d，系统已自动执行分批止盈，请确认是否需要再次平仓", decision.Symbol, tgt.Stage)
		}
	}

	// 计算本次应平仓数量（逻辑同多单）：
	var closeQty float64

	// 获取当前空仓数量
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}
	var currentQty float64
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		if sym == decision.Symbol && strings.ToLower(side) == "short" {
			q, _ := pos["positionAmt"].(float64)
			if q < 0 {
				q = -q
			}
			currentQty = q
			break
		}
	}

	// 优先使用 close_quantity
	if decision.CloseQuantity > 0 {
		closeQty = decision.CloseQuantity
	} else if decision.CloseRatio > 0 {
		if decision.CloseRatio > 1 {
			closeQty = currentQty * (decision.CloseRatio / 100.0)
		} else {
			closeQty = currentQty * decision.CloseRatio
		}
	}

	if closeQty <= 0 || currentQty == 0 {
		log.Printf("  ℹ %s 未提供有效的部分平仓数量/比例，按全平处理", decision.Symbol)
		closeQty = 0
	} else if closeQty >= currentQty {
		log.Printf("  ℹ %s 部分平仓数量>=当前仓位(%.4f>=%.4f)，按全平处理", decision.Symbol, closeQty, currentQty)
		closeQty = 0
	} else {
		log.Printf("  📐 %s 计算得到部分平仓数量: %.4f / 当前仓位: %.4f", decision.Symbol, closeQty, currentQty)
	}

	// 平仓（0 仍然表示“全平”）
	order, err := at.trader.CloseShort(decision.Symbol, closeQty)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	// 仅当被视为“全平”时，才清理该持仓的tp记忆
	if closeQty == 0 {
		delete(at.positionTargets, decision.Symbol+"_short")
		delete(at.positionFirstSeenTime, decision.Symbol+"_short")
		delete(at.positionMemory, decision.Symbol+"_short")
		log.Printf("  ✓ 全平成功，已清理 TP 记忆")
	} else {
		log.Printf("  ✓ 部分平仓成功: %s 空单 平掉 %.4f，剩余仓位继续跟踪 TP 结构", decision.Symbol, closeQty)
	}

	return nil
}

// executePartialCloseLongWithRecord 执行部分平多仓并记录详细信息
// 与 close_long 的区别：强制要求提供 close_quantity 或 close_ratio，不允许全平
func (at *AutoTrader) executePartialCloseLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🔄 部分平多仓: %s", decision.Symbol)

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 获取当前多仓数量
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}
	var currentQty float64
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		if sym == decision.Symbol && strings.ToLower(side) == "long" {
			q, _ := pos["positionAmt"].(float64)
			if q < 0 {
				q = -q
			}
			currentQty = q
			break
		}
	}

	if currentQty == 0 {
		return fmt.Errorf("❌ %s 没有多仓持仓，无法部分平仓", decision.Symbol)
	}

	// 部分平仓必须提供 close_quantity 或 close_ratio
	var closeQty float64
	if decision.CloseQuantity > 0 {
		closeQty = decision.CloseQuantity
	} else if decision.CloseRatio > 0 {
		if decision.CloseRatio > 1 {
			closeQty = currentQty * (decision.CloseRatio / 100.0)
		} else {
			closeQty = currentQty * decision.CloseRatio
		}
	} else {
		return fmt.Errorf("❌ %s 部分平仓必须提供 close_quantity 或 close_ratio 字段", decision.Symbol)
	}

	// 验证部分平仓数量
	if closeQty <= 0 {
		return fmt.Errorf("❌ %s 部分平仓数量必须大于0，当前: %.4f", decision.Symbol, closeQty)
	}
	if closeQty >= currentQty {
		return fmt.Errorf("❌ %s 部分平仓数量(%.4f)不能大于等于当前仓位(%.4f)，如需全平请使用 close_long", decision.Symbol, closeQty, currentQty)
	}

	// 检查是否到达 TP 点位
	tpInfo := ""
	posKey := decision.Symbol + "_long"
	if tgt, ok := at.positionTargets[posKey]; ok {
		currentPrice := marketData.CurrentPrice
		const tolerance = 0.002 // 0.2% 容差

		if tgt.TP1 > 0 && math.Abs(currentPrice-tgt.TP1)/tgt.TP1 <= tolerance {
			tpInfo = fmt.Sprintf("已到达 TP1 (%.4f)", tgt.TP1)
		} else if tgt.TP2 > 0 && math.Abs(currentPrice-tgt.TP2)/tgt.TP2 <= tolerance {
			tpInfo = fmt.Sprintf("已到达 TP2 (%.4f)", tgt.TP2)
		} else if tgt.TP3 > 0 && math.Abs(currentPrice-tgt.TP3)/tgt.TP3 <= tolerance {
			tpInfo = fmt.Sprintf("已到达 TP3 (%.4f)", tgt.TP3)
		} else {
			tpInfo = fmt.Sprintf("当前价格 %.4f，未到达 TP 点位 (TP1:%.4f TP2:%.4f TP3:%.4f)",
				currentPrice, tgt.TP1, tgt.TP2, tgt.TP3)
		}
	}

	closeRatioPercent := (closeQty / currentQty) * 100.0
	log.Printf("  📊 %s 部分平仓信息: %s | 平仓数量: %.4f (%.2f%%) | 剩余: %.4f",
		decision.Symbol, tpInfo, closeQty, closeRatioPercent, currentQty-closeQty)

	// 执行部分平仓
	order, err := at.trader.CloseLong(decision.Symbol, closeQty)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 部分平多仓成功: %s 平掉 %.4f (%.2f%%)，剩余仓位 %.4f 继续跟踪 TP 结构",
		decision.Symbol, closeQty, closeRatioPercent, currentQty-closeQty)

	return nil
}

// executePartialCloseShortWithRecord 执行部分平空仓并记录详细信息
// 与 close_short 的区别：强制要求提供 close_quantity 或 close_ratio，不允许全平
func (at *AutoTrader) executePartialCloseShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🔄 部分平空仓: %s", decision.Symbol)

	// 获取当前价格
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// 获取当前空仓数量
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}
	var currentQty float64
	for _, pos := range positions {
		sym, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		if sym == decision.Symbol && strings.ToLower(side) == "short" {
			q, _ := pos["positionAmt"].(float64)
			if q < 0 {
				q = -q
			}
			currentQty = q
			break
		}
	}

	if currentQty == 0 {
		return fmt.Errorf("❌ %s 没有空仓持仓，无法部分平仓", decision.Symbol)
	}

	// 部分平仓必须提供 close_quantity 或 close_ratio
	var closeQty float64
	if decision.CloseQuantity > 0 {
		closeQty = decision.CloseQuantity
	} else if decision.CloseRatio > 0 {
		if decision.CloseRatio > 1 {
			closeQty = currentQty * (decision.CloseRatio / 100.0)
		} else {
			closeQty = currentQty * decision.CloseRatio
		}
	} else {
		return fmt.Errorf("❌ %s 部分平仓必须提供 close_quantity 或 close_ratio 字段", decision.Symbol)
	}

	// 验证部分平仓数量
	if closeQty <= 0 {
		return fmt.Errorf("❌ %s 部分平仓数量必须大于0，当前: %.4f", decision.Symbol, closeQty)
	}
	if closeQty >= currentQty {
		return fmt.Errorf("❌ %s 部分平仓数量(%.4f)不能大于等于当前仓位(%.4f)，如需全平请使用 close_short", decision.Symbol, closeQty, currentQty)
	}

	// 检查是否到达 TP 点位
	tpInfo := ""
	posKey := decision.Symbol + "_short"
	if tgt, ok := at.positionTargets[posKey]; ok {
		currentPrice := marketData.CurrentPrice
		const tolerance = 0.002 // 0.2% 容差

		if tgt.TP1 > 0 && math.Abs(currentPrice-tgt.TP1)/tgt.TP1 <= tolerance {
			tpInfo = fmt.Sprintf("已到达 TP1 (%.4f)", tgt.TP1)
		} else if tgt.TP2 > 0 && math.Abs(currentPrice-tgt.TP2)/tgt.TP2 <= tolerance {
			tpInfo = fmt.Sprintf("已到达 TP2 (%.4f)", tgt.TP2)
		} else if tgt.TP3 > 0 && math.Abs(currentPrice-tgt.TP3)/tgt.TP3 <= tolerance {
			tpInfo = fmt.Sprintf("已到达 TP3 (%.4f)", tgt.TP3)
		} else {
			tpInfo = fmt.Sprintf("当前价格 %.4f，未到达 TP 点位 (TP1:%.4f TP2:%.4f TP3:%.4f)",
				currentPrice, tgt.TP1, tgt.TP2, tgt.TP3)
		}
	}

	closeRatioPercent := (closeQty / currentQty) * 100.0
	log.Printf("  📊 %s 部分平仓信息: %s | 平仓数量: %.4f (%.2f%%) | 剩余: %.4f",
		decision.Symbol, tpInfo, closeQty, closeRatioPercent, currentQty-closeQty)

	// 执行部分平仓
	order, err := at.trader.CloseShort(decision.Symbol, closeQty)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	log.Printf("  ✓ 部分平空仓成功: %s 平掉 %.4f (%.2f%%)，剩余仓位 %.4f 继续跟踪 TP 结构",
		decision.Symbol, closeQty, closeRatioPercent, currentQty-closeQty)

	return nil
}

// GetID 获取trader ID
func (at *AutoTrader) GetID() string {
	return at.id
}

// GetName 获取trader名称
func (at *AutoTrader) GetName() string {
	return at.name
}

// GetAIModel 获取AI模型
func (at *AutoTrader) GetAIModel() string {
	return at.aiModel
}

// GetExchange 获取交易所
func (at *AutoTrader) GetExchange() string {
	return at.exchange
}

// SetCustomPrompt 设置自定义交易策略prompt
func (at *AutoTrader) SetCustomPrompt(prompt string) {
	at.customPrompt = prompt
}

// SetOverrideBasePrompt 设置是否覆盖基础prompt
func (at *AutoTrader) SetOverrideBasePrompt(override bool) {
	at.overrideBasePrompt = override
}

// SetSystemPromptTemplate 设置系统提示词模板
func (at *AutoTrader) SetSystemPromptTemplate(templateName string) {
	at.systemPromptTemplate = templateName
}

// GetSystemPromptTemplate 获取当前系统提示词模板名称
func (at *AutoTrader) GetSystemPromptTemplate() string {
	return at.systemPromptTemplate
}

// GetDecisionLogger 获取决策日志记录器
func (at *AutoTrader) GetDecisionLogger() *logger.DecisionLogger {
	return at.decisionLogger
}

// GetStatus 获取系统状态（用于API）
func (at *AutoTrader) GetStatus() map[string]interface{} {
	aiProvider := "DeepSeek"
	if at.config.UseQwen {
		aiProvider = "Qwen"
	}

	return map[string]interface{}{
		"trader_id":       at.id,
		"trader_name":     at.name,
		"ai_model":        at.aiModel,
		"exchange":        at.exchange,
		"is_running":      at.isRunning,
		"start_time":      at.startTime.Format(time.RFC3339),
		"runtime_minutes": int(time.Since(at.startTime).Minutes()),
		"call_count":      at.callCount,
		"initial_balance": at.initialBalance,
		"scan_interval":   at.config.ScanInterval.String(),
		"stop_until":      at.stopUntil.Format(time.RFC3339),
		"last_reset_time": at.lastResetTime.Format(time.RFC3339),
		"ai_provider":     aiProvider,
	}
}

// GetAccountInfo 获取账户信息（用于API）
func (at *AutoTrader) GetAccountInfo() (map[string]interface{}, error) {
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取余额失败: %w", err)
	}

	// 获取账户字段
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Total Equity = 钱包余额 + 未实现盈亏
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 获取持仓计算总保证金
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	totalMarginUsed := 0.0
	totalUnrealizedPnL := 0.0
	for _, pos := range positions {
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		totalUnrealizedPnL += unrealizedPnl

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed
	}

	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	return map[string]interface{}{
		// 核心字段
		"total_equity":      totalEquity,
		"wallet_balance":    totalWalletBalance,
		"unrealized_profit": totalUnrealizedProfit,
		"available_balance": availableBalance,

		// 盈亏统计
		"total_pnl":            totalPnL,
		"total_pnl_pct":        totalPnLPct,
		"total_unrealized_pnl": totalUnrealizedPnL,
		"initial_balance":      at.initialBalance,
		"daily_pnl":            at.dailyPnL,

		// 持仓信息
		"position_count":  len(positions),
		"margin_used":     totalMarginUsed,
		"margin_used_pct": marginUsedPct,
	}, nil
}

// GetPositions 获取持仓列表（用于API）
func (at *AutoTrader) GetPositions() ([]map[string]interface{}, error) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	var result []map[string]interface{}
	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}

		// 计算占用保证金
		marginUsed := (quantity * markPrice) / float64(leverage)

		// 计算盈亏百分比
		pnlPct := 0.0
		if marginUsed > 0 {
			pnlPct = (unrealizedPnl / marginUsed) * 100
		}

		result = append(result, map[string]interface{}{
			"symbol":             symbol,
			"side":               side,
			"entry_price":        entryPrice,
			"mark_price":         markPrice,
			"quantity":           quantity,
			"leverage":           leverage,
			"unrealized_pnl":     unrealizedPnl,
			"unrealized_pnl_pct": pnlPct,
			"liquidation_price":  liquidationPrice,
			"margin_used":        marginUsed,
		})
	}

	return result, nil
}

// GetPendingOrders 获取待成交的限价单列表（用于API）
func (at *AutoTrader) GetPendingOrders() []map[string]interface{} {
	var result []map[string]interface{}

	for _, order := range at.pendingOrders {
		// 计算挂单时长
		durationMs := time.Now().UnixMilli() - order.CreateTime
		durationMin := durationMs / (1000 * 60)

		result = append(result, map[string]interface{}{
			"symbol":       order.Symbol,
			"side":         order.Side,
			"limit_price":  order.LimitPrice,
			"quantity":     order.Quantity,
			"leverage":     order.Leverage,
			"order_id":     order.OrderID,
			"tp1":          order.TP1,
			"tp2":          order.TP2,
			"tp3":          order.TP3,
			"stop_loss":    order.StopLoss,
			"take_profit":  order.TakeProfit,
			"create_time":  order.CreateTime,
			"duration_min": durationMin,
			"confidence":   order.Confidence,
			"reasoning":    order.Reasoning,
		})
	}

	return result
}

// sortDecisionsByPriority 对决策排序：先平仓，再开仓，最后hold/wait
func sortDecisionsByPriority(decisions []decision.Decision) []decision.Decision {
	if len(decisions) <= 1 {
		return decisions
	}

	getActionPriority := func(action string) int {
		switch action {
		case "close_long", "close_short", "partial_close_long", "partial_close_short":
			return 1
		case "open_long", "open_short":
			return 2
		case "hold", "wait":
			return 3
		default:
			return 999
		}
	}

	sorted := make([]decision.Decision, len(decisions))
	copy(sorted, decisions)

	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if getActionPriority(sorted[i].Action) > getActionPriority(sorted[j].Action) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

// getCandidateCoins 获取交易员的候选币种列表
func (at *AutoTrader) getCandidateCoins() ([]decision.CandidateCoin, error) {
	if len(at.tradingCoins) == 0 {
		var candidateCoins []decision.CandidateCoin

		if len(at.defaultCoins) > 0 {
			for _, coin := range at.defaultCoins {
				symbol := normalizeSymbol(coin)
				candidateCoins = append(candidateCoins, decision.CandidateCoin{
					Symbol:  symbol,
					Sources: []string{"default"},
				})
			}
			log.Printf("📋 [%s] 使用数据库默认币种: %d个币种 %v",
				at.name, len(candidateCoins), at.defaultCoins)
			return candidateCoins, nil
		} else {
			const ai500Limit = 20
			mergedPool, err := pool.GetMergedCoinPool(ai500Limit)
			if err != nil {
				return nil, fmt.Errorf("获取合并币种池失败: %w", err)
			}

			for _, symbol := range mergedPool.AllSymbols {
				sources := mergedPool.SymbolSources[symbol]
				candidateCoins = append(candidateCoins, decision.CandidateCoin{
					Symbol:  symbol,
					Sources: sources,
				})
			}

			log.Printf("📋 [%s] 数据库无默认币种配置，使用AI500+OI Top: AI500前%d + OI_Top20 = 总计%d个候选币种",
				at.name, ai500Limit, len(candidateCoins))
			return candidateCoins, nil
		}
	} else {
		var candidateCoins []decision.CandidateCoin
		for _, coin := range at.tradingCoins {
			symbol := normalizeSymbol(coin)
			candidateCoins = append(candidateCoins, decision.CandidateCoin{
				Symbol:  symbol,
				Sources: []string{"custom"},
			})
		}

		log.Printf("📋 [%s] 使用自定义币种: %d个币种 %v",
			at.name, len(candidateCoins), at.tradingCoins)
		return candidateCoins, nil
	}
}

// normalizeSymbol 标准化币种符号（确保以USDT结尾）
func normalizeSymbol(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if !strings.HasSuffix(symbol, "USDT") {
		symbol = symbol + "USDT"
	}
	return symbol
}

// executeLimitOrderLifecycle 执行限价订单生命周期管理（M2.2）
// 返回 (success, executionReport, error)
func (at *AutoTrader) executeLimitOrderLifecycle(
	symbol, side string,
	quantity, limitPrice float64,
	pricingReason string,
	gateMode string,
) (bool, *LimitOrderExecutionReport, error) {

	report := &LimitOrderExecutionReport{
		Symbol:        symbol,
		Side:          side,
		LimitPrice:    limitPrice,
		PricingReason: pricingReason,
		Quantity:      quantity,
		Status:        "STARTING",
		StartTime:     time.Now().UnixMilli(),
	}

	remainingQty := quantity

	for attempt := 0; attempt <= at.config.LimitOrderMaxRetries; attempt++ {
		report.AttemptIndex = attempt + 1 // 从1开始计数

		log.Printf("  🔄 限价%s尝试 #%d/%d: %s %.6f @ %.4f (剩余: %.6f)",
			side, attempt+1, at.config.LimitOrderMaxRetries+1,
			symbol, remainingQty, limitPrice, remainingQty)

		// 放置订单
		var orderResult map[string]interface{}
		var err error

		if side == "BUY" {
			orderResult, err = at.trader.LimitOpenLong(symbol, remainingQty, 1, limitPrice, 0) // 止损设为0表示不设置
		} else {
			orderResult, err = at.trader.LimitOpenShort(symbol, remainingQty, 1, limitPrice, 0)
		}

		if err != nil {
			report.Error = fmt.Sprintf("下单失败: %v", err)
			report.Status = "ORDER_FAILED"
			report.EndTime = time.Now().UnixMilli()
			report.DurationMs = report.EndTime - report.StartTime
			return false, report, fmt.Errorf("下单失败: %w", err)
		}

		var orderID int64
		switch id := orderResult["orderId"].(type) {
		case float64:
			orderID = int64(id)
		case int64:
			orderID = id
		case int:
			orderID = int64(id)
		default:
			report.Error = "订单ID格式错误"
			report.Status = "INVALID_ORDER_ID"
			return false, report, fmt.Errorf("订单ID格式错误: %T", orderResult["orderId"])
		}
		report.OrderID = orderID

		log.Printf("  📋 订单已挂: ID=%d, 等待成交...", orderID)

		// 等待成交或超时
		timeout := time.After(time.Duration(at.config.LimitOrderWaitSeconds) * time.Second)
		ticker := time.NewTicker(time.Duration(at.config.LimitOrderPollIntervalMs) * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				// 超时，取消订单
				log.Printf("  ⏰ 订单 #%d 超时，取消订单", orderID)
				if cancelErr := at.trader.CancelOrder(symbol, orderID); cancelErr != nil {
					log.Printf("  ⚠️ 取消订单失败: %v", cancelErr)
				}

				report.Status = "TIMEOUT"
				report.EndTime = time.Now().UnixMilli()
				report.DurationMs = report.EndTime - report.StartTime

				// 如果还有重试次数，继续下一轮
				if attempt < at.config.LimitOrderMaxRetries {
					log.Printf("  🔄 准备重试 #%d...", attempt+2)

					// 重新获取市场数据和定价
					marketData, err := market.Get(symbol)
					if err != nil {
						report.Error = fmt.Sprintf("重试时获取市场数据失败: %v", err)
						return false, report, err
					}

					filters, err := market.GetSymbolFilters(symbol)
					if err != nil {
						report.Error = fmt.Sprintf("重试时获取过滤器失败: %v", err)
						return false, report, err
					}

					newLimitPrice, newReason := market.DeriveOpenLimitPrice(side, marketData.Microstructure, filters.TickSize)
					if newLimitPrice <= 0 {
						report.Error = fmt.Sprintf("重试时推导价格失败: %s", newReason)
						return false, report, fmt.Errorf("重试时推导价格失败: %s", newReason)
					}

					limitPrice = newLimitPrice
					pricingReason = newReason
					report.LimitPrice = limitPrice
					report.PricingReason = pricingReason

					log.Printf("  📈 重新定价: %.4f (%s)", limitPrice, pricingReason)
				} else {
					report.Status = "RETRIES_EXHAUSTED"
					log.Printf("  ❌ 重试次数耗尽，放弃执行")
					return false, report, nil // 返回nil error表示放弃执行而非错误
				}
				goto next_attempt

			case <-ticker.C:
				// 查询订单状态
				orderStatus, err := at.trader.GetOrderStatus(symbol, orderID)
				if err != nil {
					log.Printf("  ⚠️ 查询订单状态失败: %v", err)
					continue
				}

				status, ok := orderStatus["status"].(string)
				if !ok {
					log.Printf("  ⚠️ 订单状态格式错误")
					continue
				}

				executedQty, _ := orderStatus["executedQty"].(float64)
				avgPrice, _ := orderStatus["avgPrice"].(float64)

				report.FilledQuantity = executedQty
				report.AvgFillPrice = avgPrice

				switch status {
				case "FILLED":
					report.Status = "FILLED"
					report.EndTime = time.Now().UnixMilli()
					report.DurationMs = report.EndTime - report.StartTime
					log.Printf("  ✅ 订单完全成交: %.6f @ %.4f", executedQty, avgPrice)
					return true, report, nil

				case "PARTIALLY_FILLED":
					log.Printf("  🔶 部分成交: %.6f/%.6f @ %.4f", executedQty, quantity, avgPrice)

					if at.config.CancelOnPartialFill {
						// 取消剩余部分
						log.Printf("  🚫 部分成交后取消剩余订单")
						at.trader.CancelOrder(symbol, orderID)
						report.Status = "PARTIALLY_FILLED" // 有成交部分，状态为部分成交
						report.EndTime = time.Now().UnixMilli()
						report.DurationMs = report.EndTime - report.StartTime
						return true, report, nil
					}
					// 继续等待

				case "CANCELED", "EXPIRED":
					report.Status = status
					report.EndTime = time.Now().UnixMilli()
					report.DurationMs = report.EndTime - report.StartTime
					log.Printf("  ❌ 订单已%s", status)
					goto next_attempt

				default:
					// 继续等待
				}
			}
		}

	next_attempt:
		// 继续下一轮尝试
	}

	// 不应该到达这里
	return false, report, fmt.Errorf("意外的执行流程结束")
}

// ExecuteLimitOpenLongForTest 执行限价开多仓（仅测试用）
func (at *AutoTrader) ExecuteLimitOpenLongForTest(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	return at.executeLimitOpenLongWithRecord(decision, actionRecord)
}

// executeLimitOpenLongWithRecord 执行限价开多仓并记录
func (at *AutoTrader) executeLimitOpenLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 获取市场数据用于定价
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 获取交易所过滤器信息
	filters, err := market.GetSymbolFilters(decision.Symbol)
	if err != nil {
		return fmt.Errorf("获取交易所过滤器失败: %w", err)
	}

	// 推导限价
	limitPrice, priceReason := market.DeriveOpenLimitPrice("BUY", marketData.Microstructure, filters.TickSize)
	if limitPrice <= 0 {
		return fmt.Errorf("推导限价失败: %s", priceReason)
	}

	// 计算并对齐数量
	margin := decision.PositionSizeUSD
	rawQuantity := (margin * float64(decision.Leverage)) / limitPrice
	quantity := market.RoundToStep(rawQuantity, filters.StepSize)

	// 检查 ExecutionGate mode - 只有 limit_only 时才启用生命周期管理
	// 这里使用 evaluateExecutionGate 函数（小写，未导出）
	gate := &market.ExecutionGate{}
	if marketData.Microstructure != nil {
		// 注意：这里无法直接调用 evaluateExecutionGate，因为它是未导出的
		// 我们需要一种不同的方法来检查 gate mode
		// 暂时使用固定的逻辑：检查 notional 是否足够低
		if marketData.Microstructure.MinNotional < 10000 {
			gate.Mode = "limit_only"
		} else {
			gate.Mode = "market_ok"
		}
	} else {
		gate.Mode = "limit_only" // 默认保守策略
	}
	if gate.Mode == "limit_only" {
		// M2.2: 启用生命周期管理
		log.Printf("  📌 限价开多仓 (生命周期管理): %s 推导限价: %.4f (原因: %s)", decision.Symbol, limitPrice, priceReason)

		success, report, err := at.executeLimitOrderLifecycle(decision.Symbol, "BUY", quantity, limitPrice, priceReason, gate.Mode)
		if err != nil {
			return fmt.Errorf("生命周期管理执行失败: %w", err)
		}

		if !success {
			if report.Status == "RETRIES_EXHAUSTED" {
				log.Printf("  ❌ 限价订单重试耗尽，放弃执行")
				actionRecord.Status = "ABORTED"
				actionRecord.Reason = "limit_retries_exhausted"
				actionRecord.ExecutionReport = report // 设置执行报告供测试验证
				return nil                            // 返回nil表示放弃执行而非错误
			}
			return fmt.Errorf("生命周期管理未成功完成")
		}

		// 记录执行结果
		actionRecord.Quantity = report.FilledQuantity
		actionRecord.Price = report.AvgFillPrice
		actionRecord.Status = "EXECUTED"
		actionRecord.ExecutionReport = report

		log.Printf("  ✅ 生命周期管理完成: 成交 %.6f @ %.4f", report.FilledQuantity, report.AvgFillPrice)
		return nil
	}

	// 普通执行（非limit_only模式）
	log.Printf("  📌 限价开多仓: %s 推导限价: %.4f (原因: %s)", decision.Symbol, limitPrice, priceReason)

	// 检查是否已有同向限价单或持仓
	posKey := decision.Symbol + "_long"
	if _, exists := at.pendingOrders[posKey]; exists {
		return fmt.Errorf("❌ %s 已有多单限价单挂单中，请先取消或等待成交", decision.Symbol)
	}

	positions, err := at.trader.GetPositions()
	if err == nil {
		// 检查总持仓数量（持仓+限价单）是否已达上限
		totalPositions := len(positions) + len(at.pendingOrders)
		if totalPositions >= 3 {
			return fmt.Errorf("❌ 总持仓数（含限价单）已达上限（3个），拒绝挂新单。当前：%d持仓 + %d限价单", len(positions), len(at.pendingOrders))
		}

		// 检查同币种同方向是否已有持仓
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
				return fmt.Errorf("❌ %s 已有多仓，无法再挂限价单", decision.Symbol)
			}
		}
	}

	// 计算并对齐数量（已在上面计算过了）
	// margin, rawQuantity, quantity 已在上面声明
	actionRecord.Quantity = quantity
	actionRecord.Price = limitPrice

	// 下限价单
	order, err := at.trader.LimitOpenLong(decision.Symbol, quantity, decision.Leverage, limitPrice, decision.StopLoss)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID

		// 保存限价单到内存
		at.pendingOrders[posKey] = &PendingOrder{
			Symbol:           decision.Symbol,
			Side:             "long",
			LimitPrice:       decision.LimitPrice,
			Quantity:         quantity,
			Leverage:         decision.Leverage,
			OrderID:          orderID,
			TP1:              decision.TP1,
			TP2:              decision.TP2,
			TP3:              decision.TP3,
			StopLoss:         decision.StopLoss,
			TakeProfit:       decision.TakeProfit,
			CreateTime:       time.Now().UnixMilli(),
			Confidence:       decision.Confidence,
			Reasoning:        decision.Reasoning,
			Thesis:           generateThesisFromReasoning(decision.Reasoning),
			CancelConditions: generateCancelConditions(decision),
		}

		// 记录创建时间
		at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

		// 增加每日开单计数（限价单也算）
		at.incrementDailyPairTrades(decision.Symbol)

		log.Printf("  ✓ 限价多单已挂: 订单ID %d, 限价%.4f, 等待成交", orderID, limitPrice)
	}

	return nil
}

// executeLimitOpenShortWithRecord 执行限价开空仓并记录
func (at *AutoTrader) executeLimitOpenShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 获取市场数据用于定价
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 获取交易所过滤器信息
	filters, err := market.GetSymbolFilters(decision.Symbol)
	if err != nil {
		return fmt.Errorf("获取交易所过滤器失败: %w", err)
	}

	// 推导限价
	limitPrice, priceReason := market.DeriveOpenLimitPrice("SELL", marketData.Microstructure, filters.TickSize)
	if limitPrice <= 0 {
		return fmt.Errorf("推导限价失败: %s", priceReason)
	}

	// 计算并对齐数量
	margin := decision.PositionSizeUSD
	rawQuantity := (margin * float64(decision.Leverage)) / limitPrice
	quantity := market.RoundToStep(rawQuantity, filters.StepSize)

	// 检查 ExecutionGate mode - 只有 limit_only 时才启用生命周期管理
	// 这里使用 evaluateExecutionGate 函数（小写，未导出）
	gate := &market.ExecutionGate{}
	if marketData.Microstructure != nil {
		// 注意：这里无法直接调用 evaluateExecutionGate，因为它是未导出的
		// 我们需要一种不同的方法来检查 gate mode
		// 暂时使用固定的逻辑：检查 notional 是否足够低
		if marketData.Microstructure.MinNotional < 10000 {
			gate.Mode = "limit_only"
		} else {
			gate.Mode = "market_ok"
		}
	} else {
		gate.Mode = "limit_only" // 默认保守策略
	}
	if gate.Mode == "limit_only" {
		// M2.2: 启用生命周期管理
		log.Printf("  📌 限价开空仓 (生命周期管理): %s 推导限价: %.4f (原因: %s)", decision.Symbol, limitPrice, priceReason)

		success, report, err := at.executeLimitOrderLifecycle(decision.Symbol, "SELL", quantity, limitPrice, priceReason, gate.Mode)
		if err != nil {
			return fmt.Errorf("生命周期管理执行失败: %w", err)
		}

		if !success {
			if report.Status == "RETRIES_EXHAUSTED" {
				log.Printf("  ❌ 限价订单重试耗尽，放弃执行")
				actionRecord.Status = "ABORTED"
				actionRecord.Reason = "limit_retries_exhausted"
				actionRecord.ExecutionReport = report // 设置执行报告供测试验证
				return nil                            // 返回nil表示放弃执行而非错误
			}
			return fmt.Errorf("生命周期管理未成功完成")
		}

		// 记录执行结果
		actionRecord.Quantity = report.FilledQuantity
		actionRecord.Price = report.AvgFillPrice
		actionRecord.Status = "EXECUTED"
		actionRecord.ExecutionReport = report

		log.Printf("  ✅ 生命周期管理完成: 成交 %.6f @ %.4f", report.FilledQuantity, report.AvgFillPrice)
		return nil
	}

	// 普通执行（非limit_only模式）
	log.Printf("  📌 限价开空仓: %s 推导限价: %.4f (原因: %s)", decision.Symbol, limitPrice, priceReason)

	// 检查是否已有同向限价单或持仓
	posKey := decision.Symbol + "_short"
	if _, exists := at.pendingOrders[posKey]; exists {
		return fmt.Errorf("❌ %s 已有空单限价单挂单中，请先取消或等待成交", decision.Symbol)
	}

	positions, err := at.trader.GetPositions()
	if err == nil {
		// 检查总持仓数量（持仓+限价单）是否已达上限
		totalPositions := len(positions) + len(at.pendingOrders)
		if totalPositions >= 3 {
			return fmt.Errorf("❌ 总持仓数（含限价单）已达上限（3个），拒绝挂新单。当前：%d持仓 + %d限价单", len(positions), len(at.pendingOrders))
		}

		// 检查同币种同方向是否已有持仓
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
				return fmt.Errorf("❌ %s 已有空仓，无法再挂限价单", decision.Symbol)
			}
		}
	}

	// 计算并对齐数量（已在上面计算过了）
	// margin, rawQuantity, quantity 已在上面声明
	actionRecord.Quantity = quantity
	actionRecord.Price = limitPrice

	// 计算并对齐数量（已在上面计算过了）
	// margin, rawQuantity, quantity 已在上面声明
	actionRecord.Quantity = quantity
	actionRecord.Price = limitPrice

	// 下限价单
	order, err := at.trader.LimitOpenShort(decision.Symbol, quantity, decision.Leverage, limitPrice, decision.StopLoss)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID

		// 保存限价单到内存
		at.pendingOrders[posKey] = &PendingOrder{
			Symbol:           decision.Symbol,
			Side:             "short",
			LimitPrice:       decision.LimitPrice,
			Quantity:         quantity,
			Leverage:         decision.Leverage,
			OrderID:          orderID,
			TP1:              decision.TP1,
			TP2:              decision.TP2,
			TP3:              decision.TP3,
			StopLoss:         decision.StopLoss,
			TakeProfit:       decision.TakeProfit,
			CreateTime:       time.Now().UnixMilli(),
			Confidence:       decision.Confidence,
			Reasoning:        decision.Reasoning,
			Thesis:           generateThesisFromReasoning(decision.Reasoning),
			CancelConditions: generateCancelConditions(decision),
		}

		// 记录创建时间
		at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

		// 增加每日开单计数（限价单也算）
		at.incrementDailyPairTrades(decision.Symbol)

		log.Printf("  ✓ 限价空单已挂: 订单ID %d, 限价%.4f, 等待成交", orderID, limitPrice)
	}

	return nil
}

// executeCancelLimitOrderWithRecord 取消限价单并记录
func (at *AutoTrader) executeCancelLimitOrderWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🗑️  取消限价单: %s 订单ID: %d", decision.Symbol, decision.OrderID)

	// 先在pendingOrders中查找（用于后续清理）
	var posKey string
	for key, order := range at.pendingOrders {
		if order.Symbol == decision.Symbol && order.OrderID == decision.OrderID {
			posKey = key
			break
		}
	}

	// 直接尝试取消订单（即使不在pendingOrders中，也可能存在同步延迟）
	err := at.trader.CancelOrder(decision.Symbol, decision.OrderID)
	if err != nil {
		// 检查是否是订单不存在或已取消的错误
		errMsg := err.Error()
		if strings.Contains(errMsg, "不存在") ||
			strings.Contains(errMsg, "not found") ||
			strings.Contains(errMsg, "does not exist") ||
			strings.Contains(errMsg, "已取消") ||
			strings.Contains(errMsg, "already cancelled") ||
			strings.Contains(errMsg, "-2011") { // Binance错误码：订单不存在
			// 订单可能已经被取消或成交，记录日志但不报错
			log.Printf("  ⚠️  订单 %s #%d 可能已被取消或成交: %v", decision.Symbol, decision.OrderID, err)
			// 如果订单在pendingOrders中，清理它
			if posKey != "" {
				delete(at.pendingOrders, posKey)
				delete(at.positionFirstSeenTime, posKey)
			}
			// ⚠️ 重要修复：即使订单不在pendingOrders中，只要订单已不存在/已取消，也应该减少计数
			// 因为限价单可能已经被取消，但计数没有更新
			if at.dailyPairTrades[decision.Symbol] > 0 {
				at.decrementDailyPairTrades(decision.Symbol)
			}
			return nil // 不报错，因为订单已经不存在了
		}
		// 其他错误才报错
		return err
	}

	// 取消成功，从内存中删除（如果存在）
	if posKey != "" {
		delete(at.pendingOrders, posKey)
		delete(at.positionFirstSeenTime, posKey)
	}

	// ⚠️ 重要修复：无论是否在pendingOrders中找到，只要取消成功，都应该减少计数
	// 因为限价单可能因为同步延迟等原因不在pendingOrders中，但确实存在并已取消
	if at.dailyPairTrades[decision.Symbol] > 0 {
		at.decrementDailyPairTrades(decision.Symbol)
	}

	log.Printf("  ✓ 已取消限价单: %s #%d", decision.Symbol, decision.OrderID)
	return nil
}

// checkExecutionGate 执行门禁检查（仅对市价开仓生效）
// determineFinalExecutionMode 确定最终执行方式
func (at *AutoTrader) determineFinalExecutionMode(gateMode, executionPreference string) (finalExecution string, override bool, overrideReason string) {
	// 默认逻辑：gate.mode=limit_only → final="limit", override=true if pref!="limit"
	// else if pref == "limit" → final="limit"
	// else (pref=="market" or "auto" or empty) → final="market"

	if gateMode == "limit_only" {
		if executionPreference == "limit" {
			return "limit", false, ""
		} else {
			return "limit", true, "gate_limit_only"
		}
	} else if executionPreference == "limit" {
		return "limit", false, ""
	} else {
		// pref=="market" or "auto" or empty → final="market"
		return "market", false, ""
	}
}

func (at *AutoTrader) checkExecutionGate(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	// 获取本轮的市场数据（应该已经获取过了，避免重复网络请求）
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		// 如果获取失败，记录警告但不阻止交易（保守策略）
		log.Printf("⚠️ 执行门禁检查失败，获取市场数据出错: %v，将允许市价开仓", err)
		return nil
	}

	// 记录 gate 信息到 actionRecord
	if marketData.Execution != nil {
		actionRecord.GateMode = marketData.Execution.Mode
		actionRecord.GateReason = marketData.Execution.Reason
	} else {
		actionRecord.GateMode = "market_ok" // 默认
		actionRecord.GateReason = "no_execution_gate"
	}

	// 记录 AI 的 execution_preference（默认处理）
	actionRecord.ExecutionPreference = decision.ExecutionPreference
	if actionRecord.ExecutionPreference == "" {
		actionRecord.ExecutionPreference = "auto"
	}

	// ExecutionGate 对齐：limit_only 模式强制 execution_preference="limit"
	if actionRecord.GateMode == "limit_only" && actionRecord.ExecutionPreference != "limit" {
		log.Printf("⚠️ ExecutionGate对齐: %s gate=limit_only，强制将AI的execution_preference从'%s'改为'limit'",
			decision.Symbol, actionRecord.ExecutionPreference)
		actionRecord.ExecutionPreference = "limit"
	}

	// 确定最终执行方式
	finalExecution, override, overrideReason := at.determineFinalExecutionMode(
		actionRecord.GateMode,
		actionRecord.ExecutionPreference,
	)

	actionRecord.FinalExecution = finalExecution
	actionRecord.Override = override
	actionRecord.OverrideReason = overrideReason

	log.Printf("🎛️ 执行方式确定: %s gate=%s, pref=%s → final=%s (override=%v, reason=%s)",
		decision.Symbol, actionRecord.GateMode, actionRecord.ExecutionPreference,
		actionRecord.FinalExecution, actionRecord.Override, actionRecord.OverrideReason)

	// 根据最终执行方式调整 action
	if finalExecution == "limit" && (decision.Action == "open_long" || decision.Action == "open_short") {
		if decision.Action == "open_long" {
			decision.Action = "limit_open_long"
			log.Printf("  📈 调整执行方式: open_long → limit_open_long")
		} else if decision.Action == "open_short" {
			decision.Action = "limit_open_short"
			log.Printf("  📉 调整执行方式: open_short → limit_open_short")
		}
	}

	return nil
}

// generateThesisFromReasoning 从reasoning中生成thesis（入场逻辑的一句话总结）
func generateThesisFromReasoning(reasoning string) string {
	if reasoning == "" {
		return "价格走势符合预期"
	}

	// 提取第一句话作为thesis
	sentences := strings.Split(reasoning, "。")
	if len(sentences) > 0 {
		firstSentence := strings.TrimSpace(sentences[0])
		if firstSentence != "" {
			return firstSentence
		}
	}

	return reasoning
}

// generateCancelConditions 生成撤单条件
func generateCancelConditions(decision *decision.Decision) string {
	var conditions []string

	// 基于止损位
	if decision.StopLoss > 0 {
		if decision.Action == "limit_open_long" {
			conditions = append(conditions, fmt.Sprintf("跌破%.4f", decision.StopLoss))
		} else if decision.Action == "limit_open_short" {
			conditions = append(conditions, fmt.Sprintf("突破%.4f", decision.StopLoss))
		}
	}

	// 时间条件
	conditions = append(conditions, "30分钟未成交")

	// 价格偏离条件
	conditions = append(conditions, "价格偏离>0.6%")

	// 结构反转条件
	if decision.Action == "limit_open_long" {
		conditions = append(conditions, "4h/1h结构转为看跌")
	} else if decision.Action == "limit_open_short" {
		conditions = append(conditions, "4h/1h结构转为看涨")
	}

	return strings.Join(conditions, " / ")
}
