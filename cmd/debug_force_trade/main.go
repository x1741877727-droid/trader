package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"nofx/config"
	"nofx/decision"
	"nofx/logger"
	"nofx/market"
	"nofx/trader"
)

// MockMarketDataProvider 模拟市场数据提供者（用于强制触发 limit_only）
type MockMarketDataProvider struct {
	data *market.Data
}

func (p *MockMarketDataProvider) Get(symbol string) (*market.Data, error) {
	return p.data, nil
}

// MockSymbolFiltersProvider 模拟的过滤器提供者（测试用）
type MockSymbolFiltersProvider struct {
	filters map[string]*market.SymbolFilters
}

func NewMockSymbolFiltersProvider() *MockSymbolFiltersProvider {
	return &MockSymbolFiltersProvider{
		filters: make(map[string]*market.SymbolFilters),
	}
}

func (p *MockSymbolFiltersProvider) SetFilters(symbol string, tickSize, stepSize, minNotional float64) {
	p.filters[symbol] = &market.SymbolFilters{
		TickSize:    tickSize,
		StepSize:    stepSize,
		MinNotional: minNotional,
	}
}

func (p *MockSymbolFiltersProvider) GetSymbolFilters(symbol string) (*market.SymbolFilters, error) {
	if filters, exists := p.filters[symbol]; exists {
		return filters, nil
	}
	// 默认值
	return &market.SymbolFilters{
		TickSize:    0.1,
		StepSize:    0.001,
		MinNotional: 10.0,
	}, nil
}

// createLimitOnlyMarketData 创建能触发 limit_only 的市场数据
func createLimitOnlyMarketData() *market.Data {
	// 构造触发 limit_only 的 microstructure：
	// depth_ratio > maxDepthRatioAbs (3.0) 或者 min_notional < minBestNotionalUsdtLimitOnly (10000.0)
	return &market.Data{
		Symbol:         "BTCUSDT",
		CurrentPrice:   50000.0,
		Microstructure: &market.MicrostructureSummary{
			BestBidPrice:  50000.00,
			BestAskPrice:  50005.00, // spread_bps = 10.0 (正常)
			SpreadBps:     10.0,
			MinNotional:   5000.0,   // < 10000，触发 limit_only
			DepthRatio:    1.0,      // 正常
			BestBidNotional: 50000.0,
			BestAskNotional: 50000.0,
		},
	}
}

func runScenarioFilled() {
	fmt.Println("🎯 场景: 限价单快速成交 (filled)")

	// 设置 ExecutionGate 配置
	market.SetExecutionGateConfig(struct {
		MaxSpreadBpsLimitOnly             float64
		MaxSpreadBpsNoTrade               float64
		MaxDepthRatioAbs                  float64
		MinDepthRatioAbs                  float64
		MaxSpreadBpsLimitPreferred        float64
		MinBestNotionalUsdtLimitOnly      float64
		MinBestNotionalUsdtLimitPreferred float64
		MinDepthNotional10LimitOnly       float64
		MinDepthNotional10LimitPreferred  float64
		NotionalMultiplierLimitOnly       float64
		NotionalMultiplierNoTrade         float64
		DefaultModeOnMissing              string
	}{
		MaxSpreadBpsLimitOnly:             25.0,
		MaxSpreadBpsNoTrade:               40.0,
		MaxDepthRatioAbs:                  3.0,
		MinDepthRatioAbs:                  0.33,
		MaxSpreadBpsLimitPreferred:        15.0,
		MinBestNotionalUsdtLimitOnly:      10000.0,
		MinBestNotionalUsdtLimitPreferred: 50000.0,
		MinDepthNotional10LimitOnly:       200000.0,
		MinDepthNotional10LimitPreferred:  500000.0,
		NotionalMultiplierLimitOnly:       8.0,
		NotionalMultiplierNoTrade:         15.0,
		DefaultModeOnMissing:              "limit_only",
	})

	// 注入市场数据（触发 limit_only）
	marketData := createLimitOnlyMarketData()
	market.SetMarketDataProvider(&MockMarketDataProvider{data: marketData})
	defer market.ResetMarketDataProvider()

	// 注入过滤器提供者
	mockFilters := NewMockSymbolFiltersProvider()
	mockFilters.SetFilters("BTCUSDT", 0.1, 0.001, 10.0)
	market.SetSymbolFiltersProvider(mockFilters)
	defer market.ResetSymbolFiltersProvider()

	// 输出当前 microstructure
	micro := marketData.Microstructure
	fmt.Printf("📊 当前 microstructure: spread_bps=%.2f, depth_ratio=%.2f, min_notional=%.0f\n",
		micro.SpreadBps, micro.DepthRatio, micro.MinNotional)

	// 检查 ExecutionGate
	filledMarketData, err := market.Get("BTCUSDT")
	if err != nil {
		log.Fatalf("获取市场数据失败: %v", err)
	}
	gate := filledMarketData.Execution
	if gate == nil {
		fmt.Printf("🎛️  ExecutionGate: mode=market_ok, reason=no_execution_gate\n")
	} else {
		fmt.Printf("🎛️  ExecutionGate: mode=%s, reason=%s\n", gate.Mode, gate.Reason)
	}

	// 创建确定性行为的 PaperTrader
	paperTrader := trader.NewPaperTrader()
	paperTrader.SetDeterministicBehavior(&trader.DeterministicBehavior{
		Enabled:        true,
		FillDelayMs:    10,  // 快速成交
		NeverFill:      false,
		FixedFillPrice: 50000.0,
	})

	// 创建全局配置
	globalConfig := &config.Config{}

	// 创建 AutoTrader（限价开仓专用配置）
	at, err := trader.NewAutoTrader(trader.AutoTraderConfig{
		ID:                       "debug-force-trade",
		Name:                     "Debug Force Trade",
		Exchange:                 "binance",
		TraderMode:               "paper", // 强制使用 paper 模式
		InitialBalance:           100000.0, // 设置初始余额
		LimitOrderWaitSeconds:    5,
		LimitOrderMaxRetries:     3,
		LimitOrderPollIntervalMs: 100,
		CancelOnPartialFill:      false,
		PostOnlyWhenLimitOnly:    true,
	}, globalConfig)
	if err != nil {
		log.Fatalf("❌ 创建 AutoTrader 失败: %v", err)
	}

	// 构造决策
	decision := &decision.Decision{
		Symbol:          "BTCUSDT",
		Action:          "limit_open_long",
		PositionSizeUSD: 1000.0,
		Leverage:        5,
	}

	actionRecord := &logger.DecisionAction{
		Action:   decision.Action,
		Symbol:   decision.Symbol,
		Price:    0,
		Quantity: 0,
	}

	// 执行限价开仓
	startTime := time.Now()
	err = at.ExecuteLimitOpenLongForTest(decision, actionRecord)
	elapsed := time.Since(startTime)

	if err != nil {
		log.Fatalf("❌ 执行失败: %v", err)
	}

	// 输出执行报告
	if actionRecord.ExecutionReport == nil {
		log.Fatal("❌ 未生成执行报告")
	}

	report := actionRecord.ExecutionReport.(*trader.LimitOrderExecutionReport)
	fmt.Printf("📋 生命周期执行报告:\n")
	fmt.Printf("   status: %s\n", report.Status)
	fmt.Printf("   attempts: %d\n", report.AttemptIndex)
	fmt.Printf("   filled_quantity: %.6f\n", report.FilledQuantity)
	fmt.Printf("   avg_fill_price: %.2f\n", report.AvgFillPrice)
	fmt.Printf("   duration_ms: %d\n", report.DurationMs)
	fmt.Printf("   实际耗时: %.2fs\n", elapsed.Seconds())

	if report.Status != "FILLED" {
		log.Fatalf("❌ 期望状态 FILLED，实际 %s", report.Status)
	}
}

func runScenarioTimeout() {
	fmt.Println("🎯 场景: 重试耗尽 (timeout)")

	// 设置 ExecutionGate 配置
	market.SetExecutionGateConfig(struct {
		MaxSpreadBpsLimitOnly             float64
		MaxSpreadBpsNoTrade               float64
		MaxDepthRatioAbs                  float64
		MinDepthRatioAbs                  float64
		MaxSpreadBpsLimitPreferred        float64
		MinBestNotionalUsdtLimitOnly      float64
		MinBestNotionalUsdtLimitPreferred float64
		MinDepthNotional10LimitOnly       float64
		MinDepthNotional10LimitPreferred  float64
		NotionalMultiplierLimitOnly       float64
		NotionalMultiplierNoTrade         float64
		DefaultModeOnMissing              string
	}{
		MaxSpreadBpsLimitOnly:             25.0,
		MaxSpreadBpsNoTrade:               40.0,
		MaxDepthRatioAbs:                  3.0,
		MinDepthRatioAbs:                  0.33,
		MaxSpreadBpsLimitPreferred:        15.0,
		MinBestNotionalUsdtLimitOnly:      10000.0,
		MinBestNotionalUsdtLimitPreferred: 50000.0,
		MinDepthNotional10LimitOnly:       200000.0,
		MinDepthNotional10LimitPreferred:  500000.0,
		NotionalMultiplierLimitOnly:       8.0,
		NotionalMultiplierNoTrade:         15.0,
		DefaultModeOnMissing:              "limit_only",
	})

	// 注入市场数据
	marketData := createLimitOnlyMarketData()
	market.SetMarketDataProvider(&MockMarketDataProvider{data: marketData})
	defer market.ResetMarketDataProvider()

	// 注入过滤器提供者
	mockFilters := NewMockSymbolFiltersProvider()
	mockFilters.SetFilters("BTCUSDT", 0.1, 0.001, 10.0)
	market.SetSymbolFiltersProvider(mockFilters)
	defer market.ResetSymbolFiltersProvider()

	// 输出当前 microstructure
	micro := marketData.Microstructure
	fmt.Printf("📊 当前 microstructure: spread_bps=%.2f, depth_ratio=%.2f, min_notional=%.0f\n",
		micro.SpreadBps, micro.DepthRatio, micro.MinNotional)

	// 检查 ExecutionGate
	timeoutMarketData, err := market.Get("BTCUSDT")
	if err != nil {
		log.Fatalf("获取市场数据失败: %v", err)
	}
	gate := timeoutMarketData.Execution
	if gate == nil {
		fmt.Printf("🎛️  ExecutionGate: mode=market_ok, reason=no_execution_gate\n")
	} else {
		fmt.Printf("🎛️  ExecutionGate: mode=%s, reason=%s\n", gate.Mode, gate.Reason)
	}

	// 创建确定性行为的 PaperTrader（永不成交）
	paperTrader := trader.NewPaperTrader()
	paperTrader.SetDeterministicBehavior(&trader.DeterministicBehavior{
		Enabled:   true,
		NeverFill: true, // 永不成交
	})

	// 创建全局配置
	globalConfig := &config.Config{}

	// 创建 AutoTrader（短超时，少重试次数）
	at2, err := trader.NewAutoTrader(trader.AutoTraderConfig{
		ID:                       "debug-force-trade-timeout",
		Name:                     "Debug Force Trade Timeout",
		Exchange:                 "binance",
		TraderMode:               "paper",
		InitialBalance:           100000.0, // 设置初始余额
		LimitOrderWaitSeconds:    1, // 短超时
		LimitOrderMaxRetries:     2, // 2次重试
		LimitOrderPollIntervalMs: 200,
		CancelOnPartialFill:      false,
		PostOnlyWhenLimitOnly:    true,
	}, globalConfig)
	if err != nil {
		log.Fatalf("❌ 创建 AutoTrader 失败: %v", err)
	}

	// 构造决策
	decision := &decision.Decision{
		Symbol:          "BTCUSDT",
		Action:          "limit_open_long",
		PositionSizeUSD: 1000.0,
		Leverage:        5,
	}

	actionRecord := &logger.DecisionAction{
		Action:   decision.Action,
		Symbol:   decision.Symbol,
		Price:    0,
		Quantity: 0,
	}

	// 执行限价开仓
	startTime := time.Now()
	err = at2.ExecuteLimitOpenLongForTest(decision, actionRecord)
	elapsed := time.Since(startTime)

	if err != nil {
		log.Fatalf("❌ 执行失败: %v", err)
	}

	// 输出执行报告
	if actionRecord.ExecutionReport == nil {
		log.Fatal("❌ 未生成执行报告")
	}

	report := actionRecord.ExecutionReport.(*trader.LimitOrderExecutionReport)
	fmt.Printf("📋 生命周期执行报告:\n")
	fmt.Printf("   status: %s\n", report.Status)
	fmt.Printf("   attempts: %d\n", report.AttemptIndex)
	fmt.Printf("   filled_quantity: %.6f\n", report.FilledQuantity)
	fmt.Printf("   avg_fill_price: %.2f\n", report.AvgFillPrice)
	fmt.Printf("   duration_ms: %d\n", report.DurationMs)
	fmt.Printf("   实际耗时: %.2fs\n", elapsed.Seconds())

	if report.Status != "RETRIES_EXHAUSTED" && report.Status != "TIMEOUT" {
		log.Fatalf("❌ 期望状态 RETRIES_EXHAUSTED 或 TIMEOUT，实际 %s", report.Status)
	}
}

func runScenarioPartial() {
	fmt.Println("🎯 场景: 部分成交 (partial)")

	// 设置 ExecutionGate 配置
	market.SetExecutionGateConfig(struct {
		MaxSpreadBpsLimitOnly             float64
		MaxSpreadBpsNoTrade               float64
		MaxDepthRatioAbs                  float64
		MinDepthRatioAbs                  float64
		MaxSpreadBpsLimitPreferred        float64
		MinBestNotionalUsdtLimitOnly      float64
		MinBestNotionalUsdtLimitPreferred float64
		MinDepthNotional10LimitOnly       float64
		MinDepthNotional10LimitPreferred  float64
		NotionalMultiplierLimitOnly       float64
		NotionalMultiplierNoTrade         float64
		DefaultModeOnMissing              string
	}{
		MaxSpreadBpsLimitOnly:             25.0,
		MaxSpreadBpsNoTrade:               40.0,
		MaxDepthRatioAbs:                  3.0,
		MinDepthRatioAbs:                  0.33,
		MaxSpreadBpsLimitPreferred:        15.0,
		MinBestNotionalUsdtLimitOnly:      10000.0,
		MinBestNotionalUsdtLimitPreferred: 50000.0,
		MinDepthNotional10LimitOnly:       200000.0,
		MinDepthNotional10LimitPreferred:  500000.0,
		NotionalMultiplierLimitOnly:       8.0,
		NotionalMultiplierNoTrade:         15.0,
		DefaultModeOnMissing:              "limit_only",
	})

	// 注入市场数据
	marketData := createLimitOnlyMarketData()
	market.SetMarketDataProvider(&MockMarketDataProvider{data: marketData})
	defer market.ResetMarketDataProvider()

	// 注入过滤器提供者
	mockFilters := NewMockSymbolFiltersProvider()
	mockFilters.SetFilters("BTCUSDT", 0.1, 0.001, 10.0)
	market.SetSymbolFiltersProvider(mockFilters)
	defer market.ResetSymbolFiltersProvider()

	// 输出当前 microstructure
	micro := marketData.Microstructure
	fmt.Printf("📊 当前 microstructure: spread_bps=%.2f, depth_ratio=%.2f, min_notional=%.0f\n",
		micro.SpreadBps, micro.DepthRatio, micro.MinNotional)

	// 检查 ExecutionGate
	partialMarketData, err := market.Get("BTCUSDT")
	if err != nil {
		log.Fatalf("获取市场数据失败: %v", err)
	}
	gate := partialMarketData.Execution
	if gate == nil {
		fmt.Printf("🎛️  ExecutionGate: mode=market_ok, reason=no_execution_gate\n")
	} else {
		fmt.Printf("🎛️  ExecutionGate: mode=%s, reason=%s\n", gate.Mode, gate.Reason)
	}

	// 创建确定性行为的 PaperTrader（部分成交 + 取消）
	paperTrader := trader.NewPaperTrader()
	paperTrader.SetDeterministicBehavior(&trader.DeterministicBehavior{
		Enabled:             true,
		FillDelayMs:         10,
		NeverFill:           false,
		PartialFillRatio:    0.4, // 40%部分成交
		FixedFillPrice:      50000.0,
		CancelOnPartialFill: true, // 部分成交后取消
	})

	// 创建全局配置
	globalConfig := &config.Config{}

	// 创建 AutoTrader
	at3, err := trader.NewAutoTrader(trader.AutoTraderConfig{
		ID:                       "debug-force-trade-partial",
		Name:                     "Debug Force Trade Partial",
		Exchange:                 "binance",
		TraderMode:               "paper",
		InitialBalance:           100000.0, // 设置初始余额
		LimitOrderWaitSeconds:    5,
		LimitOrderMaxRetries:     3,
		LimitOrderPollIntervalMs: 100,
		CancelOnPartialFill:      true, // 匹配 PaperTrader 设置
		PostOnlyWhenLimitOnly:    true,
	}, globalConfig)
	if err != nil {
		log.Fatalf("❌ 创建 AutoTrader 失败: %v", err)
	}

	// 构造决策
	decision := &decision.Decision{
		Symbol:          "BTCUSDT",
		Action:          "limit_open_long",
		PositionSizeUSD: 1000.0,
		Leverage:        5,
	}

	actionRecord := &logger.DecisionAction{
		Action:   decision.Action,
		Symbol:   decision.Symbol,
		Price:    0,
		Quantity: 0,
	}

	// 执行限价开仓
	startTime := time.Now()
	err = at3.ExecuteLimitOpenLongForTest(decision, actionRecord)
	elapsed := time.Since(startTime)

	if err != nil {
		log.Fatalf("❌ 执行失败: %v", err)
	}

	// 输出执行报告
	if actionRecord.ExecutionReport == nil {
		log.Fatal("❌ 未生成执行报告")
	}

	report := actionRecord.ExecutionReport.(*trader.LimitOrderExecutionReport)
	fmt.Printf("📋 生命周期执行报告:\n")
	fmt.Printf("   status: %s\n", report.Status)
	fmt.Printf("   attempts: %d\n", report.AttemptIndex)
	fmt.Printf("   filled_quantity: %.6f\n", report.FilledQuantity)
	fmt.Printf("   avg_fill_price: %.2f\n", report.AvgFillPrice)
	fmt.Printf("   duration_ms: %d\n", report.DurationMs)
	fmt.Printf("   实际耗时: %.2fs\n", elapsed.Seconds())

	if report.Status != "PARTIALLY_FILLED" {
		log.Fatalf("❌ 期望状态 PARTIALLY_FILLED，实际 %s", report.Status)
	}

	if report.FilledQuantity != 0.04 { // 0.4 * 0.1 = 0.04
		log.Fatalf("❌ 期望成交数量 0.04，实际 %.6f", report.FilledQuantity)
	}
}

func main() {
	var scenario = flag.String("scenario", "", "场景名称: filled, timeout, partial")
	flag.Parse()

	if *scenario == "" {
		fmt.Println("❌ 必须指定 --scenario 参数")
		fmt.Println("📖 用法: go run ./cmd/debug_force_trade --scenario=filled|timeout|partial")
		fmt.Println("📋 场景说明:")
		fmt.Println("   filled: 快速成交")
		fmt.Println("   timeout: 永不成交，触发重试耗尽")
		fmt.Println("   partial: 部分成交后取消")
		os.Exit(1)
	}

	switch *scenario {
	case "filled":
		runScenarioFilled()
	case "timeout":
		runScenarioTimeout()
	case "partial":
		runScenarioPartial()
	default:
		fmt.Printf("❌ 未知场景: %s\n", *scenario)
		fmt.Println("📖 支持的场景: filled, timeout, partial")
		os.Exit(1)
	}

	fmt.Println("✅ 场景执行完成")
}