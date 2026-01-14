package trader

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// MockTrader 用于测试的模拟交易器
type MockTrader struct {
	mu             sync.RWMutex
	orders         map[int64]*MockOrder
	nextOrderID    int64
	orderStatuses  []string // 用于控制订单状态变化
	statusIndex    int
}

// MockOrder 模拟订单
type MockOrder struct {
	OrderID        int64
	Symbol         string
	Side           string
	Type           string
	Price          float64
	Quantity       float64
	ExecutedQty    float64
	AvgPrice       float64
	Status         string
	CreateTime     int64
	UpdateTime     int64
}

// NewMockTrader 创建模拟交易器
func NewMockTrader() *MockTrader {
	return &MockTrader{
		orders:         make(map[int64]*MockOrder),
		nextOrderID:    1000000,
		orderStatuses:  []string{"NEW", "PARTIALLY_FILLED", "FILLED"}, // 默认成功路径
		statusIndex:    0,
	}
}

// SetOrderStatuses 设置订单状态变化序列，用于测试不同场景
func (t *MockTrader) SetOrderStatuses(statuses []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.orderStatuses = statuses
	t.statusIndex = 0
}

// GetBalance 模拟获取余额
func (t *MockTrader) GetBalance() (map[string]interface{}, error) {
	return map[string]interface{}{
		"USDT": map[string]interface{}{
			"free":     10000.0,
			"locked":   0.0,
			"total":    10000.0,
		},
	}, nil
}

// GetPositions 模拟获取持仓
func (t *MockTrader) GetPositions() ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}

// OpenLong 模拟开多仓
func (t *MockTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return map[string]interface{}{
		"symbol":    symbol,
		"side":      "BUY",
		"quantity":  quantity,
		"leverage":  leverage,
		"price":     50000.0,
		"orderId":   rand.Int63n(1000000),
	}, nil
}

// OpenShort 模拟开空仓
func (t *MockTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return map[string]interface{}{
		"symbol":    symbol,
		"side":      "SELL",
		"quantity":  quantity,
		"leverage":  leverage,
		"price":     50000.0,
		"orderId":   rand.Int63n(1000000),
	}, nil
}

// CloseLong 模拟平多仓
func (t *MockTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{
		"symbol":   symbol,
		"side":     "SELL",
		"quantity": quantity,
		"price":    50000.0,
	}, nil
}

// CloseShort 模拟平空仓
func (t *MockTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return map[string]interface{}{
		"symbol":   symbol,
		"side":     "BUY",
		"quantity": quantity,
		"price":    50000.0,
	}, nil
}

// SetLeverage 模拟设置杠杆
func (t *MockTrader) SetLeverage(symbol string, leverage int) error {
	return nil
}

// SetMarginMode 模拟设置仓位模式
func (t *MockTrader) SetMarginMode(symbol string, isCrossMargin bool) error {
	return nil
}

// GetMarketPrice 模拟获取市场价格
func (t *MockTrader) GetMarketPrice(symbol string) (float64, error) {
	return 50000.0, nil
}

// SetStopLoss 模拟设置止损单
func (t *MockTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	return nil
}

// SetTakeProfit 模拟设置止盈单
func (t *MockTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	return nil
}

// CancelAllOrders 模拟取消所有挂单
func (t *MockTrader) CancelAllOrders(symbol string) error {
	return nil
}

// LimitOpenLong 模拟限价开多仓
func (t *MockTrader) LimitOpenLong(symbol string, quantity float64, leverage int, limitPrice, stopLoss float64) (map[string]interface{}, error) {
	t.mu.Lock()
	orderID := t.nextOrderID
	t.nextOrderID++

	order := &MockOrder{
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

// LimitOpenShort 模拟限价开空仓
func (t *MockTrader) LimitOpenShort(symbol string, quantity float64, leverage int, limitPrice, stopLoss float64) (map[string]interface{}, error) {
	t.mu.Lock()
	orderID := t.nextOrderID
	t.nextOrderID++

	order := &MockOrder{
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

// GetOpenOrders 模拟获取挂单
func (t *MockTrader) GetOpenOrders(symbol string) ([]map[string]interface{}, error) {
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

// GetOrderStatus 模拟查询订单状态（每次调用会更新状态）
func (t *MockTrader) GetOrderStatus(symbol string, orderID int64) (map[string]interface{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	order, exists := t.orders[orderID]
	if !exists {
		return nil, fmt.Errorf("订单不存在: %d", orderID)
	}

	// 根据预设的状态序列更新订单状态
	if t.statusIndex < len(t.orderStatuses) {
		order.Status = t.orderStatuses[t.statusIndex]
		order.UpdateTime = time.Now().UnixMilli()

		// 模拟成交
		if order.Status == "PARTIALLY_FILLED" {
			order.ExecutedQty = order.Quantity * 0.5 // 50% 成交
			order.AvgPrice = order.Price
		} else if order.Status == "FILLED" {
			order.ExecutedQty = order.Quantity
			order.AvgPrice = order.Price
		}

		t.statusIndex++
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

// ResetOrderStatuses 重置订单状态序列，用于测试多个订单
func (t *MockTrader) ResetOrderStatuses() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.statusIndex = 0
}

// CancelOrder 模拟取消订单
func (t *MockTrader) CancelOrder(symbol string, orderID int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	order, exists := t.orders[orderID]
	if !exists {
		return fmt.Errorf("订单不存在: %d", orderID)
	}

	order.Status = "CANCELED"
	order.UpdateTime = time.Now().UnixMilli()

	return nil
}

// FormatQuantity 模拟格式化数量
func (t *MockTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	return fmt.Sprintf("%.6f", quantity), nil
}
