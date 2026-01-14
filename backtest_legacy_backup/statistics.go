package backtest

import (
	"fmt"
	"sort"
	"time"
)

// Statistics 统计分析结果
type Statistics struct {
	TotalCycles       int
	TotalDecisions    int
	WaitDecisions     int
	OpenDecisions     int
	WaitReasons       map[string]int // wait原因统计
	RuleFailures      map[string]int // 规则失败统计
	BySymbol          map[string]*SymbolStatistics
	ByTimePeriod      map[string]*TimePeriodStatistics
	OpportunityMissed []MissedOpportunity // 错过的机会
}

// SymbolStatistics 按币种统计
type SymbolStatistics struct {
	Symbol          string
	TotalDecisions  int
	WaitDecisions   int
	OpenDecisions   int
	WaitReasons     map[string]int
	RuleFailures    map[string]int
}

// TimePeriodStatistics 按时间段统计
type TimePeriodStatistics struct {
	Period         string
	TotalDecisions int
	WaitDecisions  int
	OpenDecisions  int
}

// MissedOpportunity 错过的机会
type MissedOpportunity struct {
	Time        time.Time
	Symbol      string
	Action      string
	Price       float64
	Reason      string
	MarketState string
}

// NewStatistics 创建统计对象
func NewStatistics() *Statistics {
	return &Statistics{
		WaitReasons:  make(map[string]int),
		RuleFailures: make(map[string]int),
		BySymbol:     make(map[string]*SymbolStatistics),
		ByTimePeriod: make(map[string]*TimePeriodStatistics),
		OpportunityMissed: make([]MissedOpportunity, 0),
	}
}

// Calculate 计算统计结果
func (s *Statistics) Calculate(decisions []DecisionRecord) {
	s.TotalDecisions = len(decisions)

	// 统计wait和open决策
	for _, record := range decisions {
		s.TotalCycles++

		if record.Decision.Action == "wait" {
			s.WaitDecisions++
			// 统计wait原因
			reason := record.Decision.Reasoning
			if reason != "" {
				s.WaitReasons[reason]++
			}
			// 记录错过的机会
			if record.Decision.Action == "wait" {
				s.OpportunityMissed = append(s.OpportunityMissed, MissedOpportunity{
					Time:        record.Time,
					Symbol:      record.Symbol,
					Action:      "open",
					Price:       record.MarketData.CurrentPrice,
					Reason:      reason,
					MarketState: func() string {
						phase4h := "unknown"
						momentum15m := "unknown"
						if record.MarketData.TrendPhase != nil {
							phase4h = fmt.Sprintf("%.1f", record.MarketData.TrendPhase.TrendStrength4h)
							momentum15m = "removed" // 已删除15m动能指标
						}
						return fmt.Sprintf("trend_strength_4h=%s, momentum_15m=%s", phase4h, momentum15m)
					}(),
				})
			}
		} else if record.Decision.Action == "open_long" || record.Decision.Action == "open_short" {
			s.OpenDecisions++
		}

		// 按币种统计
		symbolStats := s.BySymbol[record.Symbol]
		if symbolStats == nil {
			symbolStats = &SymbolStatistics{
				Symbol:       record.Symbol,
				WaitReasons:  make(map[string]int),
				RuleFailures: make(map[string]int),
			}
			s.BySymbol[record.Symbol] = symbolStats
		}
		symbolStats.TotalDecisions++
		if record.Decision.Action == "wait" {
			symbolStats.WaitDecisions++
			if record.Decision.Reasoning != "" {
				symbolStats.WaitReasons[record.Decision.Reasoning]++
			}
		} else if record.Decision.Action == "open_long" || record.Decision.Action == "open_short" {
			symbolStats.OpenDecisions++
		}

		// 按时间段统计（按小时）
		period := record.Time.Format("2006-01-02 15:00")
		periodStats := s.ByTimePeriod[period]
		if periodStats == nil {
			periodStats = &TimePeriodStatistics{
				Period: period,
			}
			s.ByTimePeriod[period] = periodStats
		}
		periodStats.TotalDecisions++
		if record.Decision.Action == "wait" {
			periodStats.WaitDecisions++
		} else if record.Decision.Action == "open_long" || record.Decision.Action == "open_short" {
			periodStats.OpenDecisions++
		}

		// 统计规则失败
		for _, failure := range record.RuleFailures {
			s.RuleFailures[failure]++
			if symbolStats != nil {
				symbolStats.RuleFailures[failure]++
			}
		}
	}
}

// GetTopWaitReasons 获取前N个wait原因
func (s *Statistics) GetTopWaitReasons(n int) []WaitReasonCount {
	type reasonCount struct {
		Reason string
		Count  int
	}

	reasons := make([]reasonCount, 0, len(s.WaitReasons))
	for reason, count := range s.WaitReasons {
		reasons = append(reasons, reasonCount{Reason: reason, Count: count})
	}

	// 按count排序
	sort.Slice(reasons, func(i, j int) bool {
		return reasons[i].Count > reasons[j].Count
	})

	// 返回前N个
	result := make([]WaitReasonCount, 0, n)
	for i := 0; i < n && i < len(reasons); i++ {
		result = append(result, WaitReasonCount{
			Reason: reasons[i].Reason,
			Count:  reasons[i].Count,
			Pct:    float64(reasons[i].Count) / float64(s.WaitDecisions) * 100,
		})
	}

	return result
}

// WaitReasonCount wait原因统计
type WaitReasonCount struct {
	Reason string
	Count  int
	Pct    float64
}

// GetTopRuleFailures 获取前N个规则失败
func (s *Statistics) GetTopRuleFailures(n int) []RuleFailureCount {
	type failureCount struct {
		Rule  string
		Count int
	}

	failures := make([]failureCount, 0, len(s.RuleFailures))
	for rule, count := range s.RuleFailures {
		failures = append(failures, failureCount{Rule: rule, Count: count})
	}

	// 按count排序
	sort.Slice(failures, func(i, j int) bool {
		return failures[i].Count > failures[j].Count
	})

	// 返回前N个
	result := make([]RuleFailureCount, 0, n)
	for i := 0; i < n && i < len(failures); i++ {
		result = append(result, RuleFailureCount{
			Rule:  failures[i].Rule,
			Count: failures[i].Count,
			Pct:   float64(failures[i].Count) / float64(s.TotalDecisions) * 100,
		})
	}

	return result
}

// RuleFailureCount 规则失败统计
type RuleFailureCount struct {
	Rule  string
	Count int
	Pct   float64
}

// GetWaitRate 获取wait率
func (s *Statistics) GetWaitRate() float64 {
	if s.TotalDecisions == 0 {
		return 0
	}
	return float64(s.WaitDecisions) / float64(s.TotalDecisions) * 100
}

// GetOpenRate 获取开仓率
func (s *Statistics) GetOpenRate() float64 {
	if s.TotalDecisions == 0 {
		return 0
	}
	return float64(s.OpenDecisions) / float64(s.TotalDecisions) * 100
}

