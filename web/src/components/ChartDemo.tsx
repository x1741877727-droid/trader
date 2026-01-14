import { useState, useEffect } from 'react';
import { TradingChart } from './TradingChart';

export const ChartDemo = () => {
  const [klineData, setKlineData] = useState<any[]>([]);

  useEffect(() => {
    // 模拟K线数据（实际应该从API获取）
    const generateMockData = () => {
      const data = [];
      const basePrice = 95000;
      let currentPrice = basePrice;
      const startTime = new Date('2024-01-15T00:00:00Z');

      for (let i = 0; i < 100; i++) {
        const time = new Date(startTime.getTime() + i * 60 * 60 * 1000);
        const open = currentPrice;
        const change = (Math.random() - 0.5) * 1000;
        const close = open + change;
        const high = Math.max(open, close) + Math.random() * 500;
        const low = Math.min(open, close) - Math.random() * 500;

        data.push({
          time: Math.floor(time.getTime() / 1000), // Unix timestamp in seconds
          open,
          high,
          low,
          close,
          volume: Math.random() * 1000000,
        });

        currentPrice = close;
      }

      return data;
    };

    setKlineData(generateMockData());
  }, []);

  // 模拟交易记录
  const mockTrades = [
    {
      time: '2024-01-15 10:00',
      type: 'short' as const,
      price: 96000,
      reasoning: '4h空头趋势，RSI超买',
    },
    {
      time: '2024-01-15 16:00',
      type: 'close' as const,
      price: 94500,
      reasoning: '到达TP2，部分止盈',
    },
  ];

  // 模拟当前持仓
  const mockPositions = [
    {
      entryPrice: 96000,
      stopLoss: 97500,
      tp1: 95000,
      tp2: 94000,
      tp3: 93000,
      side: 'short' as const,
    },
  ];

  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 p-6">
      <div className="mx-auto max-w-7xl">
        <div className="mb-6">
          <h1 className="text-3xl font-bold text-white">交易图表演示</h1>
          <p className="mt-2 text-slate-400">
            基于 TradingView Lightweight Charts 的实时K线图表
          </p>
        </div>

        <div className="mb-6 grid gap-4 md:grid-cols-3">
          <div className="rounded-lg bg-slate-900 p-4 shadow-xl">
            <div className="text-sm text-slate-400">当前价格</div>
            <div className="mt-1 text-2xl font-bold text-white">
              {klineData.length > 0 ? `$${klineData[klineData.length - 1].close.toFixed(2)}` : '-'}
            </div>
            <div className="mt-1 text-sm text-green-400">+2.34%</div>
          </div>

          <div className="rounded-lg bg-slate-900 p-4 shadow-xl">
            <div className="text-sm text-slate-400">持仓盈亏</div>
            <div className="mt-1 text-2xl font-bold text-green-400">+$1,500</div>
            <div className="mt-1 text-sm text-slate-400">+1.56%</div>
          </div>

          <div className="rounded-lg bg-slate-900 p-4 shadow-xl">
            <div className="text-sm text-slate-400">持仓时长</div>
            <div className="mt-1 text-2xl font-bold text-white">6小时</div>
            <div className="mt-1 text-sm text-slate-400">入场: 96000</div>
          </div>
        </div>

        {klineData.length > 0 && (
          <TradingChart
            symbol="BTCUSDT"
            data={klineData}
            positions={mockPositions}
            height={600}
          />
        )}

        <div className="mt-6 rounded-lg bg-slate-900 p-6 shadow-xl">
          <h3 className="mb-4 text-lg font-semibold text-white">交易历史</h3>
          <div className="space-y-3">
            {mockTrades.map((trade, index) => (
              <div
                key={index}
                className="flex items-center justify-between rounded-lg bg-slate-800 p-4"
              >
                <div className="flex items-center gap-4">
                  <div
                    className={`rounded px-3 py-1 text-sm font-semibold ${
                      trade.type === 'short'
                        ? 'bg-red-500/20 text-red-400'
                        : 'bg-blue-500/20 text-blue-400'
                    }`}
                  >
                    {trade.type === 'short' ? '开空' : '平仓'}
                  </div>
                  <div>
                    <div className="text-white">${trade.price.toFixed(2)}</div>
                    <div className="text-xs text-slate-400">{trade.time}</div>
                  </div>
                </div>
                <div className="max-w-md text-sm text-slate-400">{trade.reasoning}</div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
};

