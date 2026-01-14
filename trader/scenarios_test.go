package trader

import (
	"math"
	"testing"
	"time"

	"nofx/decision"
	"nofx/logger"
	"nofx/market"
)

// MockSymbolFiltersProvider 模拟的过滤器提供者（测试用）
type MockSymbolFiltersProvider struct {
	filters map[string]*market.SymbolFilters
}

// MockMarketDataProvider 模拟的市场数据提供者（测试用）
type MockMarketDataProvider struct {
	data *market.Data
}

func (p *MockMarketDataProvider) Get(symbol string) (*market.Data, error) {
	return p.data, nil
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

// TestScenario_LimitOnly_Filled 测试限价单快速成交场景
func TestScenario_LimitOnly_Filled(t *testing.T) {
	// 设置 ExecutionGate 配置（触发 limit_only）
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
		MinBestNotionalUsdtLimitOnly:      10000.0, // 设置较高阈值
		MinBestNotionalUsdtLimitPreferred: 50000.0,
		NotionalMultiplierLimitOnly:       8.0,
		NotionalMultiplierNoTrade:         15.0,
		DefaultModeOnMissing:              "limit_only",
	})

	// 注入模拟的市场数据提供者
	mockProvider := NewMockSymbolFiltersProvider()
	mockProvider.SetFilters("BTCUSDT", 0.1, 0.001, 10.0)
	market.SetSymbolFiltersProvider(mockProvider)
	defer market.ResetSymbolFiltersProvider()

	// 构造市场数据（低名义价值，触发 limit_only）
	micro := &market.MicrostructureSummary{
		BestBidPrice:  50000.0,
		BestAskPrice:  50005.0,
		SpreadBps:     10.0,
		MinNotional:   5000.0, // < 10000，触发 limit_only
		DepthRatio:    1.0,
	}

	// 构造固定的市场数据
	marketData := &market.Data{
		Symbol:         "BTCUSDT",
		CurrentPrice:   50000.0,
		Microstructure: micro,
	}

	// 注入市场数据
	market.SetMarketDataProvider(&MockMarketDataProvider{data: marketData})
	defer market.ResetMarketDataProvider()

	// 创建确定性行为的 PaperTrader
	paperTrader := NewPaperTrader()
	paperTrader.SetDeterministicBehavior(&DeterministicBehavior{
		Enabled:        true,
		FillDelayMs:    10,  // 10ms 快速成交
		NeverFill:      false,
		FixedFillPrice: 50000.0, // 固定成交价格
	})

	// 创建 AutoTrader
	config := AutoTraderConfig{
		ID:                    "scenario-test-filled",
		Name:                  "Scenario Test Filled",
		Exchange:              "binance",
		TraderMode:            "paper",
		LimitOrderWaitSeconds: 5,
		LimitOrderMaxRetries:  3,
		LimitOrderPollIntervalMs: 100,
		CancelOnPartialFill:   false,
		PostOnlyWhenLimitOnly: true,
	}

	at := &AutoTrader{
		config:                config,
		trader:                paperTrader,
		pendingOrders:         make(map[string]*PendingOrder),
		positionFirstSeenTime: make(map[string]int64),
		positionTargets:       make(map[string]*PositionTarget),
		positionMemory:        make(map[string]decision.PositionInfo),
		autoCloseEvents:       make([]logger.DecisionAction, 0),
		dailyPairTrades:       make(map[string]int),
		dailyTradesResetDay:   "",
		lastCoTTrace:          "",
	}

	// 构造决策
	decision := &decision.Decision{
		Symbol:          "BTCUSDT",
		Action:          "limit_open_long",
		PositionSizeUSD: 1000.0,
		Leverage:        5,
	}

	// 创建决策记录
	actionRecord := &logger.DecisionAction{
		Action:   decision.Action,
		Symbol:   decision.Symbol,
		Price:    0,
		Quantity: 0,
	}

	t.Logf("🧪 测试场景: 限价单快速成交")

	// 执行限价开仓
	err := at.executeLimitOpenLongWithRecord(decision, actionRecord)
	if err != nil {
		t.Fatalf("执行限价开仓失败: %v", err)
	}

	// 等待执行完成（限价生命周期管理）
	time.Sleep(200 * time.Millisecond)

	// 验证结果
	if actionRecord.ExecutionReport == nil {
		t.Fatal("期望有执行报告，但 ExecutionReport 为空")
	}

	report := actionRecord.ExecutionReport.(*LimitOrderExecutionReport)

	t.Logf("📊 执行报告: order_id=%d, status=%s, filled=%.6f, attempts=%d, limit_price=%.2f, avg_fill=%.2f, duration=%dms",
		report.OrderID, report.Status, report.FilledQuantity, report.AttemptIndex,
		report.LimitPrice, report.AvgFillPrice, report.DurationMs)

	// 断言关键结果
	if report.Status != "FILLED" {
		t.Errorf("期望状态 FILLED，实际 %s", report.Status)
	}

	if report.FilledQuantity <= 0 {
		t.Errorf("期望成交数量 > 0，实际 %.6f", report.FilledQuantity)
	}

	if report.AttemptIndex != 1 {
		t.Errorf("期望尝试次数 1，实际 %d", report.AttemptIndex)
	}

	if report.LimitPrice <= 0 {
		t.Errorf("期望限价 > 0，实际 %.4f", report.LimitPrice)
	}

	if report.AvgFillPrice != 50000.0 {
		t.Errorf("期望平均成交价 50000.0，实际 %.4f", report.AvgFillPrice)
	}

	t.Logf("✅ 场景1验证通过: FILLED, 成交量=%.6f, 限价=%.2f, 均价=%.2f, 尝试次数=%d",
		report.FilledQuantity, report.LimitPrice, report.AvgFillPrice, report.AttemptIndex)
}

// TestScenario_LimitOnly_Timeout_RetriesExhausted 测试重试耗尽场景
func TestScenario_LimitOnly_Timeout_RetriesExhausted(t *testing.T) {
	// 设置 ExecutionGate 配置（触发 limit_only）
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

	// 注入模拟的市场数据提供者
	mockProvider := NewMockSymbolFiltersProvider()
	mockProvider.SetFilters("BTCUSDT", 0.1, 0.001, 10.0)
	market.SetSymbolFiltersProvider(mockProvider)
	defer market.ResetSymbolFiltersProvider()

	// 构造市场数据（触发 limit_only）
	micro := &market.MicrostructureSummary{
		BestBidPrice:  50000.0,
		BestAskPrice:  50005.0,
		SpreadBps:     10.0,
		MinNotional:   5000.0,
		DepthRatio:    1.0,
	}

	marketData := &market.Data{
		Symbol:         "BTCUSDT",
		CurrentPrice:   50000.0,
		Microstructure: micro,
	}

	// 注入市场数据
	market.SetMarketDataProvider(&MockMarketDataProvider{data: marketData})
	defer market.ResetMarketDataProvider()

	// 创建确定性行为的 PaperTrader（永不成交）
	paperTrader := NewPaperTrader()
	paperTrader.SetDeterministicBehavior(&DeterministicBehavior{
		Enabled:   true,
		NeverFill: true, // 永不成交，触发超时重试
	})

	// 创建 AutoTrader（短超时，少重试次数，便于测试）
	config := AutoTraderConfig{
		ID:                    "scenario-test-timeout",
		Name:                  "Scenario Test Timeout",
		Exchange:              "binance",
		TraderMode:            "paper",
		LimitOrderWaitSeconds: 1,  // 1秒超时
		LimitOrderMaxRetries:  2,  // 最多2次重试
		LimitOrderPollIntervalMs: 200, // 200ms轮询
		CancelOnPartialFill:   false,
		PostOnlyWhenLimitOnly: true,
	}

	at := &AutoTrader{
		config:                config,
		trader:                paperTrader,
		pendingOrders:         make(map[string]*PendingOrder),
		positionFirstSeenTime: make(map[string]int64),
		positionTargets:       make(map[string]*PositionTarget),
		positionMemory:        make(map[string]decision.PositionInfo),
		autoCloseEvents:       make([]logger.DecisionAction, 0),
		dailyPairTrades:       make(map[string]int),
		dailyTradesResetDay:   "",
		lastCoTTrace:          "",
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

	t.Logf("🧪 测试场景: 重试耗尽")

	// 记录开始时间
	startTime := time.Now()

	// 执行限价开仓
	err := at.executeLimitOpenLongWithRecord(decision, actionRecord)
	if err != nil {
		t.Fatalf("执行限价开仓失败: %v", err)
	}

	// 等待执行完成（预期会超时多次）
	time.Sleep(5 * time.Second) // 等待足够时间让重试耗尽

	elapsed := time.Since(startTime)
	t.Logf("⏱️ 执行耗时: %.2f秒", elapsed.Seconds())

	// 验证结果
	if actionRecord.ExecutionReport == nil {
		t.Fatal("期望有执行报告，但 ExecutionReport 为空")
	}

	report := actionRecord.ExecutionReport.(*LimitOrderExecutionReport)

	t.Logf("📊 执行报告: order_id=%d, status=%s, filled=%.6f, attempts=%d, duration=%dms",
		report.OrderID, report.Status, report.FilledQuantity, report.AttemptIndex, report.DurationMs)

	// 断言关键结果
	if report.Status != "RETRIES_EXHAUSTED" && report.Status != "TIMEOUT" {
		t.Errorf("期望状态 RETRIES_EXHAUSTED 或 TIMEOUT，实际 %s", report.Status)
	}

	if report.FilledQuantity != 0 {
		t.Errorf("期望成交数量 0，实际 %.6f", report.FilledQuantity)
	}

	if report.AttemptIndex < 3 { // 至少尝试3次（初始+2次重试）
		t.Errorf("期望尝试次数 >= 3，实际 %d", report.AttemptIndex)
	}

	t.Logf("✅ 场景2验证通过: %s, 未成交, 尝试次数=%d", report.Status, report.AttemptIndex)
}

// TestScenario_PartialFill_Behavior 测试部分成交行为
func TestScenario_PartialFill_Behavior(t *testing.T) {
	// 测试两种子场景
	testCases := []struct {
		name               string
		cancelOnPartial    bool
		expectedFinalQty   float64
		expectedStatus     string
		expectedAttempts   int
	}{
		{
			name:             "CancelOnPartialFill=false",
			cancelOnPartial:  false,
			expectedFinalQty: 0.1, // 完全成交 (实际计算出的数量)
			expectedStatus:   "FILLED",
			expectedAttempts: 1,
		},
		{
			name:             "CancelOnPartialFill=true",
			cancelOnPartial:  true,
			expectedFinalQty: 0.04, // 部分成交后取消，但已有成交部分
			expectedStatus:   "PARTIALLY_FILLED",
			expectedAttempts: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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

			// 注入模拟的市场数据提供者
			mockProvider := NewMockSymbolFiltersProvider()
			mockProvider.SetFilters("BTCUSDT", 0.1, 0.001, 10.0)
			market.SetSymbolFiltersProvider(mockProvider)
			defer market.ResetSymbolFiltersProvider()

			// 构造市场数据
			micro := &market.MicrostructureSummary{
				BestBidPrice:  50000.0,
				BestAskPrice:  50005.0,
				SpreadBps:     10.0,
				MinNotional:   5000.0,
				DepthRatio:    1.0,
			}

			marketData := &market.Data{
				Symbol:         "BTCUSDT",
				CurrentPrice:   50000.0,
				Microstructure: micro,
			}

			// 注入市场数据
			market.SetMarketDataProvider(&MockMarketDataProvider{data: marketData})
			defer market.ResetMarketDataProvider()

			// 创建确定性行为的 PaperTrader（部分成交）
			paperTrader := NewPaperTrader()
			paperTrader.SetDeterministicBehavior(&DeterministicBehavior{
				Enabled:             true,
				FillDelayMs:         10,
				NeverFill:           false,
				PartialFillRatio:    0.4, // 40%部分成交
				FixedFillPrice:      50000.0,
				CancelOnPartialFill: tc.cancelOnPartial,
			})

			// 创建 AutoTrader
			config := AutoTraderConfig{
				ID:                    "scenario-test-partial",
				Name:                  "Scenario Test Partial",
				Exchange:              "binance",
				TraderMode:            "paper",
				LimitOrderWaitSeconds: 5,
				LimitOrderMaxRetries:  3,
				LimitOrderPollIntervalMs: 100,
				CancelOnPartialFill:   tc.cancelOnPartial,
				PostOnlyWhenLimitOnly: true,
			}

			at := &AutoTrader{
				config:                config,
				trader:                paperTrader,
				pendingOrders:         make(map[string]*PendingOrder),
				positionFirstSeenTime: make(map[string]int64),
				positionTargets:       make(map[string]*PositionTarget),
				positionMemory:        make(map[string]decision.PositionInfo),
				autoCloseEvents:       make([]logger.DecisionAction, 0),
				dailyPairTrades:       make(map[string]int),
				dailyTradesResetDay:   "",
				lastCoTTrace:          "",
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

			t.Logf("🧪 测试场景: %s", tc.name)

			// 执行限价开仓
			err := at.executeLimitOpenLongWithRecord(decision, actionRecord)
			if err != nil {
				t.Fatalf("执行限价开仓失败: %v", err)
			}

			// 等待执行完成
			time.Sleep(500 * time.Millisecond)

			// 验证结果
			if actionRecord.ExecutionReport == nil {
				t.Fatal("期望有执行报告，但 ExecutionReport 为空")
			}

			report := actionRecord.ExecutionReport.(*LimitOrderExecutionReport)

			t.Logf("📊 执行报告: order_id=%d, status=%s, filled=%.6f, attempts=%d, duration=%dms",
				report.OrderID, report.Status, report.FilledQuantity, report.AttemptIndex, report.DurationMs)

			// 断言关键结果
			if report.Status != tc.expectedStatus {
				t.Errorf("期望状态 %s，实际 %s", tc.expectedStatus, report.Status)
			}

			if math.Abs(report.FilledQuantity-tc.expectedFinalQty) > 0.000001 {
				t.Errorf("期望成交数量 %.6f，实际 %.6f", tc.expectedFinalQty, report.FilledQuantity)
			}

			if report.AttemptIndex != tc.expectedAttempts {
				t.Errorf("期望尝试次数 %d，实际 %d", tc.expectedAttempts, report.AttemptIndex)
			}

			// 检查是否有部分成交标记（通过 FilledQuantity < Quantity 判断）
			if report.FilledQuantity < report.Quantity {
				t.Logf("✅ 检测到部分成交: %.6f/%.6f", report.FilledQuantity, report.Quantity)
			}

			t.Logf("✅ 子场景验证通过: %s, 成交量=%.6f, 尝试次数=%d",
				report.Status, report.FilledQuantity, report.AttemptIndex)
		})
	}
}
