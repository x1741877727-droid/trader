import { useMemo, useState, useEffect, useRef } from 'react';
import useSWR, { mutate } from 'swr';
import { ScrollText, Sparkles, ArrowRight, Loader2 } from 'lucide-react';
import { useLanguage } from '../contexts/LanguageContext';
import { t } from '../i18n/translations';
import { api } from '../lib/api';
import type { CloseReviewSummary } from '../types';
import {
  TradeDetailModal,
  type TradeOutcome,
  withSyntheticId,
  ensureTradeId,
  formatDuration,
  getReviewStatusMeta,
} from './TradeReviewModal';

interface PerformanceAnalysis {
  recent_trades: TradeOutcome[];
  total_trades: number;
}

interface CloseReviewPanelProps {
  traderId: string;
  onTradeSelect?: (tradeId: string) => void;
}

export function CloseReviewPanel({ traderId, onTradeSelect }: CloseReviewPanelProps) {
  const { language } = useLanguage();
  const [selectedTrade, setSelectedTrade] = useState<TradeOutcome | null>(null);
  const [generatingReview, setGeneratingReview] = useState<Set<string>>(new Set());
  const [displayCount, setDisplayCount] = useState(10); // 初始显示10笔订单
  const scrollContainerRef = useRef<HTMLDivElement>(null);

  const { data: performance, error: performanceError } = useSWR<PerformanceAnalysis>(
    traderId ? `performance-${traderId}` : null,
    () => api.getPerformance(traderId),
    {
      refreshInterval: 30000,
      revalidateOnFocus: false,
      dedupingInterval: 20000,
    }
  );

  const { data: closeReviews } = useSWR<CloseReviewSummary[]>(
    traderId ? `close-reviews-${traderId}` : null,
    () => api.getCloseReviews(traderId, 120),
    {
      refreshInterval: 60000,
      revalidateOnFocus: false,
    }
  );

  const reviewMap = useMemo(() => {
    const map = new Map<string, CloseReviewSummary>();
    closeReviews?.forEach((item) => map.set(item.trade_id, item));
    return map;
  }, [closeReviews]);

  const recentTrades = performance?.recent_trades ?? [];
  
  // 当数据更新时，确保 displayCount 不超过实际数据量
  useEffect(() => {
    if (recentTrades.length > 0 && displayCount > recentTrades.length) {
      setDisplayCount(Math.min(displayCount, recentTrades.length));
    }
  }, [recentTrades.length, displayCount]);
  
  const displayTrades = recentTrades.slice(0, displayCount); // 根据 displayCount 显示订单
  const hasMore = recentTrades.length > displayCount;
  const latestReviews = (closeReviews ?? []).slice(0, 3);

  // 滚动加载逻辑
  useEffect(() => {
    const container = scrollContainerRef.current;
    if (!container || !hasMore) return;

    const handleScroll = () => {
      const { scrollTop, scrollHeight, clientHeight } = container;
      // 当滚动到距离底部 100px 时加载更多
      if (scrollHeight - scrollTop - clientHeight < 100) {
        setDisplayCount((prev) => Math.min(prev + 10, recentTrades.length));
      }
    };

    container.addEventListener('scroll', handleScroll);
    return () => container.removeEventListener('scroll', handleScroll);
  }, [hasMore, recentTrades.length]);

  const {
    data: reviewDetail,
    error: reviewDetailError,
    isLoading: reviewDetailLoading,
  } = useSWR(
    selectedTrade?.trade_id && !selectedTrade.trade_id.startsWith('synthetic-')
      ? ['close-review', selectedTrade.trade_id, traderId]
      : null,
    ([, tradeId]) => api.getCloseReview(tradeId, traderId),
    {
      revalidateOnFocus: false,
    }
  );

  const activeSummary =
    reviewDetail?.summary ??
    (selectedTrade?.trade_id ? reviewMap.get(selectedTrade.trade_id) ?? null : null);
  const activeDetail = reviewDetail?.detail ?? null;

  const handleGenerateReview = async (trade: TradeOutcome, e: React.MouseEvent) => {
    e.stopPropagation(); // 阻止触发父元素的点击事件
    
    const tradeId = trade.trade_id;
    if (!tradeId || tradeId.startsWith('synthetic-')) {
      return;
    }

    if (generatingReview.has(tradeId)) {
      return;
    }

    setGeneratingReview((prev) => new Set(prev).add(tradeId));

    try {
      await api.generateReview(tradeId, traderId);
      
      // 刷新数据
      await mutate(`close-reviews-${traderId}`);
      await mutate(['close-review', tradeId, traderId]);
      await mutate(`performance-${traderId}`);
      
      // 如果当前选中的是这个交易，刷新详情
      if (selectedTrade?.trade_id === tradeId) {
        await mutate(['close-review', tradeId, traderId]);
      } else {
        // 如果未选中，自动打开详情模态框显示复盘内容
        setSelectedTrade(withSyntheticId(trade));
        // 等待一下让数据刷新
        setTimeout(async () => {
          await mutate(['close-review', tradeId, traderId]);
        }, 500);
      }
    } catch (error) {
      console.error('生成复盘失败:', error);
      alert(error instanceof Error ? error.message : '生成复盘失败');
    } finally {
      setGeneratingReview((prev) => {
        const next = new Set(prev);
        next.delete(tradeId);
        return next;
      });
    }
  };

  return (
    <div className="binance-card p-6">
      <div className="flex items-center justify-between mb-5">
        <div>
          <h2 className="text-xl font-bold flex items-center gap-2" style={{ color: '#EAECEF' }}>
            <Sparkles className="w-5 h-5" style={{ color: '#F0B90B' }} />
            {t('closeReviewSummaryTitle', language)}
          </h2>
          <p className="text-xs mt-1" style={{ color: '#848E9C' }}>
            {t('tradeHistory', language)} · {t('aiLearning', language)}
          </p>
        </div>
        <div className="flex items-center gap-2 text-xs" style={{ color: '#F0B90B' }}>
          <Sparkles className="w-4 h-4" />
          {t('reviewPending', language)}
        </div>
      </div>

      {performanceError && (
        <div className="rounded-lg p-4 mb-4" style={{ background: 'rgba(246, 70, 93, 0.08)', color: '#F87171' }}>
          {t('loadingError', language)}
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">
        <div>
          <div className="flex items-center gap-2 mb-3">
            <ScrollText className="w-4 h-4" style={{ color: '#FCD34D' }} />
            <span className="text-sm font-semibold" style={{ color: '#FCD34D' }}>
              {t('tradeHistory', language)}
            </span>
            {recentTrades.length > 0 && (
              <span className="text-xs" style={{ color: '#848E9C' }}>
                ({displayTrades.length} / {recentTrades.length})
              </span>
            )}
          </div>

          {displayTrades.length > 0 ? (
            <div 
              ref={scrollContainerRef}
              className="space-y-3"
              style={{ 
                maxHeight: '600px', 
                overflowY: 'auto',
                paddingRight: '4px',
                scrollBehavior: 'smooth'
              }}
            >
              {displayTrades.map((trade, idx) => {
                const tradeId = ensureTradeId(trade);
                const normalizedTrade = withSyntheticId(trade);
                const hasReview = trade.trade_id ? reviewMap.has(trade.trade_id) : false;
                const reviewStatus: 'done' | 'pending' | 'missing' = hasReview
                  ? 'done'
                  : trade.trade_id
                    ? 'pending'
                    : 'missing';
                const statusMeta = getReviewStatusMeta(reviewStatus, language);
                const isProfitable = trade.pn_l >= 0;

                const isGenerating = generatingReview.has(trade.trade_id || '');
                const canGenerateReview = reviewStatus === 'pending' && trade.trade_id && !trade.trade_id.startsWith('synthetic-');

                return (
                  <div
                    key={tradeId}
                    className="w-full rounded-xl p-4 transition-all hover:translate-y-[-1px]"
                    style={{
                      background: 'rgba(30,35,41,0.6)',
                      border: '1px solid rgba(71,85,105,0.3)',
                    }}
                  >
                    <div className="flex items-center justify-between gap-3 mb-2">
                      <div className="flex items-center gap-2 flex-wrap">
                        <button
                          onClick={() => {
                            if (onTradeSelect && trade.trade_id && !trade.trade_id.startsWith('synthetic-')) {
                              onTradeSelect(trade.trade_id);
                            } else {
                              setSelectedTrade(normalizedTrade);
                            }
                          }}
                          className="text-left"
                        >
                          <span className="font-mono font-bold" style={{ color: '#EAECEF' }}>
                            {trade.symbol}
                          </span>
                        </button>
                        <span
                          className="text-xs px-2 py-0.5 rounded font-bold"
                          style={{
                            background: trade.side === 'long' ? 'rgba(14,203,129,0.2)' : 'rgba(246,70,93,0.2)',
                            color: trade.side === 'long' ? '#0ECB81' : '#F6465D',
                          }}
                        >
                          {trade.side.toUpperCase()}
                        </span>
                        {idx === 0 && (
                          <span className="text-xs px-2 py-0.5 rounded" style={{ background: 'rgba(240,185,11,0.15)', color: '#F0B90B' }}>
                            {t('latest', language)}
                          </span>
                        )}
                      </div>
                      <div className="flex items-center gap-2">
                        <span
                          className="text-xs font-semibold px-2 py-0.5 rounded-full"
                          style={{ background: statusMeta.background, color: statusMeta.color }}
                        >
                          {statusMeta.label}
                        </span>
                        {canGenerateReview && (
                          <button
                            onClick={(e) => handleGenerateReview(trade, e)}
                            disabled={isGenerating}
                            className="text-xs px-2 py-1 rounded font-semibold transition-all disabled:opacity-50 disabled:cursor-not-allowed hover:opacity-80"
                            style={{
                              background: isGenerating ? 'rgba(129,140,248,0.2)' : 'rgba(129,140,248,0.3)',
                              color: '#818CF8',
                            }}
                          >
                            {isGenerating ? (
                              <span className="flex items-center gap-1">
                                <Loader2 className="w-3 h-3 animate-spin" />
                                {t('generating', language)}
                              </span>
                            ) : (
                              t('generateReview', language)
                            )}
                          </button>
                        )}
                        <span className="font-mono font-semibold" style={{ color: isProfitable ? '#0ECB81' : '#F6465D' }}>
                          {isProfitable ? '+' : ''}
                          {(trade.pn_l_pct ?? 0).toFixed(2)}%
                        </span>
                      </div>
                    </div>
                    <button
                      onClick={() => {
                        if (onTradeSelect && trade.trade_id && !trade.trade_id.startsWith('synthetic-')) {
                          onTradeSelect(trade.trade_id);
                        } else {
                          setSelectedTrade(normalizedTrade);
                        }
                      }}
                      className="w-full text-left flex items-center justify-between text-xs"
                      style={{ color: '#94A3B8' }}
                    >
                      <span>
                        {new Date(trade.close_time).toLocaleString()}
                      </span>
                      <span>
                        ⏱️ {formatDuration(trade.duration)}
                      </span>
                    </button>
                  </div>
                );
              })}
              {hasMore && (
                <div className="text-center py-3">
                  <div className="flex items-center justify-center gap-2 text-xs" style={{ color: '#94A3B8' }}>
                    <Loader2 className="w-3 h-3 animate-spin" />
                    <span>{t('loadingMore', language)}...</span>
                  </div>
                </div>
              )}
            </div>
          ) : (
            <div className="rounded-xl p-6 text-center" style={{ background: 'rgba(15,23,42,0.6)', color: '#94A3B8' }}>
              <ScrollText className="w-10 h-10 mx-auto mb-3 opacity-70" />
              {t('noCompletedTrades', language)}
            </div>
          )}
        </div>

        <div>
          <div className="flex items-center gap-2 mb-3">
            <Sparkles className="w-4 h-4" style={{ color: '#A78BFA' }} />
            <span className="text-sm font-semibold" style={{ color: '#A78BFA' }}>
              {t('closeReviewSummaryTitle', language)}
            </span>
          </div>

          {latestReviews.length > 0 ? (
            <div className="space-y-3">
              {latestReviews.map((review) => (
                <div
                  key={review.trade_id}
                  className="rounded-xl p-4"
                  style={{ background: 'rgba(18,22,28,0.8)', border: '1px solid rgba(129,140,248,0.2)' }}
                >
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-mono font-bold" style={{ color: '#EAECEF' }}>{review.symbol}</span>
                      <span className="text-xs" style={{ color: '#94A3B8' }}>
                        {new Date(review.created_at).toLocaleString()}
                      </span>
                    </div>
                    <span className="text-xs font-semibold px-2 py-0.5 rounded-full" style={{ background: 'rgba(16,185,129,0.15)', color: '#10B981' }}>
                      {review.confidence ? `${t('confidenceScore', language)} ${review.confidence}` : t('reviewReady', language)}
                    </span>
                  </div>
                  <p className="text-sm mb-2" style={{ color: '#CBD5E1' }}>
                    {review.summary}
                  </p>
                  {review.action_items && review.action_items.length > 0 && (
                    <div className="text-xs flex items-center gap-2" style={{ color: '#F0B90B' }}>
                      <ClipboardListIcon />
                      {review.action_items[0].item}
                      {review.action_items.length > 1 && ` +${review.action_items.length - 1}`}
                    </div>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <div className="rounded-xl p-6 text-center" style={{ background: 'rgba(18,22,28,0.8)', color: '#94A3B8' }}>
              <Sparkles className="w-10 h-10 mx-auto mb-3 opacity-70" />
              {t('closeReviewMissing', language)}
            </div>
          )}

          <div className="mt-4 flex items-center justify-between text-xs" style={{ color: '#94A3B8' }}>
            <span>{t('aiLearningPoint1', language)}</span>
            <span className="inline-flex items-center gap-1" style={{ color: '#F0B90B' }}>
              {t('aiLearning', language)}
              <ArrowRight className="w-3 h-3" />
            </span>
          </div>
        </div>
      </div>

      {selectedTrade && (
        <TradeDetailModal
          trade={selectedTrade}
          summary={activeSummary ?? undefined}
          detail={activeDetail}
          loading={reviewDetailLoading}
          error={reviewDetailError as Error | undefined}
          onClose={() => setSelectedTrade(null)}
          language={language}
          traderId={traderId}
          onReviewGenerated={async () => {
            if (selectedTrade?.trade_id) {
              await mutate(['close-review', selectedTrade.trade_id, traderId]);
            }
          }}
        />
      )}
    </div>
  );
}

function ClipboardListIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2" />
      <rect x="9" y="3" width="6" height="4" rx="1" />
      <line x1="9" y1="12" x2="15" y2="12" />
      <line x1="9" y1="16" x2="15" y2="16" />
    </svg>
  );
}


