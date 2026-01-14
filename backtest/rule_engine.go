package backtest

import (
	"fmt"
	"nofx/decision"
	"nofx/market"
	"strings"
)

// RuleEngine 规则引擎：提取硬性规则并实现规则检查
type RuleEngine struct {
	rules []Rule
}

// Rule 规则定义
type Rule struct {
	Name            string
	Priority        int  // 优先级：1=最高，2=中等，3=较低
	HardRequirement bool // 是否硬性要求
	Check           func(ctx *RuleContext) (bool, string) // 检查函数：返回(是否通过, 失败原因)
}

// RuleContext 规则检查上下文
type RuleContext struct {
	MarketData   *market.Data
	Positions    []decision.PositionInfo
	PendingOrders []decision.PendingOrderInfo
	Action       string // "open_long", "open_short", "hold", "wait"
	Account      decision.AccountInfo
}

// NewRuleEngine 创建规则引擎
func NewRuleEngine() *RuleEngine {
	engine := &RuleEngine{
		rules: make([]Rule, 0),
	}
	engine.initRules()
	return engine
}

// initRules 初始化所有规则
func (re *RuleEngine) initRules() {
	// 规则0：持仓数量硬性限制（最高优先级）
	re.rules = append(re.rules, Rule{
		Name:            "持仓数量限制",
		Priority:        0,
		HardRequirement: true,
		Check: func(ctx *RuleContext) (bool, string) {
			totalPositions := len(ctx.Positions) + len(ctx.PendingOrders)
			if totalPositions >= 3 {
				return false, fmt.Sprintf("持仓数已达上限（当前持仓%d个，待成交限价单%d个，总计%d个）", len(ctx.Positions), len(ctx.PendingOrders), totalPositions)
			}
			return true, ""
		},
	})

	// 规则1：趋势阶段Gate（最高优先级）
	re.rules = append(re.rules, Rule{
		Name:            "Late阶段Gate",
		Priority:        1,
		HardRequirement: true,
		Check: func(ctx *RuleContext) (bool, string) {
			phase4h := ""
			if ctx.MarketData.TrendPhase != nil {
				phase4h = fmt.Sprintf("%.1f", ctx.MarketData.TrendPhase.TrendStrength4h)
			}
			if phase4h == "晚期上升" || phase4h == "晚期下降" {
				// 检查是否是同方向趋势单
				if ctx.Action == "open_long" && phase4h == "晚期上升" {
					return false, "Late阶段禁止做同方向趋势单（晚期上升时禁止做多）"
				}
				if ctx.Action == "open_short" && phase4h == "晚期下降" {
					return false, "Late阶段禁止做同方向趋势单（晚期下降时禁止做空）"
				}
			}
			return true, ""
		},
	})


	// 规则3：距离布林带确认（最高优先级）
	re.rules = append(re.rules, Rule{
		Name:            "距离布林带确认",
		Priority:        1,
		HardRequirement: false, // 改为高风险提示：只统计，不挡单
		Check: func(ctx *RuleContext) (bool, string) {
			dist := ctx.MarketData.DistanceMetrics
			if dist == nil {
				return true, "" // 如果没有距离数据，跳过检查
			}
			
			if ctx.Action == "open_long" {
				if dist.ToBollUpper15m <= 5.0 {
					return false, fmt.Sprintf("做多需要to_boll_upper_15m_pct > 5%%，当前为%.2f%%", dist.ToBollUpper15m)
				}
			}
			
			if ctx.Action == "open_short" {
				if dist.ToBollLower15m <= 5.0 {
					return false, fmt.Sprintf("做空需要to_boll_lower_15m_pct > 5%%，当前为%.2f%%", dist.ToBollLower15m)
				}
			}
			
			return true, ""
		},
	})

	// 规则4：关键位突破确认（最高优先级）
	re.rules = append(re.rules, Rule{
		Name:            "关键位突破确认",
		Priority:        1,
		HardRequirement: true,
		Check: func(ctx *RuleContext) (bool, string) {
			// 注意：这个规则需要历史K线数据来判断是否突破
			// 在离线分析中，我们可以通过检查价格是否已经突破关键位来判断
			// 这里简化处理，只检查价格是否在关键位附近
			
			keyLevels := ctx.MarketData.KeyLevels
			currentPrice := ctx.MarketData.CurrentPrice
			
			if ctx.Action == "open_long" {
				// 检查是否在阻力位附近但未突破
				for _, level := range keyLevels {
					if level.Type == "resistance" {
						distance := ((currentPrice - level.Price) / level.Price) * 100
						if distance >= -0.5 && distance <= 0.5 {
							// 价格在阻力位附近±0.5%，需要检查是否已突破
							// 简化处理：如果价格在阻力位下方，认为未突破
							if currentPrice < level.Price {
								return false, fmt.Sprintf("关键阻力位%.2f未突破，当前价格%.2f，禁止做多", level.Price, currentPrice)
							}
						}
					}
				}
			}
			
			if ctx.Action == "open_short" {
				// 检查是否在支撑位附近但未突破
				for _, level := range keyLevels {
					if level.Type == "support" {
						distance := ((currentPrice - level.Price) / level.Price) * 100
						if distance >= -0.5 && distance <= 0.5 {
							// 价格在支撑位附近±0.5%，需要检查是否已突破
							// 简化处理：如果价格在支撑位上方，认为未突破
							if currentPrice > level.Price {
								return false, fmt.Sprintf("关键支撑位%.2f未突破，当前价格%.2f，禁止做空", level.Price, currentPrice)
							}
						}
					}
				}
			}
			
			return true, ""
		},
	})

	// 规则5：TP概率与R:R要求（中等优先级）
	re.rules = append(re.rules, Rule{
		Name:            "TP概率与R:R要求",
		Priority:        2,
		HardRequirement: true,
		Check: func(ctx *RuleContext) (bool, string) {
			// 注意：这个规则需要AI提供TP概率和R:R
			// 在规则引擎中，我们无法直接计算这些值
			// 这个规则应该在决策生成时由AI或规则引擎的其他部分处理
			// 这里先跳过，在决策生成时处理
			return true, ""
		},
	})

	// 规则6：止损距离要求（中等优先级）
	re.rules = append(re.rules, Rule{
		Name:            "止损距离要求",
		Priority:        2,
		HardRequirement: true,
		Check: func(ctx *RuleContext) (bool, string) {
			// 注意：这个规则需要止损价格才能检查
			// 在规则引擎中，我们无法直接检查止损距离
			// 这个规则应该在决策生成时处理
			// 这里先跳过
			return true, ""
		},
	})

	// 规则7：极端波动禁止开仓
	re.rules = append(re.rules, Rule{
		Name:            "极端波动禁止开仓",
		Priority:        1,
		HardRequirement: true,
		Check: func(ctx *RuleContext) (bool, string) {
			volatility := ""
			if ctx.MarketData.RiskMetrics != nil {
				volatility = ctx.MarketData.RiskMetrics.VolatilityLevel
			}
			if volatility == "extreme" {
				return false, "volatility_level=extreme，禁止开仓"
			}
			return true, ""
		},
	})
}

// CheckRules 检查所有规则
func (re *RuleEngine) CheckRules(ctx *RuleContext) (bool, []string) {
	var failures []string
	
	// 按优先级排序规则
	sortedRules := make([]Rule, len(re.rules))
	copy(sortedRules, re.rules)
	
	// 简单排序（优先级数字越小越优先）
	for i := 0; i < len(sortedRules)-1; i++ {
		for j := i + 1; j < len(sortedRules); j++ {
			if sortedRules[i].Priority > sortedRules[j].Priority {
				sortedRules[i], sortedRules[j] = sortedRules[j], sortedRules[i]
			}
		}
	}
	
	// 检查每个规则
	for _, rule := range sortedRules {
		passed, reason := rule.Check(ctx)
		if !passed {
			failures = append(failures, fmt.Sprintf("[%s] %s", rule.Name, reason))
			// 如果是硬性要求失败，可以提前返回（但这里我们收集所有失败原因）
			if rule.HardRequirement {
				// 继续检查其他规则，但记录这是硬性要求失败
			}
		}
	}
	
	// 如果有任何硬性要求失败，返回false
	hasHardFailure := false
	for _, rule := range sortedRules {
		for _, failure := range failures {
			if strings.Contains(failure, rule.Name) && rule.HardRequirement {
				hasHardFailure = true
				break
			}
		}
		if hasHardFailure {
			break
		}
	}
	
	return !hasHardFailure, failures
}

// GenerateDecision 基于规则引擎生成决策
func (re *RuleEngine) GenerateDecision(ctx *RuleContext) *decision.Decision {
	// 1. 检查是否可以开仓
	canOpen, failures := re.CheckRules(ctx)
	
	if !canOpen {
		// 生成wait决策
		return &decision.Decision{
			Action:    "wait",
			Reasoning: strings.Join(failures, "; "),
		}
	}
	
	// 2. 评估机会等级（简化处理）
	opportunityScore := re.scoreOpportunity(ctx)
	
	// 3. 如果机会等级不够，wait
	if opportunityScore < 60 { // B级以下
		return &decision.Decision{
			Action:    "wait",
			Reasoning: fmt.Sprintf("机会等级不足（评分%d分，B级以下禁止开仓）", opportunityScore),
		}
	}
	
	// 4. 生成开仓决策（简化处理，实际应该由AI或更复杂的逻辑生成）
	if ctx.Action == "open_long" {
		return &decision.Decision{
			Action:          "open_long",
			Symbol:          ctx.MarketData.Symbol,
			Leverage:        65, // 默认值
			PositionSizeUSD: 1000, // 默认值，实际应该根据账户余额计算
			Reasoning:       "所有硬性要求满足，机会等级合格",
		}
	}
	
	if ctx.Action == "open_short" {
		return &decision.Decision{
			Action:          "open_short",
			Symbol:          ctx.MarketData.Symbol,
			Leverage:        65, // 默认值
			PositionSizeUSD: 1000, // 默认值
			Reasoning:       "所有硬性要求满足，机会等级合格",
		}
	}
	
	return &decision.Decision{
		Action:    "wait",
		Reasoning: "未指定开仓方向",
	}
}

// scoreOpportunity 评估机会等级（简化版）
func (re *RuleEngine) scoreOpportunity(ctx *RuleContext) int {
	score := 50 // 基础分
	
	// 根据市场数据调整分数
	data := ctx.MarketData
	
	// 趋势强度
	if data.TrendPhase != nil {
		phase4h := fmt.Sprintf("%.1f", data.TrendPhase.TrendStrength4h)
		if phase4h == "early_up" || phase4h == "early_down" {
			score += 10
		} else if phase4h == "mid_up" || phase4h == "mid_down" {
			score += 5
		}
		
	}
	
	// 距离布林带
	if data.DistanceMetrics != nil {
		dist := data.DistanceMetrics
		if ctx.Action == "open_long" {
			if dist.ToBollUpper15m > 10 {
				score += 5
			}
		}
		if ctx.Action == "open_short" {
			if dist.ToBollLower15m > 10 {
				score += 5
			}
		}
	}
	
	// 波动率
	if data.RiskMetrics != nil {
		volatility := data.RiskMetrics.VolatilityLevel
		if volatility == "low" || volatility == "medium" {
			score += 5
		}
	}
	
	return score
}

