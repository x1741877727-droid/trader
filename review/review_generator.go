package review

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/logger"
	"nofx/mcp"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReviewGenerator 复盘生成器
type ReviewGenerator struct {
	mcpClient *mcp.Client
}

// NewReviewGenerator 创建复盘生成器
func NewReviewGenerator(mcpClient *mcp.Client) *ReviewGenerator {
	return &ReviewGenerator{
		mcpClient: mcpClient,
	}
}

// ExtractLossTrades 从决策日志中提取亏损的交易
func ExtractLossTrades(decisionLogger *logger.DecisionLogger, limit int) ([]TradeInfo, error) {
	records, err := decisionLogger.GetLatestRecords(limit)
	if err != nil {
		return nil, fmt.Errorf("获取决策记录失败: %w", err)
	}

	// 追踪开仓和平仓的配对
	type OpenPosition struct {
		Symbol      string
		Side        string
		EntryPrice  float64
		EntryTime   time.Time
		CycleNumber int
		Quantity    float64
		Leverage    int
		StopLoss    float64
		TakeProfit  float64
		Reasoning   string
		Metadata    map[string]interface{}
	}

	openPositions := make(map[string]*OpenPosition) // key: symbol_side
	var lossTrades []TradeInfo

	// 从旧到新遍历记录
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]

		for _, decision := range record.Decisions {
			// 处理开仓
			if strings.HasPrefix(decision.Action, "open_") {
				side := "long"
				if strings.Contains(decision.Action, "short") {
					side = "short"
				}

				posKey := fmt.Sprintf("%s_%s", decision.Symbol, side)
				openPositions[posKey] = &OpenPosition{
					Symbol:      decision.Symbol,
					Side:        side,
					EntryPrice:  decision.Price,
					EntryTime:   record.Timestamp,
					CycleNumber: record.CycleNumber,
					Quantity:    decision.Quantity,
					Leverage:    decision.Leverage,
				}

				// 尝试从决策JSON中提取止损止盈
				if record.DecisionJSON != "" {
					var decisions []map[string]interface{}
					if err := json.Unmarshal([]byte(record.DecisionJSON), &decisions); err == nil {
						for _, d := range decisions {
							if s, ok := d["symbol"].(string); ok && s == decision.Symbol {
								if sl, ok := d["stop_loss"].(float64); ok {
									openPositions[posKey].StopLoss = sl
								}
								if tp, ok := d["take_profit"].(float64); ok {
									openPositions[posKey].TakeProfit = tp
								}
								if r, ok := d["reasoning"].(string); ok {
									openPositions[posKey].Reasoning = r
								}
							}
						}
					}
				}
			}

			// 处理平仓
			if strings.HasPrefix(decision.Action, "close_") {
				side := "long"
				if strings.Contains(decision.Action, "short") {
					side = "short"
				}

				posKey := fmt.Sprintf("%s_%s", decision.Symbol, side)
				openPos, exists := openPositions[posKey]
				if !exists {
					continue
				}

				// 计算盈亏
				exitPrice := decision.Price
				var pnl float64
				var pnlPct float64

				if side == "long" {
					pnl = (exitPrice - openPos.EntryPrice) * openPos.Quantity
					pnlPct = ((exitPrice - openPos.EntryPrice) / openPos.EntryPrice) * 100
				} else {
					pnl = (openPos.EntryPrice - exitPrice) * openPos.Quantity
					pnlPct = ((openPos.EntryPrice - exitPrice) / openPos.EntryPrice) * 100
				}

				// 只记录亏损的交易
				if pnl < 0 {
					holdingMinutes := int(record.Timestamp.Sub(openPos.EntryTime).Minutes())

					// 构建交易ID
					tradeID := fmt.Sprintf("%s_%d_%d",
						decision.Symbol,
						openPos.EntryTime.Unix(),
						record.Timestamp.Unix())

					lossTrades = append(lossTrades, TradeInfo{
						TradeID:        tradeID,
						Symbol:         decision.Symbol,
						Side:           side,
						EntryPrice:     openPos.EntryPrice,
						ExitPrice:      exitPrice,
						EntryTime:      openPos.EntryTime,
						ExitTime:       record.Timestamp,
						Quantity:       openPos.Quantity,
						Leverage:       openPos.Leverage,
						PnL:            pnl,
						PnLPct:         pnlPct,
						HoldingMinutes: holdingMinutes,
						StopLoss:       openPos.StopLoss,
						TakeProfit:     openPos.TakeProfit,
						EntryCycle:     openPos.CycleNumber,
						ExitCycle:      record.CycleNumber,
						EntryReasoning: openPos.Reasoning,
					})
				}

				// 删除已平仓的持仓
				delete(openPositions, posKey)
			}
		}
	}

	return lossTrades, nil
}

// FindTradeByID 从trade_id解析并查找对应的交易（不限制是否亏损）
// trade_id格式: SYMBOL_ENTRY_TIMESTAMP_EXIT_TIMESTAMP
func FindTradeByID(decisionLogger *logger.DecisionLogger, tradeID string, limit int) (*TradeInfo, error) {
	// 解析trade_id: SYMBOL_ENTRY_TIMESTAMP_EXIT_TIMESTAMP
	parts := strings.Split(tradeID, "_")
	if len(parts) < 3 {
		return nil, fmt.Errorf("无效的trade_id格式: %s", tradeID)
	}

	// 提取symbol（可能包含下划线，如SOLUSDT）
	// 最后两个部分应该是时间戳
	var symbol string
	var entryTimestamp, exitTimestamp int64
	var err error

	// 尝试从后往前解析：最后两个应该是时间戳
	if len(parts) >= 3 {
		exitTimestamp, err = parseInt64(parts[len(parts)-1])
		if err != nil {
			return nil, fmt.Errorf("无法解析exit_timestamp: %v", err)
		}
		entryTimestamp, err = parseInt64(parts[len(parts)-2])
		if err != nil {
			return nil, fmt.Errorf("无法解析entry_timestamp: %v", err)
		}
		// symbol是前面所有部分的组合
		symbol = strings.Join(parts[:len(parts)-2], "_")
	} else {
		return nil, fmt.Errorf("trade_id格式错误，需要至少3部分: %s", tradeID)
	}

	entryTime := time.Unix(entryTimestamp, 0)
	exitTime := time.Unix(exitTimestamp, 0)

	// 获取决策记录（使用较大的limit以确保能找到）
	records, err := decisionLogger.GetLatestRecords(limit)
	if err != nil {
		return nil, fmt.Errorf("获取决策记录失败: %w", err)
	}

	log.Printf("🔍 查找交易 %s: symbol=%s, entryTime=%s (%d), exitTime=%s (%d), 记录数=%d",
		tradeID, symbol, entryTime.Format("2006-01-02 15:04:05"), entryTimestamp,
		exitTime.Format("2006-01-02 15:04:05"), exitTimestamp, len(records))
	
	// 统计信息
	var symbolOpenCount, symbolCloseCount int
	var candidateMatches []string

	// 追踪开仓和平仓的配对
	type OpenPosition struct {
		Symbol      string
		Side        string
		EntryPrice  float64
		EntryTime   time.Time
		CycleNumber int
		Quantity    float64
		Leverage    int
		StopLoss    float64
		TakeProfit  float64
		Reasoning   string
	}

	// 候选匹配信息（用于在只有一个候选时放宽匹配条件）
	type CandidateMatch struct {
		OpenPos     *OpenPosition
		ExitPrice     float64
		ExitTime    time.Time
		ExitCycle   int
		Side        string
		EntryDiff   int64
		ExitDiff    int64
	}

	var bestCandidate *CandidateMatch
	var bestCandidateDiff int64 = 999999

	openPositions := make(map[string]*OpenPosition) // key: symbol_side

	// 从旧到新遍历记录，查找匹配的交易（从前往后遍历）
	for i := 0; i < len(records); i++ {
		record := records[i]

		for _, decision := range record.Decisions {
			// 处理开仓
			if strings.HasPrefix(decision.Action, "open_") {
				side := "long"
				if strings.Contains(decision.Action, "short") {
					side = "short"
				}

				// 统计目标symbol的开仓数
				if decision.Symbol == symbol {
					symbolOpenCount++
				}

				posKey := fmt.Sprintf("%s_%s", decision.Symbol, side)
				openPositions[posKey] = &OpenPosition{
					Symbol:      decision.Symbol,
					Side:        side,
					EntryPrice:  decision.Price,
					EntryTime:   record.Timestamp,
					CycleNumber: record.CycleNumber,
					Quantity:    decision.Quantity,
					Leverage:    decision.Leverage,
				}

				// 尝试从决策JSON中提取止损止盈
				if record.DecisionJSON != "" {
					var decisions []map[string]interface{}
					if err := json.Unmarshal([]byte(record.DecisionJSON), &decisions); err == nil {
						for _, d := range decisions {
							if s, ok := d["symbol"].(string); ok && s == decision.Symbol {
								if sl, ok := d["stop_loss"].(float64); ok {
									openPositions[posKey].StopLoss = sl
								}
								if tp, ok := d["take_profit"].(float64); ok {
									openPositions[posKey].TakeProfit = tp
								}
								if r, ok := d["reasoning"].(string); ok {
									openPositions[posKey].Reasoning = r
								}
							}
						}
					}
				}
			}

			// 处理平仓
			if strings.HasPrefix(decision.Action, "close_") {
				side := "long"
				if strings.Contains(decision.Action, "short") {
					side = "short"
				}

				posKey := fmt.Sprintf("%s_%s", decision.Symbol, side)
				openPos, exists := openPositions[posKey]
				if !exists {
					continue
				}

				// 检查symbol是否匹配
				if decision.Symbol == symbol {
					symbolCloseCount++
					
					// 检查时间戳是否匹配（允许60秒的误差，因为可能有精度问题或时间同步问题）
					entryTimeDiff := abs(entryTime.Unix() - openPos.EntryTime.Unix())
					exitTimeDiff := abs(exitTime.Unix() - record.Timestamp.Unix())

					// 记录调试信息（对于所有匹配symbol的交易）
					log.Printf("🔍 候选交易: symbol=%s, entryTimeDiff=%d, exitTimeDiff=%d, entryTime=%s (%d), recordEntryTime=%s (%d), exitTime=%s (%d), recordExitTime=%s (%d)",
						decision.Symbol, entryTimeDiff, exitTimeDiff,
						entryTime.Format("2006-01-02 15:04:05"), entryTimestamp,
						openPos.EntryTime.Format("2006-01-02 15:04:05"), openPos.EntryTime.Unix(),
						exitTime.Format("2006-01-02 15:04:05"), exitTimestamp,
						record.Timestamp.Format("2006-01-02 15:04:05"), record.Timestamp.Unix())
					
					// 记录候选匹配（放宽容差到600秒，以便记录更多候选）
					if entryTimeDiff <= 600 || exitTimeDiff <= 600 {
						candidateMatches = append(candidateMatches, fmt.Sprintf("平仓候选: cycle=%d, entryDiff=%d, exitDiff=%d, entryTime=%s, exitTime=%s",
							record.CycleNumber, entryTimeDiff, exitTimeDiff,
							openPos.EntryTime.Format("2006-01-02 15:04:05"),
							record.Timestamp.Format("2006-01-02 15:04:05")))
						
						// 保存最佳候选匹配（时间戳差异最小的）
						totalDiff := entryTimeDiff + exitTimeDiff
						if totalDiff < bestCandidateDiff {
							bestCandidateDiff = totalDiff
							bestCandidate = &CandidateMatch{
								OpenPos:   openPos,
								ExitPrice: decision.Price,
								ExitTime:  record.Timestamp,
								ExitCycle: record.CycleNumber,
								Side:      side,
								EntryDiff: entryTimeDiff,
								ExitDiff:  exitTimeDiff,
							}
						}
					}
					
					// 放宽时间容差到180秒，因为决策记录的时间戳可能和交易ID中的时间戳有差异
					// 这样可以处理时间同步问题或精度问题
					if entryTimeDiff <= 180 && exitTimeDiff <= 180 {
						// 找到匹配的交易
						log.Printf("✓ 找到匹配的交易 %s: entryTimeDiff=%d, exitTimeDiff=%d", tradeID, entryTimeDiff, exitTimeDiff)
						exitPrice := decision.Price
						var pnl float64
						var pnlPct float64

						if side == "long" {
							pnl = (exitPrice - openPos.EntryPrice) * openPos.Quantity
							pnlPct = ((exitPrice - openPos.EntryPrice) / openPos.EntryPrice) * 100
						} else {
							pnl = (openPos.EntryPrice - exitPrice) * openPos.Quantity
							pnlPct = ((openPos.EntryPrice - exitPrice) / openPos.EntryPrice) * 100
						}

						holdingMinutes := int(record.Timestamp.Sub(openPos.EntryTime).Minutes())

						return &TradeInfo{
							TradeID:        tradeID,
							Symbol:         decision.Symbol,
							Side:           side,
							EntryPrice:     openPos.EntryPrice,
							ExitPrice:      exitPrice,
							EntryTime:      openPos.EntryTime,
							ExitTime:       record.Timestamp,
							Quantity:       openPos.Quantity,
							Leverage:       openPos.Leverage,
							PnL:            pnl,
							PnLPct:         pnlPct,
							HoldingMinutes: holdingMinutes,
							StopLoss:       openPos.StopLoss,
							TakeProfit:     openPos.TakeProfit,
							EntryCycle:     openPos.CycleNumber,
							ExitCycle:      record.CycleNumber,
							EntryReasoning: openPos.Reasoning,
						}, nil
					}
				}

				// 删除已平仓的持仓
				delete(openPositions, posKey)
			}
		}
	}

	// 输出统计信息
	log.Printf("📊 查找统计: %s开仓数=%d, 平仓数=%d, 候选匹配数=%d", symbol, symbolOpenCount, symbolCloseCount, len(candidateMatches))
	if len(candidateMatches) > 0 {
		log.Printf("🔍 候选匹配详情:")
		for _, match := range candidateMatches {
			log.Printf("  - %s", match)
		}
	}

	// 如果只有一个候选匹配，并且时间戳差异在合理范围内（600秒内），使用它
	if len(candidateMatches) == 1 && bestCandidate != nil && bestCandidate.EntryDiff <= 600 && bestCandidate.ExitDiff <= 600 {
		log.Printf("ℹ️  只有一个候选匹配，使用最佳候选: entryTimeDiff=%d, exitTimeDiff=%d", bestCandidate.EntryDiff, bestCandidate.ExitDiff)
		exitPrice := bestCandidate.ExitPrice
		var pnl float64
		var pnlPct float64

		if bestCandidate.Side == "long" {
			pnl = (exitPrice - bestCandidate.OpenPos.EntryPrice) * bestCandidate.OpenPos.Quantity
			pnlPct = ((exitPrice - bestCandidate.OpenPos.EntryPrice) / bestCandidate.OpenPos.EntryPrice) * 100
		} else {
			pnl = (bestCandidate.OpenPos.EntryPrice - exitPrice) * bestCandidate.OpenPos.Quantity
			pnlPct = ((bestCandidate.OpenPos.EntryPrice - exitPrice) / bestCandidate.OpenPos.EntryPrice) * 100
		}

		holdingMinutes := int(bestCandidate.ExitTime.Sub(bestCandidate.OpenPos.EntryTime).Minutes())

		return &TradeInfo{
			TradeID:        tradeID,
			Symbol:         symbol,
			Side:           bestCandidate.Side,
			EntryPrice:     bestCandidate.OpenPos.EntryPrice,
			ExitPrice:      exitPrice,
			EntryTime:      bestCandidate.OpenPos.EntryTime,
			ExitTime:       bestCandidate.ExitTime,
			Quantity:        bestCandidate.OpenPos.Quantity,
			Leverage:       bestCandidate.OpenPos.Leverage,
			PnL:            pnl,
			PnLPct:         pnlPct,
			HoldingMinutes: holdingMinutes,
			StopLoss:       bestCandidate.OpenPos.StopLoss,
			TakeProfit:     bestCandidate.OpenPos.TakeProfit,
			EntryCycle:     bestCandidate.OpenPos.CycleNumber,
			ExitCycle:      bestCandidate.ExitCycle,
			EntryReasoning: bestCandidate.OpenPos.Reasoning,
		}, nil
	}

	return nil, fmt.Errorf("未找到匹配的交易: %s (%s开仓数=%d, 平仓数=%d, 候选匹配数=%d)", 
		tradeID, symbol, symbolOpenCount, symbolCloseCount, len(candidateMatches))
}

// parseInt64 解析字符串为int64
func parseInt64(s string) (int64, error) {
	var result int64
	_, err := fmt.Sscanf(s, "%d", &result)
	return result, err
}

// abs 返回int64的绝对值
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// TradeInfo 交易信息
type TradeInfo struct {
	TradeID        string
	Symbol         string
	Side           string
	EntryPrice     float64
	ExitPrice      float64
	EntryTime      time.Time
	ExitTime       time.Time
	Quantity       float64
	Leverage       int
	PnL            float64
	PnLPct         float64
	HoldingMinutes int
	StopLoss       float64
	TakeProfit     float64
	EntryCycle     int
	ExitCycle      int
	EntryReasoning string
}

// GenerateReview 为单个交易生成复盘
func (rg *ReviewGenerator) GenerateReview(trade TradeInfo, decisionLogger *logger.DecisionLogger) (*CloseReviewFile, error) {
	// 获取相关的决策记录
	records, err := decisionLogger.GetLatestRecords(1000)
	if err != nil {
		return nil, fmt.Errorf("获取决策记录失败: %w", err)
	}

	// 提取开仓和平仓周期的记录
	var entryRecord, exitRecord *logger.DecisionRecord
	for _, record := range records {
		if record.CycleNumber == trade.EntryCycle {
			entryRecord = record
		}
		if record.CycleNumber == trade.ExitCycle {
			exitRecord = record
		}
	}

	// 构建交易快照
	tradeSnapshot := TradeSnapshot{
		TradeID:        trade.TradeID,
		Symbol:         trade.Symbol,
		Side:           trade.Side,
		EntryTime:      trade.EntryTime,
		ExitTime:       trade.ExitTime,
		EntryPrice:     trade.EntryPrice,
		ExitPrice:      trade.ExitPrice,
		Quantity:       trade.Quantity,
		Leverage:       trade.Leverage,
		RiskUSD:        trade.Quantity * trade.EntryPrice / float64(trade.Leverage),
		PnL:            trade.PnL,
		PnLPct:         trade.PnLPct,
		HoldingMinutes: trade.HoldingMinutes,
		StopLoss:       trade.StopLoss,
		TakeProfit:     trade.TakeProfit,
	}

	// 构建持仓生命周期
	var lifecycle []PositionLifecycleEntry
	if entryRecord != nil {
		lifecycle = append(lifecycle, PositionLifecycleEntry{
			CycleNumber: entryRecord.CycleNumber,
			Timestamp:   entryRecord.Timestamp,
			Action:      fmt.Sprintf("open_%s", trade.Side),
			Reasoning:   trade.EntryReasoning,
		})
	}
	if exitRecord != nil {
		// 从exitRecord中提取平仓reasoning
		exitReasoning := ""
		if exitRecord.DecisionJSON != "" {
			var decisions []map[string]interface{}
			if err := json.Unmarshal([]byte(exitRecord.DecisionJSON), &decisions); err == nil {
				for _, d := range decisions {
					if s, ok := d["symbol"].(string); ok && s == trade.Symbol {
						if r, ok := d["reasoning"].(string); ok {
							exitReasoning = r
						}
					}
				}
			}
		}
		lifecycle = append(lifecycle, PositionLifecycleEntry{
			CycleNumber: exitRecord.CycleNumber,
			Timestamp:   exitRecord.Timestamp,
			Action:      fmt.Sprintf("close_%s", trade.Side),
			Reasoning:   exitReasoning,
		})
	}

	// 构建市场上下文
	marketContext := MarketContextAtClose{}
	if exitRecord != nil {
		marketContext.AccountState = map[string]interface{}{
			"total_balance":           exitRecord.AccountState.TotalBalance,
			"available_balance":       exitRecord.AccountState.AvailableBalance,
			"total_unrealized_profit": exitRecord.AccountState.TotalUnrealizedProfit,
			"position_count":          exitRecord.AccountState.PositionCount,
			"margin_used_pct":         exitRecord.AccountState.MarginUsedPct,
		}
	}

	// 构建思维链追踪
	var cotTrace strings.Builder
	if entryRecord != nil {
		cotTrace.WriteString(fmt.Sprintf("Cycle%d 入场：%s", entryRecord.CycleNumber, trade.EntryReasoning))
	}
	if exitRecord != nil {
		cotTrace.WriteString(fmt.Sprintf("；Cycle%d 平仓：%s", exitRecord.CycleNumber, lifecycle[len(lifecycle)-1].Reasoning))
	}

	// 构建AI提示词
	prompt := rg.buildReviewPrompt(tradeSnapshot, lifecycle, marketContext, cotTrace.String())

	// 调用AI生成复盘
	reviewRecord, err := rg.callAIForReview(prompt)
	if err != nil {
		return nil, fmt.Errorf("AI生成复盘失败: %w", err)
	}

	// 构建完整的复盘文件
	reviewFile := &CloseReviewFile{
		Version:           1,
		Timestamp:         time.Now(),
		TradeSnapshot:     tradeSnapshot,
		PositionLifecycle: lifecycle,
		MarketContext:     marketContext,
		CoTTrace:          cotTrace.String(),
		Review:            *reviewRecord,
		AdditionalMetadata: map[string]interface{}{
			"source_decision_cycles": []int{trade.EntryCycle, trade.ExitCycle},
			"generated_by":           "ai-auto-review",
		},
	}

	return reviewFile, nil
}

// buildReviewPrompt 构建复盘提示词
func (rg *ReviewGenerator) buildReviewPrompt(
	snapshot TradeSnapshot,
	lifecycle []PositionLifecycleEntry,
	marketContext MarketContextAtClose,
	cotTrace string,
) string {
	var sb strings.Builder

	// 加载复盘模块
	reviewModulePath := filepath.Join("prompts", "modules", "TradeReview.txt")
	reviewModule, err := os.ReadFile(reviewModulePath)
	if err != nil {
		log.Printf("⚠️ 加载TradeReview模块失败，使用默认提示词: %v", err)
		sb.WriteString("请对以下亏损交易进行深度复盘分析。\n\n")
	} else {
		sb.WriteString(string(reviewModule))
		sb.WriteString("\n\n")
	}

	// 添加交易数据
	sb.WriteString("【交易快照】\n")
	sb.WriteString(fmt.Sprintf("交易ID: %s\n", snapshot.TradeID))
	sb.WriteString(fmt.Sprintf("币种: %s\n", snapshot.Symbol))
	sb.WriteString(fmt.Sprintf("方向: %s\n", snapshot.Side))
	sb.WriteString(fmt.Sprintf("入场价: %.2f\n", snapshot.EntryPrice))
	sb.WriteString(fmt.Sprintf("出场价: %.2f\n", snapshot.ExitPrice))
	sb.WriteString(fmt.Sprintf("数量: %.8f\n", snapshot.Quantity))
	sb.WriteString(fmt.Sprintf("杠杆: %d\n", snapshot.Leverage))
	sb.WriteString(fmt.Sprintf("盈亏: %.2f (%.2f%%)\n", snapshot.PnL, snapshot.PnLPct))
	sb.WriteString(fmt.Sprintf("持仓时长: %d分钟\n", snapshot.HoldingMinutes))
	if snapshot.StopLoss > 0 {
		sb.WriteString(fmt.Sprintf("止损: %.2f\n", snapshot.StopLoss))
	}
	if snapshot.TakeProfit > 0 {
		sb.WriteString(fmt.Sprintf("止盈: %.2f\n", snapshot.TakeProfit))
	}
	sb.WriteString("\n")

	// 添加持仓生命周期
	sb.WriteString("【持仓生命周期】\n")
	for _, entry := range lifecycle {
		sb.WriteString(fmt.Sprintf("Cycle %d (%s): %s - %s\n",
			entry.CycleNumber,
			entry.Timestamp.Format("2006-01-02 15:04:05"),
			entry.Action,
			entry.Reasoning))
	}
	sb.WriteString("\n")

	// 添加思维链追踪
	if cotTrace != "" {
		sb.WriteString("【思维链追踪】\n")
		sb.WriteString(cotTrace)
		sb.WriteString("\n\n")
	}

	// 添加输出要求
	sb.WriteString("【输出要求】\n")
	sb.WriteString("请输出JSON格式的CloseReviewRecord，包含以下字段：\n")
	sb.WriteString("- trade_id, symbol, side, pnl, pnl_pct, holding_minutes\n")
	sb.WriteString("- risk_score, execution_score, signal_score (0-100)\n")
	sb.WriteString("- summary (一句话总结)\n")
	sb.WriteString("- what_went_well (至少2条)\n")
	sb.WriteString("- improvements (至少2条)\n")
	sb.WriteString("- root_cause (根本原因分析)\n")
	sb.WriteString("- extreme_intervention_review (如果亏损超过保证金的50%)\n")
	sb.WriteString("- action_items (至少1条，包含owner, item, due)\n")
	sb.WriteString("- confidence (0-100)\n")
	sb.WriteString("- reasoning (详细推理过程)\n")
	sb.WriteString("\n请直接输出JSON，不要包含markdown代码块。\n")

	return sb.String()
}

// callAIForReview 调用AI生成复盘
func (rg *ReviewGenerator) callAIForReview(prompt string) (*CloseReviewRecord, error) {
	// 调用MCP客户端（使用空system prompt，所有内容都在user prompt中）
	response, err := rg.mcpClient.CallWithMessages("", prompt)
	if err != nil {
		return nil, fmt.Errorf("调用AI失败: %w", err)
	}

	// 解析JSON响应
	var reviewRecord CloseReviewRecord
	if err := json.Unmarshal([]byte(response), &reviewRecord); err != nil {
		// 尝试提取JSON（可能被markdown包裹）
		jsonStr := extractJSONFromResponse(response)
		if err := json.Unmarshal([]byte(jsonStr), &reviewRecord); err != nil {
			return nil, fmt.Errorf("解析AI响应失败: %w, 响应: %s", err, response)
		}
	}

	return &reviewRecord, nil
}

// extractJSONFromResponse 从响应中提取JSON
func extractJSONFromResponse(response string) string {
	// 尝试提取JSON对象
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start >= 0 && end > start {
		return response[start : end+1]
	}
	return response
}
