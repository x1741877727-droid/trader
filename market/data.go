package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ExecutionGateConfig 执行门禁配置结构体
type ExecutionGateConfig struct {
	MaxSpreadBpsLimitOnly             float64 // SpreadBps >= 此值时 limit_only
	MaxSpreadBpsNoTrade               float64 // SpreadBps >= 此值时 no_trade
	MaxDepthRatioAbs                  float64 // DepthRatio > 此值时 limit_only
	MinDepthRatioAbs                  float64 // DepthRatio < 此值时 limit_only
	MaxSpreadBpsLimitPreferred        float64
	MinBestNotionalUsdtLimitOnly      float64 // 基于best level的阈值（备用）
	MinBestNotionalUsdtLimitPreferred float64 // 基于best level的阈值（备用）
	MinDepthNotional10LimitOnly       float64 // 基于前10档累计的阈值（优先使用）
	MinDepthNotional10LimitPreferred  float64 // 基于前10档累计的阈值（优先使用）
	NotionalMultiplierLimitOnly       float64 // 计划notional > k×effective_notional 时 limit_only
	NotionalMultiplierNoTrade         float64 // 计划notional > k×effective_notional 时 no_trade
	DefaultModeOnMissing              string
}

// executionGateConfig 包级别的执行门禁配置（需要在程序启动时设置）
var executionGateConfig = ExecutionGateConfig{
	MaxSpreadBpsLimitOnly:             25.0,    // 0.25% - SpreadBps >= 此值时 limit_only
	MaxSpreadBpsNoTrade:               40.0,    // 0.4% - SpreadBps >= 此值时 no_trade
	MaxDepthRatioAbs:                  3.0,     // DepthRatio > 此值时 limit_only
	MinDepthRatioAbs:                  0.33,    // DepthRatio < 此值时 limit_only
	MaxSpreadBpsLimitPreferred:        15.0,    // 0.15%
	MinBestNotionalUsdtLimitOnly:      10000.0, // 10K USDT - best level备用阈值
	MinBestNotionalUsdtLimitPreferred: 50000.0, // 50K USDT - best level备用阈值
	MinDepthNotional10LimitOnly:       200000.0, // 200K USDT - 前10档累计，极差市场强制限价
	MinDepthNotional10LimitPreferred:  500000.0, // 500K USDT - 前10档累计，一般差市场建议限价
	NotionalMultiplierLimitOnly:       8.0,     // 计划notional > 8×effective_notional 时 limit_only
	NotionalMultiplierNoTrade:         15.0,    // 计划notional > 15×effective_notional 时 no_trade
	DefaultModeOnMissing:              "limit_only",
}

// SetExecutionGateConfig 设置执行门禁配置（在程序启动时调用）
func SetExecutionGateConfig(config ExecutionGateConfig) {
	executionGateConfig = config
}

// CandleShape 单根K线的几何特征，用于AI识别各种形态
type CandleShape struct {
	Direction     string  `json:"dir"`            // "bull" / "bear" / "doji"
	BodyPct       float64 `json:"body_pct"`       // 实体占整个K线高度的比例 0~1
	UpperWickPct  float64 `json:"upper_wick_pct"` // 上影线占比 0~1
	LowerWickPct  float64 `json:"lower_wick_pct"` // 下影线占比 0~1
	RangeVsATR    float64 `json:"range_vs_atr"`   // (high-low)/ATR(20)
	ClosePosition float64 `json:"close_pos"`      // 收盘在[Low,High]中的位置 0~1
}

// SRZone 支撑/压力区
type SRZone struct {
	Lower    float64 `json:"lower"`    // 区间下边界
	Upper    float64 `json:"upper"`    // 区间上边界
	Kind     string  `json:"kind"`     // support / resistance
	Strength int     `json:"strength"` // 强度，数字越大越强
	Hits     int     `json:"hits"`     // 被价格“打到/靠近”的次数
	Basis    string  `json:"basis"`    // 形成这个区间的依据描述
}

// FibLevel 斐波那契单个比例价位
type FibLevel struct {
	Ratio float64 `json:"ratio"` // 比例，例如0.382/0.5/0.618
	Price float64 `json:"price"` // 对应价格
}

// FibSet 一段K线算出来的一整组斐波那契
type FibSet struct {
	Timeframe string     `json:"timeframe"`  // "4h" / "1h"
	Direction string     `json:"direction"`  // "up" 表示低→高这段在回调, "down" 表示高→低这段在反弹
	SwingHigh float64    `json:"swing_high"` // 这段里的最高点
	SwingLow  float64    `json:"swing_low"`  // 这段里的最低点
	Levels    []FibLevel `json:"levels"`     // 各个比例位
}

// PriceActionSummary 价格行为（精简版）
type PriceActionSummary struct {
	Timeframe      string          `json:"timeframe"`   // "4h"/"15m"
	LastSignal     string          `json:"last_signal"` // "BOS_up"|"BOS_down"|"CHoCH_up"|"CHoCH_down"|"none"
	LastSignalTime int64           `json:"last_signal_time"`
	BullishOB      []OB            `json:"bullish_ob"`  // 最近1~2个未失效
	BearishOB      []OB            `json:"bearish_ob"`  // 最近1~2个未失效
	SweptHighs     []LiquidityLine `json:"swept_highs"` // 最近1~2条
	SweptLows      []LiquidityLine `json:"swept_lows"`  // 最近1~2条
	BullSlope      float64         `json:"bull_slope"`  // 由最近两个pivot low计算
	BearSlope      float64         `json:"bear_slope"`  // 由最近两个pivot high计算
}

// OB 订单块（精简字段）
type OB struct {
	Lower   float64 `json:"lower"`
	Upper   float64 `json:"upper"`
	StartTS int64   `json:"start_ts"`
	EndTS   int64   `json:"end_ts"`
}

// LiquidityLine 被扫的流动性线
type LiquidityLine struct {
	Price float64 `json:"price"`
	Time  int64   `json:"time"`
}

// MicrostructureSummary 订单簿微观摘要（轻量）
type MicrostructureSummary struct {
	TsMs            int64   `json:"ts_ms"`
	BestBidPrice    float64 `json:"best_bid_price"`
	BestAskPrice    float64 `json:"best_ask_price"`
	BestBidQty      float64 `json:"best_bid_qty"`
	BestAskQty      float64 `json:"best_ask_qty"`
	BestBidNotional float64 `json:"best_bid_notional"` // price * qty (USDT)
	BestAskNotional float64 `json:"best_ask_notional"` // price * qty (USDT)
	MinNotional     float64 `json:"min_notional"`      // min(bidNotional, askNotional)
	DepthNotional10 float64 `json:"depth_notional_10"` // 前10档累计名义价值
	DepthRatio      float64 `json:"depth_ratio"`
	SpreadBps       float64 `json:"spread_bps"`
}

// ExecutionGate 执行门禁：基于市场微观结构评估是否适合市价单
type ExecutionGate struct {
	TsMs   int64  `json:"ts_ms"`
	Mode   string `json:"mode"`   // market_ok | limit_preferred | limit_only | no_trade
	Reason string `json:"reason"` // 一句话解释
}

// ICTPOIEntry ICT风格的兴趣价区（OB/FVG等）
type ICTPOIEntry struct {
	Type      string  `json:"type"`                // fvg_bull/fvg_bear/ob_bull/ob_bear
	Upper     float64 `json:"upper"`               // 上沿
	Lower     float64 `json:"lower"`               // 下沿
	Mid       float64 `json:"mid"`                 // 中点（例如FVG 50%）
	Timeframe string  `json:"timeframe,omitempty"` // 来源周期
}

// ICTLiquidity 流动性摘要
type ICTLiquidity struct {
	RecentSweptHigh float64   `json:"recent_swept_high,omitempty"`
	RecentSweptLow  float64   `json:"recent_swept_low,omitempty"`
	EqualHighs      []float64 `json:"equal_highs,omitempty"`
	EqualLows       []float64 `json:"equal_lows,omitempty"`
}

// ICTPremiumDiscount 溢价/折价信息
type ICTPremiumDiscount struct {
	Basis  string  `json:"basis,omitempty"` // 例如 "4h swing" / "1h swing"
	Mid    float64 `json:"mid,omitempty"`
	P618   float64 `json:"p618,omitempty"`
	P382   float64 `json:"p382,omitempty"`
	PosPct float64 `json:"pos_pct,omitempty"` // 当前价相对区间的百分位（0-100）
}

// KeyLevel 关键价位信息
type KeyLevel struct {
	Price           float64 `json:"price"`
	DistancePercent float64 `json:"distance_pct"` // 距离当前价的百分比
	Type            string  `json:"type"`         // "support" / "resistance"
	Strength        int     `json:"strength"`     // 强度评分
	Basis           string  `json:"basis"`        // 形成依据
}

// DistanceMetrics 距离度量指标
type DistanceMetrics struct {
	ToEMA20_1h          float64 `json:"to_ema20_1h_pct"`           // 距离1h EMA20的百分比
	ToEMA50_1h          float64 `json:"to_ema50_1h_pct"`           // 距离1h EMA50的百分比
	ToEMA20_4h          float64 `json:"to_ema20_4h_pct"`           // 距离4h EMA20的百分比
	ToEMA50_4h          float64 `json:"to_ema50_4h_pct"`           // 距离4h EMA50的百分比
	ToBollUpper15m      float64 `json:"to_boll_upper_15m_pct"`     // 距离15m布林上轨的百分比
	ToBollLower15m      float64 `json:"to_boll_lower_15m_pct"`     // 距离15m布林下轨的百分比
	ToBollUpper4h       float64 `json:"to_boll_upper_4h_pct"`      // 距离4h布林上轨的百分比
	ToBollLower4h       float64 `json:"to_boll_lower_4h_pct"`      // 距离4h布林下轨的百分比
	ToNearestSupport    float64 `json:"to_nearest_support_pct"`    // 距离最近支撑的百分比
	ToNearestResistance float64 `json:"to_nearest_resistance_pct"` // 距离最近阻力的百分比
}

// TrendPhaseInfo 趋势阶段信息
type TrendPhaseInfo struct {
	TrendStrength4h float64 `json:"trend_strength_4h"` // 趋势强度评分 0-100
	Confidence      float64 `json:"confidence"`        // 判断置信度 0-100
}

// RiskMetrics 风险度量指标
type RiskMetrics struct {
	ATR14PercentOfPrice float64 `json:"atr14_percent_of_price"` // ATR14占价格的百分比
	ATR3PercentOfPrice  float64 `json:"atr3_percent_of_price"`  // ATR3占价格的百分比
	VolatilityLevel     string  `json:"volatility_level"`       // "low"/"medium"/"high"/"extreme"
}

// OrderbookDepthSummary 简化的深度摘要（L2 需外部数据源）
type OrderbookDepthSummary struct {
	BestBidDepth   float64 `json:"best_bid_depth"`
	BestAskDepth   float64 `json:"best_ask_depth"`
	BidAskDepthRat float64 `json:"bid_ask_depth_ratio"`
}

// LargeTakerSummary 最近短周期内主动成交摘要（基于衍生品 taker 数据，通常 15m）
type LargeTakerSummary struct {
	MaxTakerBuyVolume15m  float64 `json:"max_taker_buy_vol_15m"`
	SumTakerBuyVolume15m  float64 `json:"sum_taker_buy_vol_15m"`
	MaxTakerSellVolume15m float64 `json:"max_taker_sell_vol_15m"`
	SumTakerSellVolume15m float64 `json:"sum_taker_sell_vol_15m"`
}

// ExchangeNetflow 交易所净流（需要外部资金流数据源，当前为 stub）
type ExchangeNetflow struct {
	NetflowUSDT float64 `json:"netflow_usdt"`
}

// DerivativesAlert 衍生品异常标记
type DerivativesAlert struct {
	OIChangePct15m     float64 `json:"oi_change_pct_15m"`    // 15m OI变化百分比
	OIChangePct1h      float64 `json:"oi_change_pct_1h"`     // 1h OI变化百分比
	OIChangePct4h      float64 `json:"oi_change_pct_4h"`     // 4h OI变化百分比
	TakerImbalanceFlag string  `json:"taker_imbalance_flag"` // "strong_buy"/"buy"/"neutral"/"sell"/"strong_sell"
	FundingSpikeFlag   bool    `json:"funding_spike_flag"`   // 资金费率是否异常
	FundingRateLevel   string  `json:"funding_rate_level"`   // "extreme_long"/"high_long"/"neutral"/"high_short"/"extreme_short"
	// Funding rate change (rolling) - added for decision signals
	FundingChangePct15m float64 `json:"funding_change_pct_15m,omitempty"`
	FundingChangePct1h  float64 `json:"funding_change_pct_1h,omitempty"`
	FundingChangePct4h  float64 `json:"funding_change_pct_4h,omitempty"`
}

// Zone 价格区间
type Zone struct {
	Upper float64 `json:"upper"` // 上边界
	Lower float64 `json:"lower"` // 下边界
}

// Data 市场数据结构
type Data struct {
	Symbol           string
	CurrentPrice     float64
	PriceChange1h    float64 // 1小时价格变化百分比
	PriceChange4h    float64 // 4小时价格变化百分比
	CurrentEMA20     float64
	CurrentMACD      float64
	CurrentRSI7      float64
	OpenInterest     *OIData
	FundingRate      float64
	IntradaySeries   *IntradayData    // 5分钟数据 - 日内
	MidTermSeries15m *MidTermData15m  // 15分钟数据 - 短期趋势
	MidTermSeries1h  *MidTermData1h   // 1小时数据 - 中期趋势
	MidTermSeries4h  *MidTermSeries4h // 4小时数据 - 长期趋势

	// 新增：识别出来的关键区（内部可用，但不再通过 JSON 提交给 AI）
	FourHourZones   []SRZone `json:"-"`
	FifteenMinZones []SRZone `json:"-"`

	// 新增：直接算好的斐波那契
	Fib4h *FibSet `json:"fib_4h"`
	Fib1h *FibSet `json:"fib_1h"`

	PriceAction5m   *PriceActionSummary `json:"price_action_5m"`
	PriceAction15m  *PriceActionSummary `json:"price_action_15m"`
	PriceAction1h   *PriceActionSummary `json:"price_action_1h"`
	PriceAction4h   *PriceActionSummary `json:"price_action_4h"`
	CandleShapes5m  []CandleShape       `json:"candles_5m,omitempty"`
	CandleShapes15m []CandleShape       `json:"candles_15m,omitempty"`
	CandleShapes1h  []CandleShape       `json:"candles_1h,omitempty"`
	CandleShapes4h  []CandleShape       `json:"candles_4h,omitempty"`
	Derivatives     *DerivativesData    `json:"derivatives,omitempty"`

	// ICT 派生字段（可选）
	ICTPOI             []ICTPOIEntry       `json:"ict_poi,omitempty"`              // OB / FVG 汇总
	ICTLiquidity       *ICTLiquidity       `json:"ict_liquidity,omitempty"`        // 流动性池摘要
	ICTPremiumDiscount *ICTPremiumDiscount `json:"ict_premium_discount,omitempty"` // 溢价/折价信息

	// 新增：派生指标字段
	KeyLevels        []KeyLevel        `json:"key_levels,omitempty"`        // 关键支撑阻力位
	DistanceMetrics  *DistanceMetrics  `json:"distance_metrics,omitempty"`  // 距离度量
	TrendPhase       *TrendPhaseInfo   `json:"trend_phase,omitempty"`       // 趋势阶段
	RiskMetrics      *RiskMetrics      `json:"risk_metrics,omitempty"`      // 风险度量
	DerivativesAlert *DerivativesAlert `json:"derivatives_alert,omitempty"` // 衍生品异常

	// 新增：轻量派生字段（执行/流动性/交易统计）
	TradeCount5m            int     `json:"trade_count_5m"`
	TradeCount15m           int     `json:"trade_count_15m"`
	LargeTakerVolume15m     float64 `json:"large_taker_volume_15m"`
	FundingChangeRate15mPct float64 `json:"funding_change_rate_15m_pct"`
	OIChangePct15m          float64 `json:"oi_change_pct_15m"`
	VolumePercentile15m     float64 `json:"volume_percentile_15m"`

	// 新增：距离历史极值指标
	DistanceToATH float64 `json:"distance_to_ath,omitempty"` // 距离历史最高价的百分比
	// 轻量订单簿微观摘要（供AI参考）
	Microstructure *MicrostructureSummary `json:"microstructure,omitempty"`
	// 执行门禁（基于微观结构评估市价单风险）
	Execution *ExecutionGate `json:"execution,omitempty"`
}

// DerivativesData 汇总每个周期的衍生品指标
type DerivativesData struct {
	OpenInterestHist    map[string][]OpenInterestHistEntry `json:"open_interest_hist,omitempty"`
	TopLongShortRatio   map[string][]LongShortRatioEntry   `json:"top_long_short_ratio,omitempty"`
	GlobalLongShortAcct map[string][]LongShortRatioEntry   `json:"global_long_short_ratio,omitempty"`
	TakerBuySellVolume  map[string][]TakerBuySellEntry     `json:"taker_buy_sell_volume,omitempty"`
	Basis               map[string][]BasisEntry            `json:"basis,omitempty"`
	FundingRateHistory  []FundingRateEntry                 `json:"funding_rate_hist,omitempty"`
}

// OpenInterestHistEntry OI 历史点位
type OpenInterestHistEntry struct {
	Timestamp            int64   `json:"ts"`
	SumOpenInterest      float64 `json:"sum_open_interest"`
	SumOpenInterestValue float64 `json:"sum_open_interest_value"`
}

// LongShortRatioEntry 多空持仓占比
type LongShortRatioEntry struct {
	Timestamp    int64   `json:"ts"`
	LongAccount  float64 `json:"long"`
	ShortAccount float64 `json:"short"`
	Ratio        float64 `json:"ratio"`
}

// TakerBuySellEntry 主动买卖量
type TakerBuySellEntry struct {
	Timestamp    int64   `json:"ts"`
	BuyVolume    float64 `json:"buy_vol"`
	SellVolume   float64 `json:"sell_vol"`
	BuySellRatio float64 `json:"buy_sell_ratio"`
}

// BasisEntry 基差
type BasisEntry struct {
	Timestamp    int64   `json:"ts"`
	Basis        float64 `json:"basis"`
	BasisRate    float64 `json:"basis_rate"`
	FuturesPrice float64 `json:"futures_price"`
	IndexPrice   float64 `json:"index_price"`
}

// FundingRateEntry 历史资金费率
type FundingRateEntry struct {
	Timestamp   int64   `json:"ts"`
	FundingRate float64 `json:"funding_rate"`
}

func (d *DerivativesData) isEmpty() bool {
	if d == nil {
		return true
	}
	return len(d.OpenInterestHist) == 0 &&
		len(d.TopLongShortRatio) == 0 &&
		len(d.GlobalLongShortAcct) == 0 &&
		len(d.TakerBuySellVolume) == 0 &&
		len(d.Basis) == 0 &&
		len(d.FundingRateHistory) == 0
}

type BollingerBand struct {
	Upper   float64
	Middle  float64
	Lower   float64
	Width   float64 // (Upper-Lower)/Middle
	Percent float64 // (price - Lower)/(Upper - Lower)
}

// MACDSignal MACD指标完整信号
type MACDSignal struct {
	MACDLine   float64 `json:"macd_line"`   // MACD线 (EMA12 - EMA26)
	SignalLine float64 `json:"signal_line"` // 信号线 (MACD线的EMA9)
	Histogram  float64 `json:"histogram"`   // 柱状图 (MACD线 - 信号线)
	Cross      string  `json:"cross"`       // "golden_cross"/"dead_cross"/"none"
	Trend      string  `json:"trend"`       // "bullish"/"bearish"/"neutral"
	Strength   float64 `json:"strength"`    // 交叉强度 (0-100)
}

// OIData Open Interest数据
type OIData struct {
	Latest  float64
	Average float64
}

// IntradayData 日内数据(5分钟间隔)
type IntradayData struct {
	MidPrices     []float64
	EMA20Values   []float64
	MACDValues    []*MACDSignal // 5分钟MACD信号序列（使用优化参数8,17,6）
	RSI7Values    []float64
	RSI14Values   []float64
	Volumes       []float64 // 成交量序列（用于放量检测）
	BuySellRatios []float64 // 买卖压力比序列（>0.6多方强，<0.4空方强）
	OBVValues     []float64 // OBV指标序列
}

// MidTermData15m 15分钟时间框架数据 - 短期趋势过滤
type MidTermData15m struct {
	MidPrices   []float64
	EMA20Values []float64
	EMA50Values []float64
	MACDValues  []*MACDSignal // 完整的MACD信号序列（包括金叉死叉）
	RSI7Values  []float64
	RSI14Values []float64
	Bollinger   *BollingerBand
	VWAP        float64   // 当前VWAP
	OBVValues   []float64 // OBV指标序列
	MFI         float64   // 资金流量指标
}

// MidTermData1h 1小时时间框架数据 - 中期趋势确认
type MidTermData1h struct {
	MidPrices    []float64
	EMA20Values  []float64
	EMA50Values  []float64
	EMA100Values []float64
	EMA200Values []float64
	MACDValues   []*MACDSignal // 完整的MACD信号序列（包括金叉死叉）
	RSI7Values   []float64
	RSI14Values  []float64
	ADX          float64 // 趋势强度指标
	DIPlus       float64 // DI+
	DIMinus      float64 // DI-
	VWAP         float64 // 当前VWAP
	OBVValues    []float64
}

// MidTermSeries4h 4小时时间框架数据 - 长期趋势
type MidTermSeries4h struct {
	MidPrices     []float64
	EMA20Values   []float64
	EMA50Values   []float64
	EMA100Values  []float64
	EMA200Values  []float64
	MACDValues    []*MACDSignal // 完整的MACD信号序列（包括金叉死叉）
	RSI7Values    []float64
	RSI14Values   []float64
	Bollinger     *BollingerBand // 4h布林带
	ADX           float64        // 趋势强度指标
	DIPlus        float64        // DI+
	DIMinus       float64        // DI-
	ATR3          float64
	ATR14         float64
	CurrentVolume float64
	AverageVolume float64
	VWAP          float64 // 当前VWAP
	OBVValues     []float64
	CMF           float64 // 资金流量指标
	MFI           float64 // 资金流量强度
}

// Kline K线数据
type Kline struct {
	OpenTime  int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime int64

	TakerBuyVolume float64
	BuySellRatio   float64

	QuoteVolume         float64
	Trades              int
	TakerBuyBaseVolume  float64
	TakerBuyQuoteVolume float64
}

// Get 获取指定代币的市场数据
func Get(symbol string) (*Data, error) {
	return marketDataProvider.Get(symbol)
}

// getMarketDataFromAPI 从API获取市场数据（原始实现）
func getMarketDataFromAPI(symbol string) (*Data, error) {
	// 标准化symbol
	symbol = Normalize(symbol)

	// 获取5分钟K线数据 (最近40个) - 日内
	klines5m, err := GetKlines(symbol, "5m", 40)
	if err != nil {
		return nil, fmt.Errorf("获取5分钟K线失败: %v", err)
	}

	// 获取15分钟K线数据（多取一些做结构/流动性检测）
	klines15m, err := GetKlines(symbol, "15m", 120)
	if err != nil {
		return nil, fmt.Errorf("获取15分钟K线失败: %v", err)
	}

	// 获取1小时K线数据（顺便也多取一些，给Fib/趋势用）
	klines1h, err := GetKlines(symbol, "1h", 120)
	if err != nil {
		return nil, fmt.Errorf("获取1小时K线失败: %v", err)
	}

	// 获取4小时K线数据（多取一些做结构/流动性检测）
	klines4h, err := GetKlines(symbol, "4h", 120)
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}

	// 基于5m最新数据的当前指标
	currentPrice := klines5m[len(klines5m)-1].Close
	currentEMA20 := CalculateEMA(klines5m, 20)
	currentMACD := CalculateMACD(klines5m)
	currentRSI7 := CalculateRSI(klines5m, 7)

	// 1h 涨跌幅，用5m往前推1h
	priceChange1h := 0.0
	if len(klines5m) >= 13 { // 5m * 12 = 60m，这里留1根缓冲
		price1hAgo := klines5m[len(klines5m)-13].Close
		if price1hAgo > 0 {
			priceChange1h = ((currentPrice - price1hAgo) / price1hAgo) * 100
		}
	}

	// 4h 涨跌幅，用一根4h前
	priceChange4h := 0.0
	if len(klines4h) >= 2 {
		price4hAgo := klines4h[len(klines4h)-2].Close
		if price4hAgo > 0 {
			priceChange4h = ((currentPrice - price4hAgo) / price4hAgo) * 100
		}
	}

	// 计算距离历史极值（ATH）的百分比
	distanceToATH := 0.0
	if len(klines4h) > 0 {
		// 使用4h数据计算ATH（历史最高价）
		ath := klines4h[0].High
		for _, k := range klines4h {
			if k.High > ath {
				ath = k.High
			}
		}
		if ath > 0 {
			distanceToATH = ((currentPrice - ath) / ath) * 100
		}
	}

	// OI
	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		oiData = &OIData{Latest: 0, Average: 0}
	}

	// funding
	fundingRate, _ := getFundingRate(symbol)

	// 衍生品多周期数据
	derivativesData := fetchDerivativesSuite(symbol)

	// 5m 日内序列
	intradayData := calculateIntradaySeries(klines5m)

	// 15m 序列
	midTermData15m := calculateMidTermSeries15m(klines15m)

	// 1h 序列
	midTermData1h := calculateMidTermSeries1h(klines1h)

	// 4h 长周期
	midTermData4h := calculateMidTermSeries4h(klines4h)

	// 新增：4h 强支撑/压力区识别
	// 已注释：不再计算4h支撑/压力位，不再提交给AI
	// fourHourZones := detect4hZones(klines4h, longerTermData)
	var fourHourZones []SRZone

	// 新增：15m 小支撑/小压力区识别
	// 恢复：计算15m支撑/压力位，只给AI最关键的结构锚点
	fifteenMinZones := detect15mZones(klines15m, midTermData15m)

	// 新增：5m/15m/4h/1h 价格行为 & 斐波那契
	fib4h := calcFibFromKlines(klines4h, "4h")
	fib1h := calcFibFromKlines(klines1h, "1h")

	// 价格行为：结构+OB+liquidity（根据周期优化参数）
	// 5m: zigzagLen=5, liquidityLen=10, trendLineLen=10 (最敏感，适应高频噪音)
	// 15m: zigzagLen=7, liquidityLen=15, trendLineLen=15 (平衡参数)
	// 1h: zigzagLen=9, liquidityLen=20, trendLineLen=20 (当前参数)
	// 4h: zigzagLen=11, liquidityLen=25, trendLineLen=25 (最保守，过滤长期噪音)
	pa5 := calcPriceActionSummary(klines5m, "5m", 5, 10, 10)
	pa15 := calcPriceActionSummary(klines15m, "15m", 7, 15, 15)
	pa1h := calcPriceActionSummary(klines1h, "1h", 9, 20, 20)
	pa4h := calcPriceActionSummary(klines4h, "4h", 11, 25, 25)

	// 新增：提取最近K线的几何特征，让AI做形态识别（每个周期只给最近20根）
	candles5m := extractCandleShapes(klines5m, 20, 20)
	candles15m := extractCandleShapes(klines15m, 20, 20)
	candles1h := extractCandleShapes(klines1h, 20, 20)
	candles4h := extractCandleShapes(klines4h, 20, 20)

	// 计算派生指标
	keyLevels := detectKeyLevels(klines4h, klines15m, currentPrice, midTermData4h, midTermData15m)
	distanceMetrics := calculateDistanceMetrics(currentPrice, midTermData1h, midTermData4h, midTermData15m, keyLevels)

	// 计算趋势阶段
	trendPhase := calculateTrendPhase(pa4h, midTermData4h, midTermData15m, midTermData1h, currentPrice, klines4h, klines1h)
	derivativesAlert := calculateDerivativesAlert(derivativesData, fundingRate)

	// ICT：POI / 流动性 / 溢价折价
	var ictPOI []ICTPOIEntry
	ictPOI = append(ictPOI, selectRecentOB(pa5, "5m", 1)...)
	ictPOI = append(ictPOI, selectRecentOB(pa4h, "4h", 2)...)
	ictPOI = append(ictPOI, selectRecentOB(pa15, "15m", 2)...)
	ictPOI = append(ictPOI, detectFVGs(klines5m, "5m", 1)...)
	ictPOI = append(ictPOI, detectFVGs(klines15m, "15m", 2)...)
	ictPOI = append(ictPOI, detectFVGs(klines1h, "1h", 2)...)

	ictLiquidity := buildLiquiditySummary(pa4h, pa15, pa5)

	var ictPremium *ICTPremiumDiscount
	high4h, low4h := highestLowest(klines4h)
	ictPremium = computePremiumDiscountFromSwing(high4h, low4h, currentPrice, "4h swing")
	if ictPremium == nil {
		high1h, low1h := highestLowest(klines1h)
		ictPremium = computePremiumDiscountFromSwing(high1h, low1h, currentPrice, "1h swing")
	}
	// ===== 轻量派生：交易统计 / 流动性 / 资金变化 =====
	tradeCount5m := 0
	start5 := len(klines5m) - 12
	if start5 < 0 {
		start5 = 0
	}
	for i := start5; i < len(klines5m); i++ {
		tradeCount5m += klines5m[i].Trades
	}

	tradeCount15m := 0
	start15 := len(klines15m) - 12
	if start15 < 0 {
		start15 = 0
	}
	for i := start15; i < len(klines15m); i++ {
		tradeCount15m += klines15m[i].Trades
	}

	largeTakerVolume15m := 0.0
	if derivativesData != nil {
		if arr, ok := derivativesData.TakerBuySellVolume["15m"]; ok && len(arr) > 0 {
			latest := arr[len(arr)-1]
			if latest.BuyVolume > latest.SellVolume {
				largeTakerVolume15m = latest.BuyVolume
			} else {
				largeTakerVolume15m = latest.SellVolume
			}
		}
	}

	fundingChangeRate15mPct := 0.0
	if derivativesData != nil && len(derivativesData.FundingRateHistory) >= 2 {
		first := derivativesData.FundingRateHistory[0].FundingRate
		last := derivativesData.FundingRateHistory[len(derivativesData.FundingRateHistory)-1].FundingRate
		if first != 0 {
			fundingChangeRate15mPct = (last - first) / math.Abs(first) * 100
		}
	}

	oiChangePct15m := 0.0
	if derivativesAlert != nil {
		oiChangePct15m = derivativesAlert.OIChangePct15m
	}

	volumePercentile15m := 0.0
	if len(klines15m) > 0 {
		sumVol := 0.0
		start := len(klines15m) - 30
		if start < 0 {
			start = 0
		}
		count := 0
		for i := start; i < len(klines15m); i++ {
			sumVol += klines15m[i].Volume
			count++
		}
		if count > 0 {
			avgVol := sumVol / float64(count)
			curVol := klines15m[len(klines15m)-1].Volume
			if avgVol > 0 {
				volumePercentile15m = (curVol / avgVol) * 100
			}
		}
	}

	// 获取订单簿微观摘要（非致命错误）
	var micro *MicrostructureSummary
	if m, err := getOrderbookSummary(symbol); err != nil {
		fmt.Printf("⚠ getOrderbookSummary failed for %s: %v\n", symbol, err)
	} else {
		micro = m
	}

	// 计算风险指标（需要在micro和volumePercentile15m之后）
	spreadBps := 0.0
	if micro != nil {
		spreadBps = micro.SpreadBps
	}
	riskMetrics := calculateRiskMetrics(currentPrice, midTermData4h, volumePercentile15m, spreadBps, symbol)

	return &Data{
		Symbol:                  symbol,
		CurrentPrice:            currentPrice,
		PriceChange1h:           priceChange1h,
		PriceChange4h:           priceChange4h,
		CurrentEMA20:            currentEMA20,
		CurrentMACD:             currentMACD,
		CurrentRSI7:             currentRSI7,
		OpenInterest:            oiData,
		FundingRate:             fundingRate,
		IntradaySeries:          intradayData,
		MidTermSeries15m:        midTermData15m,
		MidTermSeries1h:         midTermData1h,
		MidTermSeries4h:         midTermData4h,
		FourHourZones:           fourHourZones,
		FifteenMinZones:         fifteenMinZones,
		Fib4h:                   fib4h,
		Fib1h:                   fib1h,
		PriceAction5m:           pa5,
		PriceAction15m:          pa15,
		PriceAction1h:           pa1h,
		PriceAction4h:           pa4h,
		CandleShapes5m:          candles5m,
		CandleShapes15m:         candles15m,
		CandleShapes1h:          candles1h,
		CandleShapes4h:          candles4h,
		Derivatives:             derivativesData,
		KeyLevels:               keyLevels,
		DistanceMetrics:         distanceMetrics,
		TrendPhase:              trendPhase,
		RiskMetrics:             riskMetrics,
		DerivativesAlert:        derivativesAlert,
		ICTPOI:                  ictPOI,
		ICTLiquidity:            ictLiquidity,
		ICTPremiumDiscount:      ictPremium,
		TradeCount5m:            tradeCount5m,
		TradeCount15m:           tradeCount15m,
		LargeTakerVolume15m:     largeTakerVolume15m,
		FundingChangeRate15mPct: fundingChangeRate15mPct,
		OIChangePct15m:          oiChangePct15m,
		VolumePercentile15m:     volumePercentile15m,
		DistanceToATH:           distanceToATH,
		Microstructure:          micro,
		Execution:               EvaluateExecutionGate(micro, 0), // 0表示无计划仓位时的评估
	}, nil
}

// detect4hZones 根据你的文字规则，识别最近30~60根4h的强支撑/压力区
// 已注释：不再计算4h支撑/压力位，不再提交给AI
/*
func detect4hZones(klines []Kline, ctx *MidTermSeries4h) []SRZone {
	n := len(klines)
	if n == 0 {
		return nil
	}
	start := 0
	if n > 60 {
		start = n - 60
	}

	avgVol := ctx.AverageVolume
	if avgVol == 0 {
		sum := 0.0
		for i := start; i < n; i++ {
			sum += klines[i].Volume
		}
		avgVol = sum / float64(n-start)
	}

	zones := []SRZone{}

	for i := start; i < n; i++ {
		k := klines[i]

		// 支撑
		priceLow := k.Low
		if priceLow > 0 {
			tol := priceLow * 0.004 // 0.4% 宽度
			strength := 1
			basis := "swing_low"

			if k.Volume >= avgVol*0.95 {
				strength++
				basis += "+vol"
			}
			if ctx != nil && ctx.Bollinger != nil && ctx.Bollinger.Upper != ctx.Bollinger.Lower {
				percent := (priceLow - ctx.Bollinger.Lower) / (ctx.Bollinger.Upper - ctx.Bollinger.Lower)
				if percent <= 0.15 {
					strength++
					basis += "+bb_low"
				}
			}

			idx := findZoneIndex(zones, priceLow, tol, "support")
			if idx == -1 {
				zones = append(zones, SRZone{
					Lower:    priceLow - priceLow*0.002,
					Upper:    priceLow + priceLow*0.002,
					Kind:     "support",
					Strength: strength,
					Hits:     1,
					Basis:    basis,
				})
			} else {
				if priceLow < zones[idx].Lower {
					zones[idx].Lower = priceLow
				}
				if priceLow > zones[idx].Upper {
					zones[idx].Upper = priceLow
				}
				zones[idx].Hits++
				if strength > zones[idx].Strength {
					zones[idx].Strength = strength
				}
				zones[idx].Basis = basis
			}
		}

		// 压力
		priceHigh := k.High
		if priceHigh > 0 {
			tol := priceHigh * 0.004
			strength := 1
			basis := "swing_high"

			if k.Volume >= avgVol*0.95 {
				strength++
				basis += "+vol"
			}
			if ctx != nil && ctx.Bollinger != nil && ctx.Bollinger.Upper != ctx.Bollinger.Lower {
				percent := (priceHigh - ctx.Bollinger.Lower) / (ctx.Bollinger.Upper - ctx.Bollinger.Lower)
				if percent >= 0.85 {
					strength++
					basis += "+bb_high"
				}
			}

			idx := findZoneIndex(zones, priceHigh, tol, "resistance")
			if idx == -1 {
				zones = append(zones, SRZone{
					Lower:    priceHigh - priceHigh*0.002,
					Upper:    priceHigh + priceHigh*0.002,
					Kind:     "resistance",
					Strength: strength,
					Hits:     1,
					Basis:    basis,
				})
			} else {
				if priceHigh < zones[idx].Lower {
					zones[idx].Lower = priceHigh
				}
				if priceHigh > zones[idx].Upper {
					zones[idx].Upper = priceHigh
				}
				zones[idx].Hits++
				if strength > zones[idx].Strength {
					zones[idx].Strength = strength
				}
				zones[idx].Basis = basis
			}
		}
	}

	return zones
}
*/

// detect15mZones 弱级别的小区间，用来精细化，不盖过4h
func detect15mZones(klines []Kline, ctx *MidTermData15m) []SRZone {
	n := len(klines)
	if n == 0 {
		return nil
	}
	start := 0
	if n > 40 {
		start = n - 40
	}

	var bb *BollingerBand
	if ctx != nil {
		bb = ctx.Bollinger
	}

	zones := []SRZone{}

	for i := start; i < n; i++ {
		k := klines[i]

		// 小买区
		low := k.Low
		if low > 0 {
			tol := low * 0.0015
			strength := 1
			basis := "15m_low"

			if bb != nil && bb.Upper != bb.Lower {
				percent := (low - bb.Lower) / (bb.Upper - bb.Lower)
				if percent <= 0.25 {
					strength++
					basis += "+bb_low"
				}
			}

			idx := findZoneIndex(zones, low, tol, "support")
			if idx == -1 {
				zones = append(zones, SRZone{
					Lower:    low - low*0.001,
					Upper:    low + low*0.001,
					Kind:     "support",
					Strength: strength,
					Hits:     1,
					Basis:    basis,
				})
			} else {
				if low < zones[idx].Lower {
					zones[idx].Lower = low
				}
				if low > zones[idx].Upper {
					zones[idx].Upper = low
				}
				zones[idx].Hits++
				if strength > zones[idx].Strength {
					zones[idx].Strength = strength
				}
				zones[idx].Basis = basis
			}
		}

		// 小卖区
		high := k.High
		if high > 0 {
			tol := high * 0.0015
			strength := 1
			basis := "15m_high"

			if bb != nil && bb.Upper != bb.Lower {
				percent := (high - bb.Lower) / (bb.Upper - bb.Lower)
				if percent >= 0.75 {
					strength++
					basis += "+bb_high"
				}
			}

			idx := findZoneIndex(zones, high, tol, "resistance")
			if idx == -1 {
				zones = append(zones, SRZone{
					Lower:    high - high*0.001,
					Upper:    high + high*0.001,
					Kind:     "resistance",
					Strength: strength,
					Hits:     1,
					Basis:    basis,
				})
			} else {
				if high < zones[idx].Lower {
					zones[idx].Lower = high
				}
				if high > zones[idx].Upper {
					zones[idx].Upper = high
				}
				zones[idx].Hits++
				if strength > zones[idx].Strength {
					zones[idx].Strength = strength
				}
				zones[idx].Basis = basis
			}
		}
	}

	return zones
}

// pickKeyZones 从一堆SRZone中选出每一侧(支撑/压力)最强且离当前价最近的若干个
func pickKeyZones(zones []SRZone, currentPrice float64, maxPerSide int) (supports, resistances []SRZone) {
	if len(zones) == 0 || maxPerSide <= 0 {
		return
	}

	type scored struct {
		z    SRZone
		dist float64
		// 强度优先，其次命中次数
		// 我们用负的dist参与排序，使“更近”的优先级更高
	}

	var sup, res []scored

	for _, z := range zones {
		center := (z.Lower + z.Upper) / 2
		dist := math.Abs(center - currentPrice)

		s := scored{
			z:    z,
			dist: dist,
		}

		if z.Kind == "support" {
			sup = append(sup, s)
		} else if z.Kind == "resistance" {
			res = append(res, s)
		}
	}

	// strength 越大越优先，其次 hits，多者优先，最后才是离价格近
	sort.Slice(sup, func(i, j int) bool {
		if sup[i].z.Strength == sup[j].z.Strength {
			if sup[i].z.Hits == sup[j].z.Hits {
				return sup[i].dist < sup[j].dist
			}
			return sup[i].z.Hits > sup[j].z.Hits
		}
		return sup[i].z.Strength > sup[j].z.Strength
	})

	sort.Slice(res, func(i, j int) bool {
		if res[i].z.Strength == res[j].z.Strength {
			if res[i].z.Hits == res[j].z.Hits {
				return res[i].dist < res[j].dist
			}
			return res[i].z.Hits > res[j].z.Hits
		}
		return res[i].z.Strength > res[j].z.Strength
	})

	for i := 0; i < len(sup) && i < maxPerSide; i++ {
		supports = append(supports, sup[i].z)
	}
	for i := 0; i < len(res) && i < maxPerSide; i++ {
		resistances = append(resistances, res[i].z)
	}
	return
}

// findZoneIndex 在已有zones里查有没有同类且价格接近的
func findZoneIndex(zones []SRZone, price float64, tol float64, kind string) int {
	for i, z := range zones {
		if z.Kind != kind {
			continue
		}
		center := (z.Lower + z.Upper) / 2
		if math.Abs(center-price) <= tol {
			return i
		}
	}
	return -1
}

// calcFibFromKlines 从一段K线里自动找最高点/最低点并输出一组常用斐波那契
func calcFibFromKlines(klines []Kline, timeframe string) *FibSet {
	n := len(klines)
	if n < 2 {
		return nil
	}

	start := 0
	if n > 60 {
		start = n - 60
	}

	high := klines[start].High
	low := klines[start].Low
	highIdx := start
	lowIdx := start

	for i := start; i < n; i++ {
		if klines[i].High > high {
			high = klines[i].High
			highIdx = i
		}
		if klines[i].Low < low {
			low = klines[i].Low
			lowIdx = i
		}
	}

	diff := high - low
	if diff <= 0 {
		return nil
	}

	ratios := []float64{0.236, 0.382, 0.5, 0.618, 0.65, 0.786}
	levels := make([]FibLevel, 0, len(ratios))

	direction := "up"
	if highIdx < lowIdx {
		direction = "down"
	}

	if direction == "up" {
		for _, r := range ratios {
			price := high - diff*r
			levels = append(levels, FibLevel{Ratio: r, Price: price})
		}
	} else {
		for _, r := range ratios {
			price := low + diff*r
			levels = append(levels, FibLevel{Ratio: r, Price: price})
		}
	}

	return &FibSet{
		Timeframe: timeframe,
		Direction: direction,
		SwingHigh: high,
		SwingLow:  low,
		Levels:    levels,
	}
}

// GetKlines 从Binance获取K线数据（导出给API使用）
func GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/klines?symbol=%s&interval=%s&limit=%d",
		symbol, interval, limit)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawData [][]interface{}
	if err := json.Unmarshal(body, &rawData); err != nil {
		return nil, err
	}

	klines := make([]Kline, len(rawData))
	for i, item := range rawData {
		openTime := int64(item[0].(float64))
		open, _ := parseFloat(item[1])
		high, _ := parseFloat(item[2])
		low, _ := parseFloat(item[3])
		closeVal, _ := parseFloat(item[4])
		volume, _ := parseFloat(item[5])
		closeTime := int64(item[6].(float64))

		quoteVol := 0.0
		if len(item) > 7 {
			quoteVol, _ = parseFloat(item[7])
		}

		trades := 0
		if len(item) > 8 {
			trades = int(item[8].(float64))
		}

		takerBuyBase := 0.0
		if len(item) > 9 {
			takerBuyBase, _ = parseFloat(item[9])
		}

		takerBuyQuote := 0.0
		if len(item) > 10 {
			takerBuyQuote, _ = parseFloat(item[10])
		}

		buySellRatio := 0.0
		if volume > 0 {
			buySellRatio = takerBuyBase / volume
		}

		klines[i] = Kline{
			OpenTime:            openTime,
			Open:                open,
			High:                high,
			Low:                 low,
			Close:               closeVal,
			Volume:              volume,
			CloseTime:           closeTime,
			TakerBuyVolume:      takerBuyBase,
			BuySellRatio:        buySellRatio,
			QuoteVolume:         quoteVol,
			Trades:              trades,
			TakerBuyBaseVolume:  takerBuyBase,
			TakerBuyQuoteVolume: takerBuyQuote,
		}
	}

	return klines, nil
}

// CalculateEMA 计算EMA指标（导出给API使用）
func CalculateEMA(klines []Kline, period int) float64 {
	if len(klines) < period {
		return 0
	}

	sum := 0.0
	for i := 0; i < period; i++ {
		sum += klines[i].Close
	}
	ema := sum / float64(period)

	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(klines); i++ {
		ema = (klines[i].Close-ema)*multiplier + ema
	}

	return ema
}

// CalculateMACD 计算MACD指标（导出给API使用）
func CalculateMACD(klines []Kline) float64 {
	if len(klines) < 26 {
		return 0
	}
	ema12 := CalculateEMA(klines, 12)
	ema26 := CalculateEMA(klines, 26)
	return ema12 - ema26
}

// getMACDParams 根据周期返回适合加密货币市场的MACD参数
func getMACDParams(timeframe string) (fastPeriod, slowPeriod, signalPeriod int) {
	// 加密货币市场波动更快，需要更敏感的参数
	switch timeframe {
	case "5m":
		return 8, 17, 6 // 更敏感的参数，适应高频波动
	case "15m":
		return 10, 21, 8 // 平衡参数，适合中期确认
	case "1h":
		return 12, 26, 9 // 标准参数，适合小时级趋势
	case "4h":
		return 12, 26, 9 // 标准参数，适合长期趋势
	default:
		return 12, 26, 9 // 默认参数
	}
}

// getTechnicalIndicatorParams 根据周期返回适合加密货币市场的技术指标参数
func getTechnicalIndicatorParams(timeframe string) (rsiShort, rsiLong, bollingerPeriod, adxPeriod, atrShort, atrLong, mfiPeriod, cmfPeriod int, bollingerMult float64) {
	// 为加密货币市场优化的参数配置
	switch timeframe {
	case "5m":
		// 高频周期：更敏感的参数
		return 5, 10, 15, 10, 2, 5, 10, 15, 2.0
	case "15m":
		// 中期周期：平衡参数
		return 7, 14, 20, 14, 3, 14, 14, 20, 2.0
	case "1h":
		// 小时周期：标准参数
		return 7, 14, 20, 14, 3, 14, 14, 20, 2.0
	case "4h":
		// 长期周期：更平滑的参数
		return 14, 21, 25, 20, 3, 14, 20, 25, 2.0
	default:
		// 默认参数
		return 7, 14, 20, 14, 3, 14, 14, 20, 2.0
	}
}

// CalculateMACDWithParams 计算指定参数的MACD指标
func CalculateMACDWithParams(klines []Kline, fastPeriod, slowPeriod int) float64 {
	if len(klines) < slowPeriod {
		return 0
	}
	fastEMA := CalculateEMA(klines, fastPeriod)
	slowEMA := CalculateEMA(klines, slowPeriod)
	return fastEMA - slowEMA
}

// CalculateMACDSignal 计算完整的MACD信号（包括金叉死叉检测）
func CalculateMACDSignal(klines []Kline) *MACDSignal {
	return CalculateMACDSignalWithTimeframe(klines, "")
}

// CalculateMACDSignalWithTimeframe 根据时间周期计算MACD信号
func CalculateMACDSignalWithTimeframe(klines []Kline, timeframe string) *MACDSignal {
	fastPeriod, slowPeriod, signalPeriod := getMACDParams(timeframe)
	minKlines := slowPeriod + signalPeriod // 需要足够的历史数据
	if len(klines) < minKlines {
		return &MACDSignal{
			MACDLine:   0,
			SignalLine: 0,
			Histogram:  0,
			Cross:      "none",
			Trend:      "neutral",
			Strength:   0,
		}
	}

	// 计算MACD线
	macdLine := CalculateMACDWithParams(klines, fastPeriod, slowPeriod)

	// 计算信号线（MACD线的EMA）
	signalLine := 0.0
	if len(klines) >= minKlines {
		// 构造MACD线序列用于计算信号线
		macdValues := make([]float64, 0, len(klines)-slowPeriod+1)
		for i := slowPeriod - 1; i < len(klines); i++ {
			macdVal := CalculateMACDWithParams(klines[:i+1], fastPeriod, slowPeriod)
			macdValues = append(macdValues, macdVal)
		}
		if len(macdValues) >= signalPeriod {
			signalLine = CalculateEMAFromValues(macdValues, signalPeriod)
		}
	}

	histogram := macdLine - signalLine

	// 检测金叉死叉（需要至少2个历史值比较）
	cross := "none"
	strength := 0.0

	if len(klines) >= minKlines+1 {
		// 计算前一个周期的MACD和信号线
		prevMACDLine := CalculateMACDWithParams(klines[:len(klines)-1], fastPeriod, slowPeriod)
		prevSignalLine := 0.0

		// 计算前一个周期的信号线
		prevMACDValues := make([]float64, 0, len(klines)-slowPeriod)
		for i := slowPeriod - 1; i < len(klines)-1; i++ {
			prevMACDVal := CalculateMACDWithParams(klines[:i+1], fastPeriod, slowPeriod)
			prevMACDValues = append(prevMACDValues, prevMACDVal)
		}
		if len(prevMACDValues) >= signalPeriod {
			prevSignalLine = CalculateEMAFromValues(prevMACDValues, signalPeriod)
		}

		// 检测交叉
		if prevMACDLine <= prevSignalLine && macdLine > signalLine {
			cross = "golden_cross" // 金叉：MACD线从下向上穿越信号线
			strength = math.Min(100.0, math.Abs(macdLine-signalLine)*100)
		} else if prevMACDLine >= prevSignalLine && macdLine < signalLine {
			cross = "dead_cross" // 死叉：MACD线从上向下穿越信号线
			strength = math.Min(100.0, math.Abs(macdLine-signalLine)*100)
		}

		// 如果没有交叉，检查趋势
		if cross == "none" && len(klines) >= 40 {
			// 检查最近5个周期的趋势
			recentCrosses := 0
			for i := len(klines) - 5; i < len(klines); i++ {
				if i >= 1 {
					tempMACD := CalculateMACD(klines[:i])
					tempSignal := 0.0
					if i >= 35 {
						tempValues := make([]float64, 0, i-slowPeriod+1)
						for j := slowPeriod - 1; j < i; j++ {
							tempVal := CalculateMACDWithParams(klines[:j+1], fastPeriod, slowPeriod)
							tempValues = append(tempValues, tempVal)
						}
						if len(tempValues) >= signalPeriod {
							tempSignal = CalculateEMAFromValues(tempValues, signalPeriod)
						}
						if tempMACD > tempSignal {
							recentCrosses++
						} else if tempMACD < tempSignal {
							recentCrosses--
						}
					}
				}
			}
			if recentCrosses > 2 {
				cross = "bullish_trend"
			} else if recentCrosses < -2 {
				cross = "bearish_trend"
			}
		}
	}

	// 确定整体趋势
	trend := "neutral"
	if histogram > 0.0001 {
		trend = "bullish"
	} else if histogram < -0.0001 {
		trend = "bearish"
	}

	return &MACDSignal{
		MACDLine:   macdLine,
		SignalLine: signalLine,
		Histogram:  histogram,
		Cross:      cross,
		Trend:      trend,
		Strength:   strength,
	}
}

// CalculateEMAFromValues 从数值数组计算EMA
func CalculateEMAFromValues(values []float64, period int) float64 {
	if len(values) < period {
		return 0
	}

	// 计算初始SMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += values[i]
	}
	ema := sum / float64(period)

	// 计算EMA
	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(values); i++ {
		ema = (values[i]-ema)*multiplier + ema
	}

	return ema
}

// CalculateRSI 计算RSI指标（导出给API使用）
func CalculateRSI(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	gains := 0.0
	losses := 0.0

	for i := 1; i <= period; i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	for i := period + 1; i < len(klines); i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			avgGain = (avgGain*float64(period-1) + change) / float64(period)
			avgLoss = (avgLoss * float64(period-1)) / float64(period)
		} else {
			avgGain = (avgGain * float64(period-1)) / float64(period)
			avgLoss = (avgLoss*float64(period-1) + (-change)) / float64(period)
		}
	}

	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))

	return rsi
}

// extractCandleShapes 从K线中提取最近lookback根的几何特征
func extractCandleShapes(klines []Kline, lookback int, atrPeriod int) []CandleShape {
	n := len(klines)
	if n == 0 || lookback <= 0 {
		return nil
	}
	if lookback > n {
		lookback = n
	}
	start := n - lookback

	atr := calculateATR(klines, atrPeriod) // 可能为0，下面会判断
	shapes := make([]CandleShape, 0, lookback)

	for i := start; i < n; i++ {
		k := klines[i]
		fullRange := k.High - k.Low
		if fullRange <= 0 {
			// 极端错误数据，填一个占位
			shapes = append(shapes, CandleShape{
				Direction:     "doji",
				BodyPct:       0,
				UpperWickPct:  0,
				LowerWickPct:  0,
				RangeVsATR:    0,
				ClosePosition: 0.5,
			})
			continue
		}

		body := math.Abs(k.Close - k.Open)
		upperWick := k.High - math.Max(k.Close, k.Open)
		lowerWick := math.Min(k.Close, k.Open) - k.Low

		bodyPct := body / fullRange
		upperPct := 0.0
		lowerPct := 0.0
		if fullRange > 0 {
			upperPct = upperWick / fullRange
			lowerPct = lowerWick / fullRange
		}

		closePos := (k.Close - k.Low) / fullRange

		dir := "doji"
		// 实体稍微大一点才算多空，避免噪声十字星
		if bodyPct >= 0.15 {
			if k.Close >= k.Open {
				dir = "bull"
			} else {
				dir = "bear"
			}
		}

		rangeVsATR := 0.0
		if atr > 0 {
			rangeVsATR = fullRange / atr
		}

		shapes = append(shapes, CandleShape{
			Direction:     dir,
			BodyPct:       bodyPct,
			UpperWickPct:  upperPct,
			LowerWickPct:  lowerPct,
			RangeVsATR:    rangeVsATR,
			ClosePosition: closePos,
		})
	}

	return shapes
}

// calculateATR 计算ATR
func calculateATR(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	trs := make([]float64, len(klines))
	for i := 1; i < len(klines); i++ {
		high := klines[i].High
		low := klines[i].Low
		prevClose := klines[i-1].Close

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		trs[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	for i := period + 1; i < len(klines); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}

	return atr
}

// CalculateBollinger 计算布林带指标（导出给API使用）
func CalculateBollinger(klines []Kline, period int, mult float64) *BollingerBand {
	if len(klines) < period {
		return nil
	}

	start := len(klines) - period
	sum := 0.0
	for i := start; i < len(klines); i++ {
		sum += klines[i].Close
	}
	middle := sum / float64(period)

	var variance float64
	for i := start; i < len(klines); i++ {
		diff := klines[i].Close - middle
		variance += diff * diff
	}
	variance = variance / float64(period)
	stddev := math.Sqrt(variance)

	upper := middle + mult*stddev
	lower := middle - mult*stddev

	price := klines[len(klines)-1].Close

	width := 0.0
	if middle != 0 {
		width = (upper - lower) / middle
	}

	percent := 0.0
	if upper != lower {
		percent = (price - lower) / (upper - lower)
	}

	return &BollingerBand{
		Upper:   upper,
		Middle:  middle,
		Lower:   lower,
		Width:   width,
		Percent: percent,
	}
}

// calculateIntradaySeries 计算日内系列数据（5m）
func calculateIntradaySeries(klines []Kline) *IntradayData {
	data := &IntradayData{
		MidPrices:     make([]float64, 0, 40),
		EMA20Values:   make([]float64, 0, 40),
		MACDValues:    make([]*MACDSignal, 0, 40),
		RSI7Values:    make([]float64, 0, 40),
		RSI14Values:   make([]float64, 0, 40),
		Volumes:       make([]float64, 0, 40),
		BuySellRatios: make([]float64, 0, 40),
	}

	// 使用足够的历史数据，至少35根用于MACD计算
	start := len(klines) - 40
	if start < 0 {
		start = 0
	}
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)
		data.Volumes = append(data.Volumes, klines[i].Volume)
		data.BuySellRatios = append(data.BuySellRatios, klines[i].BuySellRatio)

		if i >= 19 {
			ema20 := CalculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		if i >= 25 { // 5分钟使用更快的MACD参数
			macdSignal := CalculateMACDSignalWithTimeframe(klines[:i+1], "5m")
			data.MACDValues = append(data.MACDValues, macdSignal)
		}

		if i >= 7 {
			rsi7 := CalculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := CalculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	// 计算OBV
	data.OBVValues = calculateOBV(klines)
	if len(data.OBVValues) > 10 {
		data.OBVValues = data.OBVValues[len(data.OBVValues)-10:]
	}

	return data
}

// calculateMidTermSeries15m 计算15分钟系列数据
func calculateMidTermSeries15m(klines []Kline) *MidTermData15m {
	data := &MidTermData15m{
		MidPrices:   make([]float64, 0, 50),
		EMA20Values: make([]float64, 0, 50),
		EMA50Values: make([]float64, 0, 50),
		MACDValues:  make([]*MACDSignal, 0, 50),
		RSI7Values:  make([]float64, 0, 50),
		RSI14Values: make([]float64, 0, 50),
	}

	// 使用足够的历史数据，至少35根用于MACD计算
	start := len(klines) - 50
	if start < 0 {
		start = 0
	}
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)

		if i >= 19 {
			ema20 := CalculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		if i >= 49 {
			ema50 := CalculateEMA(klines[:i+1], 50)
			data.EMA50Values = append(data.EMA50Values, ema50)
		}

		if i >= 35 { // 需要足够的数据计算完整的MACD信号
			macdSignal := CalculateMACDSignalWithTimeframe(klines[:i+1], "15m")
			data.MACDValues = append(data.MACDValues, macdSignal)
		}

		rsiShort, rsiLong, _, _, _, _, _, _, _ := getTechnicalIndicatorParams("15m")
		if i >= rsiShort {
			rsiShortVal := CalculateRSI(klines[:i+1], rsiShort)
			data.RSI7Values = append(data.RSI7Values, rsiShortVal)
		}
		if i >= rsiLong {
			rsiLongVal := CalculateRSI(klines[:i+1], rsiLong)
			data.RSI14Values = append(data.RSI14Values, rsiLongVal)
		}
	}

	_, _, bollingerPeriod, _, _, _, mfiPeriod, _, bollingerMult := getTechnicalIndicatorParams("15m")
	data.Bollinger = CalculateBollinger(klines, bollingerPeriod, bollingerMult)
	data.VWAP = calculateVWAP(klines)
	data.MFI = calculateMFI(klines, mfiPeriod)

	// 计算OBV
	data.OBVValues = calculateOBV(klines)
	if len(data.OBVValues) > 10 {
		data.OBVValues = data.OBVValues[len(data.OBVValues)-10:]
	}

	return data
}

// calculateMidTermSeries1h 计算1小时系列数据
func calculateMidTermSeries1h(klines []Kline) *MidTermData1h {
	data := &MidTermData1h{
		MidPrices:    make([]float64, 0, 50),
		EMA20Values:  make([]float64, 0, 10),
		EMA50Values:  make([]float64, 0, 10),
		EMA100Values: make([]float64, 0, 10),
		EMA200Values: make([]float64, 0, 10),
		MACDValues:   make([]*MACDSignal, 0, 50),
		RSI7Values:   make([]float64, 0, 50),
		RSI14Values:  make([]float64, 0, 50),
	}

	// 使用足够的历史数据，至少35根用于MACD计算
	start := len(klines) - 50
	if start < 0 {
		start = 0
	}
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)

		if i >= 19 {
			ema20 := CalculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		if i >= 49 {
			ema50 := CalculateEMA(klines[:i+1], 50)
			data.EMA50Values = append(data.EMA50Values, ema50)
		}

		if i >= 99 {
			ema100 := CalculateEMA(klines[:i+1], 100)
			data.EMA100Values = append(data.EMA100Values, ema100)
		}

		if i >= 199 {
			ema200 := CalculateEMA(klines[:i+1], 200)
			data.EMA200Values = append(data.EMA200Values, ema200)
		}

		if i >= 35 { // 需要足够的数据计算完整的MACD信号
			macdSignal := CalculateMACDSignalWithTimeframe(klines[:i+1], "1h")
			data.MACDValues = append(data.MACDValues, macdSignal)
		}

		rsiShort, rsiLong, _, _, _, _, _, _, _ := getTechnicalIndicatorParams("1h")
		if i >= rsiShort {
			rsiShortVal := CalculateRSI(klines[:i+1], rsiShort)
			data.RSI7Values = append(data.RSI7Values, rsiShortVal)
		}
		if i >= rsiLong {
			rsiLongVal := CalculateRSI(klines[:i+1], rsiLong)
			data.RSI14Values = append(data.RSI14Values, rsiLongVal)
		}
	}

	// 计算ADX和DI
	_, _, _, adxPeriod, _, _, _, _, _ := getTechnicalIndicatorParams("1h")
	data.ADX, data.DIPlus, data.DIMinus = calculateADX(klines, adxPeriod)
	data.VWAP = calculateVWAP(klines)

	// 计算OBV
	data.OBVValues = calculateOBV(klines)
	if len(data.OBVValues) > 10 {
		data.OBVValues = data.OBVValues[len(data.OBVValues)-10:]
	}

	return data
}

// calculateMidTermSeries4h 计算4小时时间框架数据
func calculateMidTermSeries4h(klines []Kline) *MidTermSeries4h {
	data := &MidTermSeries4h{
		MidPrices:    make([]float64, 0, 50),
		EMA20Values:  make([]float64, 0, 10),
		EMA50Values:  make([]float64, 0, 10),
		EMA100Values: make([]float64, 0, 10),
		EMA200Values: make([]float64, 0, 10),
		MACDValues:   make([]*MACDSignal, 0, 50),
		RSI7Values:   make([]float64, 0, 50),
		RSI14Values:  make([]float64, 0, 50),
	}

	// 使用足够的历史数据，至少35根用于MACD计算
	start := len(klines) - 50
	if start < 0 {
		start = 0
	}
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)

		if i >= 19 {
			ema20 := CalculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		if i >= 49 {
			ema50 := CalculateEMA(klines[:i+1], 50)
			data.EMA50Values = append(data.EMA50Values, ema50)
		}

		if i >= 99 {
			ema100 := CalculateEMA(klines[:i+1], 100)
			data.EMA100Values = append(data.EMA100Values, ema100)
		}

		if i >= 199 {
			ema200 := CalculateEMA(klines[:i+1], 200)
			data.EMA200Values = append(data.EMA200Values, ema200)
		}

		if i >= 35 { // 需要足够的数据计算完整的MACD信号
			macdSignal := CalculateMACDSignalWithTimeframe(klines[:i+1], "4h")
			data.MACDValues = append(data.MACDValues, macdSignal)
		}

		rsiShort, rsiLong, _, _, _, _, _, _, _ := getTechnicalIndicatorParams("4h")
		if i >= rsiShort {
			rsiShortVal := CalculateRSI(klines[:i+1], rsiShort)
			data.RSI7Values = append(data.RSI7Values, rsiShortVal)
		}
		if i >= rsiLong {
			rsiLongVal := CalculateRSI(klines[:i+1], rsiLong)
			data.RSI14Values = append(data.RSI14Values, rsiLongVal)
		}
	}

	// 计算技术指标
	_, _, bollingerPeriod, adxPeriod, atrShort, atrLong, _, cmfPeriod, bollingerMult := getTechnicalIndicatorParams("4h")
	data.Bollinger = CalculateBollinger(klines, bollingerPeriod, bollingerMult)
	data.ADX, data.DIPlus, data.DIMinus = calculateADX(klines, adxPeriod)
	data.ATR3 = calculateATR(klines, atrShort)
	data.ATR14 = calculateATR(klines, atrLong)

	if len(klines) > 0 {
		data.CurrentVolume = klines[len(klines)-1].Volume
		sum := 0.0
		for _, k := range klines {
			sum += k.Volume
		}
		data.AverageVolume = sum / float64(len(klines))
	}

	data.VWAP = calculateVWAP(klines)
	data.CMF = calculateCMF(klines, cmfPeriod)
	_, _, _, _, _, _, mfiPeriod, _, _ := getTechnicalIndicatorParams("4h")
	data.MFI = calculateMFI(klines, mfiPeriod)

	// 计算OBV
	data.OBVValues = calculateOBV(klines)
	if len(data.OBVValues) > 10 {
		data.OBVValues = data.OBVValues[len(data.OBVValues)-10:]
	}

	return data
}

// getOpenInterestData 获取OI数据
func getOpenInterestData(symbol string) (*OIData, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/openInterest?symbol=%s", symbol)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		OpenInterest string `json:"openInterest"`
		Symbol       string `json:"symbol"`
		Time         int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	oi, _ := strconv.ParseFloat(result.OpenInterest, 64)

	return &OIData{
		Latest:  oi,
		Average: oi * 0.999,
	}, nil
}

// getFundingRate 获取资金费率
func getFundingRate(symbol string) (float64, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/premiumIndex?symbol=%s", symbol)

	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var result struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		IndexPrice      string `json:"indexPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
		InterestRate    string `json:"interestRate"`
		Time            int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	rate, _ := strconv.ParseFloat(result.LastFundingRate, 64)
	return rate, nil
}

// fetchDerivativesSuite 抓取15m/1h/4h的衍生品指标
func fetchDerivativesSuite(symbol string) *DerivativesData {
	intervals := []string{"15m", "1h", "4h"}
	limit := 30

	data := &DerivativesData{
		OpenInterestHist:    make(map[string][]OpenInterestHistEntry),
		TopLongShortRatio:   make(map[string][]LongShortRatioEntry),
		GlobalLongShortAcct: make(map[string][]LongShortRatioEntry),
		TakerBuySellVolume:  make(map[string][]TakerBuySellEntry),
		Basis:               make(map[string][]BasisEntry),
	}

	for _, interval := range intervals {
		if entries, err := fetchOpenInterestHistory(symbol, interval, limit); err == nil && len(entries) > 0 {
			data.OpenInterestHist[interval] = entries
		}
		if entries, err := fetchTopLongShortRatio(symbol, interval, limit); err == nil && len(entries) > 0 {
			data.TopLongShortRatio[interval] = entries
		}
		if entries, err := fetchGlobalAccountRatio(symbol, interval, limit); err == nil && len(entries) > 0 {
			data.GlobalLongShortAcct[interval] = entries
		}
		if entries, err := fetchTakerBuySellRatio(symbol, interval, limit); err == nil && len(entries) > 0 {
			data.TakerBuySellVolume[interval] = entries
		}
		if entries, err := fetchBasisSeries(symbol, interval, limit); err == nil && len(entries) > 0 {
			data.Basis[interval] = entries
		}
	}

	if entries, err := fetchFundingRateHistory(symbol, limit); err == nil && len(entries) > 0 {
		data.FundingRateHistory = entries
	}

	if len(data.OpenInterestHist) == 0 {
		data.OpenInterestHist = nil
	}
	if len(data.TopLongShortRatio) == 0 {
		data.TopLongShortRatio = nil
	}
	if len(data.GlobalLongShortAcct) == 0 {
		data.GlobalLongShortAcct = nil
	}
	if len(data.TakerBuySellVolume) == 0 {
		data.TakerBuySellVolume = nil
	}
	if len(data.Basis) == 0 {
		data.Basis = nil
	}
	if data.isEmpty() {
		return nil
	}
	return data
}

func fetchOpenInterestHistory(symbol, period string, limit int) ([]OpenInterestHistEntry, error) {
	url := fmt.Sprintf("https://fapi.binance.com/futures/data/openInterestHist?symbol=%s&period=%s&limit=%d", symbol, period, limit)
	var raw []struct {
		SumOpenInterest      string `json:"sumOpenInterest"`
		SumOpenInterestValue string `json:"sumOpenInterestValue"`
		Timestamp            int64  `json:"timestamp"`
	}
	if err := performBinanceGET(url, &raw); err != nil {
		return nil, err
	}
	out := make([]OpenInterestHistEntry, 0, len(raw))
	for _, item := range raw {
		out = append(out, OpenInterestHistEntry{
			Timestamp:            item.Timestamp,
			SumOpenInterest:      safeParseFloat(item.SumOpenInterest),
			SumOpenInterestValue: safeParseFloat(item.SumOpenInterestValue),
		})
	}
	return out, nil
}

func fetchTopLongShortRatio(symbol, period string, limit int) ([]LongShortRatioEntry, error) {
	url := fmt.Sprintf("https://fapi.binance.com/futures/data/topLongShortPositionRatio?symbol=%s&period=%s&limit=%d", symbol, period, limit)
	var raw []struct {
		LongShortRatio string `json:"longShortRatio"`
		LongAccount    string `json:"longAccount"`
		ShortAccount   string `json:"shortAccount"`
		Timestamp      int64  `json:"timestamp"`
	}
	if err := performBinanceGET(url, &raw); err != nil {
		return nil, err
	}
	out := make([]LongShortRatioEntry, 0, len(raw))
	for _, item := range raw {
		out = append(out, LongShortRatioEntry{
			Timestamp:    item.Timestamp,
			LongAccount:  safeParseFloat(item.LongAccount),
			ShortAccount: safeParseFloat(item.ShortAccount),
			Ratio:        safeParseFloat(item.LongShortRatio),
		})
	}
	return out, nil
}

func fetchGlobalAccountRatio(symbol, period string, limit int) ([]LongShortRatioEntry, error) {
	url := fmt.Sprintf("https://fapi.binance.com/futures/data/globalLongShortAccountRatio?symbol=%s&period=%s&limit=%d", symbol, period, limit)
	var raw []struct {
		LongShortRatio string `json:"longShortRatio"`
		LongAccount    string `json:"longAccount"`
		ShortAccount   string `json:"shortAccount"`
		Timestamp      int64  `json:"timestamp"`
	}
	if err := performBinanceGET(url, &raw); err != nil {
		return nil, err
	}
	out := make([]LongShortRatioEntry, 0, len(raw))
	for _, item := range raw {
		out = append(out, LongShortRatioEntry{
			Timestamp:    item.Timestamp,
			LongAccount:  safeParseFloat(item.LongAccount),
			ShortAccount: safeParseFloat(item.ShortAccount),
			Ratio:        safeParseFloat(item.LongShortRatio),
		})
	}
	return out, nil
}

func fetchTakerBuySellRatio(symbol, period string, limit int) ([]TakerBuySellEntry, error) {
	url := fmt.Sprintf("https://fapi.binance.com/futures/data/takerlongshortRatio?symbol=%s&period=%s&limit=%d", symbol, period, limit)
	var raw []struct {
		BuyVol       string `json:"buyVol"`
		SellVol      string `json:"sellVol"`
		BuySellRatio string `json:"buySellRatio"`
		Timestamp    int64  `json:"timestamp"`
	}
	if err := performBinanceGET(url, &raw); err != nil {
		return nil, err
	}
	out := make([]TakerBuySellEntry, 0, len(raw))
	for _, item := range raw {
		out = append(out, TakerBuySellEntry{
			Timestamp:    item.Timestamp,
			BuyVolume:    safeParseFloat(item.BuyVol),
			SellVolume:   safeParseFloat(item.SellVol),
			BuySellRatio: safeParseFloat(item.BuySellRatio),
		})
	}
	return out, nil
}

func fetchBasisSeries(symbol, period string, limit int) ([]BasisEntry, error) {
	url := fmt.Sprintf("https://fapi.binance.com/futures/data/basis?symbol=%s&period=%s&limit=%d", symbol, period, limit)
	var raw []struct {
		Basis        string `json:"basis"`
		BasisRate    string `json:"basisRate"`
		FuturesPrice string `json:"futuresPrice"`
		IndexPrice   string `json:"indexPrice"`
		Timestamp    int64  `json:"timestamp"`
	}
	if err := performBinanceGET(url, &raw); err != nil {
		return nil, err
	}
	out := make([]BasisEntry, 0, len(raw))
	for _, item := range raw {
		out = append(out, BasisEntry{
			Timestamp:    item.Timestamp,
			Basis:        safeParseFloat(item.Basis),
			BasisRate:    safeParseFloat(item.BasisRate),
			FuturesPrice: safeParseFloat(item.FuturesPrice),
			IndexPrice:   safeParseFloat(item.IndexPrice),
		})
	}
	return out, nil
}

func fetchFundingRateHistory(symbol string, limit int) ([]FundingRateEntry, error) {
	url := fmt.Sprintf("https://fapi.binance.com/futures/data/fundingRate?symbol=%s&limit=%d", symbol, limit)
	var raw []struct {
		FundingRate string `json:"fundingRate"`
		FundingTime int64  `json:"fundingTime"`
	}
	if err := performBinanceGET(url, &raw); err != nil {
		return nil, err
	}
	out := make([]FundingRateEntry, 0, len(raw))
	for _, item := range raw {
		out = append(out, FundingRateEntry{
			Timestamp:   item.FundingTime,
			FundingRate: safeParseFloat(item.FundingRate),
		})
	}
	return out, nil
}

// getOrderbookSummary 从 Binance U 期货深度接口抓取轻量订单簿摘要（非致命）
func getOrderbookSummary(symbol string) (*MicrostructureSummary, error) {
	type binanceDepthResp struct {
		LastUpdateId int64      `json:"lastUpdateId"`
		Bids         [][]string `json:"bids"`
		Asks         [][]string `json:"asks"`
	}

	// 请求带超时，避免阻塞主循环（建议 2s）
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/depth?symbol=%s&limit=100", symbol)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("binance depth status %d: %s", resp.StatusCode, string(b))
	}

	var d binanceDepthResp
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}

	// 若无 bids/asks，返回空 summary（非致命）
	if len(d.Bids) == 0 || len(d.Asks) == 0 {
		return &MicrostructureSummary{TsMs: time.Now().UnixMilli()}, nil
	}

	bestBidPrice := safeParseFloat(d.Bids[0][0])
	bestBidQty := safeParseFloat(d.Bids[0][1])
	bestAskPrice := safeParseFloat(d.Asks[0][0])
	bestAskQty := safeParseFloat(d.Asks[0][1])

	// 计算名义价值 (price * qty)
	bestBidNotional := bestBidPrice * bestBidQty
	bestAskNotional := bestAskPrice * bestAskQty
	minNotional := math.Min(bestBidNotional, bestAskNotional)

	mid := (bestBidPrice + bestAskPrice) / 2.0
	spreadBps := 0.0
	if mid > 0 {
		spreadBps = (bestAskPrice - bestBidPrice) / mid * 1e4
	}

	tiny := 1e-12
	depthRatio := 0.0
	if bestAskQty > tiny {
		depthRatio = bestBidQty / math.Max(bestAskQty, tiny)
	}

	// 计算前10档的累计名义价值
	depthNotional10 := 0.0
	bidLevels := int(math.Min(10, float64(len(d.Bids))))
	askLevels := int(math.Min(10, float64(len(d.Asks))))

	for i := 0; i < bidLevels; i++ {
		price := safeParseFloat(d.Bids[i][0])
		qty := safeParseFloat(d.Bids[i][1])
		depthNotional10 += price * qty
	}

	for i := 0; i < askLevels; i++ {
		price := safeParseFloat(d.Asks[i][0])
		qty := safeParseFloat(d.Asks[i][1])
		depthNotional10 += price * qty
	}

	return &MicrostructureSummary{
		TsMs:            time.Now().UnixMilli(),
		BestBidPrice:    bestBidPrice,
		BestAskPrice:    bestAskPrice,
		BestBidQty:      bestBidQty,
		BestAskQty:      bestAskQty,
		BestBidNotional: bestBidNotional,
		BestAskNotional: bestAskNotional,
		MinNotional:     minNotional,
		DepthNotional10: depthNotional10,
		DepthRatio:      depthRatio,
		SpreadBps:       spreadBps,
	}, nil
}

func performBinanceGET(url string, target interface{}) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("binance request failed: %s", resp.Status)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func safeParseFloat(val string) float64 {
	if val == "" {
		return 0
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0
	}
	return f
}

// ===== 价格行为：核心复刻（精简版） =====
// 与 TradingView Pine Script 保持一致

func calcPriceActionSummary(klines []Kline, timeframe string, zigzagLen, liquidityLen, trendLineLen int) *PriceActionSummary {
	n := len(klines)

	// 至少需要一定数量的K线才能做结构/斜率判断：
	// - zigzagLen*2+2 保证左右各有 zigzagLen 根
	// - 20 做一个下限，避免太少的数据噪声太大
	minNeed := max3(zigzagLen*2+2, 20, 20)
	if n < minNeed {
		return &PriceActionSummary{Timeframe: timeframe, LastSignal: "none"}
	}

	// 1) ZigZag 趋势判断（按照 Pine Script 逻辑）
	// Pine Script: to_up := high[zigzagLen] >= ta.highest(high, zigzagLen)
	//              to_down := low[zigzagLen] <= ta.lowest(low, zigzagLen)
	//              trend := trend == 1 and to_down ? -1 : trend == -1 and to_up ? 1 : trend
	var highValIndex []int64
	var highVal []float64
	var lowValIndex []int64
	var lowVal []float64

	trend := 1 // 1 = up, -1 = down
	prevTrend := 1

	for i := zigzagLen; i < n; i++ {
		// 计算 ta.highest(high, zigzagLen) - 最近 zigzagLen 根K线的最高点
		highestHigh := klines[i-zigzagLen+1].High
		for j := i - zigzagLen + 1; j <= i; j++ {
			if klines[j].High > highestHigh {
				highestHigh = klines[j].High
			}
		}

		// 计算 ta.lowest(low, zigzagLen) - 最近 zigzagLen 根K线的最低点
		lowestLow := klines[i-zigzagLen+1].Low
		for j := i - zigzagLen + 1; j <= i; j++ {
			if klines[j].Low < lowestLow {
				lowestLow = klines[j].Low
			}
		}

		// Pine Script: to_up := high[zigzagLen] >= ta.highest(high, zigzagLen)
		toUp := klines[i-zigzagLen].High >= highestHigh

		// Pine Script: to_down := low[zigzagLen] <= ta.lowest(low, zigzagLen)
		toDown := klines[i-zigzagLen].Low <= lowestLow

		// Pine Script: trend := trend == 1 and to_down ? -1 : trend == -1 and to_up ? 1 : trend
		prevTrend = trend
		if trend == 1 && toDown {
			trend = -1
		} else if trend == -1 && toUp {
			trend = 1
		}

		// Pine Script: if ta.change(trend) != 0 and trend == 1
		// 如果趋势发生变化，记录 pivot 点
		if trend != prevTrend {
			if trend == 1 {
				// 趋势转为上升，记录高点（对应 Pine Script 的 time[zigzagLen] 和 high[zigzagLen]）
				highValIndex = append(highValIndex, klines[i-zigzagLen].OpenTime)
				highVal = append(highVal, klines[i-zigzagLen].High)
			} else if trend == -1 {
				// 趋势转为下降，记录低点（对应 Pine Script 的 time[zigzagLen] 和 low[zigzagLen]）
				lowValIndex = append(lowValIndex, klines[i-zigzagLen].OpenTime)
				lowVal = append(lowVal, klines[i-zigzagLen].Low)
			}
		}
	}

	// 如果连一个高点/低点都没找出来，就没法做结构判断，直接返回空结果
	if len(highVal) == 0 || len(lowVal) == 0 {
		return &PriceActionSummary{
			Timeframe:  timeframe,
			LastSignal: "none",
			BullSlope:  0,
			BearSlope:  0,
		}
	}

	// 2) 计算趋势线斜率（使用 trendLineLen 作为 pivot 参数，与 Pine Script 一致）
	trendPivotHighIdx, trendPivotLowIdx := computePivots(klines, trendLineLen)
	bullSlope := 0.0
	bearSlope := 0.0
	if len(trendPivotLowIdx) >= 2 {
		i1 := trendPivotLowIdx[len(trendPivotLowIdx)-2]
		i2 := trendPivotLowIdx[len(trendPivotLowIdx)-1]
		if i2 != i1 {
			bullSlope = (klines[i2].Low - klines[i1].Low) / float64(i2-i1)
		}
	}
	if len(trendPivotHighIdx) >= 2 {
		i1 := trendPivotHighIdx[len(trendPivotHighIdx)-2]
		i2 := trendPivotHighIdx[len(trendPivotHighIdx)-1]
		if i2 != i1 {
			bearSlope = (klines[i2].High - klines[i1].High) / float64(i2-i1)
		}
	}

	// 3) 结构信号（BOS/CHoCH）- 修复逻辑，与 TradingView LuxAlgo 源码一致
	// TradingView 逻辑：
	//   - 向上突破：如果 trend.bias == BEARISH → CHOCH，否则 → BOS
	//   - 向下突破：如果 trend.bias == BULLISH → CHOCH，否则 → BOS
	// 首先建立时间戳到索引的映射，提高查找效率
	timeToIdx := make(map[int64]int)
	for i := 0; i < n; i++ {
		timeToIdx[klines[i].OpenTime] = i
	}

	// 将 highValIndex 和 lowValIndex 转换为索引数组
	var highValIdx []int
	var lowValIdx []int
	for _, ts := range highValIndex {
		if idx, ok := timeToIdx[ts]; ok {
			highValIdx = append(highValIdx, idx)
		}
	}
	for _, ts := range lowValIndex {
		if idx, ok := timeToIdx[ts]; ok {
			lowValIdx = append(lowValIdx, idx)
		}
	}

	lastSignal := "none"
	var lastSignalTime int64
	// 趋势偏差：0=中性, 1=多头, -1=空头（与 TradingView 的 trend.bias 一致）
	trendBias := 0 // 初始为中性
	atr14 := calculateATR(klines, 14)

	type rawOB struct {
		bear             bool
		lower, upper     float64
		startIdx, endIdx int
	}
	var obCandidates []rawOB

	// 遍历所有K线，检测结构突破
	// 使用 crossed 标志防止重复触发（与 TradingView 的 p_ivot.crossed 一致）
	highCrossed := false
	lowCrossed := false
	hPtr, lPtr := 0, 0
	for i := 0; i < n; i++ {
		c := klines[i].Close
		var prevClose float64
		if i > 0 {
			prevClose = klines[i-1].Close
		}

		// 更新指针，找到当前K线之前最近的高点和低点
		for hPtr+1 < len(highValIdx) && highValIdx[hPtr+1] < i {
			hPtr++
			highCrossed = false // 新的 pivot high 形成，重置 crossed 标志
		}
		for lPtr+1 < len(lowValIdx) && lowValIdx[lPtr+1] < i {
			lPtr++
			lowCrossed = false // 新的 pivot low 形成，重置 crossed 标志
		}

		if hPtr >= len(highValIdx) || lPtr >= len(lowValIdx) {
			continue
		}

		lastHigh := highVal[hPtr]
		lastHighIdx := highValIdx[hPtr]
		lastLow := lowVal[lPtr]
		lastLowIdx := lowValIdx[lPtr]

		// 向上结构突破
		// TradingView: ta.crossover(close, p_ivot.currentLevel) and not p_ivot.crossed
		// 需要前一根K线未突破，当前K线突破（crossover 逻辑）
		crossoverUp := (i == 0 || prevClose <= lastHigh) && c > lastHigh
		if len(highVal) > 1 && crossoverUp && !highCrossed {
			// TradingView: tag = t_rend.bias == BEARISH ? CHOCH : BOS
			if trendBias == -1 {
				lastSignal = "CHoCH_up" // 从空头转为多头（特征变化）
			} else {
				lastSignal = "BOS_up" // 多头延续（结构突破）
			}
			lastSignalTime = klines[i].CloseTime
			trendBias = 1      // 更新趋势偏差为多头
			highCrossed = true // 标记已突破，防止重复触发

			// 生成看多OB：从上一个pivotHigh到当前i内的最低价
			// Pine Script: 从上一个 pivotHigh 到当前，找最低价
			// 找到上一个 pivotHigh（如果有的话）
			left := 0
			if hPtr > 0 {
				left = highValIdx[hPtr-1]
			} else {
				left = lastHighIdx
			}
			minLow, minLowIdx := minLowBetween(klines, left, i)
			obLower := minLow
			obUpper := minLow + atr14
			obCandidates = append(obCandidates, rawOB{
				bear: false, lower: obLower, upper: obUpper, startIdx: minLowIdx, endIdx: i,
			})
		}

		// 向下结构突破
		// TradingView: ta.crossunder(close, p_ivot.currentLevel) and not p_ivot.crossed
		// 需要前一根K线未突破，当前K线突破（crossunder 逻辑）
		crossunderDown := (i == 0 || prevClose >= lastLow) && c < lastLow
		if len(lowVal) > 1 && crossunderDown && !lowCrossed {
			// TradingView: tag = t_rend.bias == BULLISH ? CHOCH : BOS
			if trendBias == 1 {
				lastSignal = "CHoCH_down" // 从多头转为空头（特征变化）
			} else {
				lastSignal = "BOS_down" // 空头延续（结构突破）
			}
			lastSignalTime = klines[i].CloseTime
			trendBias = -1    // 更新趋势偏差为空头
			lowCrossed = true // 标记已突破，防止重复触发

			// 生成看空OB：从上一个pivotLow到当前i内的最高价
			// Pine Script: 从上一个 pivotLow 到当前，找最高价
			// 找到上一个 pivotLow（如果有的话）
			left := 0
			if lPtr > 0 {
				left = lowValIdx[lPtr-1]
			} else {
				left = lastLowIdx
			}
			maxHigh, maxHighIdx := maxHighBetween(klines, left, i)
			obUpper := maxHigh
			obLower := maxHigh - atr14
			obCandidates = append(obCandidates, rawOB{
				bear: true, lower: obLower, upper: obUpper, startIdx: maxHighIdx, endIdx: i,
			})
		}
	}

	// 4) OB有效性筛选，只保留"未失效"的最近1~2个
	var bullOB, bearOB []OB
	for k := len(obCandidates) - 1; k >= 0 && (len(bullOB) < 2 || len(bearOB) < 2); k-- {
		ob := obCandidates[k]
		broken := false
		for j := ob.endIdx; j < n; j++ {
			if ob.bear {
				// 看空OB失效：收盘 > 上沿
				if klines[j].Close > ob.upper {
					broken = true
					break
				}
			} else {
				// 看多OB失效：收盘 < 下沿
				if klines[j].Close < ob.lower {
					broken = true
					break
				}
			}
		}
		if !broken {
			rec := OB{
				Lower:   ob.lower,
				Upper:   ob.upper,
				StartTS: klines[ob.startIdx].OpenTime,
				EndTS:   klines[ob.endIdx].CloseTime,
			}
			if ob.bear {
				if len(bearOB) < 2 {
					bearOB = append(bearOB, rec)
				}
			} else {
				if len(bullOB) < 2 {
					bullOB = append(bullOB, rec)
				}
			}
		}
	}

	// 5) liquidity sweep：使用 liquidityLen 作为 pivot 参数（与 Pine Script 一致）
	liquidityPivotHighIdx, liquidityPivotLowIdx := computePivots(klines, liquidityLen)
	sweptHighs := detectSweptHighs(klines, liquidityPivotHighIdx, 2)
	sweptLows := detectSweptLows(klines, liquidityPivotLowIdx, 2)

	return &PriceActionSummary{
		Timeframe:      timeframe,
		LastSignal:     lastSignal,
		LastSignalTime: lastSignalTime,
		BullishOB:      bullOB,
		BearishOB:      bearOB,
		SweptHighs:     sweptHighs,
		SweptLows:      sweptLows,
		BullSlope:      bullSlope,
		BearSlope:      bearSlope,
	}
}

// ---- ICT helpers ----

// detectFVGs 从K线检测最近的多/空 FVG（连续三根K线的缺口），返回最多 keep 个
func detectFVGs(klines []Kline, timeframe string, keep int) []ICTPOIEntry {
	if len(klines) < 3 || keep <= 0 {
		return nil
	}
	var res []ICTPOIEntry
	for i := len(klines) - 3; i >= 0 && len(res) < keep; i-- {
		k1 := klines[i]
		k3 := klines[i+2]

		// Bullish FVG: 第一根高点 < 第三根低点
		if k1.High < k3.Low {
			lower := k1.High
			upper := k3.Low
			res = append(res, ICTPOIEntry{
				Type:      "fvg_bull",
				Upper:     upper,
				Lower:     lower,
				Mid:       (upper + lower) / 2,
				Timeframe: timeframe,
			})
			continue
		}
		// Bearish FVG: 第一根低点 > 第三根高点
		if k1.Low > k3.High {
			lower := k3.High
			upper := k1.Low
			res = append(res, ICTPOIEntry{
				Type:      "fvg_bear",
				Upper:     upper,
				Lower:     lower,
				Mid:       (upper + lower) / 2,
				Timeframe: timeframe,
			})
			continue
		}
	}
	return res
}

func selectRecentOB(pa *PriceActionSummary, timeframe string, keep int) []ICTPOIEntry {
	if pa == nil || keep <= 0 {
		return nil
	}
	var res []ICTPOIEntry
	// 多头 OB
	for i := len(pa.BullishOB) - 1; i >= 0 && len(res) < keep; i-- {
		ob := pa.BullishOB[i]
		res = append(res, ICTPOIEntry{
			Type:      "ob_bull",
			Upper:     ob.Upper,
			Lower:     ob.Lower,
			Mid:       (ob.Upper + ob.Lower) / 2,
			Timeframe: timeframe,
		})
	}
	// 空头 OB
	for i := len(pa.BearishOB) - 1; i >= 0 && len(res) < keep; i-- {
		ob := pa.BearishOB[i]
		res = append(res, ICTPOIEntry{
			Type:      "ob_bear",
			Upper:     ob.Upper,
			Lower:     ob.Lower,
			Mid:       (ob.Upper + ob.Lower) / 2,
			Timeframe: timeframe,
		})
	}
	return res
}

func buildLiquiditySummary(pa ...*PriceActionSummary) *ICTLiquidity {
	liq := &ICTLiquidity{}
	for _, p := range pa {
		if p == nil {
			continue
		}
		if len(p.SweptHighs) > 0 && liq.RecentSweptHigh == 0 {
			last := p.SweptHighs[len(p.SweptHighs)-1]
			liq.RecentSweptHigh = last.Price
		}
		if len(p.SweptLows) > 0 && liq.RecentSweptLow == 0 {
			last := p.SweptLows[len(p.SweptLows)-1]
			liq.RecentSweptLow = last.Price
		}
	}
	if liq.RecentSweptHigh == 0 && liq.RecentSweptLow == 0 &&
		len(liq.EqualHighs) == 0 && len(liq.EqualLows) == 0 {
		return nil
	}
	return liq
}

func computePremiumDiscountFromSwing(swingHigh, swingLow, price float64, basis string) *ICTPremiumDiscount {
	if swingHigh <= swingLow || price <= 0 {
		return nil
	}
	mid := (swingHigh + swingLow) / 2
	p618 := swingLow + (swingHigh-swingLow)*0.618
	p382 := swingLow + (swingHigh-swingLow)*0.382
	posPct := (price - swingLow) / (swingHigh - swingLow) * 100
	return &ICTPremiumDiscount{
		Basis:  basis,
		Mid:    mid,
		P618:   p618,
		P382:   p382,
		PosPct: posPct,
	}
}

func highestLowest(klines []Kline) (high float64, low float64) {
	if len(klines) == 0 {
		return 0, 0
	}
	high = klines[0].High
	low = klines[0].Low
	for _, k := range klines {
		if k.High > high {
			high = k.High
		}
		if k.Low < low {
			low = k.Low
		}
	}
	return high, low
}

func computePivots(klines []Kline, span int) (phIdx []int, plIdx []int) {
	n := len(klines)
	for i := span; i < n-span; i++ {
		isHigh := true
		isLow := true
		h := klines[i].High
		l := klines[i].Low
		for j := i - span; j <= i+span; j++ {
			if klines[j].High > h {
				isHigh = false
			}
			if klines[j].Low < l {
				isLow = false
			}
			if !isHigh && !isLow {
				break
			}
		}
		if isHigh {
			phIdx = append(phIdx, i)
		}
		if isLow {
			plIdx = append(plIdx, i)
		}
	}
	return
}

func maxHighBetween(klines []Kline, L, R int) (float64, int) {
	if L < 0 {
		L = 0
	}
	if R >= len(klines) {
		R = len(klines) - 1
	}
	maxV := klines[L].High
	maxI := L
	for i := L; i <= R; i++ {
		if klines[i].High > maxV {
			maxV = klines[i].High
			maxI = i
		}
	}
	return maxV, maxI
}

func minLowBetween(klines []Kline, L, R int) (float64, int) {
	if L < 0 {
		L = 0
	}
	if R >= len(klines) {
		R = len(klines) - 1
	}
	minV := klines[L].Low
	minI := L
	for i := L; i <= R; i++ {
		if klines[i].Low < minV {
			minV = klines[i].Low
			minI = i
		}
	}
	return minV, minI
}

func detectSweptHighs(klines []Kline, phIdx []int, keep int) []LiquidityLine {
	var res []LiquidityLine
	n := len(klines)
	for k := len(phIdx) - 1; k >= 0 && len(res) < keep; k-- {
		idx := phIdx[k]
		level := klines[idx].High
		// 先被刺穿（high>level），再收回（close<level）
		swept := false
		var t int64
		for i := idx + 1; i < n; i++ {
			if klines[i].High > level {
				// 产生刺穿，等待收回
				for j := i; j < n; j++ {
					if klines[j].Close < level {
						swept = true
						t = klines[j].CloseTime
						break
					}
				}
				break
			}
		}
		if swept {
			res = append(res, LiquidityLine{Price: level, Time: t})
		}
	}
	return res
}

func detectSweptLows(klines []Kline, plIdx []int, keep int) []LiquidityLine {
	var res []LiquidityLine
	n := len(klines)
	for k := len(plIdx) - 1; k >= 0 && len(res) < keep; k-- {
		idx := plIdx[k]
		level := klines[idx].Low
		// 先被刺穿（low<level），再收回（close>level）
		swept := false
		var t int64
		for i := idx + 1; i < n; i++ {
			if klines[i].Low < level {
				for j := i; j < n; j++ {
					if klines[j].Close > level {
						swept = true
						t = klines[j].CloseTime
						break
					}
				}
				break
			}
		}
		if swept {
			res = append(res, LiquidityLine{Price: level, Time: t})
		}
	}
	return res
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func max3(a, b, c int) int { return max2(max2(a, b), c) }

// Format 格式化输出市场数据
func Format(data *Data) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("current_price = %.2f, current_ema20 = %.3f, current_macd = %.3f, current_rsi (7 period) = %.3f\n\n",
		data.CurrentPrice, data.CurrentEMA20, data.CurrentMACD, data.CurrentRSI7))

	// ICT 摘要（精简：每周期仅保留1-2个最近OB/FVG）
	if len(data.ICTPOI) > 0 || data.ICTLiquidity != nil || data.ICTPremiumDiscount != nil {
		sb.WriteString("ICT summary:\n")
		if len(data.ICTPOI) > 0 {
			sb.WriteString("  POI: ")
			maxShow := len(data.ICTPOI)
			if maxShow > 2 {
				maxShow = 2 // 精简：仅保留最近1-2个
			}
			for i := 0; i < maxShow; i++ {
				poi := data.ICTPOI[i]
				sb.WriteString(fmt.Sprintf("[%s %s %.4f~%.4f mid=%.4f] ",
					poi.Timeframe, poi.Type, poi.Lower, poi.Upper, poi.Mid))
			}
			if len(data.ICTPOI) > maxShow {
				sb.WriteString("... ")
			}
			sb.WriteString("\n")
		}
		if data.ICTLiquidity != nil {
			// 精简：仅保留最近swept_high/low
			sb.WriteString(fmt.Sprintf("  Liquidity: swept_high=%.4f swept_low=%.4f\n",
				data.ICTLiquidity.RecentSweptHigh, data.ICTLiquidity.RecentSweptLow))
		}
		if data.ICTPremiumDiscount != nil {
			sb.WriteString(fmt.Sprintf("  Premium/Discount (%s): mid=%.4f p618=%.4f p382=%.4f pos=%.2f%%\n",
				data.ICTPremiumDiscount.Basis, data.ICTPremiumDiscount.Mid, data.ICTPremiumDiscount.P618, data.ICTPremiumDiscount.P382, data.ICTPremiumDiscount.PosPct))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("In addition, here is the latest %s open interest and funding rate for perps:\n\n",
		data.Symbol))

	if data.OpenInterest != nil {
		sb.WriteString(fmt.Sprintf("Open Interest: Latest: %.2f Average: %.2f\n\n",
			data.OpenInterest.Latest, data.OpenInterest.Average))
	}

	sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))

	// 显示距离历史极值指标
	sb.WriteString(fmt.Sprintf("Distance to ATH: %.2f%%\n\n", data.DistanceToATH))
	// Microstructure / orderbook summary（简洁，供AI参考）
	if data.Microstructure != nil {
		ms := data.Microstructure
		sb.WriteString(fmt.Sprintf("[Microstructure] spread_bps=%.2f, depth_ratio=%.2f, min_notional=%.0f\n\n",
			ms.SpreadBps, ms.DepthRatio, ms.MinNotional))
	}

	// ExecutionGate（执行门禁）
	if data.Execution != nil {
		eg := data.Execution
		sb.WriteString(fmt.Sprintf("[ExecutionGate] mode=%s, reason=%s\n\n", eg.Mode, eg.Reason))
	}

	if data.Derivatives != nil && !data.Derivatives.isEmpty() {
		sb.WriteString("Derivatives overview:\n\n")
		// 精简：仅保留汇总值，去掉长列表
		appendDerivativesOpenInterest(&sb, data.Derivatives.OpenInterestHist)
		// 精简：去掉Top L/S、Global L/S、Taker明细、Basis历史
		// appendDerivativesRatioSection(&sb, "Top traders L/S", data.Derivatives.TopLongShortRatio)
		// appendDerivativesRatioSection(&sb, "Global accounts L/S", data.Derivatives.GlobalLongShortAcct)
		// appendDerivativesTakerSection(&sb, data.Derivatives.TakerBuySellVolume)
		// appendDerivativesBasisSection(&sb, data.Derivatives.Basis)
		// 精简：Funding history仅保留最新值或均值
		appendFundingHistorySection(&sb, data.Derivatives.FundingRateHistory)
	}

	// 5m（精简：去掉5m长序列，仅保留当前值）
	// if data.IntradaySeries != nil {
	// 	sb.WriteString("Intraday series (5-minute intervals, oldest → latest):\n\n")
	// 	... 已精简，不再输出5m长序列
	// }

	// 15m（精简：仅保留当前值+简要摘要，去掉长序列）
	if data.MidTermSeries15m != nil {
		sb.WriteString("15m indicators (current values):\n")
		if len(data.MidTermSeries15m.MidPrices) > 0 {
			// 仅保留最后3-5个值
			lastN := 5
			if len(data.MidTermSeries15m.MidPrices) < lastN {
				lastN = len(data.MidTermSeries15m.MidPrices)
			}
			recent := data.MidTermSeries15m.MidPrices[len(data.MidTermSeries15m.MidPrices)-lastN:]
			sb.WriteString(fmt.Sprintf("Mid prices (last %d): %s\n", lastN, formatFloatSlice(recent)))
		}
		if len(data.MidTermSeries15m.EMA20Values) > 0 {
			lastN := 5
			if len(data.MidTermSeries15m.EMA20Values) < lastN {
				lastN = len(data.MidTermSeries15m.EMA20Values)
			}
			recent := data.MidTermSeries15m.EMA20Values[len(data.MidTermSeries15m.EMA20Values)-lastN:]
			sb.WriteString(fmt.Sprintf("EMA20 (last %d): %s\n", lastN, formatFloatSlice(recent)))
		}
		if len(data.MidTermSeries15m.MACDValues) > 0 {
			lastN := 5
			if len(data.MidTermSeries15m.MACDValues) < lastN {
				lastN = len(data.MidTermSeries15m.MACDValues)
			}
			recent := data.MidTermSeries15m.MACDValues[len(data.MidTermSeries15m.MACDValues)-lastN:]
			sb.WriteString(fmt.Sprintf("MACD (last %d):\n", lastN))
			for i, macd := range recent {
				if macd != nil {
					crossStr := ""
					if macd.Cross != "none" {
						crossStr = fmt.Sprintf(" [%s]", macd.Cross)
					}
					sb.WriteString(fmt.Sprintf("  %d: MACD=%.4f, Signal=%.4f, Hist=%.4f%s\n",
						i+1, macd.MACDLine, macd.SignalLine, macd.Histogram, crossStr))
				}
			}
		}
		if len(data.MidTermSeries15m.RSI7Values) > 0 {
			lastN := 5
			if len(data.MidTermSeries15m.RSI7Values) < lastN {
				lastN = len(data.MidTermSeries15m.RSI7Values)
			}
			recent := data.MidTermSeries15m.RSI7Values[len(data.MidTermSeries15m.RSI7Values)-lastN:]
			sb.WriteString(fmt.Sprintf("RSI7 (last %d): %s\n", lastN, formatFloatSlice(recent)))
		}
		if data.MidTermSeries15m.Bollinger != nil {
			bb := data.MidTermSeries15m.Bollinger
			sb.WriteString(fmt.Sprintf("15m Bollinger(20,2): upper=%.3f, middle=%.3f, lower=%.3f, width=%.4f, percent=%.3f\n",
				bb.Upper, bb.Middle, bb.Lower, bb.Width, bb.Percent))
		}
		sb.WriteString("\n")
	}

	// 1h（精简：仅保留当前值+简要摘要，去掉长序列）
	if data.MidTermSeries1h != nil {
		sb.WriteString("1h indicators (current values):\n")
		if len(data.MidTermSeries1h.MidPrices) > 0 {
			lastN := 3
			if len(data.MidTermSeries1h.MidPrices) < lastN {
				lastN = len(data.MidTermSeries1h.MidPrices)
			}
			recent := data.MidTermSeries1h.MidPrices[len(data.MidTermSeries1h.MidPrices)-lastN:]
			sb.WriteString(fmt.Sprintf("Mid prices (last %d): %s\n", lastN, formatFloatSlice(recent)))
		}
		if len(data.MidTermSeries1h.EMA20Values) > 0 {
			lastN := 3
			if len(data.MidTermSeries1h.EMA20Values) < lastN {
				lastN = len(data.MidTermSeries1h.EMA20Values)
			}
			recent := data.MidTermSeries1h.EMA20Values[len(data.MidTermSeries1h.EMA20Values)-lastN:]
			sb.WriteString(fmt.Sprintf("EMA20 (last %d): %s\n", lastN, formatFloatSlice(recent)))
		}
		if len(data.MidTermSeries1h.MACDValues) > 0 {
			lastN := 3
			if len(data.MidTermSeries1h.MACDValues) < lastN {
				lastN = len(data.MidTermSeries1h.MACDValues)
			}
			recent := data.MidTermSeries1h.MACDValues[len(data.MidTermSeries1h.MACDValues)-lastN:]
			sb.WriteString(fmt.Sprintf("MACD (last %d):\n", lastN))
			for i, macd := range recent {
				if macd != nil {
					crossStr := ""
					if macd.Cross != "none" {
						crossStr = fmt.Sprintf(" [%s]", macd.Cross)
					}
					sb.WriteString(fmt.Sprintf("  %d: MACD=%.4f, Signal=%.4f, Hist=%.4f%s\n",
						i+1, macd.MACDLine, macd.SignalLine, macd.Histogram, crossStr))
				}
			}
		}
		if len(data.MidTermSeries1h.RSI7Values) > 0 {
			lastN := 3
			if len(data.MidTermSeries1h.RSI7Values) < lastN {
				lastN = len(data.MidTermSeries1h.RSI7Values)
			}
			recent := data.MidTermSeries1h.RSI7Values[len(data.MidTermSeries1h.RSI7Values)-lastN:]
			sb.WriteString(fmt.Sprintf("RSI7 (last %d): %s\n", lastN, formatFloatSlice(recent)))
		}
		sb.WriteString("\n")
	}

	// 4h（精简：仅保留当前值+简要摘要，去掉长序列）
	if data.MidTermSeries4h != nil {
		sb.WriteString("4h indicators (current values):\n")
		if len(data.MidTermSeries4h.EMA20Values) > 0 && len(data.MidTermSeries4h.EMA50Values) > 0 {
			lastEMA20 := data.MidTermSeries4h.EMA20Values[len(data.MidTermSeries4h.EMA20Values)-1]
			lastEMA50 := data.MidTermSeries4h.EMA50Values[len(data.MidTermSeries4h.EMA50Values)-1]
			sb.WriteString(fmt.Sprintf("20-Period EMA: %.3f vs. 50-Period EMA: %.3f\n", lastEMA20, lastEMA50))
		}
		sb.WriteString(fmt.Sprintf("3-Period ATR: %.3f vs. 14-Period ATR: %.3f\n",
			data.MidTermSeries4h.ATR3, data.MidTermSeries4h.ATR14))
		sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n",
			data.MidTermSeries4h.CurrentVolume, data.MidTermSeries4h.AverageVolume))
		if len(data.MidTermSeries4h.MACDValues) > 0 {
			lastN := 3
			if len(data.MidTermSeries4h.MACDValues) < lastN {
				lastN = len(data.MidTermSeries4h.MACDValues)
			}
			recent := data.MidTermSeries4h.MACDValues[len(data.MidTermSeries4h.MACDValues)-lastN:]
			sb.WriteString(fmt.Sprintf("MACD (last %d):\n", lastN))
			for i, macd := range recent {
				if macd != nil {
					crossStr := ""
					if macd.Cross != "none" {
						crossStr = fmt.Sprintf(" [%s]", macd.Cross)
					}
					sb.WriteString(fmt.Sprintf("  %d: MACD=%.4f, Signal=%.4f, Hist=%.4f%s\n",
						i+1, macd.MACDLine, macd.SignalLine, macd.Histogram, crossStr))
				}
			}
		}
		if len(data.MidTermSeries4h.RSI7Values) > 0 {
			lastN := 3
			if len(data.MidTermSeries4h.RSI7Values) < lastN {
				lastN = len(data.MidTermSeries4h.RSI7Values)
			}
			recent := data.MidTermSeries4h.RSI7Values[len(data.MidTermSeries4h.RSI7Values)-lastN:]
			sb.WriteString(fmt.Sprintf("RSI7 (last %d): %s\n", lastN, formatFloatSlice(recent)))
		}
		if data.MidTermSeries4h.Bollinger != nil {
			bb := data.MidTermSeries4h.Bollinger
			sb.WriteString(fmt.Sprintf("4h Bollinger(20,2): upper=%.3f, middle=%.3f, lower=%.3f, width=%.4f, percent=%.3f\n",
				bb.Upper, bb.Middle, bb.Lower, bb.Width, bb.Percent))
		}
		sb.WriteString("\n")
	}

	// 打印 4h 区间
	// 已注释：不再提交4h支撑/压力位给AI
	/*
		if len(data.FourHourZones) > 0 {
			sup, res := pickKeyZones(data.FourHourZones, data.CurrentPrice, 2)

			sb.WriteString("4h key SR zones (strongest & nearest):\n")
			if len(sup) > 0 {
				sb.WriteString("supports:\n")
				for i, z := range sup {
					center := (z.Lower + z.Upper) / 2
					distPct := math.Abs(center-data.CurrentPrice) / data.CurrentPrice * 100
					sb.WriteString(fmt.Sprintf(
						"%d) %.2f ~ %.2f (strength=%d hits=%d dist=%.2f%% basis=%s)\n",
						i+1, z.Lower, z.Upper, z.Strength, z.Hits, distPct, z.Basis))
				}
			}
			if len(res) > 0 {
				sb.WriteString("resistances:\n")
				for i, z := range res {
					center := (z.Lower + z.Upper) / 2
					distPct := math.Abs(center-data.CurrentPrice) / data.CurrentPrice * 100
					sb.WriteString(fmt.Sprintf(
						"%d) %.2f ~ %.2f (strength=%d hits=%d dist=%.2f%% basis=%s)\n",
						i+1, z.Lower, z.Upper, z.Strength, z.Hits, distPct, z.Basis))
				}
			}
			sb.WriteString("\n")
		}
	*/

	// 恢复15m关键结构位：只显示最关键的2个supports和2个resistances
	if len(data.FifteenMinZones) > 0 {
		sup, res := pickKeyZones(data.FifteenMinZones, data.CurrentPrice, 2)

		sb.WriteString("15m structure anchors:\n")
		if len(sup) > 0 {
			sb.WriteString("supports: ")
			for i, z := range sup {
				center := (z.Lower + z.Upper) / 2
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("%.4f", center))
			}
			sb.WriteString("\n")
		}
		if len(res) > 0 {
			sb.WriteString("resistances: ")
			for i, z := range res {
				center := (z.Lower + z.Upper) / 2
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("%.4f", center))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// 打印斐波那契（精简：仅保留关键位，4h/1h各1组）
	if data.Fib4h != nil {
		sb.WriteString("4h Fibonacci levels (key only):\n")
		sb.WriteString(fmt.Sprintf("swing_low=%.2f swing_high=%.2f direction=%s\n", data.Fib4h.SwingLow, data.Fib4h.SwingHigh, data.Fib4h.Direction))
		// 仅保留关键比例：0.382, 0.5, 0.618
		keyRatios := []float64{0.382, 0.5, 0.618}
		for _, ratio := range keyRatios {
			for _, lvl := range data.Fib4h.Levels {
				if math.Abs(lvl.Ratio-ratio) < 0.001 {
					sb.WriteString(fmt.Sprintf("ratio=%.3f price=%.2f\n", lvl.Ratio, lvl.Price))
					break
				}
			}
		}
		sb.WriteString("\n")
	}
	if data.Fib1h != nil {
		sb.WriteString("1h Fibonacci levels (key only):\n")
		sb.WriteString(fmt.Sprintf("swing_low=%.2f swing_high=%.2f direction=%s\n", data.Fib1h.SwingLow, data.Fib1h.SwingHigh, data.Fib1h.Direction))
		// 仅保留关键比例：0.382, 0.5, 0.618
		keyRatios := []float64{0.382, 0.5, 0.618}
		for _, ratio := range keyRatios {
			for _, lvl := range data.Fib1h.Levels {
				if math.Abs(lvl.Ratio-ratio) < 0.001 {
					sb.WriteString(fmt.Sprintf("ratio=%.3f price=%.2f\n", lvl.Ratio, lvl.Price))
					break
				}
			}
		}
		sb.WriteString("\n")
	}

	// Price Action summary
	if data.PriceAction4h != nil {
		pa := data.PriceAction4h
		sb.WriteString("4h Price Action:\n")
		sb.WriteString(fmt.Sprintf("signal=%s time=%d bull_slope=%.6f bear_slope=%.6f\n",
			pa.LastSignal, pa.LastSignalTime, pa.BullSlope, pa.BearSlope))
		// 精简：仅保留最近1-2个OB
		if len(pa.BearishOB) > 0 {
			maxShow := 2
			if len(pa.BearishOB) < maxShow {
				maxShow = len(pa.BearishOB)
			}
			for i := 0; i < maxShow; i++ {
				ob := pa.BearishOB[len(pa.BearishOB)-maxShow+i]
				sb.WriteString(fmt.Sprintf("bear_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		if len(pa.BullishOB) > 0 {
			maxShow := 2
			if len(pa.BullishOB) < maxShow {
				maxShow = len(pa.BullishOB)
			}
			for i := 0; i < maxShow; i++ {
				ob := pa.BullishOB[len(pa.BullishOB)-maxShow+i]
				sb.WriteString(fmt.Sprintf("bull_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		// 精简：仅保留最近swept_high/low
		if len(pa.SweptHighs) > 0 {
			latest := pa.SweptHighs[len(pa.SweptHighs)-1]
			sb.WriteString(fmt.Sprintf("swept_high: %.4f @%d\n", latest.Price, latest.Time))
		}
		if len(pa.SweptLows) > 0 {
			latest := pa.SweptLows[len(pa.SweptLows)-1]
			sb.WriteString(fmt.Sprintf("swept_low: %.4f @%d\n", latest.Price, latest.Time))
		}
		sb.WriteString("\n")
	}

	if data.PriceAction1h != nil {
		pa := data.PriceAction1h
		sb.WriteString("1h Price Action:\n")
		sb.WriteString(fmt.Sprintf("signal=%s time=%d bull_slope=%.6f bear_slope=%.6f\n",
			pa.LastSignal, pa.LastSignalTime, pa.BullSlope, pa.BearSlope))
		// 精简：仅保留最近1-2个OB
		if len(pa.BearishOB) > 0 {
			maxShow := 2
			if len(pa.BearishOB) < maxShow {
				maxShow = len(pa.BearishOB)
			}
			for i := 0; i < maxShow; i++ {
				ob := pa.BearishOB[len(pa.BearishOB)-maxShow+i]
				sb.WriteString(fmt.Sprintf("bear_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		if len(pa.BullishOB) > 0 {
			maxShow := 2
			if len(pa.BullishOB) < maxShow {
				maxShow = len(pa.BullishOB)
			}
			for i := 0; i < maxShow; i++ {
				ob := pa.BullishOB[len(pa.BullishOB)-maxShow+i]
				sb.WriteString(fmt.Sprintf("bull_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		// 精简：仅保留最近swept_high/low
		if len(pa.SweptHighs) > 0 {
			latest := pa.SweptHighs[len(pa.SweptHighs)-1]
			sb.WriteString(fmt.Sprintf("swept_high: %.4f @%d\n", latest.Price, latest.Time))
		}
		if len(pa.SweptLows) > 0 {
			latest := pa.SweptLows[len(pa.SweptLows)-1]
			sb.WriteString(fmt.Sprintf("swept_low: %.4f @%d\n", latest.Price, latest.Time))
		}
		sb.WriteString("\n")
	}

	if data.PriceAction15m != nil {
		pa := data.PriceAction15m
		sb.WriteString("15m Price Action:\n")
		sb.WriteString(fmt.Sprintf("signal=%s time=%d bull_slope=%.6f bear_slope=%.6f\n",
			pa.LastSignal, pa.LastSignalTime, pa.BullSlope, pa.BearSlope))
		// 精简：仅保留最近1-2个OB
		if len(pa.BearishOB) > 0 {
			maxShow := 2
			if len(pa.BearishOB) < maxShow {
				maxShow = len(pa.BearishOB)
			}
			for i := 0; i < maxShow; i++ {
				ob := pa.BearishOB[len(pa.BearishOB)-maxShow+i]
				sb.WriteString(fmt.Sprintf("bear_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		if len(pa.BullishOB) > 0 {
			maxShow := 2
			if len(pa.BullishOB) < maxShow {
				maxShow = len(pa.BullishOB)
			}
			for i := 0; i < maxShow; i++ {
				ob := pa.BullishOB[len(pa.BullishOB)-maxShow+i]
				sb.WriteString(fmt.Sprintf("bull_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		// 精简：仅保留最近swept_high/low
		if len(pa.SweptHighs) > 0 {
			latest := pa.SweptHighs[len(pa.SweptHighs)-1]
			sb.WriteString(fmt.Sprintf("swept_high: %.4f @%d\n", latest.Price, latest.Time))
		}
		if len(pa.SweptLows) > 0 {
			latest := pa.SweptLows[len(pa.SweptLows)-1]
			sb.WriteString(fmt.Sprintf("swept_low: %.4f @%d\n", latest.Price, latest.Time))
		}
		sb.WriteString("\n")
	}
	// K线几何特征（精简：仅保留15m最近3-5根，去掉1h/4h）
	if len(data.CandleShapes15m) > 0 {
		sb.WriteString("15m recent candle shapes (last 3-5, oldest → latest):\n")
		lastN := 5
		if len(data.CandleShapes15m) < lastN {
			lastN = len(data.CandleShapes15m)
		}
		recent := data.CandleShapes15m[len(data.CandleShapes15m)-lastN:]
		for i, c := range recent {
			sb.WriteString(fmt.Sprintf(
				"%d) dir=%s body=%.2f upper=%.2f lower=%.2f range_vs_atr=%.2f close_pos=%.2f\n",
				i+1, c.Direction, c.BodyPct, c.UpperWickPct, c.LowerWickPct, c.RangeVsATR, c.ClosePosition))
		}
		sb.WriteString("\n")
	}
	// 精简：去掉1h/4h K线形态
	// if len(data.CandleShapes1h) > 0 { ... }
	// if len(data.CandleShapes4h) > 0 { ... }

	// 新增：派生指标输出（精简：仅保留最靠近/最强的2-3个）
	if len(data.KeyLevels) > 0 {
		sb.WriteString("Key Support/Resistance Levels (top 2-3 nearest/strongest):\n")
		// 按距离和强度排序，取前2-3个
		sortedLevels := make([]KeyLevel, len(data.KeyLevels))
		copy(sortedLevels, data.KeyLevels)
		sort.Slice(sortedLevels, func(i, j int) bool {
			// 优先距离近且强度高的
			distI := math.Abs(sortedLevels[i].DistancePercent)
			distJ := math.Abs(sortedLevels[j].DistancePercent)
			if distI < distJ {
				return true
			} else if distI == distJ {
				return sortedLevels[i].Strength > sortedLevels[j].Strength
			}
			return false
		})
		maxShow := 3
		if len(sortedLevels) < maxShow {
			maxShow = len(sortedLevels)
		}
		for i := 0; i < maxShow; i++ {
			level := sortedLevels[i]
			sb.WriteString(fmt.Sprintf("%d) %s: price=%.2f dist=%+.2f%% strength=%d basis=%s\n",
				i+1, level.Type, level.Price, level.DistancePercent, level.Strength, level.Basis))
		}
		sb.WriteString("\n")
	}

	if data.DistanceMetrics != nil {
		dm := data.DistanceMetrics
		sb.WriteString("Distance Metrics (key only):\n")
		// 精简：仅保留关键距离
		sb.WriteString(fmt.Sprintf("To 1h EMA20: %+.2f%% | To 4h EMA20: %+.2f%%\n", dm.ToEMA20_1h, dm.ToEMA20_4h))
		sb.WriteString(fmt.Sprintf("To 15m Boll Upper: %+.2f%% | Lower: %+.2f%%\n", dm.ToBollUpper15m, dm.ToBollLower15m))
		sb.WriteString(fmt.Sprintf("To 4h Boll Upper: %+.2f%% | Lower: %+.2f%%\n", dm.ToBollUpper4h, dm.ToBollLower4h))
		sb.WriteString(fmt.Sprintf("To Nearest Support: %+.2f%% | Resistance: %+.2f%%\n\n", dm.ToNearestSupport, dm.ToNearestResistance))
	}

	if data.TrendPhase != nil {
		tp := data.TrendPhase
		sb.WriteString("Trend Phase Analysis:\n")
		sb.WriteString(fmt.Sprintf("4h Trend Strength: %.1f (confidence=%.1f)\n\n", tp.TrendStrength4h, tp.Confidence))
	}

	if data.RiskMetrics != nil {
		rm := data.RiskMetrics
		sb.WriteString("Risk Metrics:\n")
		sb.WriteString(fmt.Sprintf("ATR14: %.2f%% of price | ATR3: %.2f%% of price\n", rm.ATR14PercentOfPrice, rm.ATR3PercentOfPrice))
		sb.WriteString(fmt.Sprintf("Volatility Level: %s\n\n", rm.VolatilityLevel))
	}

	if data.DerivativesAlert != nil {
		da := data.DerivativesAlert
		sb.WriteString("Derivatives Alert:\n")
		sb.WriteString(fmt.Sprintf("OI Change: 15m=%+.2f%% 1h=%+.2f%% 4h=%+.2f%%\n", da.OIChangePct15m, da.OIChangePct1h, da.OIChangePct4h))
		sb.WriteString(fmt.Sprintf("Taker Imbalance: %s\n", da.TakerImbalanceFlag))
		sb.WriteString(fmt.Sprintf("Funding Rate Level: %s (spike=%v)\n\n", da.FundingRateLevel, da.FundingSpikeFlag))
	}

	return sb.String()
}

func appendDerivativesOpenInterest(sb *strings.Builder, series map[string][]OpenInterestHistEntry) {
	if len(series) == 0 {
		return
	}
	intervals := []string{"15m", "1h", "4h"}
	wrote := false
	for _, interval := range intervals {
		entries, ok := series[interval]
		if !ok || len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		first := entries[0]
		delta := latest.SumOpenInterest - first.SumOpenInterest
		percent := 0.0
		if first.SumOpenInterest != 0 {
			percent = delta / first.SumOpenInterest * 100
		}
		sb.WriteString(fmt.Sprintf("OI %s: latest=%.2f Δ=%.2f (%+.2f%%) value=%.2f\n",
			interval, latest.SumOpenInterest, delta, percent, latest.SumOpenInterestValue))
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

func appendDerivativesRatioSection(sb *strings.Builder, title string, series map[string][]LongShortRatioEntry) {
	if len(series) == 0 {
		return
	}
	intervals := []string{"15m", "1h", "4h"}
	wrote := false
	for _, interval := range intervals {
		entries, ok := series[interval]
		if !ok || len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		sb.WriteString(fmt.Sprintf("%s %s: ratio=%.3f long=%.3f short=%.3f\n",
			title, interval, latest.Ratio, latest.LongAccount, latest.ShortAccount))
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

func appendDerivativesTakerSection(sb *strings.Builder, series map[string][]TakerBuySellEntry) {
	if len(series) == 0 {
		return
	}
	intervals := []string{"15m", "1h", "4h"}
	wrote := false
	for _, interval := range intervals {
		entries, ok := series[interval]
		if !ok || len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		sb.WriteString(fmt.Sprintf("Taker buy/sell %s: buy=%.2f sell=%.2f ratio=%.3f\n",
			interval, latest.BuyVolume, latest.SellVolume, latest.BuySellRatio))
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

func appendDerivativesBasisSection(sb *strings.Builder, series map[string][]BasisEntry) {
	if len(series) == 0 {
		return
	}
	intervals := []string{"15m", "1h", "4h"}
	wrote := false
	for _, interval := range intervals {
		entries, ok := series[interval]
		if !ok || len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		sb.WriteString(fmt.Sprintf("Basis %s: basis=%.4f rate=%.3f%% futures=%.2f index=%.2f\n",
			interval, latest.Basis, latest.BasisRate*100, latest.FuturesPrice, latest.IndexPrice))
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

func appendFundingHistorySection(sb *strings.Builder, entries []FundingRateEntry) {
	if len(entries) == 0 {
		return
	}
	latest := entries[len(entries)-1]
	first := entries[0]
	sum := 0.0
	for _, item := range entries {
		sum += item.FundingRate
	}
	avg := sum / float64(len(entries))
	delta := latest.FundingRate - first.FundingRate
	sb.WriteString(fmt.Sprintf("Funding history: latest=%.3e Δ=%.3e avg=%.3e (samples=%d)\n\n",
		latest.FundingRate, delta, avg, len(entries)))
}

// formatFloatSlice 格式化float64切片为字符串
func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = fmt.Sprintf("%.3f", v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}

// Normalize 标准化symbol,确保是USDT交易对
func Normalize(symbol string) string {
	symbol = strings.ToUpper(symbol)
	if strings.HasSuffix(symbol, "USDT") {
		return symbol
	}
	return symbol + "USDT"
}

// parseFloat 解析float值
func parseFloat(v interface{}) (float64, error) {
	switch val := v.(type) {
	case string:
		return strconv.ParseFloat(val, 64)
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case json.Number:
		return val.Float64()
	default:
		return 0, fmt.Errorf("unsupported type: %T", v)
	}
}

// ========== 新增：派生指标计算函数 ==========

// detectKeyLevels 检测关键支撑阻力位
func detectKeyLevels(klines4h, klines15m []Kline, currentPrice float64, ctx4h *MidTermSeries4h, ctx15m *MidTermData15m) []KeyLevel {
	var levels []KeyLevel

	// 从4h检测强支撑/压力
	if len(klines4h) > 0 {
		zones4h := detect4hZonesInternal(klines4h, ctx4h)
		for _, z := range zones4h {
			center := (z.Lower + z.Upper) / 2
			distPct := ((center - currentPrice) / currentPrice) * 100
			levels = append(levels, KeyLevel{
				Price:           center,
				DistancePercent: distPct,
				Type:            z.Kind,
				Strength:        z.Strength,
				Basis:           "4h_" + z.Basis,
			})
		}
	}

	// 从15m检测局部支撑/压力
	if len(klines15m) > 0 {
		zones15m := detect15mZonesInternal(klines15m, ctx15m)
		for _, z := range zones15m {
			center := (z.Lower + z.Upper) / 2
			distPct := ((center - currentPrice) / currentPrice) * 100
			levels = append(levels, KeyLevel{
				Price:           center,
				DistancePercent: distPct,
				Type:            z.Kind,
				Strength:        z.Strength,
				Basis:           "15m_" + z.Basis,
			})
		}
	}

	// 按强度和距离排序，只保留最关键的
	sort.Slice(levels, func(i, j int) bool {
		if levels[i].Strength == levels[j].Strength {
			return math.Abs(levels[i].DistancePercent) < math.Abs(levels[j].DistancePercent)
		}
		return levels[i].Strength > levels[j].Strength
	})

	// 只保留前10个最关键的
	if len(levels) > 10 {
		levels = levels[:10]
	}

	return levels
}

// detect4hZonesInternal 内部使用的4h支撑/压力检测
func detect4hZonesInternal(klines []Kline, ctx *MidTermSeries4h) []SRZone {
	n := len(klines)
	if n == 0 {
		return nil
	}
	start := 0
	if n > 60 {
		start = n - 60
	}

	avgVol := ctx.AverageVolume
	if avgVol == 0 {
		sum := 0.0
		for i := start; i < n; i++ {
			sum += klines[i].Volume
		}
		avgVol = sum / float64(n-start)
	}

	zones := []SRZone{}

	for i := start; i < n; i++ {
		k := klines[i]

		// 支撑
		priceLow := k.Low
		if priceLow > 0 {
			tol := priceLow * 0.004
			strength := 1
			basis := "swing_low"

			if k.Volume >= avgVol*0.95 {
				strength++
				basis += "+vol"
			}
			if ctx != nil && ctx.Bollinger != nil && ctx.Bollinger.Upper != ctx.Bollinger.Lower {
				percent := (priceLow - ctx.Bollinger.Lower) / (ctx.Bollinger.Upper - ctx.Bollinger.Lower)
				if percent <= 0.15 {
					strength++
					basis += "+bb_low"
				}
			}

			idx := findZoneIndex(zones, priceLow, tol, "support")
			if idx == -1 {
				zones = append(zones, SRZone{
					Lower:    priceLow - priceLow*0.002,
					Upper:    priceLow + priceLow*0.002,
					Kind:     "support",
					Strength: strength,
					Hits:     1,
					Basis:    basis,
				})
			} else {
				if priceLow < zones[idx].Lower {
					zones[idx].Lower = priceLow
				}
				if priceLow > zones[idx].Upper {
					zones[idx].Upper = priceLow
				}
				zones[idx].Hits++
				if strength > zones[idx].Strength {
					zones[idx].Strength = strength
				}
			}
		}

		// 压力
		priceHigh := k.High
		if priceHigh > 0 {
			tol := priceHigh * 0.004
			strength := 1
			basis := "swing_high"

			if k.Volume >= avgVol*0.95 {
				strength++
				basis += "+vol"
			}
			if ctx != nil && ctx.Bollinger != nil && ctx.Bollinger.Upper != ctx.Bollinger.Lower {
				percent := (priceHigh - ctx.Bollinger.Lower) / (ctx.Bollinger.Upper - ctx.Bollinger.Lower)
				if percent >= 0.85 {
					strength++
					basis += "+bb_high"
				}
			}

			idx := findZoneIndex(zones, priceHigh, tol, "resistance")
			if idx == -1 {
				zones = append(zones, SRZone{
					Lower:    priceHigh - priceHigh*0.002,
					Upper:    priceHigh + priceHigh*0.002,
					Kind:     "resistance",
					Strength: strength,
					Hits:     1,
					Basis:    basis,
				})
			} else {
				if priceHigh < zones[idx].Lower {
					zones[idx].Lower = priceHigh
				}
				if priceHigh > zones[idx].Upper {
					zones[idx].Upper = priceHigh
				}
				zones[idx].Hits++
				if strength > zones[idx].Strength {
					zones[idx].Strength = strength
				}
			}
		}
	}

	return zones
}

// detect15mZonesInternal 内部使用的15m支撑/压力检测
func detect15mZonesInternal(klines []Kline, ctx *MidTermData15m) []SRZone {
	n := len(klines)
	if n == 0 {
		return nil
	}
	start := 0
	if n > 40 {
		start = n - 40
	}

	var bb *BollingerBand
	if ctx != nil {
		bb = ctx.Bollinger
	}

	zones := []SRZone{}

	for i := start; i < n; i++ {
		k := klines[i]

		// 小支撑区
		low := k.Low
		if low > 0 {
			tol := low * 0.0015
			strength := 1
			basis := "15m_low"

			if bb != nil && bb.Upper != bb.Lower {
				percent := (low - bb.Lower) / (bb.Upper - bb.Lower)
				if percent <= 0.25 {
					strength++
					basis += "+bb_low"
				}
			}

			idx := findZoneIndex(zones, low, tol, "support")
			if idx == -1 {
				zones = append(zones, SRZone{
					Lower:    low - low*0.001,
					Upper:    low + low*0.001,
					Kind:     "support",
					Strength: strength,
					Hits:     1,
					Basis:    basis,
				})
			} else {
				if low < zones[idx].Lower {
					zones[idx].Lower = low
				}
				if low > zones[idx].Upper {
					zones[idx].Upper = low
				}
				zones[idx].Hits++
				if strength > zones[idx].Strength {
					zones[idx].Strength = strength
				}
			}
		}

		// 小压力区
		high := k.High
		if high > 0 {
			tol := high * 0.0015
			strength := 1
			basis := "15m_high"

			if bb != nil && bb.Upper != bb.Lower {
				percent := (high - bb.Lower) / (bb.Upper - bb.Lower)
				if percent >= 0.75 {
					strength++
					basis += "+bb_high"
				}
			}

			idx := findZoneIndex(zones, high, tol, "resistance")
			if idx == -1 {
				zones = append(zones, SRZone{
					Lower:    high - high*0.001,
					Upper:    high + high*0.001,
					Kind:     "resistance",
					Strength: strength,
					Hits:     1,
					Basis:    basis,
				})
			} else {
				if high < zones[idx].Lower {
					zones[idx].Lower = high
				}
				if high > zones[idx].Upper {
					zones[idx].Upper = high
				}
				zones[idx].Hits++
				if strength > zones[idx].Strength {
					zones[idx].Strength = strength
				}
			}
		}
	}

	return zones
}

// calculateDistanceMetrics 计算距离度量指标
func calculateDistanceMetrics(currentPrice float64, ctx1h *MidTermData1h, ctx4h *MidTermSeries4h, ctx15m *MidTermData15m, keyLevels []KeyLevel) *DistanceMetrics {
	dm := &DistanceMetrics{}

	// 1h EMA距离
	if ctx1h != nil && len(ctx1h.EMA20Values) > 0 {
		ema20 := ctx1h.EMA20Values[len(ctx1h.EMA20Values)-1]
		if ema20 > 0 {
			dm.ToEMA20_1h = ((currentPrice - ema20) / ema20) * 100
		}
	}
	if ctx1h != nil && len(ctx1h.EMA50Values) > 0 {
		ema50 := ctx1h.EMA50Values[len(ctx1h.EMA50Values)-1]
		if ema50 > 0 {
			dm.ToEMA50_1h = ((currentPrice - ema50) / ema50) * 100
		}
	}

	// 4h EMA距离
	if ctx4h != nil {
		if len(ctx4h.EMA20Values) > 0 {
			ema20 := ctx4h.EMA20Values[len(ctx4h.EMA20Values)-1]
			dm.ToEMA20_4h = ((currentPrice - ema20) / ema20) * 100
		}
		if len(ctx4h.EMA50Values) > 0 {
			ema50 := ctx4h.EMA50Values[len(ctx4h.EMA50Values)-1]
			dm.ToEMA50_4h = ((currentPrice - ema50) / ema50) * 100
		}
	}

	// 布林带距离
	if ctx15m != nil && ctx15m.Bollinger != nil {
		bb := ctx15m.Bollinger
		if bb.Upper > 0 {
			dm.ToBollUpper15m = ((currentPrice - bb.Upper) / bb.Upper) * 100
		}
		if bb.Lower > 0 {
			dm.ToBollLower15m = ((currentPrice - bb.Lower) / bb.Lower) * 100
		}
	}
	if ctx4h != nil && ctx4h.Bollinger != nil {
		bb := ctx4h.Bollinger
		if bb.Upper > 0 {
			dm.ToBollUpper4h = ((currentPrice - bb.Upper) / bb.Upper) * 100
		}
		if bb.Lower > 0 {
			dm.ToBollLower4h = ((currentPrice - bb.Lower) / bb.Lower) * 100
		}
	}

	// 最近支撑/阻力距离
	minSupportDist := 999.0
	minResistDist := 999.0
	for _, level := range keyLevels {
		absDist := math.Abs(level.DistancePercent)
		if level.Type == "support" && level.DistancePercent < 0 && absDist < minSupportDist {
			minSupportDist = absDist
			dm.ToNearestSupport = level.DistancePercent
		}
		if level.Type == "resistance" && level.DistancePercent > 0 && absDist < minResistDist {
			minResistDist = absDist
			dm.ToNearestResistance = level.DistancePercent
		}
	}

	return dm
}

// calculateTrendPhase 计算趋势阶段信息（优化版：增加ADX、EMA穿越次数、缠论判断）
func calculateTrendPhase(pa4h *PriceActionSummary, ctx4h *MidTermSeries4h, ctx15m *MidTermData15m, ctx1h *MidTermData1h, currentPrice float64, klines4h []Kline, klines1h []Kline) *TrendPhaseInfo {
	info := &TrendPhaseInfo{
		Confidence: 50.0,
	}

	if pa4h == nil || ctx4h == nil {
		return info
	}

	// ========== 第一步：震荡市判断（优先级最高） ==========
	// 使用多个维度综合判断震荡市，避免仅依赖BOS/CHoCH信号

	rangingScore := 0 // 震荡市得分，≥2则判定为震荡市

	// 1. ADX判断（趋势强度指标）
	// 4h ADX < 20 或 1h ADX < 20 → 震荡市
	if ctx4h.ADX > 0 && ctx4h.ADX < 20 {
		rangingScore++
	}
	if ctx1h != nil && ctx1h.ADX > 0 && ctx1h.ADX < 20 {
		rangingScore++
	}

	// 2. EMA穿越次数判断（参考CoreTradingRules的震荡过滤规则）
	// 4h级别：最近12根K线（48h）内，穿破EMA20次数 ≥ 3次 → 震荡市
	if len(klines4h) >= 12 && len(ctx4h.EMA20Values) > 0 {
		crossCount4h := 0
		startIdx := len(klines4h) - 12
		if startIdx < 0 {
			startIdx = 0
		}
		for i := startIdx + 1; i < len(klines4h); i++ {
			if i >= len(ctx4h.EMA20Values) {
				break
			}
			prevClose := klines4h[i-1].Close
			currClose := klines4h[i].Close
			ema20 := ctx4h.EMA20Values[i]
			// 判断是否穿越EMA20
			if (prevClose <= ema20 && currClose > ema20) ||
				(prevClose >= ema20 && currClose < ema20) {
				crossCount4h++
			}
		}
		if crossCount4h >= 3 {
			rangingScore++
		}
	}

	// 1h级别：最近24根K线（24h）内，穿破EMA20次数 ≥ 5次 → 震荡市
	if len(klines1h) >= 24 && ctx1h != nil && len(ctx1h.EMA20Values) > 0 {
		crossCount1h := 0
		startIdx := len(klines1h) - 24
		if startIdx < 0 {
			startIdx = 0
		}
		ema20Idx := len(ctx1h.EMA20Values) - 1
		if ema20Idx >= 0 {
			ema20_1h := ctx1h.EMA20Values[ema20Idx]
			if ema20_1h > 0 {
				for i := startIdx + 1; i < len(klines1h); i++ {
					prevClose := klines1h[i-1].Close
					currClose := klines1h[i].Close
					// 判断是否穿越EMA20
					if (prevClose <= ema20_1h && currClose > ema20_1h) ||
						(prevClose >= ema20_1h && currClose < ema20_1h) {
						crossCount1h++
					}
				}
				if crossCount1h >= 5 {
					rangingScore++
				}
			}
		}
	}

	// 4. 价格与EMA20粘合判断（1h级别）
	// ADX < 20 且价格在EMA20附近粘合 → 震荡市
	if ctx1h != nil && ctx1h.ADX > 0 && ctx1h.ADX < 20 && len(ctx1h.EMA20Values) > 0 {
		ema20_1h := ctx1h.EMA20Values[len(ctx1h.EMA20Values)-1]
		if ema20_1h > 0 {
			priceToEMA20Pct := math.Abs((currentPrice - ema20_1h) / ema20_1h * 100)
			if priceToEMA20Pct < 0.5 { // 价格距离EMA20 < 0.5% → 粘合
				rangingScore++
			}
		}
	}

	// 如果震荡市得分 ≥ 2，直接判定为震荡市
	if rangingScore >= 2 {
		info.TrendStrength4h = 30.0
		info.Confidence = 70.0 // 提高置信度，因为使用了多维度判断
		return info
	}

	// ========== 第二步：趋势市判断（如果未判定为震荡市） ==========
	// 判断4h趋势阶段
	signal := pa4h.LastSignal

	// 布林带位置
	var bbPercent float64
	if ctx4h.Bollinger != nil && ctx4h.Bollinger.Upper != ctx4h.Bollinger.Lower {
		bbPercent = (currentPrice - ctx4h.Bollinger.Lower) / (ctx4h.Bollinger.Upper - ctx4h.Bollinger.Lower)
	}

	// 判断趋势方向和阶段
	if strings.Contains(signal, "up") || strings.Contains(signal, "CHoCH_up") || strings.Contains(signal, "BOS_up") {
		// 上升趋势
		if bbPercent < 0.4 {
			info.TrendStrength4h = 70.0
			info.Confidence = 75.0
		} else if bbPercent < 0.7 {
			info.TrendStrength4h = 60.0
			info.Confidence = 70.0
		} else {
			info.TrendStrength4h = 40.0
			info.Confidence = 65.0
		}
	} else if strings.Contains(signal, "down") || strings.Contains(signal, "CHoCH_down") || strings.Contains(signal, "BOS_down") {
		// 下降趋势
		if bbPercent > 0.6 {
			info.TrendStrength4h = 70.0
			info.Confidence = 75.0
		} else if bbPercent > 0.3 {
			info.TrendStrength4h = 60.0
			info.Confidence = 70.0
		} else {
			info.TrendStrength4h = 40.0
			info.Confidence = 65.0
		}
	} else {
		// 无BOS/CHoCH信号，但震荡市得分 < 2，仍判定为震荡市（但置信度降低）
		info.TrendStrength4h = 30.0
		info.Confidence = 50.0
	}

	return info
}

// calculateRiskMetrics 计算风险度量指标
func calculateRiskMetrics(currentPrice float64, ctx4h *MidTermSeries4h, volumePercentile15m float64, spreadBps float64, symbol string) *RiskMetrics {
	rm := &RiskMetrics{
		VolatilityLevel: "medium",
	}

	if ctx4h == nil {
		return rm
	}

	if currentPrice > 0 {
		if ctx4h.ATR14 > 0 {
			rm.ATR14PercentOfPrice = (ctx4h.ATR14 / currentPrice) * 100
		}
		if ctx4h.ATR3 > 0 {
			rm.ATR3PercentOfPrice = (ctx4h.ATR3 / currentPrice) * 100
		}
	}

	// ATR阈值已在symbol分层逻辑中处理

	// 基于volume和spread的激进熔断判断 + symbol分层阈值
	isLargeCap := strings.Contains(symbol, "BTC") || strings.Contains(symbol, "ETH")
	var extremeVolThreshold, highVolThreshold float64
	var extremeATRThreshold, highATRThreshold, mediumATRThreshold float64

	if isLargeCap {
		// 大盘币更敏感
		extremeVolThreshold = 99.0
		highVolThreshold = 97.0
		extremeATRThreshold = 3.6
		highATRThreshold = 2.4
		mediumATRThreshold = 1.4
	} else {
		// 小币种稍宽松
		extremeVolThreshold = 99.0
		highVolThreshold = 97.0
		extremeATRThreshold = 4.8
		highATRThreshold = 3.2
		mediumATRThreshold = 2.0
	}

	// 激进熔断判断
	if volumePercentile15m >= extremeVolThreshold && spreadBps >= 30.0 {
		rm.VolatilityLevel = "extreme"
	} else if volumePercentile15m >= highVolThreshold && spreadBps >= 25.0 {
		rm.VolatilityLevel = "high"
	} else {
		// 回退到更激进的ATR判断
		if rm.ATR14PercentOfPrice >= extremeATRThreshold {
			rm.VolatilityLevel = "extreme"
		} else if rm.ATR14PercentOfPrice >= highATRThreshold {
			rm.VolatilityLevel = "high"
		} else if rm.ATR14PercentOfPrice >= mediumATRThreshold {
			rm.VolatilityLevel = "medium"
		} else {
			rm.VolatilityLevel = "low"
		}
	}

	return rm
}

// calculateDerivativesAlert 计算衍生品异常标记
func calculateDerivativesAlert(derivatives *DerivativesData, fundingRate float64) *DerivativesAlert {
	alert := &DerivativesAlert{
		TakerImbalanceFlag: "neutral",
		FundingSpikeFlag:   false,
		FundingRateLevel:   "neutral",
	}

	if derivatives == nil {
		return alert
	}

	// OI变化百分比
	if oi, ok := derivatives.OpenInterestHist["15m"]; ok && len(oi) >= 2 {
		first := oi[0].SumOpenInterest
		latest := oi[len(oi)-1].SumOpenInterest
		if first > 0 {
			alert.OIChangePct15m = ((latest - first) / first) * 100
		}
	}
	if oi, ok := derivatives.OpenInterestHist["1h"]; ok && len(oi) >= 2 {
		first := oi[0].SumOpenInterest
		latest := oi[len(oi)-1].SumOpenInterest
		if first > 0 {
			alert.OIChangePct1h = ((latest - first) / first) * 100
		}
	}
	if oi, ok := derivatives.OpenInterestHist["4h"]; ok && len(oi) >= 2 {
		first := oi[0].SumOpenInterest
		latest := oi[len(oi)-1].SumOpenInterest
		if first > 0 {
			alert.OIChangePct4h = ((latest - first) / first) * 100
		}
	}

	// Funding rate 变化百分比（使用 FundingRateHistory 的首尾作为近似）
	if len(derivatives.FundingRateHistory) >= 2 {
		first := derivatives.FundingRateHistory[0].FundingRate
		latest := derivatives.FundingRateHistory[len(derivatives.FundingRateHistory)-1].FundingRate
		if first != 0 {
			pct := ((latest - first) / math.Abs(first)) * 100
			alert.FundingChangePct15m = pct
			alert.FundingChangePct1h = pct
			alert.FundingChangePct4h = pct
		} else {
			// fallback: absolute change scaled
			pct := (latest - first) * 100
			alert.FundingChangePct15m = pct
			alert.FundingChangePct1h = pct
			alert.FundingChangePct4h = pct
		}
	}

	// Taker买卖失衡
	if taker, ok := derivatives.TakerBuySellVolume["15m"]; ok && len(taker) > 0 {
		latest := taker[len(taker)-1]
		ratio := latest.BuySellRatio
		if ratio > 1.5 {
			alert.TakerImbalanceFlag = "strong_buy"
		} else if ratio > 1.2 {
			alert.TakerImbalanceFlag = "buy"
		} else if ratio < 0.67 {
			alert.TakerImbalanceFlag = "strong_sell"
		} else if ratio < 0.83 {
			alert.TakerImbalanceFlag = "sell"
		}
	}

	// 资金费率异常
	if math.Abs(fundingRate) > 0.001 {
		alert.FundingSpikeFlag = true
	}

	if fundingRate > 0.0015 {
		alert.FundingRateLevel = "extreme_long"
	} else if fundingRate > 0.0005 {
		alert.FundingRateLevel = "high_long"
	} else if fundingRate < -0.0015 {
		alert.FundingRateLevel = "extreme_short"
	} else if fundingRate < -0.0005 {
		alert.FundingRateLevel = "high_short"
	}

	return alert
}

// calculateOBV 计算OBV指标
func calculateOBV(klines []Kline) []float64 {
	if len(klines) == 0 {
		return nil
	}

	obv := make([]float64, len(klines))
	obv[0] = klines[0].Volume

	for i := 1; i < len(klines); i++ {
		if klines[i].Close > klines[i-1].Close {
			obv[i] = obv[i-1] + klines[i].Volume
		} else if klines[i].Close < klines[i-1].Close {
			obv[i] = obv[i-1] - klines[i].Volume
		} else {
			obv[i] = obv[i-1]
		}
	}

	return obv
}

// calculateVWAP 计算VWAP
func calculateVWAP(klines []Kline) float64 {
	if len(klines) == 0 {
		return 0
	}

	var sumPV, sumV float64
	for _, k := range klines {
		typical := (k.High + k.Low + k.Close) / 3
		sumPV += typical * k.Volume
		sumV += k.Volume
	}

	if sumV == 0 {
		return 0
	}
	return sumPV / sumV
}

// calculateADX 计算ADX及DI+/DI-
func calculateADX(klines []Kline, period int) (adx, diPlus, diMinus float64) {
	if len(klines) < period+1 {
		return 0, 0, 0
	}

	// 计算+DM和-DM
	var plusDMs, minusDMs, trs []float64
	for i := 1; i < len(klines); i++ {
		highDiff := klines[i].High - klines[i-1].High
		lowDiff := klines[i-1].Low - klines[i].Low

		plusDM := 0.0
		if highDiff > lowDiff && highDiff > 0 {
			plusDM = highDiff
		}
		minusDM := 0.0
		if lowDiff > highDiff && lowDiff > 0 {
			minusDM = lowDiff
		}

		tr := math.Max(klines[i].High-klines[i].Low,
			math.Max(math.Abs(klines[i].High-klines[i-1].Close),
				math.Abs(klines[i].Low-klines[i-1].Close)))

		plusDMs = append(plusDMs, plusDM)
		minusDMs = append(minusDMs, minusDM)
		trs = append(trs, tr)
	}

	if len(plusDMs) < period {
		return 0, 0, 0
	}

	// 计算平滑的+DM, -DM, TR
	sumPlusDM := 0.0
	sumMinusDM := 0.0
	sumTR := 0.0
	for i := 0; i < period; i++ {
		sumPlusDM += plusDMs[i]
		sumMinusDM += minusDMs[i]
		sumTR += trs[i]
	}

	// 计算DI+和DI-
	if sumTR > 0 {
		diPlus = (sumPlusDM / sumTR) * 100
		diMinus = (sumMinusDM / sumTR) * 100
	}

	// 计算DX和ADX
	if diPlus+diMinus > 0 {
		dx := math.Abs(diPlus-diMinus) / (diPlus + diMinus) * 100
		adx = dx // 简化版，实际应该用EMA平滑
	}

	return adx, diPlus, diMinus
}

// calculateMFI 计算资金流量指标
func calculateMFI(klines []Kline, period int) float64 {
	if len(klines) < period+1 {
		return 50.0
	}

	var posFlow, negFlow float64
	for i := len(klines) - period; i < len(klines); i++ {
		if i == 0 {
			continue
		}
		typical := (klines[i].High + klines[i].Low + klines[i].Close) / 3
		prevTypical := (klines[i-1].High + klines[i-1].Low + klines[i-1].Close) / 3
		moneyFlow := typical * klines[i].Volume

		if typical > prevTypical {
			posFlow += moneyFlow
		} else if typical < prevTypical {
			negFlow += moneyFlow
		}
	}

	if negFlow == 0 {
		return 100.0
	}

	mfiRatio := posFlow / negFlow
	mfi := 100 - (100 / (1 + mfiRatio))
	return mfi
}

// calculateCMF 计算Chaikin Money Flow
func calculateCMF(klines []Kline, period int) float64 {
	if len(klines) < period {
		return 0
	}

	start := len(klines) - period
	var sumMFV, sumVol float64

	for i := start; i < len(klines); i++ {
		k := klines[i]
		if k.High == k.Low {
			continue
		}
		mfm := ((k.Close - k.Low) - (k.High - k.Close)) / (k.High - k.Low)
		mfv := mfm * k.Volume
		sumMFV += mfv
		sumVol += k.Volume
	}

	if sumVol == 0 {
		return 0
	}
	return sumMFV / sumVol
}

// EvaluateExecutionGate 基于微观结构和计划仓位评估执行门禁
func EvaluateExecutionGate(m *MicrostructureSummary, plannedNotional float64) *ExecutionGate {
	gate := &ExecutionGate{
		TsMs: time.Now().UnixMilli(),
	}

	// 如果微观结构数据缺失，使用默认保守策略
	if m == nil {
		gate.Mode = executionGateConfig.DefaultModeOnMissing
		gate.Reason = "microstructure missing"
		return gate
	}

	// 1. 检查no_trade条件（最严格，OR关系）
	if m.SpreadBps >= executionGateConfig.MaxSpreadBpsNoTrade ||
		(plannedNotional > 0 && plannedNotional > executionGateConfig.NotionalMultiplierNoTrade*m.MinNotional) {
		gate.Mode = "no_trade"
		if m.SpreadBps >= executionGateConfig.MaxSpreadBpsNoTrade {
			gate.Reason = fmt.Sprintf("spread_too_wide_%.2fbps_no_trade", m.SpreadBps)
		} else {
			gate.Reason = fmt.Sprintf("planned_notional_too_large_%.0f_vs_min_%.0f", plannedNotional, m.MinNotional)
		}
		return gate
	}

	// 2. 检查limit_only条件（OR关系）
	// 优先使用DepthNotional10（更稳定），备选MinNotional
	effectiveNotional := m.DepthNotional10
	notionalSource := "depth10"
	if m.DepthNotional10 == 0 {
		effectiveNotional = m.MinNotional
		notionalSource = "best"
	}

	if m.SpreadBps >= executionGateConfig.MaxSpreadBpsLimitOnly ||
		m.DepthRatio > executionGateConfig.MaxDepthRatioAbs ||
		m.DepthRatio < executionGateConfig.MinDepthRatioAbs ||
		(plannedNotional > 0 && plannedNotional > executionGateConfig.NotionalMultiplierLimitOnly*effectiveNotional) ||
		effectiveNotional < executionGateConfig.MinDepthNotional10LimitOnly {
		gate.Mode = "limit_only"
		if m.SpreadBps >= executionGateConfig.MaxSpreadBpsLimitOnly {
			gate.Reason = fmt.Sprintf("spread_too_wide_%.2fbps", m.SpreadBps)
		} else if m.DepthRatio > executionGateConfig.MaxDepthRatioAbs {
			gate.Reason = fmt.Sprintf("depth_ratio_too_high_%.2f", m.DepthRatio)
		} else if m.DepthRatio < executionGateConfig.MinDepthRatioAbs {
			gate.Reason = fmt.Sprintf("depth_ratio_too_low_%.2f", m.DepthRatio)
		} else if plannedNotional > 0 && plannedNotional > executionGateConfig.NotionalMultiplierLimitOnly*effectiveNotional {
			gate.Reason = fmt.Sprintf("planned_notional_large_%.0f_vs_%s_%.0f", plannedNotional, notionalSource, effectiveNotional)
		} else {
			gate.Reason = fmt.Sprintf("insufficient_%s_notional_%.0f_usdt", notionalSource, effectiveNotional)
		}
		return gate
	}

	// 3. 检查limit_preferred条件（OR关系）
	if m.SpreadBps >= executionGateConfig.MaxSpreadBpsLimitPreferred ||
		effectiveNotional < executionGateConfig.MinDepthNotional10LimitPreferred {
		gate.Mode = "limit_preferred"
		if m.SpreadBps >= executionGateConfig.MaxSpreadBpsLimitPreferred {
			gate.Reason = fmt.Sprintf("spread_wide_%.2fbps", m.SpreadBps)
		} else {
			gate.Reason = fmt.Sprintf("%s_notional_low_%.0f_usdt", notionalSource, effectiveNotional)
		}
		return gate
	}

	// 4. 所有检查通过，允许市价单
	gate.Mode = "market_ok"
	gate.Reason = fmt.Sprintf("good_conditions_%.2fbps_notional_%.0f", m.SpreadBps, m.MinNotional)
	return gate
}

// ===== M2.1: ExchangeInfo 缓存和限价定价 =====

// SymbolFilters 表示单个交易对的过滤器信息
type SymbolFilters struct {
	TickSize    float64 `json:"tickSize"`
	StepSize    float64 `json:"stepSize"`
	MinNotional float64 `json:"minNotional,omitempty"`
}

// SymbolFiltersProvider 交易对过滤器提供者接口（用于测试注入）
type SymbolFiltersProvider interface {
	GetSymbolFilters(symbol string) (*SymbolFilters, error)
}

// DefaultSymbolFiltersProvider 默认实现
type DefaultSymbolFiltersProvider struct{}

func (p *DefaultSymbolFiltersProvider) GetSymbolFilters(symbol string) (*SymbolFilters, error) {
	return getSymbolFiltersFromCache(symbol)
}

// 全局提供者变量（可被测试注入）
var symbolFiltersProvider SymbolFiltersProvider = &DefaultSymbolFiltersProvider{}

// SetSymbolFiltersProvider 设置过滤器提供者（测试用）
func SetSymbolFiltersProvider(provider SymbolFiltersProvider) {
	symbolFiltersProvider = provider
}

// ResetSymbolFiltersProvider 重置为默认提供者
func ResetSymbolFiltersProvider() {
	symbolFiltersProvider = &DefaultSymbolFiltersProvider{}
}

// MarketDataProvider 市场数据提供者接口（用于测试注入）
type MarketDataProvider interface {
	Get(symbol string) (*Data, error)
}

// DefaultMarketDataProvider 默认实现
type DefaultMarketDataProvider struct{}

func (p *DefaultMarketDataProvider) Get(symbol string) (*Data, error) {
	return getMarketDataFromAPI(symbol)
}

// 全局市场数据提供者变量（可被测试注入）
var marketDataProvider MarketDataProvider = &DefaultMarketDataProvider{}

// SetMarketDataProvider 设置市场数据提供者（测试用）
func SetMarketDataProvider(provider MarketDataProvider) {
	marketDataProvider = provider
}

// ResetMarketDataProvider 重置为默认提供者
func ResetMarketDataProvider() {
	marketDataProvider = &DefaultMarketDataProvider{}
}

// ExchangeInfoCache 交易所信息缓存
type ExchangeInfoCache struct {
	sync.RWMutex
	data      map[string]SymbolFilters
	lastFetch time.Time
	ttl       time.Duration
}

// 全局缓存实例
var exchangeInfoCache = &ExchangeInfoCache{
	data: make(map[string]SymbolFilters),
	ttl:  time.Hour, // 1小时TTL
}

// BinanceExchangeInfoResponse Binance交易所信息响应结构
type BinanceExchangeInfoResponse struct {
	Symbols []struct {
		Symbol  string `json:"symbol"`
		Filters []struct {
			FilterType  string `json:"filterType"`
			TickSize    string `json:"tickSize,omitempty"`
			StepSize    string `json:"stepSize,omitempty"`
			MinNotional string `json:"minNotional,omitempty"`
		} `json:"filters"`
	} `json:"symbols"`
}

// fetchExchangeInfo 从Binance获取交易所信息
func fetchExchangeInfo() (*BinanceExchangeInfoResponse, error) {
	url := "https://fapi.binance.com/fapi/v1/exchangeInfo"
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("获取交易所信息失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var exchangeInfo BinanceExchangeInfoResponse
	if err := json.Unmarshal(body, &exchangeInfo); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %w", err)
	}

	return &exchangeInfo, nil
}

// GetSymbolFilters 获取指定交易对的过滤器信息（带缓存）
func GetSymbolFilters(symbol string) (*SymbolFilters, error) {
	return symbolFiltersProvider.GetSymbolFilters(symbol)
}

// getSymbolFiltersFromCache 从缓存获取过滤器信息（原始实现）
func getSymbolFiltersFromCache(symbol string) (*SymbolFilters, error) {
	exchangeInfoCache.RLock()
	// 检查缓存是否有效
	if time.Since(exchangeInfoCache.lastFetch) < exchangeInfoCache.ttl {
		if filters, exists := exchangeInfoCache.data[symbol]; exists {
			exchangeInfoCache.RUnlock()
			return &filters, nil
		}
	}
	exchangeInfoCache.RUnlock()

	// 缓存失效或无数据，需要重新获取
	exchangeInfoCache.Lock()
	defer exchangeInfoCache.Unlock()

	// 双重检查，避免并发重复获取
	if time.Since(exchangeInfoCache.lastFetch) < exchangeInfoCache.ttl {
		if filters, exists := exchangeInfoCache.data[symbol]; exists {
			return &filters, nil
		}
	}

	// 从Binance获取数据
	exchangeInfo, err := fetchExchangeInfo()
	if err != nil {
		return nil, err
	}

	// 清空旧缓存
	exchangeInfoCache.data = make(map[string]SymbolFilters)

	// 解析并缓存所有交易对信息
	for _, symbolInfo := range exchangeInfo.Symbols {
		filters := SymbolFilters{}
		for _, filter := range symbolInfo.Filters {
			switch filter.FilterType {
			case "PRICE_FILTER":
				if tickSize, err := strconv.ParseFloat(filter.TickSize, 64); err == nil {
					filters.TickSize = tickSize
				}
			case "LOT_SIZE":
				if stepSize, err := strconv.ParseFloat(filter.StepSize, 64); err == nil {
					filters.StepSize = stepSize
				}
			case "MIN_NOTIONAL":
				if minNotional, err := strconv.ParseFloat(filter.MinNotional, 64); err == nil {
					filters.MinNotional = minNotional
				}
			}
		}
		exchangeInfoCache.data[symbolInfo.Symbol] = filters
	}

	exchangeInfoCache.lastFetch = time.Now()

	// 返回请求的交易对信息
	if filters, exists := exchangeInfoCache.data[symbol]; exists {
		return &filters, nil
	}

	return nil, fmt.Errorf("交易对 %s 的过滤器信息未找到", symbol)
}

// RoundToTick 将价格按tickSize四舍五入
func RoundToTick(price, tickSize float64) float64 {
	if tickSize <= 0 {
		return price
	}
	return math.Round(price/tickSize) * tickSize
}

// RoundToStep 将数量按stepSize四舍五入
func RoundToStep(qty, stepSize float64) float64 {
	if stepSize <= 0 {
		return qty
	}
	return math.Round(qty/stepSize) * stepSize
}

// DeriveOpenLimitPrice 基于盘口推导开仓限价（只用盘口，不做策略判断）
func DeriveOpenLimitPrice(side string, microstructure *MicrostructureSummary, tickSize float64) (price float64, reason string) {
	if microstructure == nil {
		return 0, "microstructure_unavailable"
	}

	spread := microstructure.BestAskPrice - microstructure.BestBidPrice

	switch side {
	case "BUY", "buy", "long":
		// 默认使用best_bid_price（maker定价）
		price = microstructure.BestBidPrice
		reason = "best_bid_maker"

		// 如果spread足够大（>=2*tickSize），尝试inside pricing
		if spread >= 2*tickSize {
			insidePrice := microstructure.BestBidPrice + tickSize
			// 确保不跨过best_ask
			if insidePrice < microstructure.BestAskPrice {
				price = insidePrice
				reason = "best_bid_plus_one_tick_inside"
			}
		}

	case "SELL", "sell", "short":
		// 默认使用best_ask_price（maker定价）
		price = microstructure.BestAskPrice
		reason = "best_ask_maker"

		// 如果spread足够大（>=2*tickSize），尝试inside pricing
		if spread >= 2*tickSize {
			insidePrice := microstructure.BestAskPrice - tickSize
			// 确保不跨过best_bid
			if insidePrice > microstructure.BestBidPrice {
				price = insidePrice
				reason = "best_ask_minus_one_tick_inside"
			}
		}

	default:
		return 0, "invalid_side"
	}

	// 确保价格按tickSize对齐
	price = RoundToTick(price, tickSize)

	return price, reason
}
