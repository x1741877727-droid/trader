package market

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sort"
)
// CandleShape 单根K线的几何特征，用于AI识别各种形态
type CandleShape struct {
	Direction     string  `json:"dir"`              // "bull" / "bear" / "doji"
	BodyPct       float64 `json:"body_pct"`        // 实体占整个K线高度的比例 0~1
	UpperWickPct  float64 `json:"upper_wick_pct"`  // 上影线占比 0~1
	LowerWickPct  float64 `json:"lower_wick_pct"`  // 下影线占比 0~1
	RangeVsATR    float64 `json:"range_vs_atr"`    // (high-low)/ATR(20)
	ClosePosition float64 `json:"close_pos"`       // 收盘在[Low,High]中的位置 0~1
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
	Timeframe      string          `json:"timeframe"`       // "4h"/"15m"
	LastSignal     string          `json:"last_signal"`     // "BOS_up"|"BOS_down"|"CHoCH_up"|"CHoCH_down"|"none"
	LastSignalTime int64           `json:"last_signal_time"`
	BullishOB      []OB            `json:"bullish_ob"`      // 最近1~2个未失效
	BearishOB      []OB            `json:"bearish_ob"`      // 最近1~2个未失效
	SweptHighs     []LiquidityLine `json:"swept_highs"`     // 最近1~2条
	SweptLows      []LiquidityLine `json:"swept_lows"`      // 最近1~2条
	BullSlope      float64         `json:"bull_slope"`      // 由最近两个pivot low计算
	BearSlope      float64         `json:"bear_slope"`      // 由最近两个pivot high计算
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

// Data 市场数据结构
type Data struct {
	Symbol            string
	CurrentPrice      float64
	PriceChange1h     float64 // 1小时价格变化百分比
	PriceChange4h     float64 // 4小时价格变化百分比
	CurrentEMA20      float64
	CurrentMACD       float64
	CurrentRSI7       float64
	OpenInterest      *OIData
	FundingRate       float64
	IntradaySeries    *IntradayData   // 5分钟数据 - 日内
	MidTermSeries15m  *MidTermData15m // 15分钟数据 - 短期趋势
	MidTermSeries1h   *MidTermData1h  // 1小时数据 - 中期趋势
	LongerTermContext *LongerTermData // 4小时数据 - 长期趋势

	// 新增：识别出来的关键区
	FourHourZones   []SRZone `json:"four_hour_zones"`
	FifteenMinZones []SRZone `json:"fifteen_min_zones"`

	// 新增：直接算好的斐波那契
	Fib4h *FibSet `json:"fib_4h"`
	Fib1h *FibSet `json:"fib_1h"`

	PriceAction4h  *PriceActionSummary `json:"price_action_4h"`
	PriceAction15m *PriceActionSummary `json:"price_action_15m"`
	CandleShapes15m []CandleShape `json:"candles_15m,omitempty"`
	CandleShapes1h  []CandleShape `json:"candles_1h,omitempty"`
	CandleShapes4h  []CandleShape `json:"candles_4h,omitempty"`
}

type BollingerBand struct {
	Upper   float64
	Middle  float64
	Lower   float64
	Width   float64 // (Upper-Lower)/Middle
	Percent float64 // (price - Lower)/(Upper - Lower)
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
	MACDValues    []float64
	RSI7Values    []float64
	RSI14Values   []float64
	Volumes       []float64 // 成交量序列（用于放量检测）
	BuySellRatios []float64 // 买卖压力比序列（>0.6多方强，<0.4空方强）
}

// MidTermData15m 15分钟时间框架数据 - 短期趋势过滤
type MidTermData15m struct {
	MidPrices   []float64
	EMA20Values []float64
	MACDValues  []float64
	RSI7Values  []float64
	RSI14Values []float64
	Bollinger   *BollingerBand
}

// MidTermData1h 1小时时间框架数据 - 中期趋势确认
type MidTermData1h struct {
	MidPrices   []float64
	EMA20Values []float64
	MACDValues  []float64
	RSI7Values  []float64
	RSI14Values []float64
}

// LongerTermData 长期数据(4小时时间框架)
type LongerTermData struct {
	EMA20         float64
	EMA50         float64
	ATR3          float64
	ATR14         float64
	CurrentVolume float64
	AverageVolume float64
	MACDValues    []float64
	RSI14Values   []float64
	Bollinger     *BollingerBand // 4h布林带
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
	// 标准化symbol
	symbol = Normalize(symbol)

	// 获取5分钟K线数据 (最近40个) - 日内
	klines5m, err := getKlines(symbol, "5m", 40)
	if err != nil {
		return nil, fmt.Errorf("获取5分钟K线失败: %v", err)
	}

	// 获取15分钟K线数据（多取一些做结构/流动性检测）
	klines15m, err := getKlines(symbol, "15m", 120)
	if err != nil {
		return nil, fmt.Errorf("获取15分钟K线失败: %v", err)
	}

	// 获取1小时K线数据（顺便也多取一些，给Fib/趋势用）
	klines1h, err := getKlines(symbol, "1h", 120)
	if err != nil {
		return nil, fmt.Errorf("获取1小时K线失败: %v", err)
	}

	// 获取4小时K线数据（多取一些做结构/流动性检测）
	klines4h, err := getKlines(symbol, "4h", 120)
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}

	// 基于5m最新数据的当前指标
	currentPrice := klines5m[len(klines5m)-1].Close
	currentEMA20 := calculateEMA(klines5m, 20)
	currentMACD := calculateMACD(klines5m)
	currentRSI7 := calculateRSI(klines5m, 7)

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

	// OI
	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		oiData = &OIData{Latest: 0, Average: 0}
	}

	// funding
	fundingRate, _ := getFundingRate(symbol)

	// 5m 日内序列
	intradayData := calculateIntradaySeries(klines5m)

	// 15m 序列
	midTermData15m := calculateMidTermSeries15m(klines15m)

	// 1h 序列
	midTermData1h := calculateMidTermSeries1h(klines1h)

	// 4h 长周期
	longerTermData := calculateLongerTermData(klines4h)

	// 新增：4h 强支撑/压力区识别
	fourHourZones := detect4hZones(klines4h, longerTermData)

	// 新增：15m 小支撑/小压力区识别
	fifteenMinZones := detect15mZones(klines15m, midTermData15m)

	// 新增：直接计算4h和1h的斐波那契
	fib4h := calcFibFromKlines(klines4h, "4h")
	fib1h := calcFibFromKlines(klines1h, "1h")

	// 价格行为：结构+OB+liquidity
	pa4h := calcPriceActionSummary(klines4h, "4h", 9, 20, 20)
	pa15 := calcPriceActionSummary(klines15m, "15m", 9, 20, 20)

	// 新增：提取最近K线的几何特征，让AI做形态识别（每个周期只给最近20根）
	candles15m := extractCandleShapes(klines15m, 20, 20)
	candles1h := extractCandleShapes(klines1h, 20, 20)
	candles4h := extractCandleShapes(klines4h, 20, 20)

	return &Data{
		Symbol:            symbol,
		CurrentPrice:      currentPrice,
		PriceChange1h:     priceChange1h,
		PriceChange4h:     priceChange4h,
		CurrentEMA20:      currentEMA20,
		CurrentMACD:       currentMACD,
		CurrentRSI7:       currentRSI7,
		OpenInterest:      oiData,
		FundingRate:       fundingRate,
		IntradaySeries:    intradayData,
		MidTermSeries15m:  midTermData15m,
		MidTermSeries1h:   midTermData1h,
		LongerTermContext: longerTermData,
		FourHourZones:     fourHourZones,
		FifteenMinZones:   fifteenMinZones,
		Fib4h:             fib4h,
		Fib1h:             fib1h,
		PriceAction4h:     pa4h,
		PriceAction15m:    pa15,
		CandleShapes15m:   candles15m,
		CandleShapes1h:    candles1h,
		CandleShapes4h:    candles4h,
	}, nil
}

// detect4hZones 根据你的文字规则，识别最近30~60根4h的强支撑/压力区
func detect4hZones(klines []Kline, ctx *LongerTermData) []SRZone {
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

// getKlines 从Binance获取K线数据
func getKlines(symbol, interval string, limit int) ([]Kline, error) {
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

// calculateEMA 计算EMA
func calculateEMA(klines []Kline, period int) float64 {
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

// calculateMACD 计算MACD
func calculateMACD(klines []Kline) float64 {
	if len(klines) < 26 {
		return 0
	}
	ema12 := calculateEMA(klines, 12)
	ema26 := calculateEMA(klines, 26)
	return ema12 - ema26
}

// calculateRSI 计算RSI
func calculateRSI(klines []Kline, period int) float64 {
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

func calculateBollinger(klines []Kline, period int, mult float64) *BollingerBand {
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
		MidPrices:     make([]float64, 0, 10),
		EMA20Values:   make([]float64, 0, 10),
		MACDValues:    make([]float64, 0, 10),
		RSI7Values:    make([]float64, 0, 10),
		RSI14Values:   make([]float64, 0, 10),
		Volumes:       make([]float64, 0, 10),
		BuySellRatios: make([]float64, 0, 10),
	}

	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)
		data.Volumes = append(data.Volumes, klines[i].Volume)
		data.BuySellRatios = append(data.BuySellRatios, klines[i].BuySellRatio)

		if i >= 19 {
			ema20 := calculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}

		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	return data
}

// calculateMidTermSeries15m 计算15分钟系列数据
func calculateMidTermSeries15m(klines []Kline) *MidTermData15m {
	data := &MidTermData15m{
		MidPrices:   make([]float64, 0, 10),
		EMA20Values: make([]float64, 0, 10),
		MACDValues:  make([]float64, 0, 10),
		RSI7Values:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
	}

	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)

		if i >= 19 {
			ema20 := calculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}

		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}
	data.Bollinger = calculateBollinger(klines, 20, 2.0)
	return data
}

// calculateMidTermSeries1h 计算1小时系列数据
func calculateMidTermSeries1h(klines []Kline) *MidTermData1h {
	data := &MidTermData1h{
		MidPrices:   make([]float64, 0, 10),
		EMA20Values: make([]float64, 0, 10),
		MACDValues:  make([]float64, 0, 10),
		RSI7Values:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
	}

	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)

		if i >= 19 {
			ema20 := calculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}

		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	return data
}

// calculateLongerTermData 计算长期数据
func calculateLongerTermData(klines []Kline) *LongerTermData {
	data := &LongerTermData{
		MACDValues:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
	}

	data.EMA20 = calculateEMA(klines, 20)
	data.EMA50 = calculateEMA(klines, 50)

	data.ATR3 = calculateATR(klines, 3)
	data.ATR14 = calculateATR(klines, 14)

	if len(klines) > 0 {
		data.CurrentVolume = klines[len(klines)-1].Volume
		sum := 0.0
		for _, k := range klines {
			sum += k.Volume
		}
		data.AverageVolume = sum / float64(len(klines))
	}

	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}
	data.Bollinger = calculateBollinger(klines, 20, 2.0)
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

// ===== 价格行为：核心复刻（精简版） =====

func calcPriceActionSummary(klines []Kline, timeframe string, zigzagLen, liquidityLen, trendLineLen int) *PriceActionSummary {
	n := len(klines)

	// 至少需要一定数量的K线才能做结构/斜率判断：
	// - zigzagLen*2+2 保证左右各有 zigzagLen 根
	// - 20 做一个下限，避免太少的数据噪声太大
	minNeed := max3(zigzagLen*2+2, 20, 20)
	if n < minNeed {
		return &PriceActionSummary{Timeframe: timeframe, LastSignal: "none"}
	}

	// 1) 枢轴序列（用于结构、liquidity与趋势线斜率）
	// 使用 zigzagLen 作为 pivot 的 span，更贴近 ICT / zigzag 的逻辑
	pivotHighIdx, pivotLowIdx := computePivots(klines, zigzagLen)

	// 如果连一个高点/低点都没找出来，就没法做结构判断，直接返回空结果
	if len(pivotHighIdx) == 0 || len(pivotLowIdx) == 0 {
		return &PriceActionSummary{
			Timeframe:  timeframe,
			LastSignal: "none",
			BullSlope:  0,
			BearSlope:  0,
		}
	}

	// 2) 计算最近的趋势线斜率（用最近两个pivot）
	bullSlope := 0.0
	bearSlope := 0.0
	if len(pivotLowIdx) >= 2 {
		i1 := pivotLowIdx[len(pivotLowIdx)-2]
		i2 := pivotLowIdx[len(pivotLowIdx)-1]
		if (klines[i2].OpenTime - klines[i1].OpenTime) != 0 {
			bullSlope = (klines[i2].Low - klines[i1].Low) / float64(i2-i1)
		}
	}
	if len(pivotHighIdx) >= 2 {
		i1 := pivotHighIdx[len(pivotHighIdx)-2]
		i2 := pivotHighIdx[len(pivotHighIdx)-1]
		if (klines[i2].OpenTime - klines[i1].OpenTime) != 0 {
			bearSlope = (klines[i2].High - klines[i1].High) / float64(i2-i1)
		}
	}

	// 3) 结构信号（BOS/CHoCH）
	lastSignal := "none"
	var lastSignalTime int64
	lastState := "" // "up" / "down"
	var lastHigh float64
	var lastLow float64
	if len(pivotHighIdx) > 0 {
		lastHigh = klines[pivotHighIdx[0]].High
	}
	if len(pivotLowIdx) > 0 {
		lastLow = klines[pivotLowIdx[0]].Low
	}
	hPtr, lPtr := 0, 0
	atr14 := calculateATR(klines, 14)

	type rawOB struct {
		bear          bool
		lower, upper  float64
		startIdx, endIdx int
	}
	var obCandidates []rawOB

	for i := max2(pivotHighIdx[0], pivotLowIdx[0]); i < n; i++ {
		// 更新最近枢轴参考
		for hPtr+1 < len(pivotHighIdx) && pivotHighIdx[hPtr+1] <= i {
			hPtr++
			lastHigh = klines[pivotHighIdx[hPtr]].High
		}
		for lPtr+1 < len(pivotLowIdx) && pivotLowIdx[lPtr+1] <= i {
			lPtr++
			lastLow = klines[pivotLowIdx[lPtr]].Low
		}

		c := klines[i].Close

		// 向上结构突破
		if c > lastHigh {
			newState := "up"
			if lastState == "up" || lastState == "" {
				lastSignal = "BOS_up"
			} else {
				lastSignal = "CHoCH_up"
			}
			lastSignalTime = klines[i].CloseTime
			lastState = newState

			// 生成看多OB：区间=上一个pivotHigh到当前i内的最低价
			left := 0
			if hPtr >= 0 && hPtr < len(pivotHighIdx) {
				left = pivotHighIdx[hPtr]
			}
			minLow, _ := minLowBetween(klines, left, i)

			obLower := minLow
			obUpper := minLow + atr14
			obCandidates = append(obCandidates, rawOB{
				bear: false, lower: obLower, upper: obUpper, startIdx: left, endIdx: i,
			})
		}

		// 向下结构突破
		if c < lastLow {
			newState := "down"
			if lastState == "down" || lastState == "" {
				lastSignal = "BOS_down"
			} else {
				lastSignal = "CHoCH_down"
			}
			lastSignalTime = klines[i].CloseTime
			lastState = newState

			// 生成看空OB：区间=上一个pivotLow到当前i内的最高价
			left := 0
			if lPtr >= 0 && lPtr < len(pivotLowIdx) {
				left = pivotLowIdx[lPtr]
			}

			maxHigh, _ := maxHighBetween(klines, left, i)
			obUpper := maxHigh
			obLower := maxHigh - atr14
			obCandidates = append(obCandidates, rawOB{
				bear: true, lower: obLower, upper: obUpper, startIdx: left, endIdx: i,
			})
		}
	}

	// 4) OB有效性筛选，只保留“未失效”的最近1~2个
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

	// 5) liquidity sweep：从最近的 pivot 高/低构造水平线，寻找“刺穿并收回”的最近2条
	sweptHighs := detectSweptHighs(klines, pivotHighIdx, 2)
	sweptLows := detectSweptLows(klines, pivotLowIdx, 2)

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

	sb.WriteString(fmt.Sprintf("In addition, here is the latest %s open interest and funding rate for perps:\n\n",
		data.Symbol))

	if data.OpenInterest != nil {
		sb.WriteString(fmt.Sprintf("Open Interest: Latest: %.2f Average: %.2f\n\n",
			data.OpenInterest.Latest, data.OpenInterest.Average))
	}

	sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))

	// 5m
	if data.IntradaySeries != nil {
		sb.WriteString("Intraday series (5-minute intervals, oldest → latest):\n\n")
		if len(data.IntradaySeries.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
		}
		if len(data.IntradaySeries.EMA20Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA indicators (20-period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
		}
		if len(data.IntradaySeries.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
		}
		if len(data.IntradaySeries.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (7-Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
		}
		if len(data.IntradaySeries.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
		}
	}

	// 15m
	if data.MidTermSeries15m != nil {
		sb.WriteString("Mid-term series (15-minute intervals, oldest → latest):\n\n")
		if len(data.MidTermSeries15m.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.MidTermSeries15m.MidPrices)))
		}
		if len(data.MidTermSeries15m.EMA20Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA indicators (20-period): %s\n\n", formatFloatSlice(data.MidTermSeries15m.EMA20Values)))
		}
		if len(data.MidTermSeries15m.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.MidTermSeries15m.MACDValues)))
		}
		if len(data.MidTermSeries15m.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (7-Period): %s\n\n", formatFloatSlice(data.MidTermSeries15m.RSI7Values)))
		}
		if len(data.MidTermSeries15m.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.MidTermSeries15m.RSI14Values)))
		}
		if data.MidTermSeries15m.Bollinger != nil {
			bb := data.MidTermSeries15m.Bollinger
			sb.WriteString(fmt.Sprintf("15m Bollinger(20,2): upper=%.3f, middle=%.3f, lower=%.3f, width=%.4f, percent=%.3f\n\n",
				bb.Upper, bb.Middle, bb.Lower, bb.Width, bb.Percent))
		}
	}

	// 1h
	if data.MidTermSeries1h != nil {
		sb.WriteString("Mid-term series (1-hour intervals, oldest → latest):\n\n")
		if len(data.MidTermSeries1h.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.MidTermSeries1h.MidPrices)))
		}
		if len(data.MidTermSeries1h.EMA20Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA indicators (20-period): %s\n\n", formatFloatSlice(data.MidTermSeries1h.EMA20Values)))
		}
		if len(data.MidTermSeries1h.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.MidTermSeries1h.MACDValues)))
		}
		if len(data.MidTermSeries1h.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (7-Period): %s\n\n", formatFloatSlice(data.MidTermSeries1h.RSI7Values)))
		}
		if len(data.MidTermSeries1h.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.MidTermSeries1h.RSI14Values)))
		}
	}

	// 4h
	if data.LongerTermContext != nil {
		sb.WriteString("Longer-term context (4-hour timeframe):\n\n")
		sb.WriteString(fmt.Sprintf("20-Period EMA: %.3f vs. 50-Period EMA: %.3f\n\n",
			data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))
		sb.WriteString(fmt.Sprintf("3-Period ATR: %.3f vs. 14-Period ATR: %.3f\n\n",
			data.LongerTermContext.ATR3, data.LongerTermContext.ATR14))
		sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
			data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))
		if len(data.LongerTermContext.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
		}
		if len(data.LongerTermContext.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14-Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
		}
		if data.LongerTermContext.Bollinger != nil {
			bb := data.LongerTermContext.Bollinger
			sb.WriteString(fmt.Sprintf("4h Bollinger(20,2): upper=%.3f, middle=%.3f, lower=%.3f, width=%.4f, percent=%.3f\n\n",
				bb.Upper, bb.Middle, bb.Lower, bb.Width, bb.Percent))
		}
	}

	// 打印 4h 区间
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

	// 打印 15m 区间（同样只给关键几档）
	if len(data.FifteenMinZones) > 0 {
		sup, res := pickKeyZones(data.FifteenMinZones, data.CurrentPrice, 2)

		sb.WriteString("15m key SR zones (local fine-tuning):\n")
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

	// 打印斐波那契
	if data.Fib4h != nil {
		sb.WriteString("4h Fibonacci levels:\n")
		sb.WriteString(fmt.Sprintf("swing_low=%.2f swing_high=%.2f direction=%s\n", data.Fib4h.SwingLow, data.Fib4h.SwingHigh, data.Fib4h.Direction))
		for _, lvl := range data.Fib4h.Levels {
			sb.WriteString(fmt.Sprintf("ratio=%.3f price=%.2f\n", lvl.Ratio, lvl.Price))
		}
		sb.WriteString("\n")
	}
	if data.Fib1h != nil {
		sb.WriteString("1h Fibonacci levels:\n")
		sb.WriteString(fmt.Sprintf("swing_low=%.2f swing_high=%.2f direction=%s\n", data.Fib1h.SwingLow, data.Fib1h.SwingHigh, data.Fib1h.Direction))
		for _, lvl := range data.Fib1h.Levels {
			sb.WriteString(fmt.Sprintf("ratio=%.3f price=%.2f\n", lvl.Ratio, lvl.Price))
		}
		sb.WriteString("\n")
	}

	// Price Action summary
	if data.PriceAction4h != nil {
		pa := data.PriceAction4h
		sb.WriteString("4h Price Action:\n")
		sb.WriteString(fmt.Sprintf("signal=%s time=%d bull_slope=%.6f bear_slope=%.6f\n",
			pa.LastSignal, pa.LastSignalTime, pa.BullSlope, pa.BearSlope))
		if len(pa.BearishOB) > 0 {
			for i, ob := range pa.BearishOB {
				sb.WriteString(fmt.Sprintf("bear_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		if len(pa.BullishOB) > 0 {
			for i, ob := range pa.BullishOB {
				sb.WriteString(fmt.Sprintf("bull_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		if len(pa.SweptHighs) > 0 {
			for i, lh := range pa.SweptHighs {
				sb.WriteString(fmt.Sprintf("swept_high #%d: %.4f @%d\n", i+1, lh.Price, lh.Time))
			}
		}
		if len(pa.SweptLows) > 0 {
			for i, ll := range pa.SweptLows {
				sb.WriteString(fmt.Sprintf("swept_low  #%d: %.4f @%d\n", i+1, ll.Price, ll.Time))
			}
		}
		sb.WriteString("\n")
	}

	if data.PriceAction15m != nil {
		pa := data.PriceAction15m
		sb.WriteString("15m Price Action:\n")
		sb.WriteString(fmt.Sprintf("signal=%s time=%d bull_slope=%.6f bear_slope=%.6f\n",
			pa.LastSignal, pa.LastSignalTime, pa.BullSlope, pa.BearSlope))
		if len(pa.BearishOB) > 0 {
			for i, ob := range pa.BearishOB {
				sb.WriteString(fmt.Sprintf("bear_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		if len(pa.BullishOB) > 0 {
			for i, ob := range pa.BullishOB {
				sb.WriteString(fmt.Sprintf("bull_ob #%d: %.4f~%.4f\n", i+1, ob.Lower, ob.Upper))
			}
		}
		if len(pa.SweptHighs) > 0 {
			for i, lh := range pa.SweptHighs {
				sb.WriteString(fmt.Sprintf("swept_high #%d: %.4f @%d\n", i+1, lh.Price, lh.Time))
			}
		}
		if len(pa.SweptLows) > 0 {
			for i, ll := range pa.SweptLows {
				sb.WriteString(fmt.Sprintf("swept_low  #%d: %.4f @%d\n", i+1, ll.Price, ll.Time))
			}
		}
		sb.WriteString("\n")
	}
		// 15m K线几何特征
		if len(data.CandleShapes15m) > 0 {
			sb.WriteString("15m recent candle shapes (oldest → latest):\n")
			for i, c := range data.CandleShapes15m {
				sb.WriteString(fmt.Sprintf(
					"%d) dir=%s body=%.2f upper=%.2f lower=%.2f range_vs_atr=%.2f close_pos=%.2f\n",
					i+1, c.Direction, c.BodyPct, c.UpperWickPct, c.LowerWickPct, c.RangeVsATR, c.ClosePosition))
			}
			sb.WriteString("\n")
		}

		// 1h K线几何特征
		if len(data.CandleShapes1h) > 0 {
			sb.WriteString("1h recent candle shapes (oldest → latest):\n")
			for i, c := range data.CandleShapes1h {
				sb.WriteString(fmt.Sprintf(
					"%d) dir=%s body=%.2f upper=%.2f lower=%.2f range_vs_atr=%.2f close_pos=%.2f\n",
					i+1, c.Direction, c.BodyPct, c.UpperWickPct, c.LowerWickPct, c.RangeVsATR, c.ClosePosition))
			}
			sb.WriteString("\n")
		}

		// 4h K线几何特征（少量即可，一般20根以内）
		if len(data.CandleShapes4h) > 0 {
			sb.WriteString("4h recent candle shapes (oldest → latest):\n")
			for i, c := range data.CandleShapes4h {
				sb.WriteString(fmt.Sprintf(
					"%d) dir=%s body=%.2f upper=%.2f lower=%.2f range_vs_atr=%.2f close_pos=%.2f\n",
					i+1, c.Direction, c.BodyPct, c.UpperWickPct, c.LowerWickPct, c.RangeVsATR, c.ClosePosition))
			}
			sb.WriteString("\n")
		}
	return sb.String()
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
	default:
		return 0, fmt.Errorf("unsupported type: %T", v)
	}
}
