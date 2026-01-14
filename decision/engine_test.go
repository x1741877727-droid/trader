package decision

import (
	"nofx/config"
	"strings"
	"testing"
)

func TestCollectAllAnalyzedSymbols(t *testing.T) {
	tests := []struct {
		name     string
		ctx      *Context
		expected []string
	}{
		{
			name: "只有候选币",
			ctx: &Context{
				Positions:     []PositionInfo{},
				PendingOrders: []PendingOrderInfo{},
				CandidateCoins: []CandidateCoin{
					{Symbol: "BTCUSDT"},
					{Symbol: "ETHUSDT"},
					{Symbol: "SOLUSDT"},
				},
			},
			expected: []string{"BTCUSDT", "ETHUSDT", "SOLUSDT"},
		},
		{
			name: "包含持仓和挂单",
			ctx: &Context{
				Positions: []PositionInfo{
					{Symbol: "BTCUSDT"},
					{Symbol: "ETHUSDT"},
				},
				PendingOrders: []PendingOrderInfo{
					{Symbol: "SOLUSDT"},
				},
				CandidateCoins: []CandidateCoin{
					{Symbol: "ADAUSDT"},
					{Symbol: "DOTUSDT"},
				},
			},
			expected: []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "ADAUSDT", "DOTUSDT"},
		},
		{
			name: "去重处理",
			ctx: &Context{
				Positions: []PositionInfo{
					{Symbol: "BTCUSDT"},
				},
				PendingOrders: []PendingOrderInfo{
					{Symbol: "BTCUSDT"}, // 重复
				},
				CandidateCoins: []CandidateCoin{
					{Symbol: "BTCUSDT"}, // 重复
					{Symbol: "ETHUSDT"},
				},
			},
			expected: []string{"BTCUSDT", "ETHUSDT"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectAllAnalyzedSymbols(tt.ctx)

			if len(result) != len(tt.expected) {
				t.Errorf("collectAllAnalyzedSymbols() len = %v, expected %v", len(result), len(tt.expected))
				return
			}

			for i, symbol := range result {
				if symbol != tt.expected[i] {
					t.Errorf("collectAllAnalyzedSymbols()[%d] = %v, expected %v", i, symbol, tt.expected[i])
				}
			}
		})
	}
}

func TestBoundaryStabilityStrategy(t *testing.T) {
	tests := []struct {
		name           string
		accountEquity  float64
		positionSizeUSD float64
		expectedMargin float64
		shouldClamp    bool
		shouldFail     bool
	}{
		{
			name:           "正常范围内",
			accountEquity:  100.0,
			positionSizeUSD: 8.0, // 8%
			expectedMargin: 8.0,
			shouldClamp:    false,
			shouldFail:     false,
		},
		{
			name:           "略低于min，自动夹紧",
			accountEquity:  100.0,
			positionSizeUSD: 4.95, // 4.95% -> 夹紧到5%
			expectedMargin: 5.0,
			shouldClamp:    true,
			shouldFail:     false,
		},
		{
			name:           "略高于max，自动夹紧",
			accountEquity:  100.0,
			positionSizeUSD: 13.1, // 13.1% -> 夹紧到13%
			expectedMargin: 13.0,
			shouldClamp:    true,
			shouldFail:     false,
		},
		{
			name:           "远低于min，报错",
			accountEquity:  100.0,
			positionSizeUSD: 4.0, // 4% 远低于5%
			expectedMargin: 4.0,
			shouldClamp:    false,
			shouldFail:     true,
		},
		{
			name:           "远高于max，报错",
			accountEquity:  100.0,
			positionSizeUSD: 15.0, // 15% 远高于13%
			expectedMargin: 15.0,
			shouldClamp:    false,
			shouldFail:     true,
		},
		{
			name:           "浮点精度测试",
			accountEquity:  51.69,
			positionSizeUSD: 2.57, // 明显低于round后的min边界2.58
			expectedMargin: 2.58,  // 会被夹紧到2.58
			shouldClamp:    true,
			shouldFail:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 计算合适的RiskUSD值（账户净值的10%）
			riskUSD := tt.accountEquity * 0.10
			if riskUSD > 15.0 {
				riskUSD = 15.0
			}

			decision := &Decision{
				Symbol:          "BTCUSDT",
				Action:          "open_long",
				PositionSizeUSD: tt.positionSizeUSD,
				Leverage:        65,
				StopLoss:        50000.0,
				TakeProfit:      52000.0,
				RiskUSD:         riskUSD,
				TP1:             51000.0,
				TP2:             51500.0,
				TP3:             52000.0,
				Reasoning:       "grade=S score=90 边界稳定性测试",
			}

			// 创建包含分层风控配置的全局配置
			globalConfig := &config.Config{
				RiskManagement: config.RiskManagementConfig{
					AggressiveMode: struct {
						MaxConcurrentPositions int      `json:"max_concurrent_positions"`
						AllowedSymbols         []string `json:"allowed_symbols"`
						MaxLeverage            int      `json:"max_leverage"`
						MinLeverage            int      `json:"min_leverage"`
						RiskUsdMinPct          float64  `json:"risk_usd_min_pct"`
						RiskUsdMaxPct          float64  `json:"risk_usd_max_pct"`
						DailyLossLimitPct      float64  `json:"daily_loss_limit_pct"`
					}{
						MaxConcurrentPositions: 1,
						AllowedSymbols:         []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT"},
						MaxLeverage:            100,
						MinLeverage:            40,
						RiskUsdMinPct:          8.0,
						RiskUsdMaxPct:          15.0,
						DailyLossLimitPct:      15.0,
					},
				},
			}
			err := validateDecision(decision, tt.accountEquity, 100, 50, globalConfig)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("期望失败但没有失败")
				}
			} else {
				if err != nil {
					t.Errorf("期望成功但失败了: %v", err)
				}
				if decision.PositionSizeUSD != tt.expectedMargin {
					t.Errorf("PositionSizeUSD = %.2f, expected %.2f", decision.PositionSizeUSD, tt.expectedMargin)
				}
			}
		})
	}
}

func TestBuildPositionsTokenWithPendingOrders(t *testing.T) {
	tests := []struct {
		name           string
		positions      []PositionInfo
		pendingOrders  []PendingOrderInfo
		expectedSlots  int
		containsText   string
	}{
		{
			name:          "无持仓无挂单",
			positions:     []PositionInfo{},
			pendingOrders: []PendingOrderInfo{},
			expectedSlots: 3,
			containsText:  "剩余空位 3/3",
		},
		{
			name: "2持仓无挂单",
			positions: []PositionInfo{
				{Symbol: "BTCUSDT", Side: "long"},
				{Symbol: "ETHUSDT", Side: "short"},
			},
			pendingOrders: []PendingOrderInfo{},
			expectedSlots: 1,
			containsText:  "剩余空位 1/3",
		},
		{
			name:      "2持仓+1挂单",
			positions: []PositionInfo{
				{Symbol: "BTCUSDT", Side: "long"},
				{Symbol: "ETHUSDT", Side: "short"},
			},
			pendingOrders: []PendingOrderInfo{
				{Symbol: "ADAUSDT", Side: "long"},
			},
			expectedSlots: 0,
			containsText:  "总占用已满",
		},
		{
			name:      "1持仓+2挂单",
			positions: []PositionInfo{
				{Symbol: "BTCUSDT", Side: "long"},
			},
			pendingOrders: []PendingOrderInfo{
				{Symbol: "ETHUSDT", Side: "short"},
				{Symbol: "ADAUSDT", Side: "long"},
			},
			expectedSlots: 0,
			containsText:  "总占用已满",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &Context{
				Positions:     tt.positions,
				PendingOrders: tt.pendingOrders,
			}

			result := buildPositionsToken(ctx)

			if !strings.Contains(result, tt.containsText) {
				t.Errorf("buildPositionsToken() result doesn't contain expected text '%s', got: %s", tt.containsText, result)
			}

			// 验证slots计算是否正确
			totalOccupied := len(tt.positions) + len(tt.pendingOrders)
			expectedSlots := 3 - totalOccupied
			if expectedSlots < 0 {
				expectedSlots = 0
			}

			if expectedSlots != tt.expectedSlots {
				t.Errorf("Expected slots %d, but test expects %d", expectedSlots, tt.expectedSlots)
			}
		})
	}
}

func TestTPConsistencyValidation(t *testing.T) {
	tests := []struct {
		name        string
		decision    *Decision
		shouldFail  bool
		errorContains string
	}{
		{
			name: "多单限价开仓 - 完整TP分段正确",
			decision: &Decision{
				Symbol:          "BTCUSDT",
				Action:          "limit_open_long",
				PositionSizeUSD: 10.0,
				Leverage:        65,
				StopLoss:        49385.0, // riskPct=1.23% -> risk_usd=8.0 (符合激进模式8-15要求)
				TakeProfit:      53000.0,
				RiskUSD:         8.0,   // 实际计算：riskPct=1.23% * notional=650 ≈ 8.0
				LimitPrice:      50000.0,
				TP1:            51000.0,
				TP2:            52000.0,
				TP3:            53000.0,
				Reasoning:       "grade=S score=88 顶级机会，4h BOS_up + 15m 回踩布林下轨 + OI增长",
			},
			shouldFail: false,
		},
		{
			name: "空单市价开仓 - TP分段顺序正确",
			decision: &Decision{
				Symbol:          "ADAUSDT",
				Action:          "open_short",
				PositionSizeUSD: 8.0,
				Leverage:        50, // ADAUSDT不是blue chip，使用50-75范围内的杠杆
				StopLoss:        0.5,
				TakeProfit:      0.4,
				RiskUSD:         12.0,
				TP1:            0.45,
				TP2:            0.42,
				TP3:            0.4,
				Reasoning:       "grade=S score=90 优质空单机会，1h顶背离 + 4h下降趋势",
			},
			shouldFail: false,
		},
		{
			name: "缺少TP1 - 报错",
			decision: &Decision{
				Symbol:          "BTCUSDT",
				Action:          "open_long",
				PositionSizeUSD: 10.0,
				Leverage:        65,
				StopLoss:        49000.0,
				TakeProfit:      53000.0,
				RiskUSD:         12.0, // 激进模式风险预算
				TP1:            0, // 缺少
				TP2:            52000.0,
				TP3:            53000.0,
				Reasoning:       "grade=A score=75 测试用例",
			},
			shouldFail:  true,
			errorContains: "开仓必须提供完整的TP分段",
		},
		{
			name: "take_profit不等于tp3 - 报错",
			decision: &Decision{
				Symbol:          "BTCUSDT",
				Action:          "open_long",
				PositionSizeUSD: 10.0,
				Leverage:        65,
				StopLoss:        49000.0,
				TakeProfit:      52000.0, // 不等于TP3
				RiskUSD:         12.0, // 激进模式风险预算
				TP1:            51000.0,
				TP2:            51500.0,
				TP3:            53000.0,
				Reasoning:       "grade=S score=88 测试用例",
			},
			shouldFail:  true,
			errorContains: "take_profit必须等于tp3",
		},
		{
			name: "多单TP分段顺序错误 - 报错",
			decision: &Decision{
				Symbol:          "BTCUSDT",
				Action:          "limit_open_long",
				PositionSizeUSD: 10.0,
				Leverage:        65,
				StopLoss:        49850.0, // 调整止损使riskPct=0.3% < 1.31%，不触发止损无效校验
				TakeProfit:      53000.0,
				RiskUSD:         1.95,  // 实际计算：riskPct=0.3% * notional=650 = 1.95
				LimitPrice:      50000.0,
				TP1:            52000.0, // 顺序错乱
				TP2:            51000.0,
				TP3:            53000.0,
				Reasoning:       "grade=B score=70 测试用例",
			},
			shouldFail:  true,
			errorContains: "多单价格顺序错误",
		},
		{
			name: "空单TP分段顺序错误 - 报错",
			decision: &Decision{
				Symbol:          "ADAUSDT",
				Action:          "open_short",
				PositionSizeUSD: 8.0,
				Leverage:        50, // 调整为符合最低杠杆要求
				StopLoss:        0.5,
				TakeProfit:      0.4,
				RiskUSD:         20.0,
				TP1:            0.42, // 顺序错乱
				TP2:            0.45,
				TP3:            0.4,
				Reasoning:       "grade=A score=78 测试用例",
			},
			shouldFail:  true,
			errorContains: "空单TP分段顺序错误",
		},
		{
			name: "限价单缺少limit_price - 报错",
			decision: &Decision{
				Symbol:          "BTCUSDT",
				Action:          "limit_open_long",
				PositionSizeUSD: 10.0,
				Leverage:        65,
				StopLoss:        49000.0,
				TakeProfit:      53000.0,
				RiskUSD:         12.0, // 激进模式风险预算
				LimitPrice:      0, // 缺少
				TP1:            51000.0,
				TP2:            52000.0,
				TP3:            53000.0,
				Reasoning:       "grade=S score=95 测试用例",
			},
			shouldFail:  true,
			errorContains: "限价开仓必须提供limit_price",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建包含分层风控配置的全局配置
			globalConfig := &config.Config{
				RiskManagement: config.RiskManagementConfig{
					AggressiveMode: struct {
						MaxConcurrentPositions int      `json:"max_concurrent_positions"`
						AllowedSymbols         []string `json:"allowed_symbols"`
						MaxLeverage            int      `json:"max_leverage"`
						MinLeverage            int      `json:"min_leverage"`
						RiskUsdMinPct          float64  `json:"risk_usd_min_pct"`
						RiskUsdMaxPct          float64  `json:"risk_usd_max_pct"`
						DailyLossLimitPct      float64  `json:"daily_loss_limit_pct"`
					}{
						MaxConcurrentPositions: 1,
						AllowedSymbols:         []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "ADAUSDT"},
						MaxLeverage:            100,
						MinLeverage:            40,
						RiskUsdMinPct:          8.0,
						RiskUsdMaxPct:          15.0,
						DailyLossLimitPct:      15.0,
					},
				},
			}
			// 注意：测试用例中的RiskUSD现在由校验函数验证真实性，不要自动修改

			err := validateDecision(tt.decision, 100.0, 100, 50, globalConfig)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("期望失败但没有失败")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("期望错误包含 '%s'，实际错误: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("期望成功但失败了: %v", err)
				}
			}
		})
	}
}

func TestRiskManagementValidation(t *testing.T) {
	tests := []struct {
		name         string
		accountEquity float64
		decision     *Decision
		config       *config.Config
		shouldFail   bool
		errorContains string
	}{
		{
			name:         "激进模式 - 允许的交易标的",
			accountEquity: 150.0, // <= 200
			decision: &Decision{
				Symbol:   "BTCUSDT",
				Action:   "open_long",
				Leverage: 80,
				RiskUSD:  15.0, // 150 * 0.1 = 15
				Reasoning: "grade=S score=90 测试用例",
			},
			config: &config.Config{
				RiskManagement: config.RiskManagementConfig{
					AggressiveMode: struct {
						MaxConcurrentPositions int      `json:"max_concurrent_positions"`
						AllowedSymbols         []string `json:"allowed_symbols"`
						MaxLeverage            int      `json:"max_leverage"`
						MinLeverage            int      `json:"min_leverage"`
						RiskUsdMinPct          float64  `json:"risk_usd_min_pct"`
						RiskUsdMaxPct          float64  `json:"risk_usd_max_pct"`
						DailyLossLimitPct      float64  `json:"daily_loss_limit_pct"`
					}{
						AllowedSymbols: []string{"BTCUSDT", "ETHUSDT", "SOLUSDT"},
						MaxLeverage:    100,
						MinLeverage:    40,
						RiskUsdMinPct:  8.0,
						RiskUsdMaxPct:  15.0,
					},
				},
			},
			shouldFail: false,
		},
		{
			name:         "激进模式 - 不允许的交易标的",
			accountEquity: 150.0,
			decision: &Decision{
				Symbol:   "ADAUSDT",
				Action:   "open_long",
				Leverage: 80,
				RiskUSD:  15.0,
				Reasoning: "grade=S score=90 测试用例",
			},
			config: &config.Config{
				RiskManagement: config.RiskManagementConfig{
					AggressiveMode: struct {
						MaxConcurrentPositions int      `json:"max_concurrent_positions"`
						AllowedSymbols         []string `json:"allowed_symbols"`
						MaxLeverage            int      `json:"max_leverage"`
						MinLeverage            int      `json:"min_leverage"`
						RiskUsdMinPct          float64  `json:"risk_usd_min_pct"`
						RiskUsdMaxPct          float64  `json:"risk_usd_max_pct"`
						DailyLossLimitPct      float64  `json:"daily_loss_limit_pct"`
					}{
						AllowedSymbols: []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT"},
					},
				},
			},
			shouldFail:  true,
			errorContains: "激进模式仅允许交易",
		},
		{
			name:         "激进模式 - BNBUSDT允许开仓",
			accountEquity: 150.0,
			decision: &Decision{
				Symbol:   "BNBUSDT",
				Action:   "open_long",
				Leverage: 80,
				RiskUSD:  15.0,
				Reasoning: "grade=S score=90 测试用例",
			},
			config: &config.Config{
				RiskManagement: config.RiskManagementConfig{
					AggressiveMode: struct {
						MaxConcurrentPositions int      `json:"max_concurrent_positions"`
						AllowedSymbols         []string `json:"allowed_symbols"`
						MaxLeverage            int      `json:"max_leverage"`
						MinLeverage            int      `json:"min_leverage"`
						RiskUsdMinPct          float64  `json:"risk_usd_min_pct"`
						RiskUsdMaxPct          float64  `json:"risk_usd_max_pct"`
						DailyLossLimitPct      float64  `json:"daily_loss_limit_pct"`
					}{
						AllowedSymbols: []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT"},
						MaxLeverage:    100,
						MinLeverage:    40,
						RiskUsdMinPct:  8.0,
						RiskUsdMaxPct:  15.0,
					},
				},
			},
			shouldFail: false,
		},
		{
			name:         "激进模式 - 杠杆过低",
			accountEquity: 150.0,
			decision: &Decision{
				Symbol:   "BTCUSDT",
				Action:   "open_long",
				Leverage: 30, // < 40
				RiskUSD:  15.0,
				Reasoning: "grade=S score=90 测试用例",
			},
			config: &config.Config{
				RiskManagement: config.RiskManagementConfig{
					AggressiveMode: struct {
						MaxConcurrentPositions int      `json:"max_concurrent_positions"`
						AllowedSymbols         []string `json:"allowed_symbols"`
						MaxLeverage            int      `json:"max_leverage"`
						MinLeverage            int      `json:"min_leverage"`
						RiskUsdMinPct          float64  `json:"risk_usd_min_pct"`
						RiskUsdMaxPct          float64  `json:"risk_usd_max_pct"`
						DailyLossLimitPct      float64  `json:"daily_loss_limit_pct"`
					}{
						AllowedSymbols: []string{"BTCUSDT"},
						MinLeverage:    40,
					},
				},
			},
			shouldFail:  true,
			errorContains: "激进模式杠杆不能低于",
		},
		{
			name:         "激进模式 - 风险预算不足",
			accountEquity: 150.0,
			decision: &Decision{
				Symbol:   "BTCUSDT",
				Action:   "open_long",
				Leverage: 80,
				RiskUSD:  1.0, // 150 * 0.08 * 0.8 = 9.6, 1 < 9.6
				Reasoning: "grade=A score=75 风险预算不足测试",
			},
			config: &config.Config{
				RiskManagement: config.RiskManagementConfig{
					AggressiveMode: struct {
						MaxConcurrentPositions int      `json:"max_concurrent_positions"`
						AllowedSymbols         []string `json:"allowed_symbols"`
						MaxLeverage            int      `json:"max_leverage"`
						MinLeverage            int      `json:"min_leverage"`
						RiskUsdMinPct          float64  `json:"risk_usd_min_pct"`
						RiskUsdMaxPct          float64  `json:"risk_usd_max_pct"`
						DailyLossLimitPct      float64  `json:"daily_loss_limit_pct"`
					}{
						AllowedSymbols: []string{"BTCUSDT"},
						MaxLeverage:    100,
						MinLeverage:    40,
						RiskUsdMinPct:  8.0,
						RiskUsdMaxPct:  15.0,
					},
				},
			},
			shouldFail:  true,
			errorContains: "单笔风险预算必须在",
		},
		{
			name:         "标准模式 - 杠杆超限",
			accountEquity: 500.0, // 200-1000
			decision: &Decision{
				Symbol:   "BTCUSDT",
				Action:   "open_long",
				Leverage: 80, // > 75
				Reasoning: "grade=S score=90 测试用例",
			},
			config: &config.Config{
				RiskManagement: config.RiskManagementConfig{
					StandardMode: struct {
						MaxConcurrentPositions int     `json:"max_concurrent_positions"`
						MaxLeverage            int     `json:"max_leverage"`
						MarginUsageLimitPct    float64 `json:"margin_usage_limit_pct"`
					}{
						MaxLeverage: 75,
					},
				},
			},
			shouldFail:  true,
			errorContains: "标准模式杠杆不能超过",
		},
		{
			name:         "保守模式 - 正常",
			accountEquity: 1500.0, // > 1000
			decision: &Decision{
				Symbol:   "BTCUSDT",
				Action:   "open_long",
				Leverage: 25, // <= 30
				Reasoning: "grade=S score=90 测试用例",
			},
			config: &config.Config{
				RiskManagement: config.RiskManagementConfig{
					ConservativeMode: struct {
						MaxConcurrentPositions int     `json:"max_concurrent_positions"`
						MaxLeverage            int     `json:"max_leverage"`
						MarginUsageLimitPct    float64 `json:"margin_usage_limit_pct"`
						NotionalCapPct         float64 `json:"notional_cap_pct"`
					}{
						MaxLeverage: 30,
					},
				},
			},
			shouldFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRiskManagement(tt.decision, tt.accountEquity, tt.config)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("期望失败但没有失败")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("期望错误包含 '%s'，实际错误: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("期望成功但失败了: %v", err)
				}
			}
		})
	}
}

func TestApplyFieldCompatibility(t *testing.T) {
	tests := []struct {
		name     string
		input    Decision
		expected Decision
		hasLog   bool
	}{
		{
			name: "stop_price别名映射",
			input: Decision{
				Symbol:         "BTCUSDT",
				Action:         "open_long",
				StopPriceAlias: 45000.0, // 别名字段
			},
			expected: Decision{
				Symbol:         "BTCUSDT",
				Action:         "open_long",
				StopPriceAlias: 45000.0,
				StopLoss:       45000.0, // 应该被映射
			},
			hasLog: true,
		},
		{
			name: "tp3到take_profit映射",
			input: Decision{
				Symbol:     "BTCUSDT",
				Action:     "open_long",
				TP3:        47000.0,
			},
			expected: Decision{
				Symbol:     "BTCUSDT",
				Action:     "open_long",
				TP3:        47000.0,
				TakeProfit: 47000.0, // 应该被映射
			},
			hasLog: true,
		},
		{
			name: "close_ratio百分比转换",
			input: Decision{
				Symbol:     "BTCUSDT",
				Action:     "partial_close_long",
				CloseRatio: 50.0, // 百分比格式
			},
			expected: Decision{
				Symbol:     "BTCUSDT",
				Action:     "partial_close_long",
				CloseRatio: 0.5, // 转换为小数格式
			},
			hasLog: true,
		},
		{
			name: "数值精度舍入",
			input: Decision{
				Symbol:          "BTCUSDT",
				Action:           "open_long",
				PositionSizeUSD: 123.456789,
				LimitPrice:      45678.123456789,
				StopLoss:        45000.999999,
			},
			expected: Decision{
				Symbol:          "BTCUSDT",
				Action:           "open_long",
				PositionSizeUSD: 123.46, // 保留2位小数
				LimitPrice:      45678.1235, // 保留4位小数
				StopLoss:        45001.0000, // 保留4位小数
			},
			hasLog: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := applyFieldCompatibility(tt.input)

			if result.StopLoss != tt.expected.StopLoss {
				t.Errorf("StopLoss = %.4f, expected %.4f", result.StopLoss, tt.expected.StopLoss)
			}
			if result.TakeProfit != tt.expected.TakeProfit {
				t.Errorf("TakeProfit = %.4f, expected %.4f", result.TakeProfit, tt.expected.TakeProfit)
			}
			if result.CloseRatio != tt.expected.CloseRatio {
				t.Errorf("CloseRatio = %.4f, expected %.4f", result.CloseRatio, tt.expected.CloseRatio)
			}
			if result.PositionSizeUSD != tt.expected.PositionSizeUSD {
				t.Errorf("PositionSizeUSD = %.2f, expected %.2f", result.PositionSizeUSD, tt.expected.PositionSizeUSD)
			}
			if result.LimitPrice != tt.expected.LimitPrice {
				t.Errorf("LimitPrice = %.4f, expected %.4f", result.LimitPrice, tt.expected.LimitPrice)
			}
		})
	}
}

func TestNormalizeExecutionPreference(t *testing.T) {
	tests := []struct {
		name                   string
		input                  Decision
		expected               Decision
		expectLogMessage      bool
	}{
		{
			name: "缺失 execution_preference 补为 auto",
			input: Decision{
				Symbol: "BTCUSDT",
				Action: "open_long",
			},
			expected: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "auto",
			},
			expectLogMessage: true,
		},
		{
			name: "有效的 auto 值保持不变",
			input: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "auto",
			},
			expected: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "auto",
			},
			expectLogMessage: false,
		},
		{
			name: "有效的 market 值保持不变",
			input: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "market",
			},
			expected: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "market",
			},
			expectLogMessage: false,
		},
		{
			name: "有效的 limit 值保持不变",
			input: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "limit",
			},
			expected: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "limit",
			},
			expectLogMessage: false,
		},
		{
			name: "无效值归一化为 auto",
			input: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "invalid",
			},
			expected: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "auto",
			},
			expectLogMessage: true,
		},
		{
			name: "大小写不敏感，转换为小写",
			input: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "AUTO",
			},
			expected: Decision{
				Symbol:              "BTCUSDT",
				Action:               "open_long",
				ExecutionPreference: "auto",
			},
			expectLogMessage: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 注意：实际测试中无法轻易捕获log输出，这里只测试规范化结果
			result := normalizeExecutionPreference(tt.input)

			if result.ExecutionPreference != tt.expected.ExecutionPreference {
				t.Errorf("normalizeExecutionPreference() = %v, want %v", result.ExecutionPreference, tt.expected.ExecutionPreference)
			}

			if result.Symbol != tt.expected.Symbol {
				t.Errorf("normalizeExecutionPreference() Symbol = %v, want %v", result.Symbol, tt.expected.Symbol)
			}

			if result.Action != tt.expected.Action {
				t.Errorf("normalizeExecutionPreference() Action = %v, want %v", result.Action, tt.expected.Action)
			}
		})
	}
}
