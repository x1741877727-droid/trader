package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultBaseDir 是Close Review文件的默认根目录
	DefaultBaseDir = "close_reviews"
)

// TradeSnapshot 记录单笔交易的输入输出快照
type TradeSnapshot struct {
	TradeID        string    `json:"trade_id"`
	Symbol         string    `json:"symbol"`
	Side           string    `json:"side"`
	EntryTime      time.Time `json:"entry_time"`
	ExitTime       time.Time `json:"exit_time"`
	EntryPrice     float64   `json:"entry_price"`
	ExitPrice      float64   `json:"exit_price"`
	Quantity       float64   `json:"quantity"`
	Leverage       int       `json:"leverage"`
	RiskUSD        float64   `json:"risk_usd"`
	PnL            float64   `json:"pnl"`
	PnLPct         float64   `json:"pnl_pct"`
	HoldingMinutes int       `json:"holding_minutes"`
	StopLoss       float64   `json:"stop_loss,omitempty"`
	TakeProfit     float64   `json:"take_profit,omitempty"`
}

// PositionLifecycleEntry 记录交易生命周期内的关键决策
type PositionLifecycleEntry struct {
	CycleNumber int                    `json:"cycle_number"`
	Timestamp   time.Time              `json:"timestamp"`
	Action      string                 `json:"action"`
	Reasoning   string                 `json:"reasoning"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// MarketContextAtClose 记录平仓时的账户与市场快照
type MarketContextAtClose struct {
	AccountState map[string]interface{} `json:"account_state,omitempty"`
	MarketData   map[string]interface{} `json:"market_data,omitempty"`
	RuntimeMeta  map[string]interface{} `json:"runtime_meta,omitempty"`
}

// CloseReviewActionItem 针对复盘输出的改进行动
type CloseReviewActionItem struct {
	Owner string `json:"owner"`
	Item  string `json:"item"`
	Due   string `json:"due"`
}

// CloseReviewRecord AI复盘输出
type CloseReviewRecord struct {
	TradeID                   string                  `json:"trade_id"`
	Symbol                    string                  `json:"symbol"`
	Side                      string                  `json:"side"`
	PnL                       float64                 `json:"pnl"`
	PnLPct                    float64                 `json:"pnl_pct"`
	HoldingMinutes            int                     `json:"holding_minutes"`
	RiskScore                 int                     `json:"risk_score"`
	ExecutionScore            int                     `json:"execution_score"`
	SignalScore               int                     `json:"signal_score"`
	Summary                   string                  `json:"summary"`
	WhatWentWell              []string                `json:"what_went_well"`
	Improvements              []string                `json:"improvements"`
	RootCause                 string                  `json:"root_cause"`
	ExtremeInterventionReview string                  `json:"extreme_intervention_review"`
	ActionItems               []CloseReviewActionItem `json:"action_items"`
	Confidence                int                     `json:"confidence"`
	Reasoning                 string                  `json:"reasoning"`
}

// CloseReviewFile 落盘的复盘文件结构
type CloseReviewFile struct {
	Version            int                      `json:"version"`
	Timestamp          time.Time                `json:"timestamp"`
	TradeSnapshot      TradeSnapshot            `json:"trade_snapshot"`
	PositionLifecycle  []PositionLifecycleEntry `json:"position_lifecycle"`
	MarketContext      MarketContextAtClose     `json:"market_context"`
	CoTTrace           string                   `json:"cot_trace"`
	Review             CloseReviewRecord        `json:"review"`
	AdditionalMetadata map[string]interface{}   `json:"additional_metadata,omitempty"`
}

// SaveCloseReview 将CloseReview写入磁盘并返回路径
func SaveCloseReview(baseDir, traderID string, file *CloseReviewFile) (string, error) {
	if file == nil {
		return "", fmt.Errorf("close review payload is nil")
	}

	if file.TradeSnapshot.TradeID == "" {
		return "", fmt.Errorf("trade_id 不能为空")
	}

	if file.Timestamp.IsZero() {
		file.Timestamp = time.Now()
	}
	if file.Version == 0 {
		file.Version = 1
	}

	if baseDir == "" {
		baseDir = DefaultBaseDir
	}

	traderDir := filepath.Join(baseDir, traderID)
	if err := os.MkdirAll(traderDir, 0o755); err != nil {
		return "", fmt.Errorf("创建 Close Review 目录失败: %w", err)
	}

	filename := fmt.Sprintf("%s.json", file.TradeSnapshot.TradeID)
	targetPath := filepath.Join(traderDir, filename)

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化 Close Review 失败: %w", err)
	}

	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		return "", fmt.Errorf("写入 Close Review 文件失败: %w", err)
	}

	return targetPath, nil
}

// LoadCloseReview 从磁盘读取Close Review
func LoadCloseReview(path string) (*CloseReviewFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 Close Review 文件失败: %w", err)
	}

	var file CloseReviewFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("解析 Close Review 文件失败: %w", err)
	}

	return &file, nil
}
