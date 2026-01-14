package backtest

import (
	"log"
	"nofx/decision"
	"nofx/market"
	"time"
)

// OfflineAnalyzer 离线分析器：按时间顺序分析历史数据
type OfflineAnalyzer struct {
	ruleEngine    *RuleEngine
	startTime     time.Time
	endTime       time.Time
	symbols       []string
	scanInterval  time.Duration
	decisions     []DecisionRecord
	statistics    *Statistics
}

// DecisionRecord 决策记录
type DecisionRecord struct {
	Time        time.Time
	Symbol      string
	MarketData  *market.Data
	Decision    *decision.Decision
	RuleFailures []string
	Account     decision.AccountInfo
	Positions   []decision.PositionInfo
}

// NewOfflineAnalyzer 创建离线分析器
func NewOfflineAnalyzer(startTime, endTime time.Time, symbols []string, scanInterval time.Duration) *OfflineAnalyzer {
	return &OfflineAnalyzer{
		ruleEngine:   NewRuleEngine(),
		startTime:    startTime,
		endTime:      endTime,
		symbols:      symbols,
		scanInterval: scanInterval,
		decisions:    make([]DecisionRecord, 0),
		statistics:   NewStatistics(),
	}
}

// Analyze 执行离线分析
func (oa *OfflineAnalyzer) Analyze() error {
	log.Printf("🚀 开始离线分析")
	log.Printf("📅 时间范围: %s 至 %s", oa.startTime.Format("2006-01-02 15:04:05"), oa.endTime.Format("2006-01-02 15:04:05"))
	log.Printf("📊 币种: %v", oa.symbols)
	log.Printf("⏱️  扫描间隔: %v", oa.scanInterval)

	// 模拟账户状态
	account := decision.AccountInfo{
		TotalEquity:      10000.0,
		AvailableBalance: 10000.0,
		TotalPnL:         0.0,
		TotalPnLPct:      0.0,
		MarginUsed:       0.0,
		MarginUsedPct:    0.0,
		PositionCount:    0,
	}

	positions := make([]decision.PositionInfo, 0)
	pendingOrders := make([]decision.PendingOrderInfo, 0)

	// 按时间顺序处理所有周期
	currentTime := oa.startTime
	cycleCount := 0

	for currentTime.Before(oa.endTime) || currentTime.Equal(oa.endTime) {
		cycleCount++
		if cycleCount%100 == 0 {
			log.Printf("⏰ 处理周期 #%d: %s", cycleCount, currentTime.Format("2006-01-02 15:04:05"))
		}

		// 对每个币种进行分析
		for _, symbol := range oa.symbols {
			// 获取市场数据（到当前时间为止）
			marketData, err := oa.getMarketDataAtTime(symbol, currentTime)
			if err != nil {
				log.Printf("⚠️  获取 %s 在 %s 的市场数据失败: %v", symbol, currentTime.Format("2006-01-02 15:04:05"), err)
				continue
			}

			// 更新账户状态（基于持仓）
			account = oa.updateAccountState(account, positions, marketData)

			// 检查持仓管理（如果有持仓）
			if len(positions) > 0 {
				oa.checkPositionManagement(&positions, marketData, currentTime)
			}

			// 检查是否可以开新仓
			if len(positions) < 3 {
				// 尝试开多仓
				decisionLong := oa.analyzeOpenLong(symbol, marketData, account, positions, pendingOrders)
				oa.recordDecision(currentTime, symbol, marketData, decisionLong, account, positions)

				// 尝试开空仓
				decisionShort := oa.analyzeOpenShort(symbol, marketData, account, positions, pendingOrders)
				oa.recordDecision(currentTime, symbol, marketData, decisionShort, account, positions)
			}
		}

		// 移动到下一个周期
		currentTime = currentTime.Add(oa.scanInterval)
	}

	log.Printf("✅ 离线分析完成，共处理 %d 个周期", cycleCount)

	// 生成统计
	oa.statistics.Calculate(oa.decisions)

	return nil
}

// getMarketDataAtTime 获取指定时间点的市场数据
func (oa *OfflineAnalyzer) getMarketDataAtTime(symbol string, t time.Time) (*market.Data, error) {
	// 注意：这里需要从历史数据中获取
	// 实际实现时，应该从Binance API获取历史K线数据，然后计算指标
	// 这里简化处理，直接调用 market.Get（但需要修改为支持历史时间）
	
	// 临时方案：使用当前市场数据（实际应该使用历史数据）
	data, err := market.Get(symbol)
	if err != nil {
		return nil, err
	}
	
	// 设置Symbol
	data.Symbol = symbol
	
	return data, nil
}

// analyzeOpenLong 分析开多仓
func (oa *OfflineAnalyzer) analyzeOpenLong(symbol string, marketData *market.Data, account decision.AccountInfo, positions []decision.PositionInfo, pendingOrders []decision.PendingOrderInfo) *decision.Decision {
	ctx := &RuleContext{
		MarketData:    marketData,
		Positions:     positions,
		PendingOrders: pendingOrders,
		Action:        "open_long",
		Account:       account,
	}

	return oa.ruleEngine.GenerateDecision(ctx)
}

// analyzeOpenShort 分析开空仓
func (oa *OfflineAnalyzer) analyzeOpenShort(symbol string, marketData *market.Data, account decision.AccountInfo, positions []decision.PositionInfo, pendingOrders []decision.PendingOrderInfo) *decision.Decision {
	ctx := &RuleContext{
		MarketData:    marketData,
		Positions:     positions,
		PendingOrders: pendingOrders,
		Action:        "open_short",
		Account:       account,
	}

	return oa.ruleEngine.GenerateDecision(ctx)
}

// checkPositionManagement 检查持仓管理
func (oa *OfflineAnalyzer) checkPositionManagement(positions *[]decision.PositionInfo, marketData *market.Data, currentTime time.Time) {
	// 检查止损/止盈
	for i := len(*positions) - 1; i >= 0; i-- {
		pos := (*positions)[i]
		if pos.Symbol != marketData.Symbol {
			continue
		}

		// 检查止损
		if pos.StopLoss > 0 {
			if pos.Side == "long" && marketData.CurrentPrice <= pos.StopLoss {
				// 触发止损
				log.Printf("🛑 %s 多仓触发止损: 入场价 %.2f, 止损价 %.2f, 当前价 %.2f", pos.Symbol, pos.EntryPrice, pos.StopLoss, marketData.CurrentPrice)
				// 移除持仓
				*positions = append((*positions)[:i], (*positions)[i+1:]...)
				continue
			}
			if pos.Side == "short" && marketData.CurrentPrice >= pos.StopLoss {
				// 触发止损
				log.Printf("🛑 %s 空仓触发止损: 入场价 %.2f, 止损价 %.2f, 当前价 %.2f", pos.Symbol, pos.EntryPrice, pos.StopLoss, marketData.CurrentPrice)
				// 移除持仓
				*positions = append((*positions)[:i], (*positions)[i+1:]...)
				continue
			}
		}

		// 检查止盈
		if pos.TP1 > 0 || pos.TP2 > 0 || pos.TP3 > 0 {
			if pos.Side == "long" {
				if pos.TP1 > 0 && marketData.CurrentPrice >= pos.TP1 && pos.TPStage < 1 {
					log.Printf("🎯 %s 多仓到达TP1: %.2f", pos.Symbol, pos.TP1)
					// 更新TP阶段
					(*positions)[i].TPStage = 1
				}
				if pos.TP2 > 0 && marketData.CurrentPrice >= pos.TP2 && pos.TPStage < 2 {
					log.Printf("🎯 %s 多仓到达TP2: %.2f", pos.Symbol, pos.TP2)
					(*positions)[i].TPStage = 2
				}
				if pos.TP3 > 0 && marketData.CurrentPrice >= pos.TP3 {
					log.Printf("🎯 %s 多仓到达TP3: %.2f", pos.Symbol, pos.TP3)
					// 移除持仓
					*positions = append((*positions)[:i], (*positions)[i+1:]...)
					continue
				}
			}
			if pos.Side == "short" {
				if pos.TP1 > 0 && marketData.CurrentPrice <= pos.TP1 && pos.TPStage < 1 {
					log.Printf("🎯 %s 空仓到达TP1: %.2f", pos.Symbol, pos.TP1)
					(*positions)[i].TPStage = 1
				}
				if pos.TP2 > 0 && marketData.CurrentPrice <= pos.TP2 && pos.TPStage < 2 {
					log.Printf("🎯 %s 空仓到达TP2: %.2f", pos.Symbol, pos.TP2)
					(*positions)[i].TPStage = 2
				}
				if pos.TP3 > 0 && marketData.CurrentPrice <= pos.TP3 {
					log.Printf("🎯 %s 空仓到达TP3: %.2f", pos.Symbol, pos.TP3)
					// 移除持仓
					*positions = append((*positions)[:i], (*positions)[i+1:]...)
					continue
				}
			}
		}
	}
}

// updateAccountState 更新账户状态
func (oa *OfflineAnalyzer) updateAccountState(account decision.AccountInfo, positions []decision.PositionInfo, marketData *market.Data) decision.AccountInfo {
	// 计算持仓盈亏
	totalUnrealizedPnL := 0.0
	totalMarginUsed := 0.0

	for _, pos := range positions {
		if pos.Symbol == marketData.Symbol {
			totalUnrealizedPnL += pos.UnrealizedPnL
			totalMarginUsed += pos.MarginUsed
		}
	}

	account.TotalEquity = account.TotalEquity + totalUnrealizedPnL
	account.MarginUsed = totalMarginUsed
	account.PositionCount = len(positions)

	if account.TotalEquity > 0 {
		account.MarginUsedPct = (totalMarginUsed / account.TotalEquity) * 100
	}

	return account
}

// recordDecision 记录决策
func (oa *OfflineAnalyzer) recordDecision(t time.Time, symbol string, marketData *market.Data, decision *decision.Decision, account decision.AccountInfo, positions []decision.PositionInfo) {
	record := DecisionRecord{
		Time:       t,
		Symbol:     symbol,
		MarketData: marketData,
		Decision:   decision,
		Account:    account,
		Positions:  positions,
	}

	// 如果决策是wait，记录失败原因
	if decision.Action == "wait" {
		// 从reasoning中提取失败原因
		record.RuleFailures = []string{decision.Reasoning}
	}

	oa.decisions = append(oa.decisions, record)
}

// GetDecisions 获取所有决策记录
func (oa *OfflineAnalyzer) GetDecisions() []DecisionRecord {
	return oa.decisions
}

// GetStatistics 获取统计结果
func (oa *OfflineAnalyzer) GetStatistics() *Statistics {
	return oa.statistics
}

