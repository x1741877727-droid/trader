package decision

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"nofx/config"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// TODO: 重新启用名义价值上限。临时关闭用于排查 position_size_usd 限制问题。
	enforceNotionalLimit     = false
	interventionLevelExtreme = "extreme"
	extremeInterventionTag   = "极端介入"
)

// getMaxConcurrentSlots 根据账户净值返回最大并发仓位数
func getMaxConcurrentSlots(accountEquity float64, config *config.RiskManagementConfig) int {
	if config == nil {
		return 3 // 默认值
	}

	if accountEquity <= 200 {
		return config.AggressiveMode.MaxConcurrentPositions
	} else if accountEquity <= 1000 {
		return config.StandardMode.MaxConcurrentPositions
	} else {
		return config.ConservativeMode.MaxConcurrentPositions
	}
}

// GetMaxConcurrentSlots 导出函数，用于测试或其他模块调用
func GetMaxConcurrentSlots(accountEquity float64, config *config.RiskManagementConfig) int {
	return getMaxConcurrentSlots(accountEquity, config)
}

// parseGradeAndScore 从reasoning中解析grade和score
// 要求格式：grade=X score=YY，必须在reasoning最前面
func parseGradeAndScore(reasoning string) (grade string, score int, err error) {
	if reasoning == "" {
		return "", 0, fmt.Errorf("reasoning为空，无法解析grade/score")
	}

	// 查找grade= 和 score= 模式
	gradePattern := `grade=([SA-F])`
	scorePattern := `score=(\d{1,3})`

	// 使用正则表达式解析grade
	gradeRegex := regexp.MustCompile(gradePattern)
	gradeMatches := gradeRegex.FindStringSubmatch(reasoning)
	if len(gradeMatches) < 2 {
		return "", 0, fmt.Errorf("reasoning中未找到有效的grade=X格式 (X必须是S/A/B/C/D/F)")
	}
	grade = gradeMatches[1]

	// 使用正则表达式解析score
	scoreRegex := regexp.MustCompile(scorePattern)
	scoreMatches := scoreRegex.FindStringSubmatch(reasoning)
	if len(scoreMatches) < 2 {
		return "", 0, fmt.Errorf("reasoning中未找到有效的score=YY格式")
	}

	score, err = strconv.Atoi(scoreMatches[1])
	if err != nil {
		return "", 0, fmt.Errorf("score格式错误: %v", err)
	}

	// 校验score范围
	if score < 0 || score > 100 {
		return "", 0, fmt.Errorf("score必须在0-100之间，当前: %d", score)
	}

	// 校验grade和score的一致性
	var expectedMin, expectedMax int
	switch grade {
	case "S":
		expectedMin, expectedMax = 85, 100
	case "A":
		expectedMin, expectedMax = 75, 84
	case "B":
		expectedMin, expectedMax = 65, 74
	case "C", "D", "F":
		expectedMin, expectedMax = 0, 64
	default:
		return "", 0, fmt.Errorf("无效的grade: %s (必须是S/A/B/C/D/F)", grade)
	}

	if score < expectedMin || score > expectedMax {
		return "", 0, fmt.Errorf("grade=%s的分数范围应为%d-%d，当前score=%d", grade, expectedMin, expectedMax, score)
	}

	return grade, score, nil
}

// DecisionErrorType 决策错误类型
type DecisionErrorType string

const (
	AI_CALL_FAILED               DecisionErrorType = "AI_CALL_FAILED"
	MARKET_DATA_FAILED           DecisionErrorType = "MARKET_DATA_FAILED"
	JSON_EXTRACT_FAILED          DecisionErrorType = "JSON_EXTRACT_FAILED"
	DECISION_VALIDATION_REJECTED DecisionErrorType = "DECISION_VALIDATION_REJECTED"
)

// DecisionError 结构化决策错误
type DecisionError struct {
	Type    DecisionErrorType
	Cause   error
	Full    *FullDecision
	Message string
}

func (e *DecisionError) Error() string {
	return fmt.Sprintf("%s: %v", e.Type, e.Cause)
}

func (e *DecisionError) Unwrap() error {
	return e.Cause
}

var (
	errDecisionExtraction = errors.New("decision_extraction_failed")
	errDecisionValidation = errors.New("decision_validation_failed")

	// 全局流式回调注册（用于 SSE 推送）
	globalStreamCallbacks = make(map[string]mcp.StreamCallback) // trader_id -> callback
	streamCallbacksMutex  sync.RWMutex
)

// RegisterStreamCallback 注册流式回调（用于 SSE）
func RegisterStreamCallback(traderID string, callback mcp.StreamCallback) {
	streamCallbacksMutex.Lock()
	defer streamCallbacksMutex.Unlock()
	globalStreamCallbacks[traderID] = callback
}

// UnregisterStreamCallback 取消注册流式回调
func UnregisterStreamCallback(traderID string) {
	streamCallbacksMutex.Lock()
	defer streamCallbacksMutex.Unlock()
	delete(globalStreamCallbacks, traderID)
}

// GetStreamCallback 获取流式回调
func GetStreamCallback(traderID string) mcp.StreamCallback {
	streamCallbacksMutex.RLock()
	defer streamCallbacksMutex.RUnlock()
	return globalStreamCallbacks[traderID]
}

// PositionInfo 持仓信息
type PositionInfo struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long" or "short"
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"`
	LiquidationPrice float64 `json:"liquidation_price"`
	MarginUsed       float64 `json:"margin_used"`
	UpdateTime       int64   `json:"update_time"`         // 持仓更新时间戳（毫秒）
	StopLoss         float64 `json:"stop_loss,omitempty"` // 当前止损价（系统已设置）
	TP1              float64 `json:"tp1,omitempty"`
	TP2              float64 `json:"tp2,omitempty"`
	TP3              float64 `json:"tp3,omitempty"`
	TPStage          int     `json:"tp_stage,omitempty"` // 0=还没到, 1=到过tp1, 2=到过tp2, 3=到过tp3
}

// PendingOrderInfo 待成交限价单信息
type PendingOrderInfo struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long" or "short"
	LimitPrice       float64 `json:"limit_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	OrderID          int64   `json:"order_id"`
	TP1              float64 `json:"tp1"`
	TP2              float64 `json:"tp2"`
	TP3              float64 `json:"tp3"`
	StopLoss         float64 `json:"stop_loss"`
	TakeProfit       float64 `json:"take_profit"`
	CreateTime       int64   `json:"create_time"`  // 创建时间戳（毫秒）
	DurationMin      int     `json:"duration_min"` // 挂单时长（分钟）
	Confidence       int     `json:"confidence"`
	Reasoning        string  `json:"reasoning"`
	Thesis           string  `json:"thesis"`            // 入场逻辑的一句话总结
	CancelConditions string  `json:"cancel_conditions"` // 撤单条件
}

// AccountInfo 账户信息
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // 账户净值
	AvailableBalance float64 `json:"available_balance"` // 可用余额
	TotalPnL         float64 `json:"total_pnl"`         // 总盈亏
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
	MarginUsed       float64 `json:"margin_used"`       // 已用保证金
	MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
	PositionCount    int     `json:"position_count"`    // 持仓数量
}

// CandidateCoin 候选币种（来自币种池）
type CandidateCoin struct {
	Symbol  string   `json:"symbol"`
	Sources []string `json:"sources"` // 来源: "ai500" 和/或 "oi_top"
}

// OITopData 持仓量增长Top数据（用于AI决策参考）
type OITopData struct {
	Rank              int
	OIDeltaPercent    float64
	OIDeltaValue      float64
	PriceDeltaPercent float64
	NetLong           float64
	NetShort          float64
}

// Context 交易上下文
type Context struct {
	CurrentTime          string                       `json:"current_time"`
	RuntimeMinutes       int                          `json:"runtime_minutes"`
	CallCount            int                          `json:"call_count"`
	Account              AccountInfo                  `json:"account"`
	Positions            []PositionInfo               `json:"positions"`
	PendingOrders        []PendingOrderInfo           `json:"pending_orders"` // 待成交限价单
	CandidateCoins       []CandidateCoin              `json:"candidate_coins"`
	DailyPairTrades      map[string]int               `json:"daily_pair_trades"` // 每个币种当日已开单数（市价+限价）
	LastDecisionRecord   *logger.DecisionRecord       `json:"-"`                 // 上一轮AI决策记录
	MarketDataMap        map[string]*market.Data      `json:"-"`
	OITopDataMap         map[string]*OITopData        `json:"-"`
	Performance          interface{}                  `json:"-"`
	BTCETHLeverage       int                          `json:"-"`
	AltcoinLeverage      int                          `json:"-"`
	RiskManagementConfig *config.RiskManagementConfig `json:"-"` // 风险管理配置
}

// Decision AI的交易决策
type Decision struct {
	Symbol          string  `json:"symbol"`
	Action          string  `json:"action"` // open_long, open_short, close_long, close_short, partial_close_long, partial_close_short, hold, wait, update_stop_loss, update_take_profit, cancel_limit_order
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`
	// 新增：AI开仓时就能把三段发过来
	TP1 float64 `json:"tp1,omitempty"`
	TP2 float64 `json:"tp2,omitempty"`
	TP3 float64 `json:"tp3,omitempty"`

	// 限价单相关字段
	LimitPrice   float64 `json:"limit_price,omitempty"`   // 限价单价格
	CurrentPrice float64 `json:"current_price,omitempty"` // 当前市场价格（用于限价单合理性校验）
	OrderID      int64   `json:"order_id,omitempty"`      // 取消订单时使用

	// 部分平仓相关字段（仅对 close_long / close_short 生效）
	// close_quantity: 直接指定本次要平掉的合约张数/币数（优先级最高）
	// close_ratio: 按当前持仓数量的比例平仓（0-1，例如 0.33 代表平 33%）
	// 两者都为空时，仍按旧逻辑视为"全平"
	CloseQuantity float64 `json:"close_quantity,omitempty"`
	CloseRatio    float64 `json:"close_ratio,omitempty"`

	NewStopLoss       float64 `json:"new_stop_loss,omitempty"`
	NewTakeProfit     float64 `json:"new_take_profit,omitempty"`
	Confidence        int     `json:"confidence,omitempty"`
	RiskUSD           float64 `json:"risk_usd,omitempty"`
	Reasoning         string  `json:"reasoning"`
	IsAddOn           bool    `json:"is_add_on,omitempty"`
	InterventionLevel string  `json:"intervention_level,omitempty"`

	// ExecutionGate 相关字段
	ExecutionPreference string `json:"execution_preference,omitempty"` // market/limit/auto（auto 表示按系统默认：MARKET）

	// 兼容性字段：用于处理字段别名（不参与业务逻辑）
	StopPriceAlias float64 `json:"stop_price,omitempty"` // 别名：stop_price -> stop_loss
	EntryAlias     float64 `json:"entry,omitempty"`      // 可选兼容
	TargetAlias    float64 `json:"target,omitempty"`     // 可选兼容
	RiskAlias      float64 `json:"risk,omitempty"`       // 可选兼容
	RewardAlias    float64 `json:"reward,omitempty"`     // 可选兼容
	RRAlias        float64 `json:"rr,omitempty"`         // 可选兼容
}

// FullDecision AI的完整决策
type FullDecision struct {
	SystemPrompt string     `json:"system_prompt"`
	UserPrompt   string     `json:"user_prompt"`
	CoTTrace     string     `json:"cot_trace"`
	Decisions    []Decision `json:"decisions"`
	Timestamp    time.Time  `json:"timestamp"`
}

func GetFullDecision(ctx *Context, mcpClient *mcp.Client, config *config.Config) (*FullDecision, error) {
	return GetFullDecisionWithCustomPrompt(ctx, mcpClient, "", false, "", config)
}

func GetFullDecisionWithCustomPrompt(ctx *Context, mcpClient *mcp.Client, customPrompt string, overrideBase bool, templateName string, config *config.Config) (*FullDecision, error) {
	return GetFullDecisionWithCustomPromptAndTraderID(ctx, mcpClient, customPrompt, overrideBase, templateName, "", config)
}

// GetFullDecisionWithCustomPromptAndTraderID 带 trader_id 的决策获取（用于流式推送）
func GetFullDecisionWithCustomPromptAndTraderID(ctx *Context, mcpClient *mcp.Client, customPrompt string, overrideBase bool, templateName string, traderID string, config *config.Config) (*FullDecision, error) {
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 获取上一轮AI决策记录（用于变化检测）
	logger := logger.NewDecisionLogger("decision_logs")
	if latestRecords, err := logger.GetLatestRecords(1); err == nil && len(latestRecords) > 0 {
		ctx.LastDecisionRecord = latestRecords[0]
	}

	systemPrompt := buildSystemPromptWithCustom(ctx, customPrompt, overrideBase, templateName)
	userPrompt := buildUserPrompt(ctx)

	// 检查是否有该 trader 的流式回调
	var streamCallback mcp.StreamCallback
	if traderID != "" {
		streamCallback = GetStreamCallback(traderID)
	}

	log.Printf("🤖 [AI调用] 系统提示词长度: %d字符", len(systemPrompt))
	log.Printf("🤖 [AI调用] 用户提示词长度: %d字符", len(userPrompt))
	log.Printf("🤖 [AI调用] 系统提示词预览: %q", systemPrompt[:min(200, len(systemPrompt))])
	log.Printf("🤖 [AI调用] 用户提示词预览: %q", userPrompt[:min(200, len(userPrompt))])

	var aiResponse string
	var err error

	if streamCallback != nil {
		// 使用流式版本
		aiResponse, err = mcpClient.CallWithMessagesStream(systemPrompt, userPrompt, streamCallback)
	} else {
		// 使用普通版本
		aiResponse, err = mcpClient.CallWithMessages(systemPrompt, userPrompt)
	}

	if err != nil {
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	usedUserPrompt := userPrompt
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, config, ctx.MarketDataMap)
	if err != nil && errors.Is(err, errDecisionExtraction) {
		initialErr := err
		log.Printf("⚠️  决策 JSON 提取失败，尝试格式纠错: %v", initialErr)
		retryPrompt := buildFormatRepairPrompt(aiResponse, initialErr)
		retryResponse, retryCallErr := mcpClient.CallWithMessages(systemPrompt, retryPrompt)
		if retryCallErr != nil {
			return nil, fmt.Errorf("首次解析失败(%v)，格式纠错调用失败: %w", initialErr, retryCallErr)
		}

		usedUserPrompt = retryPrompt
		aiResponse = retryResponse
		decision, err = parseFullDecisionResponse(retryResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, config, ctx.MarketDataMap)
	}
	if err != nil {
		// 检查是否是DecisionError
		if decisionErr, ok := err.(*DecisionError); ok {
			if decisionErr.Type == DECISION_VALIDATION_REJECTED {
				// 对于验证拒绝，返回决策但标记为rejected
				decision.Timestamp = time.Now()
				decision.SystemPrompt = systemPrompt
				decision.UserPrompt = usedUserPrompt
				return decision, decisionErr
			}
		}
		return nil, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.SystemPrompt = systemPrompt
	decision.UserPrompt = usedUserPrompt
	return decision, nil
}

// GetFullDecisionStream 流式获取决策，实时推送 CoT 内容
func GetFullDecisionStream(ctx *Context, mcpClient *mcp.Client, customPrompt string, overrideBase bool, templateName string, streamCallback mcp.StreamCallback, config *config.Config) (*FullDecision, error) {
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 获取上一轮AI决策记录（用于变化检测）
	logger := logger.NewDecisionLogger("decision_logs")
	if latestRecords, err := logger.GetLatestRecords(1); err == nil && len(latestRecords) > 0 {
		ctx.LastDecisionRecord = latestRecords[0]
	}

	systemPrompt := buildSystemPromptWithCustom(ctx, customPrompt, overrideBase, templateName)
	userPrompt := buildUserPrompt(ctx)

	// 使用流式调用
	aiResponse, err := mcpClient.CallWithMessagesStream(systemPrompt, userPrompt, streamCallback)
	if err != nil {
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	usedUserPrompt := userPrompt
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, config, ctx.MarketDataMap)
	if err != nil && errors.Is(err, errDecisionExtraction) {
		initialErr := err
		log.Printf("⚠️  决策 JSON 提取失败，尝试格式纠错: %v", initialErr)
		retryPrompt := buildFormatRepairPrompt(aiResponse, initialErr)
		// 重试时也使用流式（但可能不需要实时推送，因为这是错误修复）
		retryResponse, retryCallErr := mcpClient.CallWithMessages(systemPrompt, retryPrompt)
		if retryCallErr != nil {
			return nil, fmt.Errorf("首次解析失败(%v)，格式纠错调用失败: %w", initialErr, retryCallErr)
		}

		usedUserPrompt = retryPrompt
		aiResponse = retryResponse
		decision, err = parseFullDecisionResponse(retryResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, config, ctx.MarketDataMap)
	}
	if err != nil {
		return nil, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.SystemPrompt = systemPrompt
	decision.UserPrompt = usedUserPrompt
	return decision, nil
}

func fetchMarketDataForContext(ctx *Context) error {
	ctx.MarketDataMap = make(map[string]*market.Data)
	ctx.OITopDataMap = make(map[string]*OITopData)

	symbolSet := make(map[string]bool)
	for _, pos := range ctx.Positions {
		symbolSet[pos.Symbol] = true
	}

	maxCandidates := calculateMaxCandidates(ctx)
	for i, coin := range ctx.CandidateCoins {
		if i >= maxCandidates {
			break
		}
		symbolSet[coin.Symbol] = true
	}

	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	for symbol := range symbolSet {
		data, err := market.Get(symbol)
		if err != nil {
			continue
		}

		isExistingPosition := positionSymbols[symbol]
		if !isExistingPosition && data.OpenInterest != nil && data.CurrentPrice > 0 {
			oiValue := data.OpenInterest.Latest * data.CurrentPrice
			oiValueInMillions := oiValue / 1_000_000
			if oiValueInMillions < 15 {
				log.Printf("⚠️  %s 持仓价值过低(%.2fM USD < 15M)，跳过此币种", symbol, oiValueInMillions)
				continue
			}
		}

		ctx.MarketDataMap[symbol] = data
	}

	oiPositions, err := pool.GetOITopPositions()
	if err == nil {
		for _, pos := range oiPositions {
			symbol := pos.Symbol
			ctx.OITopDataMap[symbol] = &OITopData{
				Rank:              pos.Rank,
				OIDeltaPercent:    pos.OIDeltaPercent,
				OIDeltaValue:      pos.OIDeltaValue,
				PriceDeltaPercent: pos.PriceDeltaPercent,
				NetLong:           pos.NetLong,
				NetShort:          pos.NetShort,
			}
		}
	}

	return nil
}

func calculateMaxCandidates(ctx *Context) int {
	return len(ctx.CandidateCoins)
}

// collectAllAnalyzedSymbols 收集本轮会被模型分析的所有symbol
// 顺序：持仓symbols -> 待成交限价单symbols -> 候选币symbols（前N个）
func collectAllAnalyzedSymbols(ctx *Context) []string {
	symbolMap := make(map[string]bool)
	var symbols []string

	// 1) 当前持仓 symbols（优先级最高）
	for _, pos := range ctx.Positions {
		if !symbolMap[pos.Symbol] {
			symbolMap[pos.Symbol] = true
			symbols = append(symbols, pos.Symbol)
		}
	}

	// 2) 待成交限价单 symbols
	for _, order := range ctx.PendingOrders {
		if !symbolMap[order.Symbol] {
			symbolMap[order.Symbol] = true
			symbols = append(symbols, order.Symbol)
		}
	}

	// 3) 候选币 symbols（取前N个，与maxCandidates一致）
	maxCandidates := calculateMaxCandidates(ctx)
	for i, candidate := range ctx.CandidateCoins {
		if i >= maxCandidates {
			break
		}
		if !symbolMap[candidate.Symbol] {
			symbolMap[candidate.Symbol] = true
			symbols = append(symbols, candidate.Symbol)
		}
	}

	return symbols
}

func buildSystemPromptWithCustom(ctx *Context, customPrompt string, overrideBase bool, templateName string) string {
	if overrideBase && customPrompt != "" {
		return customPrompt
	}

	basePrompt := buildSystemPrompt(ctx, templateName)
	if customPrompt == "" {
		return basePrompt
	}

	var sb strings.Builder
	sb.WriteString(basePrompt)
	sb.WriteString("\n\n# 📌 个性化交易策略\n\n")
	sb.WriteString(customPrompt)
	sb.WriteString("\n\n注意: 以上个性化策略是对基础规则的补充，不能违背基础风险控制原则。\n")
	return sb.String()
}

func buildSystemPrompt(ctx *Context, templateName string) string {
	if templateName != "" && templateName != "default" {
		log.Printf("⚠️  模板 '%s' 已禁用，强制使用模块化提示词", templateName)
	}

	modularPrompt, err := buildModularSystemPrompt(ctx)
	if err != nil {
		log.Printf("⚠️  构建模块化提示词失败，回退到 default 模板: %v", err)
		return buildLegacySystemPrompt(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, "default")
	}

	return modularPrompt
}

func buildModularSystemPrompt(ctx *Context) (string, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("3min cycle #%d\n\n", ctx.CallCount))
	sb.WriteString("【系统层｜全局指令】\n")
	if err := appendModule(&sb, "SystemCore"); err != nil {
		return "", err
	}
	if err := appendModule(&sb, "CoreTradingRules"); err != nil {
		return "", err
	}
	if err := appendModule(&sb, "RiskGuardFlow"); err != nil {
		return "", err
	}
	sb.WriteString(buildPositionsToken(ctx))
	sb.WriteString("\n\n")

	sb.WriteString("【流程层｜仅在完成持仓检查后启用】\n")
	if len(ctx.Positions) > 0 {
		if err := appendModule(&sb, "HoldPlaybook"); err != nil {
			return "", err
		}
		// 获取动态的最大并发仓位数
		maxSlots := getMaxConcurrentSlots(ctx.Account.TotalEquity, ctx.RiskManagementConfig)
		if len(ctx.Positions) < maxSlots {
			if err := appendModule(&sb, "MultiAssetOpportunityScan"); err != nil {
				return "", err
			}
		}
		if err := appendModule(&sb, "PositionManagement"); err != nil {
			return "", err
		}
		// 策略矩阵作为流程参考：仅在完成持仓检查后按需拼接，使策略建议结合持仓状态与具体流程
		if err := appendModule(&sb, "TradingStrategyMatrix"); err != nil {
			return "", err
		}
		if err := appendModule(&sb, "OpportunityScoring"); err != nil {
			return "", err
		}
		sb.WriteString("【工具层｜补充模块（仅作补充，不可单独决策）】\n")
		if err := appendModule(&sb, "TechnicalIndicators"); err != nil {
			return "", err
		}
		if err := appendModule(&sb, "RiskManagement"); err != nil {
			return "", err
		}
		if err := appendModule(&sb, "QuickReference"); err != nil {
			return "", err
		}
	} else {
		if err := appendModule(&sb, "OpenSetup"); err != nil {
			return "", err
		}
		// 拼接策略矩阵于无持仓流程：让策略在完整持仓检查与 OpenSetup 背景下生成具体框架建议
		if err := appendModule(&sb, "TradingStrategyMatrix"); err != nil {
			return "", err
		}
		if err := appendModule(&sb, "OpportunityScoring"); err != nil {
			return "", err
		}
		sb.WriteString("【工具层｜补充模块（仅作补充，不可单独决策）】\n")
		if err := appendModule(&sb, "TechnicalIndicators"); err != nil {
			return "", err
		}
		// Note: ChanTheory and MarketStateAndTrend modules removed — their evidence fields remain available via TechnicalIndicators / RiskManagement
		if err := appendModule(&sb, "RiskManagement"); err != nil {
			return "", err
		}
		if err := appendModule(&sb, "QuickReference"); err != nil {
			return "", err
		}
	}

	sb.WriteString("【输出层｜必须最后执行】\n")
	if err := appendModule(&sb, "OutputFormat"); err != nil {
		return "", err
	}
	if err := appendModule(&sb, "DecisionChecklist"); err != nil {
		return "", err
	}

	return sb.String(), nil
}

func buildPositionsToken(ctx *Context) string {
	// 获取动态的最大并发仓位数
	maxSlots := getMaxConcurrentSlots(ctx.Account.TotalEquity, ctx.RiskManagementConfig)

	// 计算总占用位置：持仓 + 待成交限价单
	totalOccupied := len(ctx.Positions) + len(ctx.PendingOrders)
	remainingSlots := maxSlots - totalOccupied
	if remainingSlots < 0 {
		remainingSlots = 0
	}

	if len(ctx.Positions) == 0 {
		return fmt.Sprintf("positions=0（当前持仓0个，剩余空位 %d/%d），已完成持仓确认 → 执行 OpenSetup 分支\n⚠️ 开仓前必须检查：总占用位置 < %d（当前可开仓，含待成交限价单）",
			remainingSlots, maxSlots, maxSlots)
	}

	var highlights []string
	for i, pos := range ctx.Positions {
		if i >= 2 {
			break
		}
		highlights = append(highlights, fmt.Sprintf("%s %s", pos.Symbol, strings.ToUpper(pos.Side)))
	}

	// 添加pending orders信息
	pendingInfo := ""
	if len(ctx.PendingOrders) > 0 {
		pendingInfo = fmt.Sprintf(" + %d待成交限价单", len(ctx.PendingOrders))
	}

	warningMsg := ""
	if remainingSlots == 0 {
		warningMsg = fmt.Sprintf("\n⚠️ 总占用已满（%d持仓%s = %d/%d），禁止开新仓！", len(ctx.Positions), pendingInfo, totalOccupied, maxSlots)
	} else if remainingSlots == 1 {
		warningMsg = fmt.Sprintf("\n⚠️ 仅剩%d个空位，开仓前必须确认总占用 < %d（含待成交限价单）", remainingSlots, maxSlots)
	}

	return fmt.Sprintf("positions>0（%d 持仓%s，剩余空位 %d/%d），已完成持仓确认 → 执行 HoldPlaybook 分支%s",
		len(ctx.Positions), pendingInfo, remainingSlots, maxSlots, warningMsg)
}

func appendModule(sb *strings.Builder, moduleName string) error {
	content, err := loadModuleContent(moduleName)
	if err != nil {
		return err
	}

	sb.WriteString(fmt.Sprintf("@modules/%s\n", moduleDisplayName(moduleName)))
	sb.WriteString(content)
	sb.WriteString("\n\n")

	return nil
}

var (
	moduleCache   = make(map[string]string)
	moduleCacheMu sync.RWMutex
)

func loadModuleContent(moduleName string) (string, error) {
	moduleCacheMu.RLock()
	if content, ok := moduleCache[moduleName]; ok {
		moduleCacheMu.RUnlock()
		return content, nil
	}
	moduleCacheMu.RUnlock()

	modulePath := filepath.Join(promptsDir, "modules", fmt.Sprintf("%s.txt", moduleName))
	data, err := os.ReadFile(modulePath)
	if err != nil {
		return "", fmt.Errorf("读取模块 %s 失败: %w", moduleName, err)
	}

	content := string(data)
	moduleCacheMu.Lock()
	moduleCache[moduleName] = content
	moduleCacheMu.Unlock()

	return content, nil
}

func moduleDisplayName(moduleName string) string {
	switch moduleName {
	case "RiskGuardFlow":
		return "RiskGuard-Flow"
	default:
		return moduleName
	}
}

func buildLegacySystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int, templateName string) string {
	var sb strings.Builder

	if templateName == "" {
		templateName = "default"
	}

	template, err := GetPromptTemplate(templateName)
	if err != nil {
		log.Printf("⚠️  提示词模板 '%s' 不存在，使用 default: %v", templateName, err)
		template, err = GetPromptTemplate("default")
		if err != nil {
			log.Printf("❌ 无法加载任何提示词模板，使用内置简化版本")
			sb.WriteString("你是专业的加密货币交易AI。请根据市场数据做出交易决策。\n\n")
		} else {
			sb.WriteString(template.Content)
			sb.WriteString("\n\n")
		}
	} else {
		sb.WriteString(template.Content)
		sb.WriteString("\n\n")
	}

	// 按你最新的要求，追加硬约束，跟默认模板保持一致
	sb.WriteString("我所说的主流币就是ETHUSDT，SOLUSDT，BTCUSDT。除了这三个以外都是山寨币:\n")
	sb.WriteString("即使这个币没有开单也要输出:\n")
	sb.WriteString("```json\n[\n")
	// 这里明确示范：position_size_usd = 实际保证金（举例用账户净值 8%）
	sb.WriteString(fmt.Sprintf(
		"  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": %.2f, \"stop_loss\": 97000, \"tp1\": 94000, \"tp2\": 92500, \"tp3\": 91000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 6.0, \"reasoning\": \"下跌趋势+MACD死叉，已通过保证金在5%%~13%%、总保证金≤70%%、杠杆65~100检查\"},\n",
		btcEthLeverage, accountEquity*0.08,
	))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\", \"reasoning\": \"触达目标位，止盈离场\"},\n")
	sb.WriteString("  {\"symbol\": \"BTCUSDT\", \"action\": \"update_stop_loss\", \"new_stop_loss\": 96500, \"reasoning\": \"价格突破止盈点1，将止损抬高至平仓价\"}\n")
	sb.WriteString("]\n```\n\n")

	sb.WriteString("字段说明:\n")
	sb.WriteString("- `position_size_usd`: 本笔单**实际占用的保证金**（单位 USDT，不是名义价值，不等于保证金×杠杆）。\n")
	sb.WriteString("- 开仓时必须同时返回: tp1, tp2, tp3；且 take_profit 必须等于 tp3。\n")
	sb.WriteString("- `action`: open_long | open_short | cancel_limit_order | close_long | close_short | partial_close_long | partial_close_short | hold | wait | update_stop_loss | update_take_profit\n")
	sb.WriteString("- `confidence`: 0-100（开仓建议≥75）\n")
	sb.WriteString("- 市价开仓（open_long/open_short）必填: leverage, position_size_usd, stop_loss, take_profit, tp1, tp2, tp3, confidence, risk_usd, reasoning\n")
	sb.WriteString("- 限价挂单（limit_open_long/limit_open_short）适用于市价与理想价偏离 ≥0.5%、4h 已进入 Late 阶段或 15m/5m 出现极端瀑布/拉升的场景。必须提供 limit_price，并在 reasoning 中写明挂单价区、触发确认（如“15m CHoCH_up + OI 回流”）与撤单条件。\n")
	sb.WriteString("- 取消限价单（cancel_limit_order）必填: order_id（从\"待成交限价单\"中获取）, reasoning（必须详细说明取消原因：点位是否合理、市场条件是否变化、价格是否偏离目标、取消后的计划等）\n\n")
	sb.WriteString("⚠️ 限价单管理：系统会在持仓信息中显示所有待成交限价单。如果AI发现限价单点位有问题、市场条件已变化或不应继续挂单，可以自主使用 cancel_limit_order 取消，但必须在 reasoning 中详细说明取消原因。\n\n")
	sb.WriteString("⚠️ 若暂不挂单，请使用 wait，并写出计划价位/确认条件/放弃条件；若决定挂单，reasoning 中要说明结构位置和确认逻辑。\n\n")

	return sb.String()
}

func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("时间: %s | 周期: #%d | 运行: %d分钟\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		sb.WriteString(fmt.Sprintf("BTC: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7))
	}

	sb.WriteString(fmt.Sprintf("账户: 净值%.2f | 余额%.2f (%.1f%%) | 盈亏%+.2f%% | 保证金%.1f%% | 持仓%d个\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// 显示每日开单计数（结构化格式，便于AI引用）
	// 包含所有本轮会被分析的symbols：持仓 -> 挂单 -> 候选币
	analyzedSymbols := collectAllAnalyzedSymbols(ctx)
	sb.WriteString("daily_pair_trades = ```text\n{\n")
	for i, symbol := range analyzedSymbols {
		count := ctx.DailyPairTrades[symbol] // 如果不存在，默认为0
		comma := ","
		if i == len(analyzedSymbols)-1 {
			comma = ""
		}
		sb.WriteString(fmt.Sprintf("  '%s': %d%s\n", symbol, count, comma))
	}
	sb.WriteString("}\n")
	sb.WriteString("```\n\n")

	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for i, pos := range ctx.Positions {
			holdingDuration := ""
			if pos.UpdateTime > 0 {
				durationMs := time.Now().UnixMilli() - pos.UpdateTime
				durationMin := durationMs / (1000 * 60)
				if durationMin < 60 {
					holdingDuration = fmt.Sprintf(" | 持仓时长%d分钟", durationMin)
				} else {
					durationHour := durationMin / 60
					durationMinRemainder := durationMin % 60
					holdingDuration = fmt.Sprintf(" | 持仓时长%d小时%d分钟", durationHour, durationMinRemainder)
				}
			}

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场价%.4f 当前价%.4f | 盈亏%+.2f%% | 杠杆%dx | 保证金%.0f | 强平价%.4f%s\n\n",
				i+1, pos.Symbol, strings.ToUpper(pos.Side),
				pos.EntryPrice, pos.MarkPrice, pos.UnrealizedPnLPct,
				pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

			// B) 添加结构止损指引
			sb.WriteString("如果你认为应该结构保护止损，请输出 update_stop_loss 并提供 new_stop_loss=结构位价格（必须是结构点：1h/15m swing low/high/破位回踩点），不要只写建议\n\n")
			// ← 这里就是关键，把它喂回去
			if pos.TP1 > 0 || pos.TP2 > 0 || pos.TP3 > 0 {
				sb.WriteString(fmt.Sprintf("TPs: tp1=%.4f tp2=%.4f tp3=%.4f | 当前止盈阶段=%d\n\n",
					pos.TP1, pos.TP2, pos.TP3, pos.TPStage))
			} else {
				sb.WriteString("\n")
			}

			if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(market.Format(marketData))
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("当前持仓: 无\n\n")
	}

	// 显示待成交限价单
	if len(ctx.PendingOrders) > 0 {
		sb.WriteString("## 待成交限价单\n")
		for i, order := range ctx.PendingOrders {
			// 获取当前市价
			currentPrice := 0.0
			if marketData, ok := ctx.MarketDataMap[order.Symbol]; ok {
				currentPrice = marketData.CurrentPrice
			}

			priceDiff := 0.0
			priceDiffPct := 0.0
			if currentPrice > 0 {
				if order.Side == "long" {
					priceDiff = order.LimitPrice - currentPrice
					priceDiffPct = (priceDiff / currentPrice) * 100
				} else {
					priceDiff = currentPrice - order.LimitPrice
					priceDiffPct = (priceDiff / currentPrice) * 100
				}
			}

			sb.WriteString(fmt.Sprintf("%d. %s %s 限价单 #%d | 限价%.4f",
				i+1, order.Symbol, strings.ToUpper(order.Side), order.OrderID, order.LimitPrice))
			if currentPrice > 0 {
				sb.WriteString(fmt.Sprintf(" | 当前价%.4f (距离%.2f%%)", currentPrice, priceDiffPct))
			}
			sb.WriteString(fmt.Sprintf(" | 数量%.4f | 杠杆%dx | 挂单时长%d分钟\n",
				order.Quantity, order.Leverage, order.DurationMin))
			sb.WriteString(fmt.Sprintf("   止损: %.4f | TP1: %.4f TP2: %.4f TP3: %.4f | 信心度: %d\n",
				order.StopLoss, order.TP1, order.TP2, order.TP3, order.Confidence))
			if order.Reasoning != "" {
				sb.WriteString(fmt.Sprintf("   挂单理由: %s\n", order.Reasoning))
			}
			if order.Thesis != "" {
				sb.WriteString(fmt.Sprintf("   入场逻辑: %s\n", order.Thesis))
			}
			if order.CancelConditions != "" {
				sb.WriteString(fmt.Sprintf("   撤单条件: %s\n", order.CancelConditions))
			}
			sb.WriteString("\n")
			sb.WriteString("   ⚠️ 如果发现限价单点位不合理、市场条件已变化或不应继续挂单，可以使用 cancel_limit_order 取消，但必须在 reasoning 中详细说明取消原因。\n\n")
		}
	} else {
		sb.WriteString("待成交限价单: 无\n\n")
	}

	// 显示上一轮AI决策摘要（大幅简化）
	if ctx.LastDecisionRecord != nil {
		sb.WriteString("## 上一轮决策摘要\n")

		// 为每个symbol显示简化的决策摘要
		for _, decision := range ctx.LastDecisionRecord.Decisions {
			summary := fmt.Sprintf("%s: %s", decision.Symbol, decision.Action)

			sb.WriteString(fmt.Sprintf("- %s\n", summary))
		}
	}

	// 只显示主要交易币种：BTCUSDT, ETHUSDT, SOLUSDT, BNBUSDT
	mainSymbols := []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT"}
	sb.WriteString(fmt.Sprintf("## 候选币种 (%d个)\n\n", len(mainSymbols)))
	displayedCount := 0
	for _, symbol := range mainSymbols {
		marketData, hasData := ctx.MarketDataMap[symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := ""
		// 简化sourceTags，因为我们不再有coin.Sources信息
		sourceTags = " (主要交易币种)"

		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, symbol, sourceTags))
		sb.WriteString(market.Format(marketData))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	if ctx.Performance != nil {
		type PerformanceData struct {
			SharpeRatio float64 `json:"sharpe_ratio"`
		}
		var perfData PerformanceData
		if jsonData, err := json.Marshal(ctx.Performance); err == nil {
			if err := json.Unmarshal(jsonData, &perfData); err == nil {
				sb.WriteString(fmt.Sprintf("## 📊 夏普比率: %.2f\n\n", perfData.SharpeRatio))
			}
		}
	}

	sb.WriteString("---\n\n")

	sb.WriteString("⚠️ 输出顺序固定: 先写思维链（不可包含 JSON 括号），再输出唯一一次 JSON 决策数组，并且在 ] 后立即结束回复。\n")
	sb.WriteString("⚠️ 所有补充说明必须写入各决策的 `reasoning` 字段，JSON 外层禁止使用任何 ``` 包裹。\n\n")
	sb.WriteString("现在请分析并输出决策（思维链 + JSON）\n")

	return sb.String()
}

func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int, config *config.Config, marketDataMap map[string]*market.Data) (*FullDecision, error) {
	log.Printf("🔍 [解析] 开始解析AI响应 (长度: %d字符)", len(aiResponse))
	log.Printf("🔍 [解析] AI响应预览: %q", aiResponse[:min(300, len(aiResponse))])

	cotTrace := extractCoTTrace(aiResponse)
	log.Printf("🔍 [解析] 提取的思维链长度: %d字符", len(cotTrace))

	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		log.Printf("❌ [解析] JSON提取失败: %v", err)
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("%w: %w\n\n=== AI思维链分析 ===\n%s", errDecisionExtraction, err, cotTrace)
	}

	// 为每个决策设置当前价格（用于限价单合理性校验）
	for i := range decisions {
		if marketData, exists := marketDataMap[decisions[i].Symbol]; exists && marketData != nil {
			decisions[i].CurrentPrice = marketData.CurrentPrice
		}
	}

	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage, config); err != nil {
		decisionResp := &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}
		return decisionResp, &DecisionError{
			Type:    DECISION_VALIDATION_REJECTED,
			Cause:   err,
			Full:    decisionResp,
			Message: fmt.Sprintf("决策被风控拦截: %v", err),
		}
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: decisions,
	}, nil
}

func buildFormatRepairPrompt(previousOutput string, parseErr error) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🚨 解析失败: %v\n", parseErr))
	sb.WriteString("忽略上一轮输出，严格按 @modules/OutputFormat 重答\n\n")
	sb.WriteString("a. 输出两段：思维链 + JSON数组，JSON结束立刻停止\n")
	sb.WriteString("b. 思维链禁止出现方括号字符，避免被误判为JSON起始\n")
	sb.WriteString("c. 不要用代码块/围栏包裹JSON（不要出现那三个反引号字符）\n")
	return sb.String()
}

func extractCoTTrace(response string) string {
	// 首先查找JSON数组的开始位置
	jsonStart := strings.Index(response, "[")
	if jsonStart > 0 {
		// 如果找到了 [，取前面的内容作为CoT
		cot := strings.TrimSpace(response[:jsonStart])
		// 如果CoT为空或只有标题，尝试查找其他可能的分割点
		if cot == "" || strings.HasPrefix(cot, "===") {
			// 查找第一个有意义的行
			lines := strings.Split(response[:jsonStart], "\n")
			for i, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "===") {
					return strings.TrimSpace(strings.Join(lines[i:], "\n"))
				}
			}
		}
		return cot
	}

	// 如果没有找到 [，返回整个响应作为CoT
	return strings.TrimSpace(response)
}

func extractDecisions(response string) ([]Decision, error) {
	// 首先检查响应是否包含JSON数组特征
	if !strings.Contains(response, "[") || !strings.Contains(response, "{") {
		return nil, fmt.Errorf("响应中未找到JSON数组特征（缺少 [ 或 { 字符）")
	}

	// 尝试提取被```json包裹的JSON
	if strings.Contains(response, "```json") {
		jsonStart := strings.Index(response, "```json")
		if jsonStart != -1 {
			jsonStart += len("```json")
			jsonEnd := strings.Index(response[jsonStart:], "```")
			if jsonEnd != -1 {
				response = strings.TrimSpace(response[jsonStart : jsonStart+jsonEnd])
			}
		}
	} else if strings.Contains(response, "```") {
		// 尝试提取被```包裹的JSON（不带json标记）
		jsonStart := strings.Index(response, "```")
		if jsonStart != -1 {
			jsonStart += len("```")
			jsonEnd := strings.Index(response[jsonStart:], "```")
			if jsonEnd != -1 {
				response = strings.TrimSpace(response[jsonStart : jsonStart+jsonEnd])
			}
		}
	}

	// 改进的JSON数组查找逻辑：避免匹配markdown列表项如 [✓]
	// 方法1：优先查找 [{ 模式（JSON数组开始后跟着对象）
	arrayStart := -1
	if idx := strings.Index(response, "[{"); idx != -1 {
		arrayStart = idx
	} else {
		// 方法2：从后往前查找最后一个 ]，然后向前匹配对应的 [
		// 这样可以避免匹配到markdown列表项
		lastBracketEnd := strings.LastIndex(response, "]")
		if lastBracketEnd != -1 {
			// 从最后一个 ] 向前查找匹配的 [
			for i := lastBracketEnd; i >= 0; i-- {
				if response[i] == '[' {
					// 检查这是否是一个有效的JSON数组开始
					// 应该后面跟着 { 或 ]（空数组）
					if i+1 < len(response) {
						nextChar := response[i+1]
						if nextChar == '{' || nextChar == ']' || nextChar == ' ' || nextChar == '\n' || nextChar == '\t' {
							arrayStart = i
							break
						}
					}
				}
			}
		}

		// 方法3：如果还是没找到，尝试查找单独的 [（但跳过markdown列表项）
		if arrayStart == -1 {
			// 使用正则表达式查找 [ 后面跟着 { 或 ] 或空白字符的模式
			re := regexp.MustCompile(`\[[\s\n]*[{\]]`)
			matches := re.FindStringIndex(response)
			if matches != nil {
				arrayStart = matches[0]
			} else {
				// 最后尝试：查找任何 [，但跳过明显的markdown列表项
				idx := strings.Index(response, "[")
				if idx != -1 {
					// 检查后面是否跟着markdown标记（如 ✓、✗、- 等）
					if idx+1 < len(response) {
						end := idx + 3
						if end > len(response) {
							end = len(response)
						}
						nextChars := response[idx:end]
						// 如果不是markdown标记，可能是JSON数组
						if !strings.HasPrefix(nextChars, "[✓") &&
							!strings.HasPrefix(nextChars, "[✗") &&
							!strings.HasPrefix(nextChars, "[-") &&
							!strings.HasPrefix(nextChars, "[x") &&
							!strings.HasPrefix(nextChars, "[X") {
							arrayStart = idx
						}
					} else {
						arrayStart = idx
					}
				}
			}
		}
	}

	if arrayStart == -1 {
		return nil, fmt.Errorf("无法找到JSON数组起始\n完整响应（前500字符）: %s", truncateString(response, 500))
	}

	arrayEnd := findMatchingBracket(response, arrayStart)
	if arrayEnd == -1 {
		return nil, fmt.Errorf("无法找到JSON数组结束\n响应片段: %s", truncateString(response[arrayStart:], 200))
	}

	jsonContent := strings.TrimSpace(response[arrayStart : arrayEnd+1])

	// 在修复之前，先尝试解析，如果失败再修复
	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
		// 如果直接解析失败，尝试修复后再解析
		jsonContent = fixMissingQuotes(jsonContent)
		if err2 := json.Unmarshal([]byte(jsonContent), &decisions); err2 != nil {
			// 如果修复后还是失败，提供更详细的错误信息
			contextStart := arrayStart - 50
			if contextStart < 0 {
				contextStart = 0
			}
			contextEnd := arrayEnd + 50
			if contextEnd > len(response) {
				contextEnd = len(response)
			}
			return nil, fmt.Errorf("JSON解析失败: %w\n原始错误: %v\nJSON内容（前200字符）: %s\n上下文: %s", err2, err, truncateString(jsonContent, 200), truncateString(response[contextStart:contextEnd], 300))
		}
	}

	// 字段兼容层：处理字段别名映射
	for i := range decisions {
		decisions[i] = applyFieldCompatibility(decisions[i])
	}

	// 规范化 execution_preference 字段
	for i := range decisions {
		decisions[i] = normalizeExecutionPreference(decisions[i])
	}

	return decisions, nil
}

// applyFieldCompatibility 字段兼容层：处理字段别名映射和数值容错
func applyFieldCompatibility(d Decision) Decision {
	// A) 字段别名兼容
	if d.StopLoss == 0 && d.StopPriceAlias != 0 {
		log.Printf("⚠️ 字段兼容: %s 使用别名 stop_price=%.4f 映射到 stop_loss", d.Symbol, d.StopPriceAlias)
		d.StopLoss = d.StopPriceAlias
	}

	if d.TakeProfit == 0 && d.TP3 != 0 {
		log.Printf("⚠️ 字段兼容: %s 缺失 take_profit，使用 tp3=%.4f 作为 take_profit", d.Symbol, d.TP3)
		d.TakeProfit = d.TP3
	}

	// 可选兼容：entry/stop/target/risk/reward/rr 字段（仅记录，不参与校验）
	if d.EntryAlias != 0 {
		log.Printf("⚠️ 可选兼容: %s 检测到 entry=%.4f（已忽略，不参与业务逻辑）", d.Symbol, d.EntryAlias)
	}
	if d.TargetAlias != 0 {
		log.Printf("⚠️ 可选兼容: %s 检测到 target=%.4f（已忽略，不参与业务逻辑）", d.Symbol, d.TargetAlias)
	}
	if d.RiskAlias != 0 {
		log.Printf("⚠️ 可选兼容: %s 检测到 risk=%.4f（已忽略，不参与业务逻辑）", d.Symbol, d.RiskAlias)
	}
	if d.RewardAlias != 0 {
		log.Printf("⚠️ 可选兼容: %s 检测到 reward=%.4f（已忽略，不参与业务逻辑）", d.Symbol, d.RewardAlias)
	}
	if d.RRAlias != 0 {
		log.Printf("⚠️ 可选兼容: %s 检测到 rr=%.4f（已忽略，不参与业务逻辑）", d.Symbol, d.RRAlias)
	}

	// B) close_ratio 归一化
	if d.CloseRatio != 0 {
		originalRatio := d.CloseRatio
		if d.CloseRatio > 1 {
			// 如果是百分比格式（0-100），转换为小数格式（0-1）
			d.CloseRatio = d.CloseRatio / 100.0
			log.Printf("⚠️ close_ratio归一化: %s close_ratio %.2f -> %.4f（从百分比转换）", d.Symbol, originalRatio, d.CloseRatio)
		}
		// clamp 到 [0,1]
		if d.CloseRatio < 0 {
			log.Printf("⚠️ close_ratio修正: %s close_ratio %.4f < 0，修正为0", d.Symbol, d.CloseRatio)
			d.CloseRatio = 0
		} else if d.CloseRatio > 1 {
			log.Printf("⚠️ close_ratio修正: %s close_ratio %.4f > 1，修正为1", d.Symbol, d.CloseRatio)
			d.CloseRatio = 1
		}
	}

	// C) 数值容错：对关键数值字段做精度处理
	if d.PositionSizeUSD != 0 {
		d.PositionSizeUSD = roundToPrecision(d.PositionSizeUSD, 2) // 保证金保留2位小数
	}

	if d.LimitPrice != 0 {
		d.LimitPrice = roundToPrecision(d.LimitPrice, 4) // 价格保留4位小数（适合加密货币）
	}

	if d.StopLoss != 0 {
		d.StopLoss = roundToPrecision(d.StopLoss, 4)
	}

	if d.TakeProfit != 0 {
		d.TakeProfit = roundToPrecision(d.TakeProfit, 4)
	}

	if d.TP1 != 0 {
		d.TP1 = roundToPrecision(d.TP1, 4)
	}

	if d.TP2 != 0 {
		d.TP2 = roundToPrecision(d.TP2, 4)
	}

	if d.TP3 != 0 {
		d.TP3 = roundToPrecision(d.TP3, 4)
	}

	if d.NewStopLoss != 0 {
		d.NewStopLoss = roundToPrecision(d.NewStopLoss, 4)
	}

	if d.NewTakeProfit != 0 {
		d.NewTakeProfit = roundToPrecision(d.NewTakeProfit, 4)
	}

	if d.RiskUSD != 0 {
		d.RiskUSD = roundToPrecision(d.RiskUSD, 2) // 风险金额保留2位小数
	}

	return d
}

// roundToPrecision 对浮点数进行精度舍入，避免浮点误差
func roundToPrecision(value float64, precision int) float64 {
	multiplier := math.Pow(10, float64(precision))
	return math.Round(value*multiplier) / multiplier
}

// normalizeExecutionPreference 规范化 execution_preference 字段
func normalizeExecutionPreference(d Decision) Decision {
	if d.ExecutionPreference == "" {
		log.Printf("⚠️ 决策验证: %s 缺失 execution_preference，补为 'auto'", d.Symbol)
		d.ExecutionPreference = "auto"
	} else {
		// 规范化值：只接受 auto/market/limit，其他值都转为 auto，且统一为小写
		switch strings.ToLower(d.ExecutionPreference) {
		case "auto", "market", "limit":
			d.ExecutionPreference = strings.ToLower(d.ExecutionPreference) // 统一为小写
		default:
			log.Printf("⚠️ 决策验证: %s 无效 execution_preference '%s'，归一化为 'auto'", d.Symbol, d.ExecutionPreference)
			d.ExecutionPreference = "auto"
		}
	}
	return d
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func fixMissingQuotes(jsonStr string) string {
	// 替换Unicode引号
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"")
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"")
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")

	// 清理其他可能导致JSON解析失败的Unicode字符
	// 移除零宽字符和其他控制字符（但保留换行符和制表符用于格式化）
	jsonStr = strings.ReplaceAll(jsonStr, "\u200b", "") // 零宽空格
	jsonStr = strings.ReplaceAll(jsonStr, "\u200c", "") // 零宽非连字符
	jsonStr = strings.ReplaceAll(jsonStr, "\u200d", "") // 零宽连字符
	jsonStr = strings.ReplaceAll(jsonStr, "\ufeff", "") // BOM标记

	// 修复缺失的左引号：检测 "字段名": 后面直接跟非引号字符的情况
	// 例如: "reasoning":弱势震荡 -> "reasoning": "弱势震荡
	// 使用正则表达式查找 "字段名": 后面不是引号、数字、true、false、null、{ 或 [ 的情况
	re := regexp.MustCompile(`"([a-zA-Z_]+)":\s*([^"\d\-tfn{\[\s][^,}\]]*)(,|}|\])`)
	jsonStr = re.ReplaceAllStringFunc(jsonStr, func(match string) string {
		// 提取字段名和值
		submatches := re.FindStringSubmatch(match)
		if len(submatches) == 4 {
			fieldName := submatches[1]
			value := strings.TrimSpace(submatches[2])
			terminator := submatches[3]
			// 为值添加引号，并转义特殊字符
			value = strings.ReplaceAll(value, `"`, `\"`)
			value = strings.ReplaceAll(value, "\n", "\\n")
			value = strings.ReplaceAll(value, "\r", "\\r")
			value = strings.ReplaceAll(value, "\t", "\\t")
			return fmt.Sprintf(`"%s": "%s"%s`, fieldName, value, terminator)
		}
		return match
	})

	return jsonStr
}

func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, config *config.Config) error {
	extremeCount := 0

	for i := range decisions {
		if decisions[i].InterventionLevel == interventionLevelExtreme {
			extremeCount++
		}
		if err := validateDecision(&decisions[i], accountEquity, btcEthLeverage, altcoinLeverage, config); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}

	if extremeCount > 1 {
		return fmt.Errorf("极端介入操作一次决策最多允许 1 笔，当前检测到 %d 笔", extremeCount)
	}

	return nil
}

// validateRiskManagement 验证分层风控规则
func validateRiskManagement(d *Decision, accountEquity float64, config *config.Config) error {
	// 确定当前账户模式
	var mode string
	var modeConfig interface{}

	if accountEquity <= 200 {
		mode = "aggressive"
		modeConfig = config.RiskManagement.AggressiveMode
	} else if accountEquity <= 1000 {
		mode = "standard"
		modeConfig = config.RiskManagement.StandardMode
	} else {
		mode = "conservative"
		modeConfig = config.RiskManagement.ConservativeMode
	}

	log.Printf("🎯 账户净值 %.2f，启用%s模式风控", accountEquity, mode)

	// 对于开仓操作，进行严格校验
	if d.Action == "open_long" || d.Action == "open_short" || d.Action == "limit_open_long" || d.Action == "limit_open_short" {
		switch mode {
		case "aggressive":
			cfg := modeConfig.(struct {
				MaxConcurrentPositions int      `json:"max_concurrent_positions"`
				AllowedSymbols         []string `json:"allowed_symbols"`
				MaxLeverage            int      `json:"max_leverage"`
				MinLeverage            int      `json:"min_leverage"`
				RiskUsdMinPct          float64  `json:"risk_usd_min_pct"`
				RiskUsdMaxPct          float64  `json:"risk_usd_max_pct"`
				DailyLossLimitPct      float64  `json:"daily_loss_limit_pct"`
			})

			// 检查交易标的限制
			allowed := false
			for _, symbol := range cfg.AllowedSymbols {
				if d.Symbol == symbol {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("激进模式仅允许交易 %v，当前标的 %s 不支持", cfg.AllowedSymbols, d.Symbol)
			}

			// 杠杆范围校验（激进模式放宽下限）
			if d.Leverage < cfg.MinLeverage {
				return fmt.Errorf("激进模式杠杆不能低于%d，当前%d", cfg.MinLeverage, d.Leverage)
			}
			if d.Leverage > cfg.MaxLeverage {
				return fmt.Errorf("杠杆不能超过%d，当前%d", cfg.MaxLeverage, d.Leverage)
			}

			// 风险预算校验（grade影响）
			// 解析grade以确定风险乘数
			grade, _, err := parseGradeAndScore(d.Reasoning)
			if err != nil {
				return fmt.Errorf("reasoning格式错误: %v", err)
			}

			riskMultiplier := 1.0
			switch grade {
			case "S":
				riskMultiplier = 1.0
			case "A":
				riskMultiplier = 0.8
			case "B":
				riskMultiplier = 0.6
			}

			if d.RiskUSD <= 0 {
				return fmt.Errorf("必须提供有效的risk_usd")
			}
			minRisk := accountEquity * cfg.RiskUsdMinPct / 100 * riskMultiplier
			maxRisk := accountEquity * cfg.RiskUsdMaxPct / 100 * riskMultiplier
			if d.RiskUSD < minRisk || d.RiskUSD > maxRisk {
				return fmt.Errorf("grade=%s单笔风险预算必须在%.2f~%.2f之间(账户净值的%.1f%%~%.1f%%)，当前%.2f",
					grade, minRisk, maxRisk, cfg.RiskUsdMinPct*riskMultiplier, cfg.RiskUsdMaxPct*riskMultiplier, d.RiskUSD)
			}

		case "standard":
			cfg := modeConfig.(struct {
				MaxConcurrentPositions int     `json:"max_concurrent_positions"`
				MaxLeverage            int     `json:"max_leverage"`
				MarginUsageLimitPct    float64 `json:"margin_usage_limit_pct"`
			})

			// 杠杆上限校验
			if d.Leverage > cfg.MaxLeverage {
				return fmt.Errorf("标准模式杠杆不能超过%d，当前%d", cfg.MaxLeverage, d.Leverage)
			}

		case "conservative":
			cfg := modeConfig.(struct {
				MaxConcurrentPositions int     `json:"max_concurrent_positions"`
				MaxLeverage            int     `json:"max_leverage"`
				MarginUsageLimitPct    float64 `json:"margin_usage_limit_pct"`
				NotionalCapPct         float64 `json:"notional_cap_pct"`
			})

			// 杠杆上限校验
			if d.Leverage > cfg.MaxLeverage {
				return fmt.Errorf("保守模式杠杆不能超过%d，当前%d", cfg.MaxLeverage, d.Leverage)
			}
		}
	}

	return nil
}

func findMatchingBracket(s string, start int) int {
	if start >= len(s) || s[start] != '[' {
		return -1
	}

	depth := 0
	inString := false
	var stringChar rune
	escapeNext := false

	for i := start; i < len(s); i++ {
		ch := rune(s[i])

		if inString {
			if escapeNext {
				escapeNext = false
				continue
			}
			if ch == '\\' {
				escapeNext = true
				continue
			}
			if ch == stringChar {
				inString = false
			}
			continue
		}

		switch ch {
		case '"', '\'':
			inString = true
			stringChar = ch
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
			if depth < 0 {
				return -1
			}
		}
	}

	return -1
}

func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, config *config.Config) error {
	// 只保留你现在要的几种 action
	validActions := map[string]bool{
		"open_long":           true,
		"open_short":          true,
		"close_long":          true,
		"close_short":         true,
		"partial_close_long":  true,
		"partial_close_short": true,
		"hold":                true,
		"wait":                true,
		"update_stop_loss":    true,
		"update_take_profit":  true,
		"limit_open_long":     true,
		"limit_open_short":    true,
		"cancel_limit_order":  true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("无效的action: %s", d.Action)
	}

	switch d.Action {
	case "open_long", "open_short", "limit_open_long", "limit_open_short":
		// 0) grade/score 解析和校验（硬性要求）
		grade, score, err := parseGradeAndScore(d.Reasoning)
		if err != nil {
			return fmt.Errorf("reasoning格式错误: %v", err)
		}

		// 根据grade决定是否允许开仓
		if grade == "C" || grade == "D" || grade == "F" {
			return fmt.Errorf("grade=%s (score=%d)，不允许开仓", grade, score)
		}

		// 根据grade调整风险控制策略
		var allowMarketOrder bool = true

		switch grade {
		case "S":
			allowMarketOrder = true // 允许市价（若门禁允许）
		case "A":
			allowMarketOrder = true // 允许市价
		case "B":
			allowMarketOrder = false // 只允许限价开仓
		default:
			return fmt.Errorf("无效的grade: %s", grade)
		}

		// B级机会禁止市价开仓
		if !allowMarketOrder && (d.Action == "open_long" || d.Action == "open_short") {
			return fmt.Errorf("grade=%s (score=%d)，仅允许限价开仓，不允许市价开仓", grade, score)
		}

		// 1) 杠杆验证：只保留最大杠杆上限，最低要求50x
		minLeverage := 50
		maxLeverage := altcoinLeverage
		maxNotional := accountEquity * 1.5 // 单币种名义上限（山寨）

		isBlueChip := d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" || d.Symbol == "SOLUSDT" || d.Symbol == "BNBUSDT"
		if isBlueChip {
			maxLeverage = btcEthLeverage // 主流币的上限用 BTC/ETH 的
			maxNotional = accountEquity * 10
		}

		if d.Leverage < minLeverage || d.Leverage > maxLeverage {
			return fmt.Errorf("%s 杠杆必须在 %d-%d 之间，当前: %d", d.Symbol, minLeverage, maxLeverage, d.Leverage)
		}

		// 1.1) RR真实性校验（用tp3计算，要求RR≥1.8）
		var entryPrice float64
		var hasEntryPrice bool
		if d.Action == "limit_open_long" || d.Action == "limit_open_short" {
			if d.LimitPrice <= 0 {
				return fmt.Errorf("限价开仓必须提供limit_price")
			}
			entryPrice = d.LimitPrice
			hasEntryPrice = true
		} else if d.CurrentPrice > 0 {
			// 市价单使用当前价作为entry参考价
			entryPrice = d.CurrentPrice
			hasEntryPrice = true
		}

		if hasEntryPrice {
			var risk, reward float64
			if d.Action == "open_long" || d.Action == "limit_open_long" {
				risk = entryPrice - d.StopLoss
				reward = d.TP3 - entryPrice
			} else { // open_short 或 limit_open_short
				risk = d.StopLoss - entryPrice
				reward = entryPrice - d.TP3
			}

			if risk <= 0 {
				return fmt.Errorf("止损设置错误，risk必须大于0")
			}
			if reward <= 0 {
				return fmt.Errorf("止盈设置错误，reward必须大于0")
			}

			rr := reward / risk
			minRR := 1.8 // 可以做成配置项，默认1.8
			if rr < minRR {
				return fmt.Errorf("盈亏比过低，RR=%.2f（要求≥%.1f），risk=%.4f, reward=%.4f，请收紧止损或降低止盈预期",
					rr, minRR, risk, reward)
			}
		}

		// 2) position_size_usd 现在语义 = 实际保证金，必须 > 0
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("保证金(position_size_usd)必须大于0: %.2f", d.PositionSizeUSD)
		}

		// 边界稳定策略：先round到2位小数，避免浮点误差
		margin := math.Round(d.PositionSizeUSD*100) / 100
		lev := float64(d.Leverage)
		notional := margin * lev // 真正在交易所上的名义价值

		// 2.3) risk_usd 真实性校验（防止"嘴上风控"）
		if hasEntryPrice && d.RiskUSD > 0 {
			riskPct := math.Abs((entryPrice - d.StopLoss) / entryPrice)
			expectedRiskUsd := notional * riskPct

			// 使用8%容差或最小0.5U
			tolerance := math.Max(expectedRiskUsd*0.08, 0.5)
			diff := math.Abs(d.RiskUSD - expectedRiskUsd)

			if diff > tolerance {
				return fmt.Errorf("风控不真实：decision.risk_usd=%.2f，与实际计算risk=%.2f差距过大(容差%.2f)，请修正risk_usd=riskPct(%.2f%%)*notional(%.2f)",
					d.RiskUSD, expectedRiskUsd, tolerance, riskPct*100, notional)
			}
		}

		// 2.4) "止损必须先于爆仓"校验（全仓+高杠杆必备）
		if hasEntryPrice {
			riskPct := math.Abs((entryPrice - d.StopLoss) / entryPrice)
			maxSafeRiskPct := 0.85 / float64(d.Leverage) // 近似安全约束

			if riskPct >= maxSafeRiskPct {
				return fmt.Errorf("止损无效：riskPct=%.2f%% ≥ 安全上限%.2f%%(0.85/%dx)，止损将在爆仓前触发，请降低杠杆或收紧止损",
					riskPct*100, maxSafeRiskPct*100, d.Leverage)
			}
		}

		// 2.1) 币安最小名义要求：notional >= 20
		if notional < 20 {
			return fmt.Errorf("%s 开仓名义价值过小，要求≥20U，当前≈%.2f（保证金≈%.2f 杠杆=%d）",
				d.Symbol, notional, margin, d.Leverage)
		}

		// 2.2) 单币种名义上限（暂时关闭，用于排查保证金与名义限制冲突）
		if enforceNotionalLimit {
			tolerance := maxNotional * 0.01
			if notional > maxNotional+tolerance {
				if isBlueChip {
					return fmt.Errorf(
						"BTC/ETH/SOLUSDT单币种名义价值不能超过%.0f USDT，实际≈%.0f（保证金≈%.2f 杠杆=%d）",
						maxNotional, notional, margin, d.Leverage,
					)
				} else {
					return fmt.Errorf(
						"山寨币单币种名义价值不能超过%.0f USDT，实际≈%.0f（保证金≈%.2f 杠杆=%d）",
						maxNotional, notional, margin, d.Leverage,
					)
				}
			}
		}

		// 3) 单笔保证金 5%~13%（非补仓才卡）- 边界稳定策略
		if !d.IsAddOn {
			minMargin := math.Round((accountEquity*0.05)*100) / 100 // round到2位小数
			maxMargin := math.Round((accountEquity*0.13)*100) / 100 // round到2位小数

			// 边界稳定容差：净值的0.3%或固定0.2U，取较大者
			clampTolerance := accountEquity * 0.003 // 0.3%容差用于自动夹紧
			if clampTolerance < 0.2 {
				clampTolerance = 0.2 // 最小0.2U
			}

			// 硬fail容差：超出此范围才真正报错
			hardFailTolerance := accountEquity * 0.01 // 1%硬容差
			if hardFailTolerance < 1.0 {
				hardFailTolerance = 1.0 // 最小1U
			}

			// 边界稳定策略：自动夹紧到边界
			if margin < minMargin && margin >= minMargin-clampTolerance {
				log.Printf("⚠️ 边界稳定: %s position_size_usd %.2f 略低于min %.2f，自动夹紧到min", d.Symbol, margin, minMargin)
				d.PositionSizeUSD = minMargin // 更新到min边界
				margin = minMargin            // 更新局部变量用于后续计算
			} else if margin < minMargin-clampTolerance {
				return fmt.Errorf("开仓保证金过小，要求≥账户的5%% (%.2f)，当前保证金≈%.2f (超出容差%.2f)", minMargin, margin, hardFailTolerance)
			}

			if margin > maxMargin && margin <= maxMargin+clampTolerance {
				log.Printf("⚠️ 边界稳定: %s position_size_usd %.2f 略高于max %.2f，自动夹紧到max", d.Symbol, margin, maxMargin)
				d.PositionSizeUSD = maxMargin // 更新到max边界
				margin = maxMargin            // 更新局部变量用于后续计算
			} else if margin > maxMargin+clampTolerance {
				return fmt.Errorf("开仓保证金过大，要求≤账户的13%% (%.2f)，当前保证金≈%.2f (超出容差%.2f)", maxMargin, margin, hardFailTolerance)
			}
		}
		// 补仓就直接放过保证金区间这一步

		// 4) 止损/止盈一致性校验
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}

		// 开仓必须提供完整的TP分段
		if d.TP1 <= 0 || d.TP2 <= 0 || d.TP3 <= 0 {
			return fmt.Errorf("开仓必须提供完整的TP分段(tp1/tp2/tp3)，当前tp1=%.2f, tp2=%.2f, tp3=%.2f", d.TP1, d.TP2, d.TP3)
		}

		// take_profit必须等于tp3
		if d.TakeProfit != d.TP3 {
			return fmt.Errorf("take_profit必须等于tp3，当前take_profit=%.2f, tp3=%.2f，请修正take_profit=tp3", d.TakeProfit, d.TP3)
		}

		// 限价单价格合理性校验（软校验）- 已移到RR校验中处理

		// TP分段顺序校验
		if d.Action == "open_long" || d.Action == "limit_open_long" {
			// 多单：stop_loss < entry参考价 < tp1 < tp2 < tp3
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
			if hasEntryPrice && !(d.StopLoss < entryPrice && entryPrice < d.TP1 && d.TP1 < d.TP2 && d.TP2 < d.TP3) {
				return fmt.Errorf("多单价格顺序错误：stop_loss(%.2f) < entry_price(%.2f) < tp1(%.2f) < tp2(%.2f) < tp3(%.2f) 不满足，请重新排序TP分段", d.StopLoss, entryPrice, d.TP1, d.TP2, d.TP3)
			}
			if !hasEntryPrice && !(d.TP1 < d.TP2 && d.TP2 < d.TP3) {
				return fmt.Errorf("多单TP分段顺序错误：tp1(%.2f) < tp2(%.2f) < tp3(%.2f) 不满足，请重新排序TP分段", d.TP1, d.TP2, d.TP3)
			}
		} else if d.Action == "open_short" || d.Action == "limit_open_short" {
			// 空单：stop_loss > entry参考价 > tp1 > tp2 > tp3
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
			if hasEntryPrice && !(d.StopLoss > entryPrice && entryPrice > d.TP1 && d.TP1 > d.TP2 && d.TP2 > d.TP3) {
				return fmt.Errorf("空单价格顺序错误：stop_loss(%.2f) > entry_price(%.2f) > tp1(%.2f) > tp2(%.2f) > tp3(%.2f) 不满足，请重新排序TP分段", d.StopLoss, entryPrice, d.TP1, d.TP2, d.TP3)
			}
			if !hasEntryPrice && !(d.TP1 > d.TP2 && d.TP2 > d.TP3) {
				return fmt.Errorf("空单TP分段顺序错误：tp1(%.2f) > tp2(%.2f) > tp3(%.2f) 不满足，请重新排序TP分段", d.TP1, d.TP2, d.TP3)
			}
		}

		// 5) 分层风控校验（根据账户净值自动切换模式）
		if err := validateRiskManagement(d, accountEquity, config); err != nil {
			return err
		}

		// 6) TP1/TP2/TP3 必须给
		if d.TP1 <= 0 || d.TP2 <= 0 || d.TP3 <= 0 {
			return fmt.Errorf("开仓必须提供 tp1、tp2、tp3 且 > 0")
		}

		// 7) 限价单额外校验
		if d.Action == "limit_open_long" || d.Action == "limit_open_short" {
			if d.LimitPrice <= 0 {
				return fmt.Errorf("限价单必须提供 limit_price 且 > 0")
			}

			// 限价单价格合理性检查
			if d.Action == "limit_open_long" {
				// 多单：限价应该低于当前市价（等待回调入场）
				// 这里不做硬性校验，由AI判断
			} else {
				// 空单：限价应该高于当前市价（等待反弹入场）
				// 这里不做硬性校验，由AI判断
			}
		}

	case "cancel_limit_order":
		if d.OrderID == 0 {
			return fmt.Errorf("cancel_limit_order 需要提供 order_id")
		}
		if d.Reasoning == "" {
			return fmt.Errorf("cancel_limit_order 需要给出取消理由")
		}

	case "update_stop_loss":
		if d.NewStopLoss <= 0 {
			return fmt.Errorf("update_stop_loss 需要提供 new_stop_loss")
		}
		if d.Reasoning == "" {
			return fmt.Errorf("update_stop_loss 需要给出调整理由")
		}

	case "update_take_profit":
		if d.NewTakeProfit <= 0 {
			return fmt.Errorf("update_take_profit 需要提供 new_take_profit")
		}
		if d.Reasoning == "" {
			return fmt.Errorf("update_take_profit 需要给出调整理由")
		}

	case "close_long", "close_short", "hold", "wait":
		if d.Reasoning == "" {
			return fmt.Errorf("%s 需要给出reasoning说明", d.Action)
		}

	case "partial_close_long", "partial_close_short":
		// 部分平仓必须提供 close_quantity 或 close_ratio
		if d.CloseQuantity <= 0 && d.CloseRatio <= 0 {
			return fmt.Errorf("%s 必须提供 close_quantity 或 close_ratio（0-1 或 0-100）", d.Action)
		}
		// close_ratio 必须在合理范围内（0-1 或 0-100）
		if d.CloseRatio > 0 {
			if d.CloseRatio > 100 {
				return fmt.Errorf("%s 的 close_ratio 不能超过 100（表示100%%）", d.Action)
			}
		}
		if d.Reasoning == "" {
			return fmt.Errorf("%s 需要给出reasoning说明（必须包含：到达点位、平仓比例、剩余仓位计划）", d.Action)
		}
	}

	if err := validateIntervention(d); err != nil {
		return err
	}

	return nil
}

func validateIntervention(d *Decision) error {
	// 将 "normal" 视为空字符串（正常操作，不需要特殊介入）
	if d.InterventionLevel == "" || d.InterventionLevel == "normal" {
		d.InterventionLevel = "" // 标准化为空字符串
		return nil
	}

	if d.InterventionLevel != interventionLevelExtreme {
		return fmt.Errorf("不支持的 intervention_level: %s", d.InterventionLevel)
	}

	if d.Action != "close_long" && d.Action != "close_short" && d.Action != "update_stop_loss" {
		return fmt.Errorf("intervention_level=extreme 仅能配合 close_long/close_short/update_stop_loss，当前 action=%s", d.Action)
	}

	if d.Confidence < 85 {
		return fmt.Errorf("极端介入必须提供 confidence ≥85，当前为 %d", d.Confidence)
	}

	if !strings.Contains(d.Reasoning, extremeInterventionTag) {
		return fmt.Errorf("极端介入 reasoning 必须包含“%s”标记并说明多周期确认", extremeInterventionTag)
	}

	return nil
}
