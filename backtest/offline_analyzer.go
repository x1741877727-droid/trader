package backtest

import (
	"fmt"
	"log"
	"nofx/decision"
	"nofx/market"
	"strings"
	"sync"
	"time"
)

// Params 回测参数（精简版）
type Params struct {
	Symbols      []string      `json:"symbols"`
	StartTime    time.Time     `json:"start_time"`
	EndTime      time.Time     `json:"end_time"`
	ScanInterval time.Duration `json:"scan_interval"` // 单周期长度，例如 3 * time.Minute
}

// Result 回测结果（用于 API 返回）
type Result struct {
	Params     Params      `json:"params"`
	Statistics *Statistics `json:"statistics"`
}

// OfflineAnalyzer 离线分析器：按时间顺序用 RuleEngine 模拟“是否允许开仓”
type OfflineAnalyzer struct {
	ruleEngine *RuleEngine
	params     Params
	stats      *Statistics
}

// ===== 回测任务管理（用于实时进度查询）=====

// JobStatus 表示一个回测任务的状态
type JobStatus struct {
	ID           string     `json:"id"`
	Params       Params     `json:"params"`
	TotalCycles  int        `json:"total_cycles"`
	CurrentCycle int        `json:"current_cycle"`
	Status       string     `json:"status"` // pending/running/completed/failed
	Error        string     `json:"error,omitempty"`
	Result       *Result    `json:"result,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

var (
	jobStore   = make(map[string]*JobStatus)
	jobStoreMu sync.RWMutex
)

// NewJob 创建一个新的回测任务并保存到内存
func NewJob(p Params) *JobStatus {
	id := fmt.Sprintf("bt_%d", time.Now().UnixNano())

	// 预估总周期数：((end - start) / interval) + 1
	total := 0
	if !p.EndTime.Before(p.StartTime) && p.ScanInterval > 0 {
		dur := p.EndTime.Sub(p.StartTime)
		total = int(dur/p.ScanInterval) + 1
	}

	job := &JobStatus{
		ID:           id,
		Params:       p,
		TotalCycles:  total,
		CurrentCycle: 0,
		Status:       "pending",
		StartedAt:    time.Now(),
	}

	jobStoreMu.Lock()
	jobStore[id] = job
	jobStoreMu.Unlock()

	return job
}

// GetJob 通过ID获取任务状态
func GetJob(id string) (*JobStatus, bool) {
	jobStoreMu.RLock()
	defer jobStoreMu.RUnlock()
	job, ok := jobStore[id]
	return job, ok
}

// StartJob 创建并异步启动一个回测任务
func StartJob(p Params) *JobStatus {
	job := NewJob(p)

	go func() {
		analyzer := NewOfflineAnalyzer(p)
		analyzer.RunWithJob(job)
	}()

	return job
}

// NewOfflineAnalyzer 创建离线分析器
func NewOfflineAnalyzer(p Params) *OfflineAnalyzer {
	// 默认值兜底
	if len(p.Symbols) == 0 {
		p.Symbols = []string{"BTCUSDT", "ETHUSDT"}
	}
	if p.ScanInterval <= 0 {
		p.ScanInterval = 3 * time.Minute
	}
	if p.EndTime.Before(p.StartTime) {
		p.EndTime = p.StartTime.Add(24 * time.Hour)
	}

	return &OfflineAnalyzer{
		ruleEngine: NewRuleEngine(),
		params:     p,
		stats:      NewStatistics(),
	}
}

// Run 执行一次完整回测（仅基于规则引擎，不接真实下单）
func (oa *OfflineAnalyzer) Run() (*Result, error) {
	return oa.RunWithProgress(nil)
}

// RunWithJob 在异步任务中执行回测，并实时更新 JobStatus（用于前端进度显示）
func (oa *OfflineAnalyzer) RunWithJob(job *JobStatus) {
	jobStoreMu.Lock()
	job.Status = "running"
	job.CurrentCycle = 0
	jobStoreMu.Unlock()

	result, err := oa.RunWithProgress(func(cycle int) {
		jobStoreMu.Lock()
		job.CurrentCycle = cycle
		jobStoreMu.Unlock()
	})

	jobStoreMu.Lock()
	defer jobStoreMu.Unlock()
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		now := time.Now()
		job.FinishedAt = &now
		return
	}
	job.Status = "completed"
	job.Result = result
	now := time.Now()
	job.FinishedAt = &now
}

// RunWithProgress 是 Run 的内部版本，允许传入一个回调在每个周期更新进度
func (oa *OfflineAnalyzer) RunWithProgress(onCycle func(cycle int)) (*Result, error) {
	log.Printf("🚀 开始规则层回测，时间范围: %s ~ %s，币种: %v，周期: %v",
		oa.params.StartTime.Format("2006-01-02 15:04:05"),
		oa.params.EndTime.Format("2006-01-02 15:04:05"),
		oa.params.Symbols,
		oa.params.ScanInterval,
	)

	current := oa.params.StartTime
	cycle := 0

	// 简化版账户 & 持仓信息（只为兼容 RuleContext 结构）
	account := decision.AccountInfo{
		TotalEquity:      10000,
		AvailableBalance: 10000,
	}
	var positions []decision.PositionInfo
	var pending []decision.PendingOrderInfo

	for !current.After(oa.params.EndTime) {
		cycle++
		if onCycle != nil {
			onCycle(cycle)
		}
		if cycle%100 == 0 {
			log.Printf("⏱️  回测周期 #%d: %s", cycle, current.Format("2006-01-02 15:04:05"))
		}

		for _, symbol := range oa.params.Symbols {
			data, err := market.Get(symbol)
			if err != nil || data == nil {
				log.Printf("⚠️ 获取 %s 市场数据失败: %v", symbol, err)
				continue
			}

			// 多单方向
			oa.evaluateOne(symbol, "open_long", data, account, positions, pending, current)
			// 空单方向
			oa.evaluateOne(symbol, "open_short", data, account, positions, pending, current)
		}

		current = current.Add(oa.params.ScanInterval)
	}

	log.Printf("✅ 规则层回测完成：%s", oa.stats.DebugSummary())
	return &Result{
		Params:     oa.params,
		Statistics: oa.stats,
	}, nil
}

// evaluateOne 对某个 symbol+action 在当前快照下跑一遍 RuleEngine
func (oa *OfflineAnalyzer) evaluateOne(
	symbol string,
	action string,
	data *market.Data,
	account decision.AccountInfo,
	positions []decision.PositionInfo,
	pending []decision.PendingOrderInfo,
	t time.Time,
) {
	ctx := &RuleContext{
		MarketData:    data,
		Positions:     positions,
		PendingOrders: pending,
		Action:        action,
		Account:       account,
	}

	allowed, failures := oa.ruleEngine.CheckRules(ctx)

	rec := DecisionRecord{
		Time:   t,
		Symbol: symbol,
		Action: "wait",
	}

	// 记录市场阶段信息（便于后续分析）
	if data.TrendPhase != nil {
		rec.MarketPhase = fmt.Sprintf("trend_strength_4h=%.1f",
			data.TrendPhase.TrendStrength4h,
		)
	}

	if !allowed {
		rec.Action = "wait"
		rec.Reasoning = strings.Join(failures, "; ")
	} else {
		// 如果所有硬性规则都放行，我们认为“规则层允许开仓”
		rec.Action = action
		rec.Reasoning = "所有硬性规则通过（仅规则层，不含AI判断）"
	}

	// 无论是否通过硬性规则检查，都记录所有触发的规则（包含高风险提示）
	if len(failures) > 0 {
		rec.RuleFailures = failures
	}

	oa.stats.AddRecord(rec)
}
