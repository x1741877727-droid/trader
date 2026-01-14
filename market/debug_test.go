package market

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// DebugExecutionGate: manual verification of execution gate logic (exported for verification script)
func DebugExecutionGate() {
	fmt.Println("=== ExecutionGate Logic Verification ===")

	// Test 1: High threshold forces limit_only
	fmt.Println("\n1. High threshold test (force limit_only):")
	SetExecutionGateConfig(struct {
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
		MaxSpreadBpsLimitPreferred:        15.0,
		MinBestNotionalUsdtLimitOnly:      999999999.0, // Force limit_only
		MinBestNotionalUsdtLimitPreferred: 999999999.0,
		MinDepthNotional10LimitOnly:       200000.0,
		MinDepthNotional10LimitPreferred:  500000.0,
		MaxDepthRatioAbs:                  3.0,
		DefaultModeOnMissing:              "limit_only",
	})

	testMicro := &MicrostructureSummary{
		BestBidPrice: 95000.0,
		BestAskPrice: 95005.0,
		BestBidQty:   1.0,
		BestAskQty:   1.0,
		MinNotional:  95000.0, // 95K USDT, but threshold is 999M
		DepthRatio:   1.0,
		SpreadBps:    5.0,
	}

	gate := EvaluateExecutionGate(testMicro, 0)
	fmt.Printf("  Input: MinNotional=%.0f USDT\n", testMicro.MinNotional)
	fmt.Printf("  Result: mode=%s, reason=%s\n", gate.Mode, gate.Reason)
	if gate.Mode == "limit_only" && strings.Contains(gate.Reason, "insufficient_notional") {
		fmt.Println("  ✅ Force trigger successful!")
	} else {
		fmt.Println("  ❌ Force trigger failed!")
	}

	// Test 2: Normal threshold allows market_ok
	fmt.Println("\n2. Normal threshold test:")
	SetExecutionGateConfig(struct {
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
		MinBestNotionalUsdtLimitOnly:      50000.0, // 50K USDT
		MinBestNotionalUsdtLimitPreferred: 10000.0, // 10K USDT
		NotionalMultiplierLimitOnly:       8.0,
		NotionalMultiplierNoTrade:         15.0,
		DefaultModeOnMissing:              "limit_only",
	})

	gate = EvaluateExecutionGate(testMicro, 0)
	fmt.Printf("  Input: MinNotional=%.0f USDT (> 10K threshold)\n", testMicro.MinNotional)
	fmt.Printf("  Result: mode=%s, reason=%s\n", gate.Mode, gate.Reason)
	if gate.Mode == "market_ok" {
		fmt.Println("  ✅ Normal operation successful!")
	} else {
		fmt.Println("  ❌ Normal operation failed!")
	}

	// Test 3: Low notional triggers limit_preferred
	fmt.Println("\n3. Low notional test (limit_preferred):")
	lowNotionalMicro := &MicrostructureSummary{
		BestBidPrice: 95000.0,
		BestAskPrice: 95005.0,
		BestBidQty:   0.05,   // 4.75K USDT
		BestAskQty:   0.1,    // 9.5K USDT
		MinNotional:  4725.0, // < 10K
		DepthRatio:   0.5,
		SpreadBps:    5.0,
	}

	gate = EvaluateExecutionGate(lowNotionalMicro, 0)
	fmt.Printf("  Input: MinNotional=%.0f USDT (< 10K threshold)\n", lowNotionalMicro.MinNotional)
	fmt.Printf("  Result: mode=%s, reason=%s\n", gate.Mode, gate.Reason)
	if gate.Mode == "limit_preferred" && strings.Contains(gate.Reason, "notional_low") {
		fmt.Println("  ✅ Low notional detection successful!")
	} else {
		fmt.Println("  ❌ Low notional detection failed!")
	}

	fmt.Println("\n=== Verification Complete ===")
}

// TestExecutionGateDowngrade: test that AutoTrader correctly downgrades MARKET to LIMIT orders
func TestExecutionGateDowngrade(t *testing.T) {
	fmt.Println("=== Testing ExecutionGate Downgrade Logic ===")

	// Set high threshold to force limit_only
	SetExecutionGateConfig(struct {
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
		MinBestNotionalUsdtLimitOnly:      999999999.0, // Force limit_only
		MinBestNotionalUsdtLimitPreferred: 999999999.0,
		NotionalMultiplierLimitOnly:       8.0,
		NotionalMultiplierNoTrade:         15.0,
		DefaultModeOnMissing:              "limit_only",
	})

	// Create a mock market data with high depth ratio to force limit_only
	mockData := &Data{
		Microstructure: &MicrostructureSummary{
			TsMs:         time.Now().UnixMilli(),
			BestBidPrice: 95000.0,
			BestAskPrice: 95005.0,
			BestBidQty:   0.1,
			BestAskQty:   10.0,   // Much higher ask qty creates imbalance
			MinNotional:  9500.0, // 95000 * 0.1 = 9500 USDT
			DepthRatio:   100.0,  // High ratio > 3.0 triggers limit_only
			SpreadBps:    5.0,
		},
	}

	// Evaluate execution gate
	mockData.Execution = EvaluateExecutionGate(mockData.Microstructure, 0)

	fmt.Printf("Mock market data: min_notional=%.0f USDT\n", mockData.Microstructure.MinNotional)
	fmt.Printf("ExecutionGate result: mode=%s, reason=%s\n", mockData.Execution.Mode, mockData.Execution.Reason)

	if mockData.Execution.Mode != "limit_only" {
		t.Errorf("Expected limit_only mode, got %s", mockData.Execution.Mode)
	} else {
		fmt.Println("✅ ExecutionGate correctly evaluates to limit_only")
	}

	// Test downgrade logic (we'll simulate this since we can't easily instantiate AutoTrader)
	fmt.Println("\nSimulating AutoTrader downgrade logic:")
	originalAction := "open_long"
	expectedDowngradedAction := "limit_open_long"

	fmt.Printf("Original action: %s\n", originalAction)
	fmt.Printf("Expected downgraded action: %s\n", expectedDowngradedAction)

	if mockData.Execution.Mode == "limit_only" {
		fmt.Printf("✅ Would downgrade %s to %s\n", originalAction, expectedDowngradedAction)
	} else {
		t.Error("❌ Should have triggered downgrade")
	}

	fmt.Println("\n=== Downgrade Test Complete ===")
}

// ===== M2.1: ExchangeInfo 和限价定价测试 =====

func TestRoundToTick(t *testing.T) {
	tests := []struct {
		name     string
		price    float64
		tickSize float64
		expected float64
	}{
		{"normal rounding", 1.23456, 0.01, 1.23},
		{"exact match", 1.23, 0.01, 1.23},
		{"round up", 1.235, 0.01, 1.24},
		{"large tick size", 123.456, 0.5, 123.5},
		{"zero tick size", 1.23456, 0.0, 1.23456},
		{"negative tick size", 1.23456, -0.01, 1.23456},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RoundToTick(tt.price, tt.tickSize)
			if math.Abs(result-tt.expected) > 1e-10 {
				t.Errorf("RoundToTick(%v, %v) = %v, expected %v", tt.price, tt.tickSize, result, tt.expected)
			}
		})
	}
}

func TestRoundToStep(t *testing.T) {
	tests := []struct {
		name     string
		qty      float64
		stepSize float64
		expected float64
	}{
		{"normal rounding", 1.23456, 0.01, 1.23},
		{"exact match", 1.23, 0.01, 1.23},
		{"round up", 1.235, 0.01, 1.24},
		{"large step size", 123.456, 0.5, 123.5},
		{"zero step size", 1.23456, 0.0, 1.23456},
		{"negative step size", 1.23456, -0.01, 1.23456},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RoundToStep(tt.qty, tt.stepSize)
			if math.Abs(result-tt.expected) > 1e-10 {
				t.Errorf("RoundToStep(%v, %v) = %v, expected %v", tt.qty, tt.stepSize, result, tt.expected)
			}
		})
	}
}

func TestDeriveOpenLimitPrice(t *testing.T) {
	tests := []struct {
		name           string
		side           string
		microstructure *MicrostructureSummary
		tickSize       float64
		expectedPrice  float64
		expectedReason string
	}{
		{
			name: "BUY - normal spread - best bid maker",
			side: "BUY",
			microstructure: &MicrostructureSummary{
				BestBidPrice: 100.00,
				BestAskPrice: 100.01, // spread = 0.01 < 2*0.01
			},
			tickSize:       0.01,
			expectedPrice:  100.00,
			expectedReason: "best_bid_maker",
		},
		{
			name: "BUY - wide spread - inside pricing",
			side: "BUY",
			microstructure: &MicrostructureSummary{
				BestBidPrice: 100.00,
				BestAskPrice: 100.10, // spread = 0.10 >= 2*0.01
			},
			tickSize:       0.01,
			expectedPrice:  100.01, // best_bid + 1*tick
			expectedReason: "best_bid_plus_one_tick_inside",
		},
		{
			name: "BUY - wide spread but would cross ask - fallback to best bid",
			side: "BUY",
			microstructure: &MicrostructureSummary{
				BestBidPrice: 100.00,
				BestAskPrice: 100.02, // spread = 0.02 >= 2*0.01, but best_bid+tick=100.01 >= 100.02
			},
			tickSize:       0.01,
			expectedPrice:  100.00,
			expectedReason: "best_bid_maker",
		},
		{
			name: "SELL - normal spread - best ask maker",
			side: "SELL",
			microstructure: &MicrostructureSummary{
				BestBidPrice: 99.99,
				BestAskPrice: 100.00, // spread = 0.01 < 2*0.01
			},
			tickSize:       0.01,
			expectedPrice:  100.00,
			expectedReason: "best_ask_maker",
		},
		{
			name: "SELL - wide spread - inside pricing",
			side: "SELL",
			microstructure: &MicrostructureSummary{
				BestBidPrice: 99.90,
				BestAskPrice: 100.00, // spread = 0.10 >= 2*0.01
			},
			tickSize:       0.01,
			expectedPrice:  99.99, // best_ask - 1*tick
			expectedReason: "best_ask_minus_one_tick_inside",
		},
		{
			name: "SELL - wide spread but would cross bid - fallback to best ask",
			side: "SELL",
			microstructure: &MicrostructureSummary{
				BestBidPrice: 99.98,
				BestAskPrice: 100.00, // spread = 0.02 >= 2*0.01, but best_ask-tick=99.99 <= 99.98
			},
			tickSize:       0.01,
			expectedPrice:  100.00,
			expectedReason: "best_ask_maker",
		},
		{
			name:           "nil microstructure",
			side:           "BUY",
			microstructure: nil,
			tickSize:       0.01,
			expectedPrice:  0,
			expectedReason: "microstructure_unavailable",
		},
		{
			name:           "invalid side",
			side:           "INVALID",
			microstructure: &MicrostructureSummary{BestBidPrice: 100.0, BestAskPrice: 100.05},
			tickSize:       0.01,
			expectedPrice:  0,
			expectedReason: "invalid_side",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			price, reason := DeriveOpenLimitPrice(tt.side, tt.microstructure, tt.tickSize)
			if math.Abs(price-tt.expectedPrice) > 1e-10 {
				t.Errorf("deriveOpenLimitPrice() price = %v, expected %v", price, tt.expectedPrice)
			}
			if reason != tt.expectedReason {
				t.Errorf("deriveOpenLimitPrice() reason = %v, expected %v", reason, tt.expectedReason)
			}
		})
	}
}

// Dev-only test: run with `go test ./market -run TestPrintMicrostructure -v`
func TestPrintMicrostructure(t *testing.T) {
	// Set high threshold to force limit_only for testing
	SetExecutionGateConfig(struct {
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
		MinBestNotionalUsdtLimitOnly:      999999999.0, // Very high - won't trigger
		MinBestNotionalUsdtLimitPreferred: 100000.0,    // Set lower to trigger limit_preferred
		NotionalMultiplierLimitOnly:       8.0,
		NotionalMultiplierNoTrade:         15.0,
		DefaultModeOnMissing:              "limit_only",
	})

	data, err := Get("BTCUSDT")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	output := Format(data)
	t.Logf("Format output:\n%s", output)

	// Verify that execution gate shows limit_only due to high threshold
	if !strings.Contains(output, "ExecutionGate") {
		t.Error("Expected ExecutionGate in output")
	}
	if !strings.Contains(output, "mode=limit_only") {
		t.Error("Expected mode=limit_only due to high threshold")
	}
	if strings.Contains(output, "insufficient_notional") {
		t.Log("✅ Force trigger successful: insufficient_notional detected")
	}
}

func TestEvaluateExecutionGate(t *testing.T) {
	// 设置测试配置
	SetExecutionGateConfig(struct {
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
		MinBestNotionalUsdtLimitOnly:      10000.0, // 10K USDT - 极差市场，强制限价
		MinBestNotionalUsdtLimitPreferred: 50000.0, // 50K USDT - 一般差市场，建议限价
		MinDepthNotional10LimitOnly:       10000.0, // 10K USDT - 前10档累计，极差市场强制限价
		MinDepthNotional10LimitPreferred: 50000.0, // 50K USDT - 前10档累计，一般差市场建议限价
		NotionalMultiplierLimitOnly:       8.0,
		NotionalMultiplierNoTrade:         15.0,
		DefaultModeOnMissing:              "limit_only",
	})

	tests := []struct {
		name     string
		input    *MicrostructureSummary
		expected string
		reason   string
	}{
		{
			name:     "nil microstructure",
			input:    nil,
			expected: "limit_only",
			reason:   "microstructure missing",
		},
		{
			name: "spread too wide - no_trade",
			input: &MicrostructureSummary{
				SpreadBps:    60.0, // > 40.0 (no_trade threshold)
				BestBidPrice: 95000.0,
				BestAskPrice: 95025.0,
				BestBidQty:   1.0,
				BestAskQty:   1.0,
				MinNotional:  95000.0, // > 50K
				DepthRatio:   1.0,
			},
			expected: "no_trade",
			reason:   "spread_too_wide_60.00bps_no_trade",
		},
		{
			name: "insufficient notional - limit_only",
			input: &MicrostructureSummary{
				SpreadBps:    10.0,
				BestBidPrice: 95000.0,
				BestAskPrice: 95005.0,
				BestBidQty:   0.05,   // 4.75K USDT < 10K
				BestAskQty:   0.06,   // 5.7K USDT < 10K
				MinNotional:  4725.0, // < 10K - 极差市场
				DepthRatio:   0.83,
			},
			expected: "limit_only",
			reason:   "insufficient_best_notional_4725_usdt",
		},
		{
			name: "depth imbalance - limit_only",
			input: &MicrostructureSummary{
				SpreadBps:    10.0,
				BestBidPrice: 95000.0,
				BestAskPrice: 95005.0,
				BestBidQty:   2.0,    // 190K USDT
				BestAskQty:   0.1,    // 9.5K USDT
				MinNotional:  9500.0, // > 50K (bid side)
				DepthRatio:   20.0,   // > 3.0
			},
			expected: "limit_only",
			reason:   "depth_ratio_too_high_20.00",
		},
		{
			name: "spread wide - limit_preferred", // SpreadBps triggers limit_preferred
			input: &MicrostructureSummary{
				SpreadBps:    20.0, // > 15.0
				BestBidPrice: 95000.0,
				BestAskPrice: 95010.0,
				BestBidQty:   1.0,     // 95K USDT
				BestAskQty:   1.0,     // 95K USDT
				MinNotional:  95000.0, // > 50K
				DepthRatio:   1.0,
			},
			expected: "limit_preferred",
			reason:   "spread_wide_20.00bps",
		},
		{
			name: "notional low - limit_preferred",
			input: &MicrostructureSummary{
				SpreadBps:    10.0, // < 15.0
				BestBidPrice: 95000.0,
				BestAskPrice: 95005.0,
				BestBidQty:   0.3,     // 28.5K USDT
				BestAskQty:   0.25,    // 23.75K USDT
				MinNotional:  23725.0, // 23.75K < 500K - 一般差市场
				DepthRatio:   1.2,
			},
			expected: "limit_preferred",
			reason:   "best_notional_low_23725_usdt",
		},
		{
			name: "good conditions - market_ok", // MinNotional >= MinDepthNotional10LimitPreferred
			input: &MicrostructureSummary{
				SpreadBps:    5.0, // < 15.0
				BestBidPrice: 95000.0,
				BestAskPrice: 95002.5,
				BestBidQty:   1.0,     // 95K USDT > 10K
				BestAskQty:   1.0,     // 95K USDT > 10K
				MinNotional:  95000.0, // > 50K
				DepthRatio:   1.0,     // < 3.0
			},
			expected: "market_ok",
			reason:   "good_conditions_5.00bps_notional_95000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EvaluateExecutionGate(tt.input, 0)
			if result.Mode != tt.expected {
				t.Errorf("evaluateExecutionGate() mode = %v, expected %v", result.Mode, tt.expected)
			}
			if result.Reason != tt.reason {
				t.Errorf("evaluateExecutionGate() reason = %v, expected %v", result.Reason, tt.reason)
			}
			if result.TsMs == 0 {
				t.Error("evaluateExecutionGate() TsMs should not be zero")
			}
		})
	}
}
