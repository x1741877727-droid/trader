import { useEffect, useState, useRef } from 'react';
import useSWR from 'swr';
import { api } from './lib/api';
import { EquityChart } from './components/EquityChart';
import { TradingChart } from './components/TradingChart';
import { AITradersPage } from './components/AITradersPage';
import { LoginPage } from './components/LoginPage';
import { RegisterPage } from './components/RegisterPage';
import { CompetitionPage } from './components/CompetitionPage';
import { LandingPage } from './pages/LandingPage';
import { BacktestPage } from './pages/BacktestPage';
import AILearning from './components/AILearning';
import { CloseReviewPanel } from './components/CloseReviewPanel';
import { TradeReviewPage } from './pages/TradeReviewPage';
import { LanguageProvider, useLanguage } from './contexts/LanguageContext';
import { AuthProvider, useAuth } from './contexts/AuthContext';
import { t, type Language } from './i18n/translations';
import { useSystemConfig } from './hooks/useSystemConfig';
import { Zap } from 'lucide-react';
import type {
  SystemStatus,
  AccountInfo,
  Position,
  PendingOrder,
  DecisionRecord,
  Statistics,
  TraderInfo,
} from './types';

type Page = 'competition' | 'traders' | 'trader' | 'chart' | 'review';

const formatOrderDuration = (minutes?: number, language: Language = 'en') => {
  if (minutes === undefined || minutes < 1) {
    return language === 'zh' ? '刚刚' : 'just now';
  }
  const total = Math.floor(minutes);
  if (total >= 60) {
    const hours = Math.floor(total / 60);
    const mins = total % 60;
    if (language === 'zh') {
      return mins > 0 ? `${hours}小时${mins}分钟` : `${hours}小时`;
    }
    return mins > 0 ? `${hours}h ${mins}m` : `${hours}h`;
  }
  return language === 'zh' ? `${total}分钟` : `${total}m`;
};

const truncateReasoning = (text?: string, maxLength = 160) => {
  if (!text) return '--';
  return text.length > maxLength ? `${text.slice(0, maxLength - 1)}…` : text;
};

// K线图表组件
function TradingChartSection({
  selectedSymbol,
  selectedInterval,
  setSelectedSymbol,
  setSelectedInterval,
  chartData,
  setChartData,
  chartLoading,
  setChartLoading,
  positions,
  trader,
}: {
  selectedSymbol: string;
  selectedInterval: string;
  setSelectedSymbol: (symbol: string) => void;
  setSelectedInterval: (interval: string) => void;
  chartData: any[];
  setChartData: (data: any[]) => void;
  chartLoading: boolean;
  setChartLoading: (loading: boolean) => void;
  positions: any[];
  trader: TraderInfo;
}) {
  // 获取候选币种列表
  const candidateSymbols = (trader.candidate_coins && trader.candidate_coins.length > 0)
    ? trader.candidate_coins
    : ['BTCUSDT', 'ETHUSDT', 'SOLUSDT'];

  // 默认币种
  const currentSymbol = selectedSymbol || (candidateSymbols ? candidateSymbols[0] : 'BTCUSDT');

  // 获取K线数据
  useEffect(() => {
    let isCancelled = false;

    const fetchData = async () => {
      if (!currentSymbol) return;

      setChartLoading(true);
      try {
        // 为了计算EMA200，需要至少200根K线
        // 5m 周期使用 300 根K线（约25小时），其他周期使用 250 根（确保有足够数据计算EMA200）
        const interval = selectedInterval || '4h';
        const limit = interval === '5m' ? 300 : 250;
        const data = await api.getKlines(currentSymbol, interval, limit);
        if (!isCancelled) {
          setChartData(data.klines);
        }
      } catch (error) {
        console.error('获取K线数据失败:', error);
      } finally {
        if (!isCancelled) {
          setChartLoading(false);
        }
      }
    };

    fetchData();

    // 每3分钟更新一次
    const interval = setInterval(fetchData, 3 * 60 * 1000);

    return () => {
      isCancelled = true;
      clearInterval(interval);
    };
  }, [currentSymbol, selectedInterval]);

  // 处理持仓数据
  const currentPositions = positions
    .filter((p: any) => p.symbol === currentSymbol)
    .map((p: any) => ({
      entryPrice: p.entry_price != null ? Number(p.entry_price) : undefined,
      stopLoss: p.stop_loss != null ? Number(p.stop_loss) : undefined,
      tp1: p.tp1 != null ? Number(p.tp1) : undefined,
      tp2: p.tp2 != null ? Number(p.tp2) : undefined,
      tp3: p.tp3 != null ? Number(p.tp3) : undefined,
      side: p.side?.toLowerCase() || 'long',
    }))
    .filter((p: any) => p.entryPrice != null); // 至少需要 entryPrice 才显示

  const intervals = ['5m', '15m', '1h', '4h', '1d'];

  return (
    <div className="binance-card p-4 mb-4 animate-slide-in">
      {/* 币种和周期选择器 */}
      <div className="flex items-center justify-between mb-4">
        {/* 币种选择 */}
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold" style={{ color: '#848E9C' }}>
            币种:
          </span>
          <div className="flex gap-2">
            {candidateSymbols &&
              candidateSymbols.map((symbol: string) => (
                <button
                  key={symbol}
                  onClick={() => setSelectedSymbol(symbol)}
                  className={`px-3 py-1 rounded text-xs font-semibold transition-all ${
                    currentSymbol === symbol ? 'scale-105' : 'opacity-60 hover:opacity-100'
                  }`}
                  style={
                    currentSymbol === symbol
                      ? { background: '#F0B90B', color: '#000' }
                      : { background: '#2B3139', color: '#EAECEF' }
                  }
                >
                  {symbol.replace('USDT', '')}
                </button>
              ))}
          </div>
        </div>

        {/* 周期选择 */}
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold" style={{ color: '#848E9C' }}>
            周期:
          </span>
          <div className="flex gap-2">
            {intervals.map((interval) => (
              <button
                key={interval}
                onClick={() => setSelectedInterval(interval)}
                className={`px-3 py-1 rounded text-xs font-semibold transition-all ${
                  selectedInterval === interval ? 'scale-105' : 'opacity-60 hover:opacity-100'
                }`}
                style={
                  selectedInterval === interval
                    ? { background: 'rgba(99, 102, 241, 0.2)', color: '#6366F1' }
                    : { background: '#2B3139', color: '#EAECEF' }
                }
              >
                {interval.toUpperCase()}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* 图表 */}
      {chartLoading ? (
        <div className="flex items-center justify-center h-96" style={{ color: '#848E9C' }}>
          <div className="text-center">
            <div
              className="w-8 h-8 border-4 border-t-transparent rounded-full animate-spin mx-auto mb-2"
              style={{ borderColor: '#F0B90B', borderTopColor: 'transparent' }}
            ></div>
            <div>加载中...</div>
          </div>
        </div>
      ) : chartData.length > 0 ? (
        <TradingChart
          symbol={currentSymbol}
          data={chartData}
          positions={currentPositions}
          height={450}
        />
      ) : (
        <div className="flex items-center justify-center h-96" style={{ color: '#848E9C' }}>
          暂无数据
        </div>
      )}
    </div>
  );
}

// Trader Chart Page - 专门用于展示当前选中 Trader 的 K 线图表页面
function TraderChartPage({
  selectedTrader,
  positions,
  language,
  selectedSymbol,
  setSelectedSymbol,
  selectedInterval,
  setSelectedInterval,
  chartData,
  setChartData,
  chartLoading,
  setChartLoading,
}: {
  selectedTrader?: TraderInfo;
  positions?: Position[];
  language: Language;
  selectedSymbol: string;
  setSelectedSymbol: (symbol: string) => void;
  selectedInterval: string;
  setSelectedInterval: (interval: string) => void;
  chartData: any[];
  setChartData: (data: any[]) => void;
  chartLoading: boolean;
  setChartLoading: (loading: boolean) => void;
}) {
  if (!selectedTrader) {
    return (
      <div className="space-y-6">
        <div className="binance-card p-6 animate-pulse">
          <div className="skeleton h-8 w-48 mb-3"></div>
          <div className="skeleton h-64 w-full"></div>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="mb-4 rounded p-6 animate-scale-in" style={{ background: 'linear-gradient(135deg, rgba(240, 185, 11, 0.12) 0%, rgba(252, 213, 53, 0.04) 100%)', border: '1px solid rgba(240, 185, 11, 0.2)' }}>
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <span className="w-10 h-10 rounded-full flex items-center justify-center text-xl" style={{ background: 'linear-gradient(135deg, #F0B90B 0%, #FCD535 100%)' }}>
              🤖
            </span>
            <div>
              <div className="text-xl font-bold" style={{ color: '#EAECEF' }}>
                {selectedTrader.trader_name}
              </div>
              <div className="text-xs mt-1" style={{ color: '#848E9C' }}>
                {t('tradingPanel', language)} · {selectedTrader.trader_id}
              </div>
            </div>
          </div>
        </div>
      </div>

      <TradingChartSection
        selectedSymbol={selectedSymbol}
        selectedInterval={selectedInterval}
        setSelectedSymbol={setSelectedSymbol}
        setSelectedInterval={setSelectedInterval}
        chartData={chartData}
        setChartData={setChartData}
        chartLoading={chartLoading}
        setChartLoading={setChartLoading}
        positions={positions || []}
        trader={selectedTrader}
      />
    </div>
  );
}

// 获取友好的AI模型名称
function getModelDisplayName(modelId: string): string {
  switch (modelId.toLowerCase()) {
    case 'deepseek':
      return 'DeepSeek';
    case 'qwen':
      return 'Qwen';
    case 'claude':
      return 'Claude';
    default:
      return modelId.toUpperCase();
  }
}

function App() {
  const { language, setLanguage } = useLanguage();
  const { user, token, logout, isLoading } = useAuth();
  const { config: systemConfig, loading: configLoading } = useSystemConfig();
  const [route, setRoute] = useState(window.location.pathname);

  // 从URL hash读取初始页面状态（支持刷新保持页面）
  const getInitialPage = (): Page => {
    const hash = window.location.hash.slice(1); // 去掉 #
    return hash === 'trader' || hash === 'details' ? 'trader' : 'competition';
  };

  const [currentPage, setCurrentPage] = useState<Page>(getInitialPage());
  const [selectedTraderId, setSelectedTraderId] = useState<string | undefined>();
  const [selectedTradeId, setSelectedTradeId] = useState<string | undefined>();
  const [lastUpdate, setLastUpdate] = useState<string>('--:--:--');
  
  // K线图表相关状态（仅在 trader 页面使用）
  const [selectedSymbol, setSelectedSymbol] = useState<string>('');
  const [selectedInterval, setSelectedInterval] = useState<string>('4h');
  const [chartData, setChartData] = useState<any[]>([]);
  const [chartLoading, setChartLoading] = useState(false);

  // 监听URL hash变化，同步页面状态
  useEffect(() => {
    const handleHashChange = () => {
      const hash = window.location.hash.slice(1);
      if (hash === 'trader' || hash === 'details') {
        setCurrentPage('trader');
      } else if (hash === 'competition' || hash === '') {
        setCurrentPage('competition');
      }
    };

    window.addEventListener('hashchange', handleHashChange);
    return () => window.removeEventListener('hashchange', handleHashChange);
  }, []);

  // 切换页面时更新URL hash (当前通过按钮直接调用setCurrentPage，这个函数暂时保留用于未来扩展)
  // const navigateToPage = (page: Page) => {
  //   setCurrentPage(page);
  //   window.location.hash = page === 'competition' ? '' : 'trader';
  // };

  // 获取trader列表
  const { data: traders } = useSWR<TraderInfo[]>('traders', api.getTraders, {
    refreshInterval: 10000,
  });

  // 当获取到traders后，设置默认选中第一个
  useEffect(() => {
    if (traders && traders.length > 0 && !selectedTraderId) {
      setSelectedTraderId(traders[0].trader_id);
    }
  }, [traders, selectedTraderId]);

  const isTraderOrChartPage = currentPage === 'trader' || currentPage === 'chart';

  // 如果在 trader 或 chart 页面，获取该 trader 的数据
  const { data: status } = useSWR<SystemStatus>(
    isTraderOrChartPage && selectedTraderId
      ? `status-${selectedTraderId}`
      : null,
    () => api.getStatus(selectedTraderId),
    {
      refreshInterval: 15000, // 15秒刷新（配合后端15秒缓存）
      revalidateOnFocus: false, // 禁用聚焦时重新验证，减少请求
      dedupingInterval: 10000, // 10秒去重，防止短时间内重复请求
    }
  );

  const { data: account } = useSWR<AccountInfo>(
    isTraderOrChartPage && selectedTraderId
      ? `account-${selectedTraderId}`
      : null,
    () => api.getAccount(selectedTraderId),
    {
      refreshInterval: 15000, // 15秒刷新（配合后端15秒缓存）
      revalidateOnFocus: false, // 禁用聚焦时重新验证，减少请求
      dedupingInterval: 10000, // 10秒去重，防止短时间内重复请求
    }
  );

  const { data: positions } = useSWR<Position[]>(
    isTraderOrChartPage && selectedTraderId
      ? `positions-${selectedTraderId}`
      : null,
    () => api.getPositions(selectedTraderId),
    {
      refreshInterval: 15000, // 15秒刷新（配合后端15秒缓存）
      revalidateOnFocus: false, // 禁用聚焦时重新验证，减少请求
      dedupingInterval: 10000, // 10秒去重，防止短时间内重复请求
    }
  );

  const { data: pendingOrders } = useSWR<PendingOrder[]>(
    isTraderOrChartPage && selectedTraderId
      ? `pending-orders-${selectedTraderId}`
      : null,
    () => api.getPendingOrders(selectedTraderId),
    {
      refreshInterval: 15000,
      revalidateOnFocus: false,
      dedupingInterval: 10000,
    }
  );

  const { data: decisions } = useSWR<DecisionRecord[]>(
    isTraderOrChartPage && selectedTraderId
      ? `decisions/latest-${selectedTraderId}`
      : null,
    () => api.getLatestDecisions(selectedTraderId),
    {
      refreshInterval: 30000, // 30秒刷新（决策更新频率较低）
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  );

  const { data: stats } = useSWR<Statistics>(
    isTraderOrChartPage && selectedTraderId
      ? `statistics-${selectedTraderId}`
      : null,
    () => api.getStatistics(selectedTraderId),
    {
      refreshInterval: 30000, // 30秒刷新（统计数据更新频率较低）
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  );

  useEffect(() => {
    if (account) {
      const now = new Date().toLocaleTimeString();
      setLastUpdate(now);
    }
  }, [account]);

  const selectedTrader = traders?.find((t) => t.trader_id === selectedTraderId);

  // Handle routing
  useEffect(() => {
    const handlePopState = () => {
      setRoute(window.location.pathname);
    };
    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, []);

  // Show loading spinner while checking auth or config
  if (isLoading || configLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center" style={{ background: '#0B0E11' }}>
        <div className="text-center">
          <img src="/images/logo.png" alt="NoFx Logo" className="w-16 h-16 mx-auto mb-4 animate-pulse" />
          <p style={{ color: '#EAECEF' }}>{t('loading', language)}</p>
        </div>
      </div>
    );
  }

  // Show landing page for root route when not authenticated
  if (!systemConfig?.admin_mode && (!user || !token)) {
    if (route === '/login') {
      return <LoginPage />;
    }
    if (route === '/register') {
      return <RegisterPage />;
    }
    // Default to landing page when not authenticated
    return <LandingPage />;
  }

  // Backtest page route（简单挂在 /backtest，主要给你自己测试用）
  if (route === '/backtest') {
    return (
      <div className="min-h-screen" style={{ background: '#0B0E11', color: '#EAECEF' }}>
        <div className="max-w-6xl mx-auto px-6 py-6">
          <BacktestPage />
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen" style={{ background: '#0B0E11', color: '#EAECEF' }}>
      {/* Header - Binance Style */}
      <header className="glass sticky top-0 z-50 backdrop-blur-xl">
        <div className="max-w-[1920px] mx-auto px-6 py-4">
          <div className="relative flex items-center">
            {/* Left - Logo and Title */}
            <div className="flex items-center gap-3">
              <div className="w-8 h-8 flex items-center justify-center">
                <img src="/icons/nofx.svg?v=2" alt="NOFX" className="w-8 h-8" />
              </div>
              <div>
                <h1 className="text-xl font-bold" style={{ color: '#EAECEF' }}>
                  {t('appTitle', language)}
                </h1>
                <p className="text-xs mono" style={{ color: '#848E9C' }}>
                  {t('subtitle', language)}
                </p>
              </div>
            </div>
            
            {/* Center - Page Toggle (absolutely positioned) */}
            <div className="absolute left-1/2 transform -translate-x-1/2 flex gap-1 rounded p-1" style={{ background: '#1E2329' }}>
              <button
                onClick={() => setCurrentPage('competition')}
                className={`px-3 py-2 rounded text-sm font-semibold transition-all`}
                style={currentPage === 'competition'
                  ? { background: '#F0B90B', color: '#000' }
                  : { background: 'transparent', color: '#848E9C' }
                }
              >
                {t('aiCompetition', language)}
              </button>
              <button
                onClick={() => setCurrentPage('traders')}
                className={`px-3 py-2 rounded text-sm font-semibold transition-all`}
                style={currentPage === 'traders'
                  ? { background: '#F0B90B', color: '#000' }
                  : { background: 'transparent', color: '#848E9C' }
                }
              >
                {t('aiTraders', language)}
              </button>
              <button
                onClick={() => setCurrentPage('trader')}
                className={`px-3 py-2 rounded text-sm font-semibold transition-all`}
                style={currentPage === 'trader'
                  ? { background: '#F0B90B', color: '#000' }
                  : { background: 'transparent', color: '#848E9C' }
                }
              >
                {t('tradingPanel', language)}
              </button>
              <button
                onClick={() => setCurrentPage('chart')}
                className={`px-3 py-2 rounded text-sm font-semibold transition-all`}
                style={currentPage === 'chart'
                  ? { background: '#F0B90B', color: '#000' }
                  : { background: 'transparent', color: '#848E9C' }
                }
              >
                📊 图表
              </button>
              <button
                onClick={() => {
                  // 简单方式：直接跳转到 /backtest 路由
                  window.history.pushState({}, '', '/backtest');
                  setRoute('/backtest');
                }}
                className="px-3 py-2 rounded text-sm font-semibold transition-all"
                style={route === '/backtest'
                  ? { background: '#F0B90B', color: '#000' }
                  : { background: 'transparent', color: '#848E9C' }
                }
              >
                回测
              </button>
            </div>
            
            {/* Right - Actions */}
            <div className="ml-auto flex items-center gap-3">

              {/* User Info - Only show if not in admin mode */}
              {!systemConfig?.admin_mode && user && (
                <div className="flex items-center gap-2 px-3 py-2 rounded" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
                  <div className="w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold" style={{ background: '#F0B90B', color: '#000' }}>
                    {user.email[0].toUpperCase()}
                  </div>
                  <span className="text-sm" style={{ color: '#EAECEF' }}>{user.email}</span>
                </div>
              )}
              
              {/* Admin Mode Indicator */}
              {systemConfig?.admin_mode && (
                <div className="flex items-center gap-2 px-3 py-2 rounded" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
                  <Zap className="w-4 h-4" style={{ color: '#F0B90B' }} />
                  <span className="text-sm font-semibold" style={{ color: '#F0B90B' }}>{t('adminMode', language)}</span>
                </div>
              )}

              {/* Language Toggle */}
              <div className="flex gap-1 rounded p-1" style={{ background: '#1E2329' }}>
                <button
                  onClick={() => setLanguage('zh')}
                  className="px-3 py-1.5 rounded text-xs font-semibold transition-all"
                  style={language === 'zh'
                    ? { background: '#F0B90B', color: '#000' }
                    : { background: 'transparent', color: '#848E9C' }
                  }
                >
                  中文
                </button>
                <button
                  onClick={() => setLanguage('en')}
                  className="px-3 py-1.5 rounded text-xs font-semibold transition-all"
                  style={language === 'en'
                    ? { background: '#F0B90B', color: '#000' }
                    : { background: 'transparent', color: '#848E9C' }
                  }
                >
                  EN
                </button>
              </div>

              {/* Logout Button - Only show if not in admin mode */}
              {!systemConfig?.admin_mode && (
                <button
                  onClick={logout}
                  className="px-3 py-2 rounded text-sm font-semibold transition-all hover:scale-105"
                  style={{ background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D', border: '1px solid rgba(246, 70, 93, 0.2)' }}
                >
                  {t('logout', language)}
                </button>
              )}
            </div>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main className="max-w-[1920px] mx-auto px-6 py-6">
        {currentPage === 'review' && selectedTradeId && selectedTraderId ? (
          <TradeReviewPage
            tradeId={selectedTradeId}
            traderId={selectedTraderId}
            onBack={() => {
              setCurrentPage('trader');
              setSelectedTradeId(undefined);
            }}
          />
        ) : currentPage === 'competition' ? (
          <CompetitionPage />
        ) : currentPage === 'traders' ? (
          <AITradersPage 
            onTraderSelect={(traderId) => {
              setSelectedTraderId(traderId);
              setCurrentPage('trader');
            }}
          />
        ) : currentPage === 'chart' ? (
          <TraderChartPage
            selectedTrader={selectedTrader}
            positions={positions}
            language={language}
            selectedSymbol={selectedSymbol}
            setSelectedSymbol={setSelectedSymbol}
            selectedInterval={selectedInterval}
            setSelectedInterval={setSelectedInterval}
            chartData={chartData}
            setChartData={setChartData}
            chartLoading={chartLoading}
            setChartLoading={setChartLoading}
          />
        ) : (
          <TraderDetailsPage
            selectedTrader={selectedTrader}
            status={status}
            account={account}
            positions={positions}
            pendingOrders={pendingOrders}
            decisions={decisions}
            stats={stats}
            lastUpdate={lastUpdate}
            language={language}
            traders={traders}
            selectedTraderId={selectedTraderId}
            onTraderSelect={setSelectedTraderId}
            setSelectedTradeId={setSelectedTradeId}
            setCurrentPage={setCurrentPage}
          />
        )}
      </main>

      {/* Footer */}
      <footer className="mt-16" style={{ borderTop: '1px solid #2B3139', background: '#181A20' }}>
        <div className="max-w-[1920px] mx-auto px-6 py-6 text-center text-sm" style={{ color: '#5E6673' }}>
          <p>{t('footerTitle', language)}</p>
          <p className="mt-1">{t('footerWarning', language)}</p>
          <div className="mt-4">
            <a
              href="https://github.com/tinkle-community/nofx"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-3 py-2 rounded text-sm font-semibold transition-all hover:scale-105"
              style={{ background: '#1E2329', color: '#848E9C', border: '1px solid #2B3139' }}
              onMouseEnter={(e) => {
                e.currentTarget.style.background = '#2B3139';
                e.currentTarget.style.color = '#EAECEF';
                e.currentTarget.style.borderColor = '#F0B90B';
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.background = '#1E2329';
                e.currentTarget.style.color = '#848E9C';
                e.currentTarget.style.borderColor = '#2B3139';
              }}
            >
              <svg width="18" height="18" viewBox="0 0 16 16" fill="currentColor">
                <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/>
              </svg>
              GitHub
            </a>
          </div>
        </div>
      </footer>
    </div>
  );
}

// Trader Details Page Component
function TraderDetailsPage({
  selectedTrader,
  status,
  account,
  positions,
  pendingOrders,
  decisions,
  lastUpdate,
  language,
  traders,
  selectedTraderId,
  onTraderSelect,
  setSelectedTradeId,
  setCurrentPage,
}: {
  selectedTrader?: TraderInfo;
  traders?: TraderInfo[];
  selectedTraderId?: string;
  onTraderSelect: (traderId: string) => void;
  status?: SystemStatus;
  account?: AccountInfo;
  positions?: Position[];
  pendingOrders?: PendingOrder[];
  decisions?: DecisionRecord[];
  stats?: Statistics;
  lastUpdate: string;
  language: Language;
  setSelectedTradeId: (tradeId: string) => void;
  setCurrentPage: (page: Page) => void;
}) {
  const [showAiDrawer, setShowAiDrawer] = useState(false);
  const latestDecision = decisions && decisions.length > 0 ? decisions[0] : undefined;
  
  // 实时思考流状态（用于"最近决策"区域）
  const [currentStreamingCycle, setCurrentStreamingCycle] = useState<number | null>(null); // 当前正在思考的周期号
  const [streamingContent, setStreamingContent] = useState<string>(''); // 当前实时思考内容
  const [isStreaming, setIsStreaming] = useState(false);
  const streamEventSourceRef = useRef<EventSource | null>(null);
  // 保存每个周期的实时思考流内容（cycle_number -> content）
  const [streamingHistory, setStreamingHistory] = useState<Map<number, string>>(new Map());

  // 自动建立 SSE 连接（当有选中的 trader 时）
  useEffect(() => {
    if (selectedTraderId) {
      // 如果已经有连接，先关闭
      if (streamEventSourceRef.current) {
        streamEventSourceRef.current.close();
        streamEventSourceRef.current = null;
      }

      // 计算下一个周期号（使用当前的 decisions 值）
      const currentDecisions = decisions || [];
      const nextCycle = currentDecisions.length > 0 ? currentDecisions[0].cycle_number + 1 : 1;
      console.log('建立 SSE 连接，预期周期号:', nextCycle, '当前决策数:', currentDecisions.length);
      setCurrentStreamingCycle(nextCycle);
      setStreamingContent('');
      setIsStreaming(true);
      // 清空历史记录（新连接时清空）
      setStreamingHistory(new Map());

      const eventSource = api.createAIStream(
        selectedTraderId,
        (data) => {
          console.log('收到 SSE 消息:', data);
          if (data.type === 'partial_cot') {
            setStreamingContent((prev: string) => {
              const newContent = prev + data.data;
              // 同时保存到历史记录中
              if (currentStreamingCycle) {
                setStreamingHistory(prevMap => {
                  const newMap = new Map(prevMap);
                  newMap.set(currentStreamingCycle!, newContent);
                  return newMap;
                });
              }
              return newContent;
            });
          } else if (data.type === 'connected') {
            const connectedMsg = '已连接到 AI 思考流，等待下一次决策...\n\n';
            setStreamingContent(connectedMsg);
            if (currentStreamingCycle) {
              setStreamingHistory(prevMap => {
                const newMap = new Map(prevMap);
                newMap.set(currentStreamingCycle!, connectedMsg);
                return newMap;
              });
            }
          } else if (data.type === 'error') {
            const errorMsg = `\n\n❌ 错误: ${data.message || '未知错误'}\n`;
            setStreamingContent((prev: string) => prev + errorMsg);
            setIsStreaming(false);
          } else if (data.type === 'closed') {
            setIsStreaming(false);
          }
        },
        (error) => {
          console.error('SSE 错误:', error);
          setIsStreaming(false);
        }
      );
      streamEventSourceRef.current = eventSource;

      // 清理函数
      return () => {
        if (streamEventSourceRef.current) {
          streamEventSourceRef.current.close();
          streamEventSourceRef.current = null;
        }
        setIsStreaming(false);
        setStreamingContent('');
        setCurrentStreamingCycle(null);
      };
    }
  }, [selectedTraderId]); // 只在 trader 切换时建立连接

  // 检测周期重置：如果最新的决策周期号小于我们记录的周期号，说明周期重置了
  useEffect(() => {
    if (decisions && decisions.length > 0 && currentStreamingCycle) {
      const latest = decisions[0];
      // 如果最新的周期号小于我们记录的周期号，说明周期重置了
      if (latest.cycle_number < currentStreamingCycle) {
        console.log('检测到周期重置：最新周期', latest.cycle_number, '小于记录的周期', currentStreamingCycle, '，重置前端状态');
        // 清空实时流历史记录
        setStreamingHistory(new Map());
        // 重置为下一个周期号
        const nextCycle = latest.cycle_number + 1;
        setCurrentStreamingCycle(nextCycle);
        setStreamingContent('');
        setIsStreaming(true);
        return;
      }
    }
  }, [decisions, currentStreamingCycle]);

  // 当收到新决策时，保存实时流内容并准备下一个周期
  useEffect(() => {
    if (decisions && decisions.length > 0 && currentStreamingCycle) {
      const latest = decisions[0];
      // 检查是否是新周期（通过比较周期号）
      if (latest.cycle_number >= currentStreamingCycle) {
        // 保存当前的实时流内容到历史记录（如果有的话）
        if (streamingContent) {
          setStreamingHistory(prevMap => {
            const newMap = new Map(prevMap);
            newMap.set(latest.cycle_number, streamingContent);
            return newMap;
          });
        }
        
        // 立即准备下一个周期（不清除SSE连接，只更新状态）
        const nextCycle = latest.cycle_number + 1;
        setCurrentStreamingCycle(nextCycle);
        setStreamingContent(''); // 清空当前内容，准备接收下一个周期的数据
        setIsStreaming(true); // 标记为正在思考，准备接收下一个周期
        console.log('周期', latest.cycle_number, '完成，准备接收下一个周期的实时思考流，周期号:', nextCycle);
        
        // 3秒后隐藏实时流卡片（但保持SSE连接和状态，继续接收下一个周期的数据）
        const timer = setTimeout(() => {
          // 这里不清除 currentStreamingCycle，保持连接继续接收下一个周期的数据
          // 只是暂时隐藏实时流卡片，当新数据到来时会自动显示
        }, 3000);
        return () => clearTimeout(timer);
      }
    }
  }, [decisions, currentStreamingCycle, streamingContent]); // 依赖 decisions、currentStreamingCycle 和 streamingContent

  if (!selectedTrader) {
    return (
      <div className="space-y-6">
        {/* Loading Skeleton - Binance Style */}
        <div className="binance-card p-6 animate-pulse">
          <div className="skeleton h-8 w-48 mb-3"></div>
          <div className="flex gap-4">
            <div className="skeleton h-4 w-32"></div>
            <div className="skeleton h-4 w-24"></div>
            <div className="skeleton h-4 w-28"></div>
          </div>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="binance-card p-5 animate-pulse">
              <div className="skeleton h-4 w-24 mb-3"></div>
              <div className="skeleton h-8 w-32"></div>
            </div>
          ))}
        </div>
        <div className="binance-card p-6 animate-pulse">
          <div className="skeleton h-6 w-40 mb-4"></div>
          <div className="skeleton h-64 w-full"></div>
        </div>
      </div>
    );
  }

  return (
    <div>
      {/* Trader Header */}
      <div className="mb-6 rounded p-6 animate-scale-in" style={{ background: 'linear-gradient(135deg, rgba(240, 185, 11, 0.15) 0%, rgba(252, 213, 53, 0.05) 100%)', border: '1px solid rgba(240, 185, 11, 0.2)', boxShadow: '0 0 30px rgba(240, 185, 11, 0.15)' }}>
        <div className="flex items-start justify-between mb-3">
          <h2 className="text-2xl font-bold flex items-center gap-2" style={{ color: '#EAECEF' }}>
            <span className="w-10 h-10 rounded-full flex items-center justify-center text-xl" style={{ background: 'linear-gradient(135deg, #F0B90B 0%, #FCD535 100%)' }}>
              🤖
            </span>
            {selectedTrader.trader_name}
          </h2>
          
          {/* Trader Selector */}
          {traders && traders.length > 0 && (
            <div className="flex items-center gap-2">
              <span className="text-sm" style={{ color: '#848E9C' }}>{t('switchTrader', language)}:</span>
              <select
                value={selectedTraderId}
                onChange={(e) => onTraderSelect(e.target.value)}
                className="rounded px-3 py-2 text-sm font-medium cursor-pointer transition-colors"
                style={{ background: '#1E2329', border: '1px solid #2B3139', color: '#EAECEF' }}
              >
                {traders.map((trader) => (
                  <option key={trader.trader_id} value={trader.trader_id}>
                    {trader.trader_name}
                  </option>
                ))}
              </select>
            </div>
          )}
        </div>
        <div className="flex items-center gap-4 text-sm" style={{ color: '#848E9C' }}>
          <span>AI Model: <span className="font-semibold" style={{ color: selectedTrader.ai_model.includes('qwen') ? '#c084fc' : '#60a5fa' }}>{getModelDisplayName(selectedTrader.ai_model.split('_').pop() || selectedTrader.ai_model)}</span></span>
          {status && (
            <>
              <span>•</span>
              <span>Cycles: {status.call_count}</span>
              <span>•</span>
              <span>Runtime: {status.runtime_minutes} min</span>
            </>
          )}
          <button
            onClick={() => setShowAiDrawer(true)}
            className="ml-auto px-3 py-1 rounded text-xs font-bold hover:opacity-90 transition-all"
            style={{ background: 'rgba(99, 102, 241, 0.15)', border: '1px solid rgba(99, 102, 241, 0.3)', color: '#c084fc' }}
          >
            AI 思维链 / 调用详情
          </button>
        </div>
      </div>

      {/* Debug Info */}
      {account && (
        <div className="mb-4 p-3 rounded text-xs font-mono" style={{ background: '#1E2329', border: '1px solid #2B3139' }}>
          <div style={{ color: '#848E9C' }}>
            🔄 Last Update: {lastUpdate} | Total Equity: {account?.total_equity?.toFixed(2) || '0.00'} |
            Available: {account?.available_balance?.toFixed(2) || '0.00'} | P&L: {account?.total_pnl?.toFixed(2) || '0.00'}{' '}
            ({account?.total_pnl_pct?.toFixed(2) || '0.00'}%)
          </div>
        </div>
      )}

      {/* Account Overview */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-8">
        <StatCard
          title={t('totalEquity', language)}
          value={`${account?.total_equity?.toFixed(2) || '0.00'} USDT`}
          change={account?.total_pnl_pct || 0}
          positive={(account?.total_pnl ?? 0) > 0}
        />
        <StatCard
          title={t('availableBalance', language)}
          value={`${account?.available_balance?.toFixed(2) || '0.00'} USDT`}
          subtitle={`${(account?.available_balance && account?.total_equity ? ((account.available_balance / account.total_equity) * 100).toFixed(1) : '0.0')}% ${t('free', language)}`}
        />
        <StatCard
          title={t('totalPnL', language)}
          value={`${account?.total_pnl !== undefined && account.total_pnl >= 0 ? '+' : ''}${account?.total_pnl?.toFixed(2) || '0.00'} USDT`}
          change={account?.total_pnl_pct || 0}
          positive={(account?.total_pnl ?? 0) >= 0}
        />
        <StatCard
          title={t('positions', language)}
          value={`${account?.position_count || 0}`}
          subtitle={`${t('margin', language)}: ${account?.margin_used_pct?.toFixed(1) || '0.0'}%`}
        />
      </div>

      {/* 主要内容区：左右分屏 */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-6">
        {/* 左侧：净值曲线 + 持仓 */}
        <div className="space-y-6">
          {/* Equity Chart */}
          <div className="animate-slide-in" style={{ animationDelay: '0.1s' }}>
            <EquityChart traderId={selectedTrader.trader_id} />
          </div>

          {/* Current Positions */}
          <div className="binance-card p-6 animate-slide-in" style={{ animationDelay: '0.15s' }}>
        <div className="flex items-center justify-between mb-5">
          <h2 className="text-xl font-bold flex items-center gap-2" style={{ color: '#EAECEF' }}>
            📈 {t('currentPositions', language)}
          </h2>
          <div className="flex items-center gap-2">
          {positions && positions.length > 0 && (
            <div className="text-xs px-3 py-1 rounded" style={{ background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B', border: '1px solid rgba(240, 185, 11, 0.2)' }}>
              {positions.length} {t('active', language)}
            </div>
          )}
            {pendingOrders && pendingOrders.length > 0 && (
              <div className="text-xs px-3 py-1 rounded" style={{ background: 'rgba(99, 102, 241, 0.12)', color: '#A5B4FC', border: '1px solid rgba(99, 102, 241, 0.3)' }}>
                {pendingOrders.length} {t('waiting', language)}
              </div>
            )}
          </div>
        </div>
        {positions && positions.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left border-b border-gray-800">
                <tr>
                  <th className="pb-3 font-semibold text-gray-400">{t('symbol', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('side', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('entryPrice', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('markPrice', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('quantity', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('positionValue', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('leverage', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('unrealizedPnL', language)}</th>
                  <th className="pb-3 font-semibold text-gray-400">{t('liqPrice', language)}</th>
                </tr>
              </thead>
              <tbody>
                {positions.map((pos, i) => (
                  <tr key={i} className="border-b border-gray-800 last:border-0">
                    <td className="py-3 font-mono font-semibold">{pos.symbol}</td>
                    <td className="py-3">
                      <span
                        className="px-2 py-1 rounded text-xs font-bold"
                        style={pos.side === 'long'
                          ? { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81' }
                          : { background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D' }
                        }
                      >
                        {t(pos.side === 'long' ? 'long' : 'short', language)}
                      </span>
                    </td>
                    <td className="py-3 font-mono" style={{ color: '#EAECEF' }}>{(pos.entry_price ?? 0).toFixed(4)}</td>
                    <td className="py-3 font-mono" style={{ color: '#EAECEF' }}>{(pos.mark_price ?? 0).toFixed(4)}</td>
                    <td className="py-3 font-mono" style={{ color: '#EAECEF' }}>{(pos.quantity ?? 0).toFixed(4)}</td>
                    <td className="py-3 font-mono font-bold" style={{ color: '#EAECEF' }}>
                      {((pos.quantity ?? 0) * (pos.mark_price ?? 0)).toFixed(2)} USDT
                    </td>
                    <td className="py-3 font-mono" style={{ color: '#F0B90B' }}>{pos.leverage ?? 0}x</td>
                    <td className="py-3 font-mono">
                      <span
                        style={{ color: (pos.unrealized_pnl ?? 0) >= 0 ? '#0ECB81' : '#F6465D', fontWeight: 'bold' }}
                      >
                        {(pos.unrealized_pnl ?? 0) >= 0 ? '+' : ''}
                        {(pos.unrealized_pnl ?? 0).toFixed(2)} ({(pos.unrealized_pnl_pct ?? 0).toFixed(2)}%)
                      </span>
                    </td>
                    <td className="py-3 font-mono" style={{ color: '#848E9C' }}>
                      {(pos.liquidation_price ?? 0).toFixed(4)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="text-center py-16" style={{ color: '#848E9C' }}>
            <div className="text-6xl mb-4 opacity-50">📊</div>
            <div className="text-lg font-semibold mb-2">{t('noPositions', language)}</div>
            <div className="text-sm">{t('noActivePositions', language)}</div>
          </div>
        )}
        {pendingOrders && pendingOrders.length > 0 && (
          <div className="mt-8 pt-5 border-t" style={{ borderColor: '#2B3139' }}>
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-lg font-semibold flex items-center gap-2" style={{ color: '#EAECEF' }}>
                📌 {t('pendingLimitOrders', language)}
              </h3>
              <div className="text-xs px-3 py-1 rounded" style={{ background: 'rgba(165, 180, 252, 0.15)', color: '#C4B5FD', border: '1px solid rgba(165, 180, 252, 0.3)' }}>
                {pendingOrders.length} {t('waiting', language)}
              </div>
            </div>
            <div className="space-y-4">
              {pendingOrders.map((order) => (
                <div key={order.order_id} className="rounded-2xl p-4" style={{ background: '#151A1F', border: '1px solid #2B3139' }}>
                  <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
                    <div className="flex items-center gap-3">
                      <span className="font-mono font-semibold text-lg" style={{ color: '#EAECEF' }}>{order.symbol}</span>
                      <span
                        className="px-2 py-0.5 rounded text-xs font-bold"
                        style={order.side === 'long'
                          ? { background: 'rgba(14, 203, 129, 0.12)', color: '#0ECB81' }
                          : { background: 'rgba(246, 70, 93, 0.12)', color: '#F6465D' }}
                      >
                        {t(order.side === 'long' ? 'long' : 'short', language)}
                      </span>
                      <span className="text-xs px-2 py-0.5 rounded-full font-semibold" style={{ background: 'rgba(99, 102, 241, 0.15)', color: '#A5B4FC' }}>
                        LIMIT #{order.order_id}
                      </span>
                    </div>
                    <div className="text-xs font-mono" style={{ color: '#94A3B8' }}>
                      {t('age', language)}: {formatOrderDuration(order.duration_min, language)}
                    </div>
                  </div>
                  <div className="grid grid-cols-1 md:grid-cols-3 gap-3 text-sm">
                    <div style={{ color: '#94A3B8' }}>
                      {t('limitPrice', language)}:{' '}
                      <span className="font-mono text-base" style={{ color: '#EAECEF' }}>
                        {(order.limit_price ?? 0).toFixed(4)}
                      </span>
                    </div>
                    <div style={{ color: '#94A3B8' }}>
                      {t('quantity', language)}:{' '}
                      <span className="font-mono text-base" style={{ color: '#EAECEF' }}>
                        {(order.quantity ?? 0).toFixed(4)}
                      </span>
                    </div>
                    <div style={{ color: '#94A3B8' }}>
                      {t('leverage', language)}:{' '}
                      <span className="font-mono text-base" style={{ color: '#F0B90B' }}>
                        {order.leverage ?? 0}x
                      </span>
                    </div>
                    <div style={{ color: '#94A3B8' }}>
                      {t('stopLoss', language)}:{' '}
                      <span className="font-mono" style={{ color: '#EAECEF' }}>
                        {(order.stop_loss ?? 0).toFixed(4)}
                      </span>
                    </div>
                    <div style={{ color: '#94A3B8' }}>
                      TP3:{' '}
                      <span className="font-mono" style={{ color: '#EAECEF' }}>
                        {(order.tp3 ?? 0).toFixed(4)}
                      </span>
                    </div>
                    <div style={{ color: '#94A3B8' }}>
                      {t('confidenceScore', language)}:{' '}
                      <span className="font-mono" style={{ color: '#EAECEF' }}>
                        {order.confidence ?? 0}
                      </span>
                    </div>
                  </div>
                  <div className="mt-3 text-xs leading-relaxed" style={{ color: '#94A3B8' }}>
                    {t('reasoningLabel', language)}:{' '}
                    <span style={{ color: '#EAECEF' }}>{truncateReasoning(order.reasoning)}</span>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
          </div>
        </div>
        {/* 左侧结束 */}

        {/* 右侧：Recent Decisions - 卡片容器 */}
        <div className="binance-card p-6 animate-slide-in h-fit lg:sticky lg:top-24 lg:max-h-[calc(100vh-120px)]" style={{ animationDelay: '0.2s' }}>
          {/* 标题 */}
          <div className="flex items-center gap-3 mb-5 pb-4 border-b" style={{ borderColor: '#2B3139' }}>
            <div className="w-10 h-10 rounded-xl flex items-center justify-center text-xl" style={{
              background: 'linear-gradient(135deg, #6366F1 0%, #8B5CF6 100%)',
              boxShadow: '0 4px 14px rgba(99, 102, 241, 0.4)'
            }}>
              🧠
            </div>
            <div>
              <h2 className="text-xl font-bold" style={{ color: '#EAECEF' }}>{t('recentDecisions', language)}</h2>
              {decisions && decisions.length > 0 && (
                <div className="text-xs" style={{ color: '#848E9C' }}>
                  {t('lastCycles', language, { count: decisions.length })}
                </div>
              )}
            </div>
          </div>

          {/* 决策列表 - 可滚动 */}
          <div className="space-y-4 overflow-y-auto pr-2" style={{ maxHeight: 'calc(100vh - 280px)' }}>
            {/* 实时思考流卡片（如果有正在进行的思考） */}
            {isStreaming && currentStreamingCycle && (
              <StreamingDecisionCard
                cycleNumber={currentStreamingCycle}
                content={streamingContent}
                language={language}
                isCompleted={false}
              />
            )}
            
            {/* 历史决策列表 */}
            {decisions && decisions.length > 0 ? (
              decisions.map((decision, i) => (
                <DecisionCard 
                  key={i} 
                  decision={decision} 
                  language={language}
                  streamingContent={streamingHistory.get(decision.cycle_number)}
                />
              ))
            ) : !isStreaming ? (
              <div className="py-16 text-center">
                <div className="text-6xl mb-4 opacity-30">🧠</div>
                <div className="text-lg font-semibold mb-2" style={{ color: '#EAECEF' }}>{t('noDecisionsYet', language)}</div>
                <div className="text-sm" style={{ color: '#848E9C' }}>{t('aiDecisionsWillAppear', language)}</div>
              </div>
            ) : null}
          </div>
        </div>
        {/* 右侧结束 */}
      </div>

      {/* AI Learning & Performance Analysis */}
      <div className="mb-6 animate-slide-in" style={{ animationDelay: '0.25s' }}>
        <CloseReviewPanel 
          traderId={selectedTrader.trader_id}
          onTradeSelect={(tradeId) => {
            setSelectedTradeId(tradeId);
            setCurrentPage('review');
          }}
        />
      </div>
      <div className="mb-6 animate-slide-in" style={{ animationDelay: '0.3s' }}>
        <AILearning 
          traderId={selectedTrader.trader_id}
          onTradeSelect={(tradeId) => {
            setSelectedTradeId(tradeId);
            setCurrentPage('review');
          }}
        />
      </div>

      {/* AI 思维链 / 调用详情抽屉 */}
      {showAiDrawer && (
        <div className="fixed inset-0 z-50 flex items-start justify-end bg-black bg-opacity-50" onClick={() => setShowAiDrawer(false)}>
          <div
            className="w-full md:w-[600px] h-full overflow-y-auto rounded-l-lg shadow-2xl animate-slide-in"
            style={{ background: '#0B0E11', borderLeft: '1px solid #2B3139' }}
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between px-5 py-4 border-b" style={{ borderColor: '#2B3139' }}>
              <div>
                <div className="text-sm font-semibold" style={{ color: '#EAECEF' }}>AI 思维链 / 调用详情</div>
                <div className="text-xs" style={{ color: '#848E9C' }}>
                  {latestDecision
                    ? `Cycle #${latestDecision.cycle_number} · ${new Date(latestDecision.timestamp).toLocaleString()}`
                    : '暂无决策记录'}
                </div>
              </div>
              <button
                onClick={() => setShowAiDrawer(false)}
                className="text-sm px-2 py-1 rounded hover:bg-[#1E2329]"
                style={{ color: '#848E9C' }}
              >
                关闭
              </button>
            </div>

            <div className="p-5 space-y-4">
              {/* 历史决策显示 */}
              {latestDecision ? (
                <DecisionCard decision={latestDecision} language={language} />
              ) : (
                <div className="text-sm" style={{ color: '#848E9C' }}>
                  暂无决策数据，等待 AI 产生新一轮决策。
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// Stat Card Component - Binance Style Enhanced
function StatCard({
  title,
  value,
  change,
  positive,
  subtitle,
}: {
  title: string;
  value: string;
  change?: number;
  positive?: boolean;
  subtitle?: string;
}) {
  return (
    <div className="stat-card animate-fade-in">
      <div className="text-xs mb-2 mono uppercase tracking-wider" style={{ color: '#848E9C' }}>{title}</div>
      <div className="text-2xl font-bold mb-1 mono" style={{ color: '#EAECEF' }}>{value}</div>
      {change !== undefined && (
        <div className="flex items-center gap-1">
          <div
            className="text-sm mono font-bold"
            style={{ color: positive ? '#0ECB81' : '#F6465D' }}
          >
            {positive ? '▲' : '▼'} {positive ? '+' : ''}
            {change.toFixed(2)}%
          </div>
        </div>
      )}
      {subtitle && <div className="text-xs mt-2 mono" style={{ color: '#848E9C' }}>{subtitle}</div>}
    </div>
  );
}

// Streaming Decision Card - 实时思考流卡片
function StreamingDecisionCard({ cycleNumber, content, language, isCompleted }: { cycleNumber: number; content: string; language: Language; isCompleted?: boolean }) {
  const containerRef = useRef<HTMLDivElement>(null);

  // 自动滚动到底部（仅在思考中时）
  useEffect(() => {
    if (containerRef.current && !isCompleted) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [content, isCompleted]);

  return (
    <div className="rounded p-5 transition-all duration-300" style={{ 
      border: isCompleted ? '1px solid #2B3139' : '2px solid #0ECB81', 
      background: '#1E2329', 
      boxShadow: isCompleted ? '0 2px 8px rgba(0, 0, 0, 0.3)' : '0 2px 8px rgba(14, 203, 129, 0.2)' 
    }}>
      {/* Header */}
      <div className="flex items-start justify-between mb-3">
        <div>
          <div className="font-semibold flex items-center gap-2" style={{ color: '#EAECEF' }}>
            {t('cycle', language)} #{cycleNumber}
            {!isCompleted && <div className="w-2 h-2 rounded-full animate-pulse" style={{ background: '#0ECB81' }}></div>}
          </div>
          <div className="text-xs" style={{ color: '#848E9C' }}>
            {new Date().toLocaleString()} · {isCompleted ? '思考完成' : 'AI 思考中...'}
          </div>
        </div>
        <div
          className="px-3 py-1 rounded text-xs font-bold"
          style={isCompleted 
            ? { background: 'rgba(99, 102, 241, 0.1)', color: '#6366F1' }
            : { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81' }
          }
        >
          {isCompleted ? '已完成' : '思考中'}
        </div>
      </div>

      {/* 实时思考内容 */}
      <div
        ref={containerRef}
        className="text-sm font-mono whitespace-pre-wrap max-h-96 overflow-y-auto p-4 rounded"
        style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF', minHeight: '150px' }}
      >
        {content || <span style={{ color: '#848E9C' }}>等待 AI 输出...</span>}
      </div>
    </div>
  );
}

// Decision Card Component with CoT Trace - Binance Style
function DecisionCard({ decision, language, streamingContent }: { decision: DecisionRecord; language: Language; streamingContent?: string }) {
  const [showInputPrompt, setShowInputPrompt] = useState(false);
  const [showCoT, setShowCoT] = useState(false);
  const [showStreaming, setShowStreaming] = useState(false);

  return (
    <div className="rounded p-5 transition-all duration-300 hover:translate-y-[-2px]" style={{ border: '1px solid #2B3139', background: '#1E2329', boxShadow: '0 2px 8px rgba(0, 0, 0, 0.3)' }}>
      {/* Header */}
      <div className="flex items-start justify-between mb-3">
        <div>
          <div className="font-semibold" style={{ color: '#EAECEF' }}>{t('cycle', language)} #{decision.cycle_number}</div>
          <div className="text-xs" style={{ color: '#848E9C' }}>
            {new Date(decision.timestamp).toLocaleString()}
          </div>
        </div>
        <div
          className="px-3 py-1 rounded text-xs font-bold"
          style={(() => {
            const status = decision.status || (decision.success ? 'ok' : 'error');
            switch (status) {
              case 'ok':
                return { background: 'rgba(14, 203, 129, 0.1)', color: '#0ECB81' };
              case 'warning':
                return { background: 'rgba(255, 193, 7, 0.1)', color: '#FFC107' };
              case 'error':
              default:
                return { background: 'rgba(246, 70, 93, 0.1)', color: '#F6465D' };
            }
          })()}
        >
          {(() => {
            const status = decision.status || (decision.success ? 'ok' : 'error');
            switch (status) {
              case 'ok':
                return t('success', language);
              case 'warning':
                return decision.error_type === 'DECISION_VALIDATION_REJECTED' ? t('decisionRejected', language) : t('decisionWarning', language);
              case 'error':
              default:
                return t('decisionError', language);
            }
          })()}
        </div>
      </div>

      {/* Input Prompt - Collapsible */}
      {decision.input_prompt && (
        <div className="mb-3">
          <button
            onClick={() => setShowInputPrompt(!showInputPrompt)}
            className="flex items-center gap-2 text-sm transition-colors"
            style={{ color: '#60a5fa' }}
          >
            <span className="font-semibold">📥 {t('inputPrompt', language)}</span>
            <span className="text-xs">{showInputPrompt ? t('collapse', language) : t('expand', language)}</span>
          </button>
          {showInputPrompt && (
            <div className="mt-2 rounded p-4 text-sm font-mono whitespace-pre-wrap max-h-96 overflow-y-auto" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}>
              {decision.input_prompt}
            </div>
          )}
        </div>
      )}

      {/* 验证错误详情（如果有） */}
      {decision.validation_errors && decision.validation_errors.length > 0 && (
        <div className="mb-3 p-3 rounded text-sm" style={{ background: 'rgba(255, 193, 7, 0.05)', border: '1px solid rgba(255, 193, 7, 0.2)' }}>
          <div className="font-semibold mb-2" style={{ color: '#FFC107' }}>⚠️ 风控拦截详情</div>
          <div className="space-y-1">
            {decision.validation_errors.map((error, idx) => (
              <div key={idx} className="text-xs" style={{ color: '#EAECEF' }}>
                <span className="font-mono" style={{ color: '#FFC107' }}>{error.symbol}</span>
                <span className="mx-2" style={{ color: '#848E9C' }}>{error.action}</span>
                <span style={{ color: '#EAECEF' }}>{error.reason}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* 实时思考流（如果存在） */}
      {streamingContent && (
        <div className="mb-3">
          <button
            onClick={() => setShowStreaming(!showStreaming)}
            className="flex items-center gap-2 text-sm transition-colors"
            style={{ color: '#0ECB81' }}
          >
            <span className="font-semibold">⚡ 实时思考流</span>
            <span className="text-xs">{showStreaming ? t('collapse', language) : t('expand', language)}</span>
          </button>
          {showStreaming && (
            <div className="mt-2 rounded p-4 text-sm font-mono whitespace-pre-wrap max-h-96 overflow-y-auto" style={{ background: '#0B0E11', border: '1px solid #0ECB81', color: '#EAECEF' }}>
              {streamingContent}
            </div>
          )}
        </div>
      )}

      {/* AI Chain of Thought - Collapsible */}
      {decision.cot_trace && (
        <div className="mb-3">
          <button
            onClick={() => setShowCoT(!showCoT)}
            className="flex items-center gap-2 text-sm transition-colors"
            style={{ color: '#F0B90B' }}
          >
            <span className="font-semibold">📤 {t('aiThinking', language)}（最终结果）</span>
            <span className="text-xs">{showCoT ? t('collapse', language) : t('expand', language)}</span>
          </button>
          {showCoT && (
            <div className="mt-2 rounded p-4 text-sm font-mono whitespace-pre-wrap max-h-96 overflow-y-auto" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}>
              {decision.cot_trace}
            </div>
          )}
        </div>
      )}

      {/* Decisions Actions */}
      {decision.decisions && decision.decisions.length > 0 && (
        <div className="space-y-2 mb-3">
          {decision.decisions.map((action, j) => (
            <div key={j} className="flex items-center gap-2 text-sm rounded px-3 py-2" style={{ background: '#0B0E11' }}>
              <span className="font-mono font-bold" style={{ color: '#EAECEF' }}>{action.symbol}</span>
              <span
                className="px-2 py-0.5 rounded text-xs font-bold"
                style={action.action.includes('open')
                  ? { background: 'rgba(96, 165, 250, 0.1)', color: '#60a5fa' }
                  : { background: 'rgba(240, 185, 11, 0.1)', color: '#F0B90B' }
                }
              >
                {action.action}
              </span>
              {action.leverage > 0 && <span style={{ color: '#F0B90B' }}>{action.leverage}x</span>}
              {action.price > 0 && (
                <span className="font-mono text-xs" style={{ color: '#848E9C' }}>@{action.price.toFixed(4)}</span>
              )}
              <span style={{ color: action.success ? '#0ECB81' : '#F6465D' }}>
                {action.success ? '✓' : '✗'}
              </span>
              {action.error && <span className="text-xs ml-2" style={{ color: '#F6465D' }}>{action.error}</span>}
            </div>
          ))}
        </div>
      )}

      {/* Account State Summary */}
      {decision.account_state && (
        <div className="flex gap-4 text-xs mb-3 rounded px-3 py-2" style={{ background: '#0B0E11', color: '#848E9C' }}>
          <span>净值: {(decision.account_state.total_balance ?? 0).toFixed(2)} USDT</span>
          <span>可用: {(decision.account_state.available_balance ?? 0).toFixed(2)} USDT</span>
          <span>保证金率: {(decision.account_state.margin_used_pct ?? 0).toFixed(1)}%</span>
          <span>持仓: {decision.account_state.position_count ?? 0}</span>
        </div>
      )}

      {/* Execution Logs */}
      {decision.execution_log && decision.execution_log.length > 0 && (
        <div className="space-y-1">
          {decision.execution_log.map((log, k) => (
            <div
              key={k}
              className="text-xs font-mono"
              style={{ color: log.includes('✓') || log.includes('成功') ? '#0ECB81' : '#F6465D' }}
            >
              {log}
            </div>
          ))}
        </div>
      )}

      {/* Error Message */}
      {decision.error_message && (
        <div className="text-sm rounded px-3 py-2 mt-3" style={{ color: '#F6465D', background: 'rgba(246, 70, 93, 0.1)' }}>
          ❌ {decision.error_message}
        </div>
      )}
    </div>
  );
}

// Wrap App with providers
export default function AppWithProviders() {
  return (
    <LanguageProvider>
      <AuthProvider>
        <App />
      </AuthProvider>
    </LanguageProvider>
  );
}
