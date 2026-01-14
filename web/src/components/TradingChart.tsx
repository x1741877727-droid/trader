import { useEffect, useRef, useState } from 'react';
import {
  createChart,
  ColorType,
  CandlestickSeries,
  LineSeries,
  HistogramSeries,
  IChartApi,
  Time,
  ISeriesApi,
} from 'lightweight-charts';

interface TradingChartProps {
  symbol: string;
  data: Array<{
    time: number;
    open: number;
    high: number;
    low: number;
    close: number;
    volume?: number;
    ema20?: number;
    ema50?: number;
    ema200?: number;
    macd?: number;
    rsi?: number;
    bb_upper?: number;
    bb_middle?: number;
    bb_lower?: number;
  }>;
  positions?: Array<{
    entryPrice?: number;
    stopLoss?: number;
    tp1?: number;
    tp2?: number;
    tp3?: number;
    side: 'long' | 'short';
  }>;
  height?: number;
}

export const TradingChart: React.FC<TradingChartProps> = ({
  symbol,
  data,
  positions = [],
  height = 500,
}) => {
  const mainChartRef = useRef<HTMLDivElement>(null);
  const volumeChartRef = useRef<HTMLDivElement>(null);
  const macdChartRef = useRef<HTMLDivElement>(null);
  const rsiChartRef = useRef<HTMLDivElement>(null);
  const chartsRef = useRef<{
    mainChart?: IChartApi;
    volumeChart?: IChartApi;
    macdChart?: IChartApi;
    rsiChart?: IChartApi;
  }>({});
  const subscriptionsRef = useRef<Array<() => void>>([]);
  
  // 指标显示状态
  const [indicatorVisibility, setIndicatorVisibility] = useState({
    ema20: true,
    ema50: true,
    ema200: true,
    bollinger: true,
    volume: true,
    macd: true,
    rsi: true,
  });
  
  // 保存系列引用以便控制显示/隐藏
  const seriesRef = useRef<{
    ema20?: ISeriesApi<'Line'>;
    ema50?: ISeriesApi<'Line'>;
    ema200?: ISeriesApi<'Line'>;
    bbUpper?: ISeriesApi<'Line'>;
    bbMiddle?: ISeriesApi<'Line'>;
    bbLower?: ISeriesApi<'Line'>;
    volume?: ISeriesApi<'Histogram'>;
    macd?: ISeriesApi<'Histogram'>;
    rsi?: ISeriesApi<'Line'>;
  }>({});

  useEffect(() => {
    if (!mainChartRef.current) return;
    // Volume、MACD 和 RSI 图表只在需要时创建
    if (indicatorVisibility.volume && !volumeChartRef.current) return;
    if (indicatorVisibility.macd && !macdChartRef.current) return;
    if (indicatorVisibility.rsi && !rsiChartRef.current) return;

    // 清理旧图表和订阅
    subscriptionsRef.current.forEach(unsubscribe => {
      try {
        unsubscribe();
      } catch (e) {
        // 忽略已销毁的订阅错误
      }
    });
    subscriptionsRef.current = [];

    if (chartsRef.current.mainChart) {
      try {
        chartsRef.current.mainChart.remove();
      } catch (e) {
        // 图表可能已经被销毁
      }
    }
    if (chartsRef.current.macdChart) {
      try {
        chartsRef.current.macdChart.remove();
      } catch (e) {
        // 图表可能已经被销毁
      }
    }
    if (chartsRef.current.volumeChart) {
      try {
        chartsRef.current.volumeChart.remove();
      } catch (e) {
        // 图表可能已经被销毁
      }
    }
    if (chartsRef.current.rsiChart) {
      try {
        chartsRef.current.rsiChart.remove();
      } catch (e) {
        // 图表可能已经被销毁
      }
    }

    const chartOptions = {
      layout: {
        background: { type: ColorType.Solid, color: '#0B0E11' },
        textColor: '#848E9C',
      },
      grid: {
        vertLines: { color: '#1E2329' },
        horzLines: { color: '#1E2329' },
      },
      crosshair: {
        mode: 1,
      },
      rightPriceScale: {
        borderColor: '#2B3139',
      },
      timeScale: {
        borderColor: '#2B3139',
        timeVisible: true,
        secondsVisible: false,
        rightOffset: 12, // 右侧留出空间，允许向左滚动
        barSpacing: 6, // 默认K线间距
        minBarSpacing: 0.5, // 最小间距（防止过度放大）
        maxBarSpacing: 50, // 最大间距（防止过度缩小）
        fixLeftEdge: false, // 不固定左边缘，允许向左滚动
        fixRightEdge: false, // 不固定右边缘
        shiftVisibleRangeOnNewBar: true, // 新数据到达时自动滚动
        // 增强滚轮缩放体验
        allowBoldLabels: true,
      },
    };

    // ===== 主图表 (K线 + EMA + 布林带) =====
    const mainChart = createChart(mainChartRef.current, {
      ...chartOptions,
      width: mainChartRef.current.clientWidth,
      height: height,
    });
    chartsRef.current.mainChart = mainChart;

    // K线
    const candlestickSeries = mainChart.addSeries(CandlestickSeries, {
      upColor: '#0ECB81',
      downColor: '#F6465D',
      borderVisible: false,
      wickUpColor: '#0ECB81',
      wickDownColor: '#F6465D',
    });

    const candleData = data.map(d => ({
      time: d.time as Time,
      open: d.open,
      high: d.high,
      low: d.low,
      close: d.close,
    }));
    candlestickSeries.setData(candleData);

    // EMA20
    const ema20Series = mainChart.addSeries(LineSeries, {
      color: '#F0B90B',
      lineWidth: 2,
      title: 'EMA20',
      visible: indicatorVisibility.ema20,
    });
    seriesRef.current.ema20 = ema20Series;
    const ema20Data = data
      .filter(d => d.ema20 && d.ema20 > 0)
      .map(d => ({
        time: d.time as Time,
        value: d.ema20!,
      }));
    if (ema20Data.length > 0) {
      ema20Series.setData(ema20Data);
    }

    // EMA50
    const ema50Series = mainChart.addSeries(LineSeries, {
      color: '#3861FB',
      lineWidth: 2,
      title: 'EMA50',
      visible: indicatorVisibility.ema50,
    });
    seriesRef.current.ema50 = ema50Series;
    const ema50Data = data
      .filter(d => d.ema50 && d.ema50 > 0)
      .map(d => ({
        time: d.time as Time,
        value: d.ema50!,
      }));
    if (ema50Data.length > 0) {
      ema50Series.setData(ema50Data);
    }

    // EMA200
    const ema200Series = mainChart.addSeries(LineSeries, {
      color: '#FF6B6B',
      lineWidth: 2,
      title: 'EMA200',
      visible: indicatorVisibility.ema200,
    });
    seriesRef.current.ema200 = ema200Series;
    const ema200Data = data
      .filter(d => d.ema200 && d.ema200 > 0)
      .map(d => ({
        time: d.time as Time,
        value: d.ema200!,
      }));
    if (ema200Data.length > 0) {
      ema200Series.setData(ema200Data);
    }

    // 布林带上轨
    const bbUpperSeries = mainChart.addSeries(LineSeries, {
      color: '#848E9C',
      lineWidth: 1,
      lineStyle: 2, // 虚线
      title: 'BB Upper',
      visible: indicatorVisibility.bollinger,
    });
    seriesRef.current.bbUpper = bbUpperSeries;
    const bbUpperData = data
      .filter(d => d.bb_upper && d.bb_upper > 0)
      .map(d => ({
        time: d.time as Time,
        value: d.bb_upper!,
      }));
    if (bbUpperData.length > 0) {
      bbUpperSeries.setData(bbUpperData);
    }

    // 布林带中轨
    const bbMiddleSeries = mainChart.addSeries(LineSeries, {
      color: '#848E9C',
      lineWidth: 1,
      title: 'BB Middle',
      visible: indicatorVisibility.bollinger,
    });
    seriesRef.current.bbMiddle = bbMiddleSeries;
    const bbMiddleData = data
      .filter(d => d.bb_middle && d.bb_middle > 0)
      .map(d => ({
        time: d.time as Time,
        value: d.bb_middle!,
      }));
    if (bbMiddleData.length > 0) {
      bbMiddleSeries.setData(bbMiddleData);
    }

    // 布林带下轨
    const bbLowerSeries = mainChart.addSeries(LineSeries, {
      color: '#848E9C',
      lineWidth: 1,
      lineStyle: 2, // 虚线
      title: 'BB Lower',
      visible: indicatorVisibility.bollinger,
    });
    seriesRef.current.bbLower = bbLowerSeries;
    const bbLowerData = data
      .filter(d => d.bb_lower && d.bb_lower > 0)
      .map(d => ({
        time: d.time as Time,
        value: d.bb_lower!,
      }));
    if (bbLowerData.length > 0) {
      bbLowerSeries.setData(bbLowerData);
    }

    // 添加持仓水平线
    if (positions.length > 0) {
      positions.forEach(pos => {
        // 只创建有效的价格线，跳过 undefined/null 值
        if (pos.entryPrice != null && !isNaN(pos.entryPrice)) {
          candlestickSeries.createPriceLine({
            price: pos.entryPrice,
            color: '#3861FB',
            lineWidth: 2,
            lineStyle: 0,
            axisLabelVisible: true,
            title: `Entry: ${pos.entryPrice.toFixed(2)}`,
          });
        }

        if (pos.stopLoss != null && !isNaN(pos.stopLoss)) {
          candlestickSeries.createPriceLine({
            price: pos.stopLoss,
            color: '#F6465D',
            lineWidth: 2,
            lineStyle: 2,
            axisLabelVisible: true,
            title: `SL: ${pos.stopLoss.toFixed(2)}`,
          });
        }

        if (pos.tp1 != null && !isNaN(pos.tp1)) {
          candlestickSeries.createPriceLine({
            price: pos.tp1,
            color: '#0ECB81',
            lineWidth: 1,
            lineStyle: 2,
            axisLabelVisible: true,
            title: `TP1: ${pos.tp1.toFixed(2)}`,
          });
        }

        if (pos.tp2 != null && !isNaN(pos.tp2)) {
          candlestickSeries.createPriceLine({
            price: pos.tp2,
            color: '#0ECB81',
            lineWidth: 1,
            lineStyle: 2,
            axisLabelVisible: true,
            title: `TP2: ${pos.tp2.toFixed(2)}`,
          });
        }

        if (pos.tp3 != null && !isNaN(pos.tp3)) {
          candlestickSeries.createPriceLine({
            price: pos.tp3,
            color: '#0ECB81',
            lineWidth: 2,
            lineStyle: 2,
            axisLabelVisible: true,
            title: `TP3: ${pos.tp3.toFixed(2)}`,
          });
        }
      });
    }

    // ===== 成交量子图 =====
    if (indicatorVisibility.volume && volumeChartRef.current) {
      const volumeChart = createChart(volumeChartRef.current, {
        ...chartOptions,
        width: volumeChartRef.current.clientWidth,
        height: 100,
      });
      chartsRef.current.volumeChart = volumeChart;

      const volumeSeries = volumeChart.addSeries(HistogramSeries, {
        priceFormat: {
          type: 'volume',
        },
        priceScaleId: '',
      });
      seriesRef.current.volume = volumeSeries;
      
      // 设置价格刻度的边距（通过价格刻度选项）
      volumeChart.priceScale('right').applyOptions({
        scaleMargins: {
          top: 0.8,
          bottom: 0,
        },
      });

      const volumeData = data
        .filter(d => d.volume && d.volume > 0)
        .map((d, index) => {
          // 根据涨跌设置颜色（涨=绿色，跌=红色）
          const isUp = index > 0 ? d.close >= data[index - 1].close : d.close >= d.open;
          return {
            time: d.time as Time,
            value: d.volume!,
            color: isUp ? '#0ECB81' : '#F6465D',
          };
        });
      if (volumeData.length > 0) {
        volumeSeries.setData(volumeData);
      }

      // 同步时间轴
      mainChart.timeScale().subscribeVisibleTimeRangeChange((timeRange) => {
        if (timeRange) {
          try {
            volumeChart.timeScale().setVisibleRange(timeRange);
          } catch (e) {
            // 图表可能已被销毁
          }
        }
      });

      volumeChart.timeScale().subscribeVisibleTimeRangeChange((timeRange) => {
        if (timeRange && chartsRef.current.mainChart) {
          try {
            chartsRef.current.mainChart.timeScale().setVisibleRange(timeRange);
          } catch (e) {
            // 图表可能已被销毁
          }
        }
      });
    } else {
      seriesRef.current.volume = undefined;
      chartsRef.current.volumeChart = undefined;
    }

    // ===== MACD 子图 =====
    if (indicatorVisibility.macd && macdChartRef.current) {
      const macdChart = createChart(macdChartRef.current, {
        ...chartOptions,
        width: macdChartRef.current.clientWidth,
        height: 120,
      });
      chartsRef.current.macdChart = macdChart;

      const macdSeries = macdChart.addSeries(HistogramSeries, {
        color: '#0ECB81',
        priceFormat: {
          type: 'price',
          precision: 2,
          minMove: 0.01,
        },
      });
      seriesRef.current.macd = macdSeries;

      const macdData = data
        .filter(d => d.macd !== undefined && d.macd !== 0)
        .map(d => ({
          time: d.time as Time,
          value: d.macd!,
          color: d.macd! >= 0 ? '#0ECB81' : '#F6465D',
        }));
      if (macdData.length > 0) {
        macdSeries.setData(macdData);
      }
    } else {
      seriesRef.current.macd = undefined;
      chartsRef.current.macdChart = undefined;
    }

    // ===== RSI 子图 =====
    if (indicatorVisibility.rsi && rsiChartRef.current) {
      const rsiChart = createChart(rsiChartRef.current, {
        ...chartOptions,
        width: rsiChartRef.current.clientWidth,
        height: 100,
      });
      chartsRef.current.rsiChart = rsiChart;

      const rsiSeries = rsiChart.addSeries(LineSeries, {
        color: '#A371F7',
        lineWidth: 2,
      });
      seriesRef.current.rsi = rsiSeries;

      const rsiData = data
        .filter(d => d.rsi && d.rsi > 0)
        .map(d => ({
          time: d.time as Time,
          value: d.rsi!,
        }));
      if (rsiData.length > 0) {
        rsiSeries.setData(rsiData);
      }

      // RSI 参考线（30/70）
      rsiSeries.createPriceLine({
        price: 70,
        color: '#F6465D',
        lineWidth: 1,
        lineStyle: 2,
        axisLabelVisible: false,
        title: 'Overbought',
      });

      rsiSeries.createPriceLine({
        price: 30,
        color: '#0ECB81',
        lineWidth: 1,
        lineStyle: 2,
        axisLabelVisible: false,
        title: 'Oversold',
      });
    } else {
      seriesRef.current.rsi = undefined;
      chartsRef.current.rsiChart = undefined;
    }

    // ===== 增强滚轮缩放功能 =====
    // 监听滚轮事件，实现更精确的缩放控制
    const handleWheel = (e: WheelEvent) => {
      // 如果按住 Shift 键，进行水平平移
      if (e.shiftKey) {
        e.preventDefault();
        const timeScale = mainChart.timeScale();
        const visibleRange = timeScale.getVisibleRange();
        if (visibleRange) {
          // 将 Time 类型转换为数字进行计算
          const fromTime = typeof visibleRange.from === 'number' ? visibleRange.from : parseInt(String(visibleRange.from));
          const toTime = typeof visibleRange.to === 'number' ? visibleRange.to : parseInt(String(visibleRange.to));
          const range = toTime - fromTime;
          const scrollAmount = (e.deltaY > 0 ? 1 : -1) * range * 0.1; // 每次滚动10%的范围
          let newFrom = Math.max(0, fromTime - scrollAmount);
          let newTo = toTime - scrollAmount;
          
          // 确保不会滚动到数据范围之外
          // 从图表数据中获取时间范围
          if (candleData.length > 0) {
            const firstTimeValue = candleData[0].time;
            const lastTimeValue = candleData[candleData.length - 1].time;
            const firstTime: number = typeof firstTimeValue === 'number' 
              ? firstTimeValue 
              : parseInt(String(firstTimeValue));
            const lastTime: number = typeof lastTimeValue === 'number'
              ? lastTimeValue
              : parseInt(String(lastTimeValue));
            const clampedFrom = Math.max(firstTime, newFrom);
            const clampedTo = Math.min(lastTime, newTo);
            
            if (clampedTo - clampedFrom > range * 0.5) { // 确保可见范围不会太小
              // 转换回 Time 类型
              timeScale.setVisibleRange({ 
                from: clampedFrom as Time, 
                to: clampedTo as Time 
              });
            }
          }
        }
        return;
      }

      // Ctrl/Cmd + 滚轮 = 更精细的缩放控制
      if (e.ctrlKey || e.metaKey) {
        e.preventDefault();
        const timeScale = mainChart.timeScale();
        const currentOptions = timeScale.options();
        const currentBarSpacing = currentOptions.barSpacing || 6;
        const zoomFactor = e.deltaY > 0 ? 0.95 : 1.05; // 更精细的缩放（5%步进）
        const newBarSpacing = Math.max(
          0.5,
          Math.min(50, currentBarSpacing * zoomFactor)
        );
        
        // 应用新的 barSpacing（lightweight-charts 会自动以鼠标位置为中心缩放）
        timeScale.applyOptions({ barSpacing: newBarSpacing });
        return;
      }
      
      // 默认滚轮缩放：lightweight-charts 内置的滚轮缩放已经很好
      // 它默认以鼠标位置为中心进行缩放，我们不需要额外处理
    };

    // 添加滚轮事件监听（使用 passive: false 以便可以 preventDefault）
    const chartContainer = mainChartRef.current;
    if (chartContainer) {
      chartContainer.addEventListener('wheel', handleWheel, { passive: false });
    }

    // 同步时间轴
    // 注意：subscribeVisibleTimeRangeChange 可能返回 void，不跟踪这些订阅
    // 当图表被销毁时，订阅会自动取消
    mainChart.timeScale().subscribeVisibleTimeRangeChange((timeRange) => {
      if (timeRange) {
        try {
          if (chartsRef.current.volumeChart) {
            chartsRef.current.volumeChart.timeScale().setVisibleRange(timeRange);
          }
          if (chartsRef.current.macdChart) {
            chartsRef.current.macdChart.timeScale().setVisibleRange(timeRange);
          }
          if (chartsRef.current.rsiChart) {
            chartsRef.current.rsiChart.timeScale().setVisibleRange(timeRange);
          }
        } catch (e) {
          // 图表可能已被销毁
        }
      }
    });

    if (chartsRef.current.macdChart) {
      chartsRef.current.macdChart.timeScale().subscribeVisibleTimeRangeChange((timeRange) => {
        if (timeRange && chartsRef.current.mainChart) {
          try {
            chartsRef.current.mainChart.timeScale().setVisibleRange(timeRange);
            if (chartsRef.current.volumeChart) {
              chartsRef.current.volumeChart.timeScale().setVisibleRange(timeRange);
            }
            if (chartsRef.current.rsiChart) {
              chartsRef.current.rsiChart.timeScale().setVisibleRange(timeRange);
            }
          } catch (e) {
            // 图表可能已被销毁
          }
        }
      });
    }

    if (chartsRef.current.rsiChart) {
      chartsRef.current.rsiChart.timeScale().subscribeVisibleTimeRangeChange((timeRange) => {
        if (timeRange && chartsRef.current.mainChart) {
          try {
            chartsRef.current.mainChart.timeScale().setVisibleRange(timeRange);
            if (chartsRef.current.volumeChart) {
              chartsRef.current.volumeChart.timeScale().setVisibleRange(timeRange);
            }
            if (chartsRef.current.macdChart) {
              chartsRef.current.macdChart.timeScale().setVisibleRange(timeRange);
            }
          } catch (e) {
            // 图表可能已被销毁
          }
        }
      });
    }

    // 响应式调整
    const handleResize = () => {
      if (mainChartRef.current) {
        try {
          if (chartsRef.current.mainChart) {
            chartsRef.current.mainChart.applyOptions({
              width: mainChartRef.current.clientWidth,
            });
          }
          if (chartsRef.current.volumeChart && volumeChartRef.current) {
            chartsRef.current.volumeChart.applyOptions({
              width: volumeChartRef.current.clientWidth,
            });
          }
          if (chartsRef.current.macdChart && macdChartRef.current) {
            chartsRef.current.macdChart.applyOptions({
              width: macdChartRef.current.clientWidth,
            });
          }
          if (chartsRef.current.rsiChart && rsiChartRef.current) {
            chartsRef.current.rsiChart.applyOptions({
              width: rsiChartRef.current.clientWidth,
            });
          }
        } catch (e) {
          // 图表可能已被销毁
        }
      }
    };

    window.addEventListener('resize', handleResize);

    // 自动适配
    try {
      mainChart.timeScale().fitContent();
      if (chartsRef.current.volumeChart) {
        chartsRef.current.volumeChart.timeScale().fitContent();
      }
      if (chartsRef.current.macdChart) {
        chartsRef.current.macdChart.timeScale().fitContent();
      }
      if (chartsRef.current.rsiChart) {
        chartsRef.current.rsiChart.timeScale().fitContent();
      }
    } catch (e) {
      // 忽略错误
    }

    return () => {
      window.removeEventListener('resize', handleResize);
      
      // 移除滚轮事件监听
      if (chartContainer) {
        chartContainer.removeEventListener('wheel', handleWheel);
      }
      
      // 取消所有订阅
      subscriptionsRef.current.forEach(unsubscribe => {
        try {
          unsubscribe();
        } catch (e) {
          // 忽略已销毁的订阅错误
        }
      });
      subscriptionsRef.current = [];

      // 清理图表
      if (chartsRef.current.mainChart) {
        try {
          chartsRef.current.mainChart.remove();
        } catch (e) {
          // 图表可能已经被销毁
        }
        chartsRef.current.mainChart = undefined;
      }
      if (chartsRef.current.volumeChart) {
        try {
          chartsRef.current.volumeChart.remove();
        } catch (e) {
          // 图表可能已经被销毁
        }
        chartsRef.current.volumeChart = undefined;
      }
      if (chartsRef.current.macdChart) {
        try {
          chartsRef.current.macdChart.remove();
        } catch (e) {
          // 图表可能已经被销毁
        }
        chartsRef.current.macdChart = undefined;
      }
      if (chartsRef.current.rsiChart) {
        try {
          chartsRef.current.rsiChart.remove();
        } catch (e) {
          // 图表可能已经被销毁
        }
        chartsRef.current.rsiChart = undefined;
      }
    };
  }, [data, positions, height, indicatorVisibility]);

  // 控制指标显示/隐藏（仅用于主图表的指标，MACD 和 RSI 通过重新创建图表来控制）
  useEffect(() => {
    if (seriesRef.current.ema20) {
      seriesRef.current.ema20.applyOptions({ visible: indicatorVisibility.ema20 });
    }
    if (seriesRef.current.ema50) {
      seriesRef.current.ema50.applyOptions({ visible: indicatorVisibility.ema50 });
    }
    if (seriesRef.current.ema200) {
      seriesRef.current.ema200.applyOptions({ visible: indicatorVisibility.ema200 });
    }
    if (seriesRef.current.bbUpper) {
      seriesRef.current.bbUpper.applyOptions({ visible: indicatorVisibility.bollinger });
    }
    if (seriesRef.current.bbMiddle) {
      seriesRef.current.bbMiddle.applyOptions({ visible: indicatorVisibility.bollinger });
    }
    if (seriesRef.current.bbLower) {
      seriesRef.current.bbLower.applyOptions({ visible: indicatorVisibility.bollinger });
    }
    // Volume、MACD 和 RSI 通过重新创建图表来控制，不需要这里处理
  }, [indicatorVisibility]);

  const toggleIndicator = (key: keyof typeof indicatorVisibility) => {
    setIndicatorVisibility(prev => ({
      ...prev,
      [key]: !prev[key],
    }));
  };

  return (
    <div className="w-full rounded-lg p-4 shadow-xl" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-lg font-semibold" style={{ color: '#EAECEF' }}>{symbol} K线图</h3>
      </div>

      {/* 缩放提示 */}
      <div className="mb-2 text-xs" style={{ color: '#848E9C' }}>
        <span>💡 滚轮缩放 | </span>
        <span>Ctrl/Cmd + 滚轮：精细缩放 | </span>
        <span>Shift + 滚轮：水平平移</span>
      </div>

      {/* 指标控制面板 */}
      <div className="mb-3 flex flex-wrap items-center gap-3 text-xs">
        <span className="font-semibold" style={{ color: '#848E9C' }}>指标:</span>
        <label className="flex items-center gap-1 cursor-pointer">
          <input
            type="checkbox"
            checked={indicatorVisibility.ema20}
            onChange={() => toggleIndicator('ema20')}
            className="cursor-pointer"
            style={{ accentColor: '#F0B90B' }}
          />
          <span style={{ color: '#848E9C' }}>EMA20</span>
        </label>
        <label className="flex items-center gap-1 cursor-pointer">
          <input
            type="checkbox"
            checked={indicatorVisibility.ema50}
            onChange={() => toggleIndicator('ema50')}
            className="cursor-pointer"
            style={{ accentColor: '#3861FB' }}
          />
          <span style={{ color: '#848E9C' }}>EMA50</span>
        </label>
        <label className="flex items-center gap-1 cursor-pointer">
          <input
            type="checkbox"
            checked={indicatorVisibility.ema200}
            onChange={() => toggleIndicator('ema200')}
            className="cursor-pointer"
            style={{ accentColor: '#FF6B6B' }}
          />
          <span style={{ color: '#848E9C' }}>EMA200</span>
        </label>
        <label className="flex items-center gap-1 cursor-pointer">
          <input
            type="checkbox"
            checked={indicatorVisibility.bollinger}
            onChange={() => toggleIndicator('bollinger')}
            className="cursor-pointer"
            style={{ accentColor: '#848E9C' }}
          />
          <span style={{ color: '#848E9C' }}>布林带</span>
        </label>
        <label className="flex items-center gap-1 cursor-pointer">
          <input
            type="checkbox"
            checked={indicatorVisibility.volume}
            onChange={() => toggleIndicator('volume')}
            className="cursor-pointer"
            style={{ accentColor: '#848E9C' }}
          />
          <span style={{ color: '#848E9C' }}>成交量</span>
        </label>
        <label className="flex items-center gap-1 cursor-pointer">
          <input
            type="checkbox"
            checked={indicatorVisibility.macd}
            onChange={() => toggleIndicator('macd')}
            className="cursor-pointer"
            style={{ accentColor: '#0ECB81' }}
          />
          <span style={{ color: '#848E9C' }}>MACD</span>
        </label>
        <label className="flex items-center gap-1 cursor-pointer">
          <input
            type="checkbox"
            checked={indicatorVisibility.rsi}
            onChange={() => toggleIndicator('rsi')}
            className="cursor-pointer"
            style={{ accentColor: '#A371F7' }}
          />
          <span style={{ color: '#848E9C' }}>RSI</span>
        </label>
      </div>

      {/* 主图表 */}
      <div ref={mainChartRef} className="rounded mb-2" />

      {/* 成交量 */}
      <div className="mt-2" style={{ display: indicatorVisibility.volume ? 'block' : 'none' }}>
        <div className="text-xs font-semibold mb-1" style={{ color: '#848E9C' }}>成交量</div>
        <div ref={volumeChartRef} className="rounded" />
      </div>

      {/* MACD */}
      <div className="mt-2" style={{ display: indicatorVisibility.macd ? 'block' : 'none' }}>
        <div className="text-xs font-semibold mb-1" style={{ color: '#848E9C' }}>MACD</div>
        <div ref={macdChartRef} className="rounded" />
      </div>

      {/* RSI */}
      <div className="mt-2" style={{ display: indicatorVisibility.rsi ? 'block' : 'none' }}>
        <div className="text-xs font-semibold mb-1" style={{ color: '#848E9C' }}>RSI</div>
        <div ref={rsiChartRef} className="rounded" />
      </div>

      {/* 图例 */}
      <div className="mt-3 flex flex-wrap gap-3 text-xs" style={{ color: '#848E9C' }}>
        <div className="flex items-center gap-1">
          <div className="h-0.5 w-4" style={{ background: '#F0B90B' }}></div>
          <span>EMA20</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="h-0.5 w-4" style={{ background: '#3861FB' }}></div>
          <span>EMA50</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="h-0.5 w-4" style={{ background: '#FF6B6B' }}></div>
          <span>EMA200</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="h-0.5 w-4 border-b border-dashed" style={{ borderColor: '#848E9C' }}></div>
          <span>布林带</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="h-0.5 w-4" style={{ background: '#0ECB81' }}></div>
          <span>做多</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="h-0.5 w-4" style={{ background: '#F6465D' }}></div>
          <span>做空</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="h-0.5 w-4" style={{ background: '#3861FB' }}></div>
          <span>入场价</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="h-0.5 w-4 border-b-2 border-dashed" style={{ borderColor: '#F6465D' }}></div>
          <span>止损</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="h-0.5 w-4 border-b-2 border-dashed" style={{ borderColor: '#0ECB81' }}></div>
          <span>止盈</span>
        </div>
      </div>
    </div>
  );
};
