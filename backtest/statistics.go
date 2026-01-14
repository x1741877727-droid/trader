package backtest

import (
	"fmt"
	"sort"
	"time"
)

// DecisionRecord 用于回测统计的一条决策记录
// 注意：这里只存我们做统计需要的最小信息，尽量保持轻量。
type DecisionRecord struct {
	Time        time.Time
	Symbol      string
	Action      string // open_long/open_short/wait
	Reasoning   string
	RuleFailures []string
	MarketPhase string // 例: "trend_strength_4h=60.0, momentum_15m=弱上涨"
}

// Statistics 统计分析结果（借鉴官方 nofx 设计，做了精简）
type Statistics struct {
	TotalDecisions int                         `json:"total_decisions"`
	WaitDecisions  int                         `json:"wait_decisions"`
	OpenDecisions  int                         `json:"open_decisions"`
	WaitReasons    map[string]int              `json:"wait_reasons"`
	RuleFailures   map[string]int              `json:"rule_failures"`
	BySymbol       map[string]*SymbolStats     `json:"by_symbol"`
}

// SymbolStats 按币种统计
type SymbolStats struct {
	Symbol         string            `json:"symbol"`
	TotalDecisions int               `json:"total_decisions"`
	WaitDecisions  int               `json:"wait_decisions"`
	OpenDecisions  int               `json:"open_decisions"`
	WaitReasons    map[string]int    `json:"wait_reasons"`
	RuleFailures   map[string]int    `json:"rule_failures"`
}

// NewStatistics 创建统计对象
func NewStatistics() *Statistics {
	return &Statistics{
		WaitReasons:  make(map[string]int),
		RuleFailures: make(map[string]int),
		BySymbol:     make(map[string]*SymbolStats),
	}
}

// AddRecord 将一条决策记录纳入统计
func (s *Statistics) AddRecord(rec DecisionRecord) {
	s.TotalDecisions++

	if rec.Action == "wait" {
		s.WaitDecisions++
		if rec.Reasoning != "" {
			s.WaitReasons[rec.Reasoning]++
		}
	} else if rec.Action == "open_long" || rec.Action == "open_short" {
		s.OpenDecisions++
	}

	// 按币种统计
	ss := s.BySymbol[rec.Symbol]
	if ss == nil {
		ss = &SymbolStats{
			Symbol:       rec.Symbol,
			WaitReasons:  make(map[string]int),
			RuleFailures: make(map[string]int),
		}
		s.BySymbol[rec.Symbol] = ss
	}
	ss.TotalDecisions++
	if rec.Action == "wait" {
		ss.WaitDecisions++
		if rec.Reasoning != "" {
			ss.WaitReasons[rec.Reasoning]++
		}
	} else if rec.Action == "open_long" || rec.Action == "open_short" {
		ss.OpenDecisions++
	}

	for _, rf := range rec.RuleFailures {
		s.RuleFailures[rf]++
		ss.RuleFailures[rf]++
	}
}

// WaitRate 获取整体 wait 比例
func (s *Statistics) WaitRate() float64 {
	if s.TotalDecisions == 0 {
		return 0
	}
	return float64(s.WaitDecisions) / float64(s.TotalDecisions) * 100
}

// OpenRate 获取整体开仓比例
func (s *Statistics) OpenRate() float64 {
	if s.TotalDecisions == 0 {
		return 0
	}
	return float64(s.OpenDecisions) / float64(s.TotalDecisions) * 100
}

// WaitReasonCount wait 原因统计
type WaitReasonCount struct {
	Reason string  `json:"reason"`
	Count  int     `json:"count"`
	Pct    float64 `json:"pct"`
}

// RuleFailureCount 规则失败统计
type RuleFailureCount struct {
	Rule string  `json:"rule"`
	Count int    `json:"count"`
	Pct   float64 `json:"pct"`
}

// TopWaitReasons 返回前 N 个最常见的 wait 原因
func (s *Statistics) TopWaitReasons(n int) []WaitReasonCount {
	type rc struct {
		k string
		v int
	}
	tmp := make([]rc, 0, len(s.WaitReasons))
	for k, v := range s.WaitReasons {
		tmp = append(tmp, rc{k: k, v: v})
	}
	sort.Slice(tmp, func(i, j int) bool { return tmp[i].v > tmp[j].v })

	res := make([]WaitReasonCount, 0, n)
	for i := 0; i < n && i < len(tmp); i++ {
		pct := 0.0
		if s.WaitDecisions > 0 {
			pct = float64(tmp[i].v) / float64(s.WaitDecisions) * 100
		}
		res = append(res, WaitReasonCount{
			Reason: tmp[i].k,
			Count:  tmp[i].v,
			Pct:    pct,
		})
	}
	return res
}

// TopRuleFailures 返回前 N 个最常触发的规则失败
func (s *Statistics) TopRuleFailures(n int) []RuleFailureCount {
	type fc struct {
		k string
		v int
	}
	tmp := make([]fc, 0, len(s.RuleFailures))
	for k, v := range s.RuleFailures {
		tmp = append(tmp, fc{k: k, v: v})
	}
	sort.Slice(tmp, func(i, j int) bool { return tmp[i].v > tmp[j].v })

	res := make([]RuleFailureCount, 0, n)
	for i := 0; i < n && i < len(tmp); i++ {
		pct := 0.0
		if s.TotalDecisions > 0 {
			pct = float64(tmp[i].v) / float64(s.TotalDecisions) * 100
		}
		res = append(res, RuleFailureCount{
			Rule:  tmp[i].k,
			Count: tmp[i].v,
			Pct:   pct,
		})
	}
	return res
}

// DebugSummary 返回一个简短的文字总结，便于日志查看
func (s *Statistics) DebugSummary() string {
	return fmt.Sprintf("total=%d, wait=%d(%.1f%%), open=%d(%.1f%%)",
		s.TotalDecisions,
		s.WaitDecisions,
		s.WaitRate(),
		s.OpenDecisions,
		s.OpenRate(),
	)
}













