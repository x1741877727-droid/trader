package decision

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"regexp"
	"strings"
	"time"
)

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
	UpdateTime       int64   `json:"update_time"` // 持仓更新时间戳（毫秒）
	TP1              float64 `json:"tp1,omitempty"`
	TP2              float64 `json:"tp2,omitempty"`
	TP3              float64 `json:"tp3,omitempty"`
	TPStage          int     `json:"tp_stage,omitempty"` // 0=还没到, 1=到过tp1, 2=到过tp2, 3=到过tp3
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
	CurrentTime     string                  `json:"current_time"`
	RuntimeMinutes  int                     `json:"runtime_minutes"`
	CallCount       int                     `json:"call_count"`
	Account         AccountInfo             `json:"account"`
	Positions       []PositionInfo          `json:"positions"`
	CandidateCoins  []CandidateCoin         `json:"candidate_coins"`
	MarketDataMap   map[string]*market.Data `json:"-"`
	OITopDataMap    map[string]*OITopData   `json:"-"`
	Performance     interface{}             `json:"-"`
	BTCETHLeverage  int                     `json:"-"`
	AltcoinLeverage int                     `json:"-"`
}

// Decision AI的交易决策
type Decision struct {
	Symbol          string  `json:"symbol"`
	Action          string  `json:"action"` // open_long, open_short, close_long, close_short, hold, wait, update_stop_loss, update_take_profit, limit_open_long, limit_open_short, cancel_limit_order
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`
	// 新增：AI开仓时就能把三段发过来
	TP1 float64 `json:"tp1,omitempty"`
	TP2 float64 `json:"tp2,omitempty"`
	TP3 float64 `json:"tp3,omitempty"`

	// 限价单相关字段
	LimitPrice float64 `json:"limit_price,omitempty"` // 限价单价格
	OrderID    int64   `json:"order_id,omitempty"`    // 取消订单时使用

	NewStopLoss   float64 `json:"new_stop_loss,omitempty"`
	NewTakeProfit float64 `json:"new_take_profit,omitempty"`
	Confidence    int     `json:"confidence,omitempty"`
	RiskUSD       float64 `json:"risk_usd,omitempty"`
	Reasoning     string  `json:"reasoning"`
	IsAddOn       bool    `json:"is_add_on,omitempty"`
}

// FullDecision AI的完整决策
type FullDecision struct {
	SystemPrompt string     `json:"system_prompt"`
	UserPrompt   string     `json:"user_prompt"`
	CoTTrace     string     `json:"cot_trace"`
	Decisions    []Decision `json:"decisions"`
	Timestamp    time.Time  `json:"timestamp"`
}

func GetFullDecision(ctx *Context, mcpClient *mcp.Client) (*FullDecision, error) {
	return GetFullDecisionWithCustomPrompt(ctx, mcpClient, "", false, "")
}

func GetFullDecisionWithCustomPrompt(ctx *Context, mcpClient *mcp.Client, customPrompt string, overrideBase bool, templateName string) (*FullDecision, error) {
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}

	systemPrompt := buildSystemPromptWithCustom(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, customPrompt, overrideBase, templateName)
	userPrompt := buildUserPrompt(ctx)

	aiResponse, err := mcpClient.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
	if err != nil {
		return nil, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.SystemPrompt = systemPrompt
	decision.UserPrompt = userPrompt
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

func buildSystemPromptWithCustom(accountEquity float64, btcEthLeverage, altcoinLeverage int, customPrompt string, overrideBase bool, templateName string) string {
	if overrideBase && customPrompt != "" {
		return customPrompt
	}

	basePrompt := buildSystemPrompt(accountEquity, btcEthLeverage, altcoinLeverage, templateName)
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

func buildSystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int, templateName string) string {
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
	sb.WriteString("# 输出格式\n\n")
	sb.WriteString("第一步: 思维链（纯文本），说明本轮经过了哪些层的检查。\n\n")
	sb.WriteString("第二步: JSON决策数组\n\n")
	sb.WriteString("总持仓数不能大于三个:\n")
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
	sb.WriteString("- `action`: open_long | open_short | limit_open_long | limit_open_short | cancel_limit_order | close_long | close_short | hold | wait | update_stop_loss | update_take_profit\n")
	sb.WriteString("- `confidence`: 0-100（开仓建议≥75）\n")
	sb.WriteString("- 市价开仓（open_long/open_short）必填: leverage, position_size_usd, stop_loss, take_profit, tp1, tp2, tp3, confidence, risk_usd, reasoning\n")
	sb.WriteString("- 限价开仓（limit_open_long/limit_open_short）额外必填: limit_price（挂单价格，多单低于市价、空单高于市价）\n")
	sb.WriteString("- 取消限价单（cancel_limit_order）必填: order_id, reasoning\n\n")
	sb.WriteString("限价单示例:\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\"symbol\": \"ETHUSDT\", \"action\": \"limit_open_long\", \"limit_price\": 3850, \"leverage\": 75, \"position_size_usd\": 6.0, \"stop_loss\": 3820, \"tp1\": 3900, \"tp2\": 3950, \"tp3\": 4000, \"take_profit\": 4000, \"confidence\": 82, \"reasoning\": \"回调到斐波38.2%+订单块支撑，等待入场\"}\n")
	sb.WriteString("```\n\n")

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

	sb.WriteString(fmt.Sprintf("## 候选币种 (%d个)\n\n", len(ctx.MarketDataMap)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := ""
		if len(coin.Sources) > 1 {
			sourceTags = " (AI500+OI_Top双重信号)"
		} else if len(coin.Sources) == 1 && coin.Sources[0] == "oi_top" {
			sourceTags = " (OI_Top持仓增长)"
		}

		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
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
	sb.WriteString("现在请分析并输出决策（思维链 + JSON）\n")

	return sb.String()
}

func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int) (*FullDecision, error) {
	cotTrace := extractCoTTrace(aiResponse)

	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("提取决策失败: %w\n\n=== AI思维链分析 ===\n%s", err, cotTrace)
	}

	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}, fmt.Errorf("决策验证失败: %w\n\n=== AI思维链分析 ===\n%s", err, cotTrace)
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: decisions,
	}, nil
}

func extractCoTTrace(response string) string {
	jsonStart := strings.Index(response, "[")
	if jsonStart > 0 {
		return strings.TrimSpace(response[:jsonStart])
	}
	return strings.TrimSpace(response)
}

func extractDecisions(response string) ([]Decision, error) {
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

	arrayStart := strings.Index(response, "[")
	if arrayStart == -1 {
		return nil, fmt.Errorf("无法找到JSON数组起始\n完整响应（前500字符）: %s", truncateString(response, 500))
	}

	arrayEnd := findMatchingBracket(response, arrayStart)
	if arrayEnd == -1 {
		return nil, fmt.Errorf("无法找到JSON数组结束\n响应片段: %s", truncateString(response[arrayStart:], 200))
	}

	jsonContent := strings.TrimSpace(response[arrayStart : arrayEnd+1])
	jsonContent = fixMissingQuotes(jsonContent)

	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, jsonContent)
	}

	return decisions, nil
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
			// 为值添加引号
			return fmt.Sprintf(`"%s": "%s"%s`, fieldName, value, terminator)
		}
		return match
	})

	return jsonStr
}

func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	for i, decision := range decisions {
		if err := validateDecision(&decision, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

func findMatchingBracket(s string, start int) int {
	if start >= len(s) || s[start] != '[' {
		return -1
	}

	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	// 只保留你现在要的几种 action
	validActions := map[string]bool{
		"open_long":          true,
		"open_short":         true,
		"close_long":         true,
		"close_short":        true,
		"hold":               true,
		"wait":               true,
		"update_stop_loss":   true,
		"update_take_profit": true,
		"limit_open_long":    true,
		"limit_open_short":   true,
		"cancel_limit_order": true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("无效的action: %s", d.Action)
	}

	switch d.Action {
	case "open_long", "open_short", "limit_open_long", "limit_open_short":
		// 1) 按币种分最低/最高杠杆
		minLeverage := 30
		maxLeverage := altcoinLeverage
		maxNotional := accountEquity * 1.5 // 单币种名义上限（山寨）

		isBlueChip := d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" || d.Symbol == "SOLUSDT"
		if isBlueChip {
			minLeverage = 65             // 主流最低杠杆改成65
			maxLeverage = btcEthLeverage // 主流币的上限用 BTC/ETH 的
			maxNotional = accountEquity * 10
		}

		if d.Leverage < minLeverage || d.Leverage > maxLeverage {
			return fmt.Errorf("%s 杠杆必须在 %d-%d 之间，当前: %d", d.Symbol, minLeverage, maxLeverage, d.Leverage)
		}

		// 2) position_size_usd 现在语义 = 实际保证金，必须 > 0
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("保证金(position_size_usd)必须大于0: %.2f", d.PositionSizeUSD)
		}

		margin := d.PositionSizeUSD
		lev := float64(d.Leverage)
		notional := margin * lev // 真正在交易所上的名义价值

		// 2.1) 币安最小名义要求：notional >= 20
		if notional < 20 {
			return fmt.Errorf("%s 开仓名义价值过小，要求≥20U，当前≈%.2f（保证金≈%.2f 杠杆=%d）",
				d.Symbol, notional, margin, d.Leverage)
		}

		// 2.2) 单币种名义上限（用 notional 来卡，而不是用 margin）
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

		// 3) 单笔保证金 5%~13%（非补仓才卡）
		if !d.IsAddOn {
			minMargin := accountEquity * 0.03
			maxMargin := accountEquity * 0.13
			if margin < minMargin {
				return fmt.Errorf("开仓保证金过小，要求≥账户的5%% (%.2f)，当前保证金≈%.2f", minMargin, margin)
			}
			if margin > maxMargin {
				return fmt.Errorf("开仓保证金过大，要求≤账户的13%% (%.2f)，当前保证金≈%.2f", maxMargin, margin)
			}
		}
		// 补仓就直接放过保证金区间这一步

		// 4) 止损/止盈合法性
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}

		// 做多/做空关系
		if d.Action == "open_long" && d.StopLoss >= d.TakeProfit {
			return fmt.Errorf("做多时止损价必须小于止盈价")
		}
		if d.Action == "open_short" && d.StopLoss <= d.TakeProfit {
			return fmt.Errorf("做空时止损价必须大于止盈价")
		}

		// 5) 风险回报比 ≥ 1:2（你原有的逻辑保留）
		var entryPrice float64
		if d.Action == "open_long" {
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2
		} else {
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2
		}

		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == "open_long" {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}

		if riskRewardRatio < 2.0 {
			return fmt.Errorf(
				"风险回报比过低(%.2f:1)，必须≥2.0:1 [风险:%.2f%% 收益:%.2f%%] [止损:%.2f 止盈:%.2f]",
				riskRewardRatio, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit,
			)
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
	}

	return nil
}
