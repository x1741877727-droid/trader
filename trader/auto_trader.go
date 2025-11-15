package trader

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/decision"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"strings"
	"time"
)

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
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"` // "long"/"short"
	LimitPrice float64 `json:"limit_price"`
	Quantity   float64 `json:"quantity"`
	Leverage   int     `json:"leverage"`
	OrderID    int64   `json:"order_id"`
	TP1        float64 `json:"tp1"`
	TP2        float64 `json:"tp2"`
	TP3        float64 `json:"tp3"`
	StopLoss   float64 `json:"stop_loss"`
	TakeProfit float64 `json:"take_profit"`
	CreateTime int64   `json:"create_time"` // 创建时间戳（毫秒）
	Confidence int     `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

// AutoTraderConfig 自动交易配置（简化版 - AI全权决策）
type AutoTraderConfig struct {
	// Trader标识
	ID      string // Trader唯一标识（用于日志目录等）
	Name    string // Trader显示名称
	AIModel string // AI模型: "qwen" 或 "deepseek"

	// 交易平台选择
	Exchange string // "binance", "hyperliquid" 或 "aster"

	// 币安API配置
	BinanceAPIKey    string
	BinanceSecretKey string

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
	trader                Trader // 使用Trader接口（支持多平台）
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

	// 记住所有待成交的限价单
	pendingOrders map[string]*PendingOrder // key: "BTCUSDT_long" / "ETHUSDT_short"
}

// NewAutoTrader 创建自动交易器
func NewAutoTrader(config AutoTraderConfig) (*AutoTrader, error) {
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

	switch config.Exchange {
	case "binance":
		log.Printf("🏦 [%s] 使用币安合约交易", config.Name)
		trader = NewFuturesTrader(config.BinanceAPIKey, config.BinanceSecretKey)
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
		pendingOrders:         make(map[string]*PendingOrder),
	}, nil
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

	log.Printf("\n" + strings.Repeat("=", 70))
	log.Printf("⏰ %s - AI决策周期 #%d", time.Now().Format("2006-01-02 15:04:05"), at.callCount)
	log.Printf(strings.Repeat("=", 70))

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

	// 2. 重置日盈亏（每天重置）
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.dailyPnL = 0
		at.lastResetTime = time.Now()
		log.Println("📅 日盈亏已重置")
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

	// 4. 调用AI获取完整决策
	log.Printf("🤖 正在请求AI分析并决策... [模板: %s]", at.systemPromptTemplate)

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

	decisionResp, err := decision.GetFullDecisionWithCustomPrompt(ctx, at.mcpClient, finalPrompt, at.overrideBasePrompt, at.systemPromptTemplate)

	// 即使有错误，也保存思维链、决策和输入prompt（用于debug）
	if decisionResp != nil {
		record.SystemPrompt = decisionResp.SystemPrompt // 保存系统提示词
		record.InputPrompt = decisionResp.UserPrompt
		record.CoTTrace = decisionResp.CoTTrace
		if len(decisionResp.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(decisionResp.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		}
	}

	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("获取AI决策失败: %v", err)

		// 打印系统提示词和AI思维链（即使有错误，也要输出以便调试）
		if decisionResp != nil {
			if decisionResp.SystemPrompt != "" {
				log.Printf("\n" + strings.Repeat("=", 70))
				log.Printf("📋 系统提示词 [模板: %s] (错误情况)", at.systemPromptTemplate)
				log.Println(strings.Repeat("=", 70))
				log.Println(decisionResp.SystemPrompt)
				log.Printf(strings.Repeat("=", 70) + "\n")
			}

			if decisionResp.CoTTrace != "" {
				log.Printf("\n" + strings.Repeat("-", 70))
				log.Println("💭 AI思维链分析（错误情况）:")
				log.Println(strings.Repeat("-", 70))
				log.Println(decisionResp.CoTTrace)
				log.Printf(strings.Repeat("-", 70) + "\n")
			}
		}

		at.decisionLogger.LogDecision(record)
		return fmt.Errorf("获取AI决策失败: %w", err)
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

// autoCheckAndUpdateStopLoss 自动检测所有持仓的TP触及情况并抬止损（代码层自动执行）
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

		// 如果订单不存在，说明已成交或已取消，从待处理列表中移除
		if !orderExists {
			log.Printf("  ✓ 限价单已成交或取消: %s %s (订单ID: %d), 从待处理列表中移除",
				pendingOrder.Symbol, pendingOrder.Side, pendingOrder.OrderID)
			delete(at.pendingOrders, posKey)
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

		if newStage > tgt.Stage {
			switch newStage {
			case 1: // 到达 TP1：平掉 1/3 仓位
				partialCloseQty = qty * (1.0 / 3.0)
				partialCloseRatio = "1/3"
			case 2: // 到达 TP2：平掉剩余仓位的 1/2（即再平 1/3，总共平了 2/3）
				partialCloseQty = qty * 0.5
				partialCloseRatio = "1/2 剩余（总计 2/3）"
			case 3: // 到达 TP3：交易所的止盈单会自动平掉全部
				// 不需要手动平仓，TP3止盈单会自动触发
				log.Printf("  🎯 %s %s 到达TP3，等待止盈单自动平仓", symbol, strings.ToUpper(side))
			}

			// 执行分批平仓（TP1和TP2时）
			if partialCloseQty > 0 && newStage < 3 {
				log.Printf("  💰 分批止盈: %s %s | Stage=%d | 平仓 %s (数量: %.4f)",
					symbol, strings.ToUpper(side), newStage, partialCloseRatio, partialCloseQty)

				var closeErr error
				switch strings.ToUpper(side) {
				case "LONG":
					_, closeErr = at.trader.CloseLong(symbol, partialCloseQty)
				case "SHORT":
					_, closeErr = at.trader.CloseShort(symbol, partialCloseQty)
				}

				if closeErr != nil {
					log.Printf("  ❌ %s 分批平仓失败: %v", symbol, closeErr)
					// 继续执行抬止损，即使分批平仓失败
				} else {
					log.Printf("  ✅ %s %s 成功平仓 %s，剩余仓位继续持有", symbol, strings.ToUpper(side), partialCloseRatio)

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

		// 执行抬止损
		log.Printf("  📈 自动抬止损: %s %s | 阶段 %d→%d | 止损 %.4f→%.4f",
			symbol, strings.ToUpper(side), tgt.Stage, newStage, tgt.CurrentSL, newSL)

		if err := at.trader.SetStopLoss(symbol, strings.ToUpper(side), qty, newSL); err != nil {
			log.Printf("  ❌ %s 设置止损失败: %v", symbol, err)
			continue
		}

		// 更新内存记录
		tgt.CurrentSL = newSL
		if newStage > tgt.Stage {
			tgt.Stage = newStage
		}

		log.Printf("  ✅ %s %s 止损已自动抬升至 %.4f (Stage=%d)", symbol, strings.ToUpper(side), newSL, tgt.Stage)
	}

	return nil
}

// buildDynamicPrompt 把当前持仓的 tp1/tp2/tp3 拼成一段，喂回给AI，让它知道什么时候该发 update_stop_loss
func (at *AutoTrader) buildDynamicPrompt(ctx *decision.Context) string {
	if len(ctx.Positions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# 当前持仓止盈结构（系统自动分批止盈+抬止损）\n")
	sb.WriteString("# TP1: 自动平仓 1/3 + 抬止损到开仓价\n")
	sb.WriteString("# TP2: 自动平仓 1/2剩余（总计平 2/3） + 抬止损到 (entry+TP1)/2\n")
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

		positionInfos = append(positionInfos, decision.PositionInfo{
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
		})
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

			delete(at.positionFirstSeenTime, key)
			// 同步清理该持仓的TP记忆
			delete(at.positionTargets, key)
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

	// 6. 构建上下文
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
		Positions:      positionInfos,
		CandidateCoins: candidateCoins,
		Performance:    performance,
	}

	return ctx, nil
}

// executeDecisionWithRecord 执行AI决策并记录详细信息
func (at *AutoTrader) executeDecisionWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	switch decision.Action {
	case "open_long":
		return at.executeOpenLongWithRecord(decision, actionRecord)
	case "open_short":
		return at.executeOpenShortWithRecord(decision, actionRecord)
	case "close_long":
		return at.executeCloseLongWithRecord(decision, actionRecord)
	case "close_short":
		return at.executeCloseShortWithRecord(decision, actionRecord)
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

	// 算出“理论新止损”和新阶段
	newSL, newStage := computeTrailingSL(entry, side, tgt, lastPrice)

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

	// ✅ 只有不是补仓时才检查有没有同方向仓位
	if !decision.IsAddOn {
		positions, err := at.trader.GetPositions()
		if err == nil {
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

	// ✅ 只有不是补仓时才检查有没有同方向仓位
	if !decision.IsAddOn {
		positions, err := at.trader.GetPositions()
		if err == nil {
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

	// 平仓
	order, err := at.trader.CloseLong(decision.Symbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	// 清理该持仓的tp记忆
	delete(at.positionTargets, decision.Symbol+"_long")

	log.Printf("  ✓ 平仓成功")
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

	// 平仓
	order, err := at.trader.CloseShort(decision.Symbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	// 清理该持仓的tp记忆
	delete(at.positionTargets, decision.Symbol+"_short")

	log.Printf("  ✓ 平仓成功")
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
		case "close_long", "close_short":
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

// executeLimitOpenLongWithRecord 执行限价开多仓并记录
func (at *AutoTrader) executeLimitOpenLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📌 限价开多仓: %s 限价: %.4f", decision.Symbol, decision.LimitPrice)

	// 检查是否已有同向限价单或持仓
	posKey := decision.Symbol + "_long"
	if _, exists := at.pendingOrders[posKey]; exists {
		return fmt.Errorf("❌ %s 已有多单限价单挂单中，请先取消或等待成交", decision.Symbol)
	}

	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
				return fmt.Errorf("❌ %s 已有多仓，无法再挂限价单", decision.Symbol)
			}
		}
	}

	// 计算数量
	margin := decision.PositionSizeUSD
	quantity := (margin * float64(decision.Leverage)) / decision.LimitPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = decision.LimitPrice

	// 下限价单
	order, err := at.trader.LimitOpenLong(decision.Symbol, quantity, decision.Leverage, decision.LimitPrice, decision.StopLoss)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID

		// 保存限价单到内存
		at.pendingOrders[posKey] = &PendingOrder{
			Symbol:     decision.Symbol,
			Side:       "long",
			LimitPrice: decision.LimitPrice,
			Quantity:   quantity,
			Leverage:   decision.Leverage,
			OrderID:    orderID,
			TP1:        decision.TP1,
			TP2:        decision.TP2,
			TP3:        decision.TP3,
			StopLoss:   decision.StopLoss,
			TakeProfit: decision.TakeProfit,
			CreateTime: time.Now().UnixMilli(),
			Confidence: decision.Confidence,
			Reasoning:  decision.Reasoning,
		}

		// 记录创建时间
		at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

		log.Printf("  ✓ 限价多单已挂: 订单ID %d, 限价%.4f, 等待成交", orderID, decision.LimitPrice)
	}

	return nil
}

// executeLimitOpenShortWithRecord 执行限价开空仓并记录
func (at *AutoTrader) executeLimitOpenShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📌 限价开空仓: %s 限价: %.4f", decision.Symbol, decision.LimitPrice)

	// 检查是否已有同向限价单或持仓
	posKey := decision.Symbol + "_short"
	if _, exists := at.pendingOrders[posKey]; exists {
		return fmt.Errorf("❌ %s 已有空单限价单挂单中，请先取消或等待成交", decision.Symbol)
	}

	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
				return fmt.Errorf("❌ %s 已有空仓，无法再挂限价单", decision.Symbol)
			}
		}
	}

	// 计算数量
	margin := decision.PositionSizeUSD
	quantity := (margin * float64(decision.Leverage)) / decision.LimitPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = decision.LimitPrice

	// 下限价单
	order, err := at.trader.LimitOpenShort(decision.Symbol, quantity, decision.Leverage, decision.LimitPrice, decision.StopLoss)
	if err != nil {
		return err
	}

	// 记录订单ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID

		// 保存限价单到内存
		at.pendingOrders[posKey] = &PendingOrder{
			Symbol:     decision.Symbol,
			Side:       "short",
			LimitPrice: decision.LimitPrice,
			Quantity:   quantity,
			Leverage:   decision.Leverage,
			OrderID:    orderID,
			TP1:        decision.TP1,
			TP2:        decision.TP2,
			TP3:        decision.TP3,
			StopLoss:   decision.StopLoss,
			TakeProfit: decision.TakeProfit,
			CreateTime: time.Now().UnixMilli(),
			Confidence: decision.Confidence,
			Reasoning:  decision.Reasoning,
		}

		// 记录创建时间
		at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

		log.Printf("  ✓ 限价空单已挂: 订单ID %d, 限价%.4f, 等待成交", orderID, decision.LimitPrice)
	}

	return nil
}

// executeCancelLimitOrderWithRecord 取消限价单并记录
func (at *AutoTrader) executeCancelLimitOrderWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🗑️  取消限价单: %s 订单ID: %d", decision.Symbol, decision.OrderID)

	// 在pendingOrders中查找并删除
	var posKey string
	for key, order := range at.pendingOrders {
		if order.Symbol == decision.Symbol && order.OrderID == decision.OrderID {
			posKey = key
			break
		}
	}

	if posKey == "" {
		return fmt.Errorf("❌ 未找到订单 %s #%d", decision.Symbol, decision.OrderID)
	}

	// 取消订单
	if err := at.trader.CancelOrder(decision.Symbol, decision.OrderID); err != nil {
		return err
	}

	// 从内存中删除
	delete(at.pendingOrders, posKey)
	delete(at.positionFirstSeenTime, posKey)

	log.Printf("  ✓ 已取消限价单: %s #%d", decision.Symbol, decision.OrderID)
	return nil
}
