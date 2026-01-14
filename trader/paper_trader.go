package trader

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"nofx/market"
)

// PaperOrder 纸交易订单
type PaperOrder struct {
	OrderID         int64
	Symbol          string
	Side            string
	Type            string
	Price           float64
	Quantity        float64
	ExecutedQty     float64
	AvgPrice        float64
	Status          string
	CreateTime      int64
	UpdateTime      int64
	WillNeverFill   bool    // 是否永远不成交（用于测试timeout）
	PartialFillStep int     // 部分成交步骤 (0=未开始, 1=部分成交, 2=完全成交)
}

// DeterministicBehavior 确定性行为配置（仅测试用）
type DeterministicBehavior struct {
	Enabled               bool          // 是否启用确定性模式
	FillDelayMs           int           // 固定成交延迟（毫秒），-1表示立即成交
	NeverFill             bool          // 永不成交
	FillAfterPolls        int           // 第几次轮询后成交（0=立即，>0=指定次数）
	PartialFillRatio      float64       // 部分成交比例（0.0-1.0），0表示不部分成交
	FixedFillPrice        float64       // 固定成交价格，0表示使用市场价格
	CancelOnPartialFill   bool          // 是否在部分成交时取消剩余
}

// PaperTrader 纸交易器，用于测试执行链路
type PaperTrader struct {
	mu             sync.RWMutex
	orders         map[int64]*PaperOrder
	nextOrderID    int64
	balances       map[string]float64
	positions      []map[string]interface{}
	fillDelayMinMs int  // 最小成交延迟(ms)
	fillDelayMaxMs int  // 最大成交延迟(ms)
	neverFillRatio float64 // 永不成交订单比例 (0.0-1.0)

	// 确定性行为（仅测试用）
	deterministicBehavior *DeterministicBehavior
}

// NewPaperTrader 创建纸交易器
func NewPaperTrader() *PaperTrader {
	return &PaperTrader{
		orders:         make(map[int64]*PaperOrder),
		nextOrderID:    2000000, // 从200万开始，与真实订单ID区分
		balances:       map[string]float64{"USDT": 100000.0},
		positions:      make([]map[string]interface{}, 0),
		fillDelayMinMs: 500,   // 默认500ms最小延迟
		fillDelayMaxMs: 3000,  // 默认3秒最大延迟
		neverFillRatio: 0.1,   // 默认10%订单永不成交
	}
}

// SetFillDelays 设置成交延迟范围
func (t *PaperTrader) SetFillDelays(minMs, maxMs int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fillDelayMinMs = minMs
	t.fillDelayMaxMs = maxMs
}

// SetNeverFillRatio 设置永不成交订单比例
func (t *PaperTrader) SetNeverFillRatio(ratio float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	t.neverFillRatio = ratio
}

// SetDeterministicBehavior 设置确定性行为（仅测试用）
func (t *PaperTrader) SetDeterministicBehavior(behavior *DeterministicBehavior) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.deterministicBehavior = behavior
}

// startOrderLifecycle 启动订单生命周期协程
func (t *PaperTrader) startOrderLifecycle(order *PaperOrder) {
	go func() {
		t.mu.RLock()
		deterministic := t.deterministicBehavior
		t.mu.RUnlock()

		var willNeverFill bool
		var delay time.Duration

		if deterministic != nil && deterministic.Enabled {
			// 确定性模式
			willNeverFill = deterministic.NeverFill
			if deterministic.FillDelayMs >= 0 {
				delay = time.Duration(deterministic.FillDelayMs) * time.Millisecond
			} else {
				delay = 0 // 立即成交
			}
		} else {
			// 随机模式（原有逻辑）
			willNeverFill = rand.Float64() < t.neverFillRatio
			delay = time.Duration(rand.Intn(t.fillDelayMaxMs-t.fillDelayMinMs)+t.fillDelayMinMs) * time.Millisecond
		}

		if willNeverFill {
			log.Printf("📝 纸交易订单 %d 设置为永不成交模式", order.OrderID)
			return // 永不成交的订单不启动生命周期
		}

		// 延迟后成交
		if delay > 0 {
			time.Sleep(delay)
		}

		t.mu.Lock()
		defer t.mu.Unlock()

		// 检查订单是否已被取消
		if order.Status == "CANCELED" {
			return
		}

		// 获取当前市场价格
		marketData, err := market.Get(order.Symbol)
		if err != nil {
			log.Printf("⚠️ 纸交易获取市场数据失败: %v", err)
			return
		}

		// 确定成交价格
		var fillPrice float64
		if deterministic != nil && deterministic.Enabled && deterministic.FixedFillPrice > 0 {
			fillPrice = deterministic.FixedFillPrice
		} else {
			// 模拟成交价格（在当前价格附近波动）
			if order.Side == "BUY" {
				// 买单用ask价格附近
				if deterministic != nil && deterministic.Enabled {
					fillPrice = marketData.CurrentPrice // 确定性模式使用精确价格
				} else {
					fillPrice = marketData.CurrentPrice * (1 + (rand.Float64()-0.5)*0.001) // ±0.05%波动
				}
			} else {
				// 卖单用bid价格附近
				if deterministic != nil && deterministic.Enabled {
					fillPrice = marketData.CurrentPrice // 确定性模式使用精确价格
				} else {
					fillPrice = marketData.CurrentPrice * (1 + (rand.Float64()-0.5)*0.001) // ±0.05%波动
				}
			}
		}

		// 确保价格合理
		if fillPrice <= 0 {
			fillPrice = marketData.CurrentPrice
		}

		// 部分成交逻辑
		var doPartialFill bool
		var partialRatio float64
		var cancelOnPartial bool

		if deterministic != nil && deterministic.Enabled {
			// 确定性模式
			doPartialFill = deterministic.PartialFillRatio > 0
			partialRatio = deterministic.PartialFillRatio
			cancelOnPartial = deterministic.CancelOnPartialFill
		} else {
			// 随机模式
			doPartialFill = order.PartialFillStep == 0 && rand.Float64() < 0.3 // 30%概率部分成交
			partialRatio = 0.5 // 固定50%
			cancelOnPartial = false // 默认不取消
		}

		if doPartialFill && order.PartialFillStep == 0 {
			// 第一次部分成交
			partialQty := order.Quantity * partialRatio
			order.ExecutedQty = partialQty
			order.AvgPrice = fillPrice
			order.Status = "PARTIALLY_FILLED"
			order.UpdateTime = time.Now().UnixMilli()
			order.PartialFillStep = 1

			log.Printf("📝 纸交易订单 %d 部分成交: %.6f/%.6f @ %.4f",
				order.OrderID, partialQty, order.Quantity, fillPrice)

			if cancelOnPartial {
				// 确定性模式：部分成交后取消
				log.Printf("📝 纸交易订单 %d 部分成交后取消剩余部分", order.OrderID)
				return
			}

			// 继续等待剩余部分（仅在随机模式或确定性模式且不取消的情况下）
			go func() {
				if deterministic != nil && deterministic.Enabled {
					// 确定性模式：立即成交剩余部分
					time.Sleep(10 * time.Millisecond)
				} else {
					// 随机模式：较短延迟
					time.Sleep(delay / 2)
				}

				t.mu.Lock()
				defer t.mu.Unlock()

				if order.Status == "CANCELED" {
					return
				}

				// 剩余部分成交
				remainingQty := order.Quantity - order.ExecutedQty
				newFillPrice := fillPrice
				if deterministic == nil || !deterministic.Enabled {
					// 随机模式：更小的波动
					newFillPrice = fillPrice * (1 + (rand.Float64()-0.5)*0.0005)
				}

				// 加权平均价格
				totalValue := order.ExecutedQty*order.AvgPrice + remainingQty*newFillPrice
				order.ExecutedQty = order.Quantity
				order.AvgPrice = totalValue / order.Quantity
				order.Status = "FILLED"
				order.UpdateTime = time.Now().UnixMilli()
				order.PartialFillStep = 2

				log.Printf("📝 纸交易订单 %d 完全成交: %.6f @ %.4f (总均价: %.4f)",
					order.OrderID, remainingQty, newFillPrice, order.AvgPrice)
			}()
		} else {
			// 直接完全成交
			order.ExecutedQty = order.Quantity
			order.AvgPrice = fillPrice
			order.Status = "FILLED"
			order.UpdateTime = time.Now().UnixMilli()

			log.Printf("📝 纸交易订单 %d 完全成交: %.6f @ %.4f",
				order.OrderID, order.Quantity, fillPrice)
		}
	}()
}

// GetBalance 获取余额
func (t *PaperTrader) GetBalance() (map[string]interface{}, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]interface{})
	for currency, amount := range t.balances {
		result[currency] = map[string]interface{}{
			"free":     amount,
			"locked":   0.0,
			"total":    amount,
		}
	}
	return result, nil
}

// GetPositions 获取持仓
func (t *PaperTrader) GetPositions() ([]map[string]interface{}, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.positions, nil
}

// OpenLong 开多仓
func (t *PaperTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return map[string]interface{}{
		"symbol":    symbol,
		"side":      "BUY",
		"quantity":  quantity,
		"leverage":  leverage,
		"price":     50000.0, // 模拟价格
		"orderId":   rand.Int63n(1000000),
	}, nil
}

// OpenShort 开空仓
func (t *PaperTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return map[string]interface{}{
		"symbol":    symbol,
		"side":      "SELL",
		"quantity":  quantity,
		"leverage":  leverage,
		"price":     50000.0, // 模拟价格
		"orderId":   rand.Int63n(1000000),
	}, nil
}

// CloseLong 平多仓
func (t *PaperTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{
		"symbol":   symbol,
		"side":     "SELL",
		"quantity": quantity,
		"price":    50000.0, // 模拟价格
	}, nil
}

// CloseShort 平空仓
func (t *PaperTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{
		"symbol":   symbol,
		"side":     "BUY",
		"quantity": quantity,
		"price":    50000.0, // 模拟价格
	}, nil
}

// SetLeverage 设置杠杆
func (t *PaperTrader) SetLeverage(symbol string, leverage int) error {
	return nil
}

// SetMarginMode 设置仓位模式
func (t *PaperTrader) SetMarginMode(symbol string, isCrossMargin bool) error {
	return nil
}

// GetMarketPrice 获取市场价格
func (t *PaperTrader) GetMarketPrice(symbol string) (float64, error) {
	marketData, err := market.Get(symbol)
	if err != nil {
		return 0, err
	}
	return marketData.CurrentPrice, nil
}

// SetStopLoss 设置止损单
func (t *PaperTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	return nil
}

// SetTakeProfit 设置止盈单
func (t *PaperTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	return nil
}

// CancelAllOrders 取消所有挂单
func (t *PaperTrader) CancelAllOrders(symbol string) error {
	return nil
}

// LimitOpenLong 限价开多仓
func (t *PaperTrader) LimitOpenLong(symbol string, quantity float64, leverage int, limitPrice, stopLoss float64) (map[string]interface{}, error) {
	t.mu.Lock()
	orderID := t.nextOrderID
	t.nextOrderID++

	order := &PaperOrder{
		OrderID:     orderID,
		Symbol:      symbol,
		Side:        "BUY",
		Type:        "LIMIT",
		Price:       limitPrice,
		Quantity:    quantity,
		ExecutedQty: 0,
		AvgPrice:    0,
		Status:      "NEW",
		CreateTime:  time.Now().UnixMilli(),
		UpdateTime:  time.Now().UnixMilli(),
	}
	t.orders[orderID] = order
	t.mu.Unlock()

	log.Printf("📝 纸交易限价开多仓: %s %.6f @ %.4f (订单ID: %d)", symbol, quantity, limitPrice, orderID)

	// 启动订单生命周期
	t.startOrderLifecycle(order)

	return map[string]interface{}{
		"symbol":     symbol,
		"orderId":    orderID,
		"side":       "BUY",
		"type":       "LIMIT",
		"price":      limitPrice,
		"quantity":   quantity,
		"status":     "NEW",
	}, nil
}

// LimitOpenShort 限价开空仓
func (t *PaperTrader) LimitOpenShort(symbol string, quantity float64, leverage int, limitPrice, stopLoss float64) (map[string]interface{}, error) {
	t.mu.Lock()
	orderID := t.nextOrderID
	t.nextOrderID++

	order := &PaperOrder{
		OrderID:     orderID,
		Symbol:      symbol,
		Side:        "SELL",
		Type:        "LIMIT",
		Price:       limitPrice,
		Quantity:    quantity,
		ExecutedQty: 0,
		AvgPrice:    0,
		Status:      "NEW",
		CreateTime:  time.Now().UnixMilli(),
		UpdateTime:  time.Now().UnixMilli(),
	}
	t.orders[orderID] = order
	t.mu.Unlock()

	log.Printf("📝 纸交易限价开空仓: %s %.6f @ %.4f (订单ID: %d)", symbol, quantity, limitPrice, orderID)

	// 启动订单生命周期
	t.startOrderLifecycle(order)

	return map[string]interface{}{
		"symbol":     symbol,
		"orderId":    orderID,
		"side":       "SELL",
		"type":       "LIMIT",
		"price":      limitPrice,
		"quantity":   quantity,
		"status":     "NEW",
	}, nil
}

// GetOpenOrders 获取挂单
func (t *PaperTrader) GetOpenOrders(symbol string) ([]map[string]interface{}, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]map[string]interface{}, 0)
	for _, order := range t.orders {
		if order.Symbol == symbol && (order.Status == "NEW" || order.Status == "PARTIALLY_FILLED") {
			result = append(result, map[string]interface{}{
				"orderId":     order.OrderID,
				"symbol":      order.Symbol,
				"side":        order.Side,
				"type":        order.Type,
				"price":       order.Price,
				"quantity":    order.Quantity,
				"executedQty": order.ExecutedQty,
				"avgPrice":    order.AvgPrice,
				"status":      order.Status,
			})
		}
	}
	return result, nil
}

// GetOrderStatus 查询订单状态
func (t *PaperTrader) GetOrderStatus(symbol string, orderID int64) (map[string]interface{}, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	order, exists := t.orders[orderID]
	if !exists {
		return nil, fmt.Errorf("订单不存在: %d", orderID)
	}

	return map[string]interface{}{
		"orderId":      order.OrderID,
		"symbol":       order.Symbol,
		"side":         order.Side,
		"type":         order.Type,
		"price":        order.Price,
		"quantity":     order.Quantity,
		"executedQty":  order.ExecutedQty,
		"avgPrice":     order.AvgPrice,
		"status":       order.Status,
		"time":         order.CreateTime,
		"updateTime":   order.UpdateTime,
	}, nil
}

// CancelOrder 取消订单
func (t *PaperTrader) CancelOrder(symbol string, orderID int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	order, exists := t.orders[orderID]
	if !exists {
		return fmt.Errorf("订单不存在: %d", orderID)
	}

	if order.Status == "FILLED" || order.Status == "CANCELED" {
		return fmt.Errorf("订单已完成或已取消: %s", order.Status)
	}

	order.Status = "CANCELED"
	order.UpdateTime = time.Now().UnixMilli()

	log.Printf("📝 纸交易取消订单: %d", orderID)

	return nil
}

// FormatQuantity 格式化数量
func (t *PaperTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	return fmt.Sprintf("%.6f", quantity), nil
}
