import React from 'react';
import { Clock, Sparkles, ClipboardList, Loader2 } from 'lucide-react';
import { t, type Language } from '../i18n/translations';
import type { CloseReviewSummary, CloseReviewFile, PositionLifecycleEntry } from '../types';
import { api } from '../lib/api';
import { mutate } from 'swr';

export interface TradeOutcome {
  trade_id?: string;
  symbol: string;
  side: string;
  quantity: number;
  leverage: number;
  open_price: number;
  close_price: number;
  position_value: number;
  margin_used: number;
  pn_l: number;
  pn_l_pct: number;
  duration: string;
  open_time: string;
  close_time: string;
  was_stop_loss: boolean;
}

export interface TradeDetailModalProps {
  trade: TradeOutcome;
  summary?: CloseReviewSummary | null;
  detail: CloseReviewFile | null;
  loading: boolean;
  error?: Error;
  onClose: () => void;
  language: Language;
  traderId?: string;
  onReviewGenerated?: () => void;
}

export function TradeDetailModal({
  trade,
  summary,
  detail,
  loading,
  error,
  onClose,
  language,
  traderId,
  onReviewGenerated,
}: TradeDetailModalProps) {
  const [generatingReview, setGeneratingReview] = React.useState(false);
  const reviewRecord = detail?.review ?? summary ?? null;
  const requestSnapshot = extractRequestSnapshot(detail);
  const timelineEntries = extractTimelineEntries(detail);
  const snapshot = detail?.trade_snapshot ?? buildSnapshotFromTrade(trade);
  const hasRealTradeId = trade.trade_id && !trade.trade_id.startsWith('synthetic-');
  const statusMeta = getReviewStatusMeta(
    reviewRecord ? 'done' : hasRealTradeId ? 'pending' : 'missing',
    language
  );
  
  const handleGenerateReview = async () => {
    if (!trade.trade_id || trade.trade_id.startsWith('synthetic-') || !traderId) {
      return;
    }
    
    if (generatingReview) {
      return;
    }
    
    setGeneratingReview(true);
    try {
      await api.generateReview(trade.trade_id, traderId);
      
      // 刷新数据
      if (traderId) {
        await mutate(`close-reviews-${traderId}`);
        await mutate(['close-review', trade.trade_id, traderId]);
        await mutate(`performance-${traderId}`);
      }
      
      if (onReviewGenerated) {
        onReviewGenerated();
      }
    } catch (error) {
      console.error('生成复盘失败:', error);
      alert(error instanceof Error ? error.message : '生成复盘失败');
    } finally {
      setGeneratingReview(false);
    }
  };

  const statCards = [
    { label: t('entryTime', language), value: formatTimestamp(snapshot?.entry_time || trade.open_time) },
    { label: t('exitTime', language), value: formatTimestamp(snapshot?.exit_time || trade.close_time) },
    {
      label: t('tradePnL', language),
      value: formatSignedCurrency(trade.pn_l),
      accent: trade.pn_l >= 0 ? '#10B981' : '#F87171',
    },
    {
      label: t('tradePnLPct', language),
      value: trade.pn_l_pct !== undefined ? `${trade.pn_l_pct.toFixed(2)}%` : '--',
      accent: trade.pn_l_pct >= 0 ? '#10B981' : '#F87171',
    },
    { label: t('quantity', language), value: trade.quantity?.toFixed(4) ?? '--' },
    { label: t('leverage', language), value: trade.leverage ? `${trade.leverage}x` : '--' },
    { label: t('marginUsedShort', language), value: formatCurrency(trade.margin_used) },
    {
      label: t('holdingDuration', language),
      value: trade.duration ? formatDuration(trade.duration) : snapshot?.holding_minutes ? `${snapshot.holding_minutes}m` : '--',
    },
  ];

  const confidence = reviewRecord?.confidence ?? summary?.confidence;
  const actionItems = reviewRecord?.action_items ?? summary?.action_items ?? [];
  const highlights = reviewRecord?.what_went_well ?? summary?.what_went_well ?? [];
  const improvements = reviewRecord?.improvements ?? summary?.improvements ?? [];

  // 防止背景滚动
  React.useEffect(() => {
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = '';
    };
  }, []);

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-[#0B0E11] p-4">
      <div className="w-full max-w-5xl max-h-[90vh] overflow-y-auto rounded-2xl border border-[#2B3139] bg-[#0B0E11] p-6 shadow-2xl relative">
        <button
          onClick={onClose}
          className="absolute top-4 right-4 px-3 py-1 rounded text-sm font-semibold"
          style={{ background: '#2B3139', color: '#EAECEF' }}
        >
          {t('closeModal', language)}
        </button>
        <div className="mb-6">
          <div className="flex items-start justify-between gap-4">
            <div>
              <div className="flex items-center gap-3 flex-wrap">
                <h3 className="text-2xl font-bold" style={{ color: '#EAECEF' }}>
                  {trade.symbol}
                </h3>
                <span
                  className="text-xs px-2 py-1 rounded font-bold"
                  style={{
                    background: trade.side === 'long' ? 'rgba(14, 203, 129, 0.2)' : 'rgba(246, 70, 93, 0.2)',
                    color: trade.side === 'long' ? '#10B981' : '#F87171',
                  }}
                >
                  {trade.side.toUpperCase()}
                </span>
                <div className="text-xs font-semibold px-2 py-1 rounded-full" style={{
                  background: statusMeta.background,
                  color: statusMeta.color,
                }}>
                  {statusMeta.label}
                </div>
                {confidence !== undefined && (
                  <div className="text-xs font-semibold px-2 py-1 rounded-full" style={{
                    background: 'rgba(129, 140, 248, 0.15)',
                    color: '#818CF8',
                  }}>
                    {t('confidenceScore', language)}: {confidence}
                  </div>
                )}
              </div>
              <div className="text-xs mt-2" style={{ color: '#94A3B8' }}>
                {formatTimestamp(trade.close_time)} · {trade.trade_id || t('reviewMissing', language)}
              </div>
            </div>
          </div>
          {loading && (
            <div className="mt-3 flex items-center gap-2 text-xs" style={{ color: '#A78BFA' }}>
              <Clock className="w-4 h-4 animate-spin" />
              {t('loadingReview', language)}
            </div>
          )}
          {error && (
            <div className="mt-3 text-xs" style={{ color: '#F87171' }}>
              {error.message}
            </div>
          )}
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-6">
          {statCards.map((card) => (
            <div
              key={card.label}
              className="rounded-xl p-4"
              style={{ background: 'rgba(30,35,41,0.6)', border: '1px solid rgba(43,49,57,0.8)' }}
            >
              <div className="text-xs uppercase mb-1" style={{ color: '#94A3B8' }}>
                {card.label}
              </div>
              <div className="text-lg font-bold" style={{ color: card.accent ?? '#EAECEF' }}>
                {card.value}
              </div>
            </div>
          ))}
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-6">
          <div className="rounded-2xl border border-[#2B3139] p-4" style={{ background: 'rgba(30,35,41,0.6)' }}>
            <div className="flex items-center gap-2 mb-3">
              <Clock className="w-4 h-4" style={{ color: '#F0B90B' }} />
              <span className="text-sm font-semibold" style={{ color: '#EAECEF' }}>
                {t('requestSnapshot', language)}
              </span>
            </div>
            {requestSnapshot ? (
              <pre className="text-xs whitespace-pre-wrap max-h-60 overflow-y-auto" style={{ color: '#CBD5F5' }}>
                {stringifyMetadata(requestSnapshot)}
              </pre>
            ) : (
              <div className="text-sm" style={{ color: '#94A3B8' }}>
                {t('snapshotPlaceholder', language)}
              </div>
            )}
          </div>

          <div className="rounded-2xl border border-[#2B3139] p-4" style={{ background: 'rgba(30,35,41,0.6)' }}>
            <div className="flex items-center gap-2 mb-3">
              <Sparkles className="w-4 h-4" style={{ color: '#FCD34D' }} />
              <span className="text-sm font-semibold" style={{ color: '#EAECEF' }}>
                {t('majorDecisions', language)}
              </span>
            </div>
            {timelineEntries.length > 0 ? (
              <div className="space-y-3 max-h-60 overflow-y-auto pr-1">
                {timelineEntries.map((entry, idx) => (
                  <div
                    key={`${entry.cycle_number ?? idx}-${entry.timestamp ?? idx}`}
                    className="rounded-lg p-3"
                    style={{ background: 'rgba(15,23,42,0.6)', border: '1px solid rgba(148,163,184,0.2)' }}
                  >
                    <div className="flex items-center justify-between mb-1">
                      <div className="flex items-center gap-2">
                        <div className="w-8 h-8 rounded-full flex items-center justify-center text-xs font-bold" style={{
                          background: 'rgba(129, 140, 248, 0.2)',
                          color: '#C4B5FD',
                        }}>
                          #{entry.cycle_number ?? idx + 1}
                        </div>
                        <div>
                          <div className="text-sm font-semibold" style={{ color: '#EAECEF' }}>
                            {entry.action || t('majorDecisionStep', language, { index: idx + 1 })}
                          </div>
                          <div className="text-xs" style={{ color: '#94A3B8' }}>
                            {formatTimestamp(entry.timestamp)}
                          </div>
                        </div>
                      </div>
                    </div>
                    {entry.reasoning && (
                      <div className="text-xs" style={{ color: '#CBD5E1' }}>
                        {entry.reasoning}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            ) : (
              <div className="text-sm" style={{ color: '#94A3B8' }}>
                {t('majorDecisionsEmpty', language)}
              </div>
            )}
          </div>
        </div>

        <div className="rounded-2xl border border-[#2B3139] p-5" style={{ background: 'rgba(18,22,28,0.85)' }}>
          <div className="flex items-center gap-2 mb-4">
            <Sparkles className="w-5 h-5" style={{ color: '#F0B90B' }} />
            <h4 className="text-lg font-semibold" style={{ color: '#EAECEF' }}>
              {t('closeReviewSummaryTitle', language)}
            </h4>
          </div>
          {reviewRecord ? (
            <div className="space-y-4">
              <p className="text-base font-semibold" style={{ color: '#EAECEF' }}>
                {reviewRecord.summary}
              </p>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {highlights.length > 0 && (
                  <div className="rounded-lg p-3" style={{ background: 'rgba(16,185,129,0.08)' }}>
                    <div className="text-sm font-semibold mb-2" style={{ color: '#10B981' }}>
                      {t('reviewHighlights', language)}
                    </div>
                    <ul className="list-disc pl-4 text-sm space-y-1" style={{ color: '#D1FAE5' }}>
                      {highlights.map((item, idx) => (
                        <li key={`ww-${idx}`}>{item}</li>
                      ))}
                    </ul>
                  </div>
                )}
                {improvements.length > 0 && (
                  <div className="rounded-lg p-3" style={{ background: 'rgba(248,113,113,0.08)' }}>
                    <div className="text-sm font-semibold mb-2" style={{ color: '#F87171' }}>
                      {t('improvementAreas', language)}
                    </div>
                    <ul className="list-disc pl-4 text-sm space-y-1" style={{ color: '#FECACA' }}>
                      {improvements.map((item, idx) => (
                        <li key={`imp-${idx}`}>{item}</li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>

              {(reviewRecord.root_cause || reviewRecord.extreme_intervention_review) && (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  {reviewRecord.root_cause && (
                    <div className="rounded-lg p-3" style={{ background: 'rgba(15,23,42,0.6)' }}>
                      <div className="text-sm font-semibold mb-2" style={{ color: '#EAECEF' }}>
                        {t('rootCause', language)}
                      </div>
                      <div className="text-sm" style={{ color: '#CBD5E1' }}>
                        {reviewRecord.root_cause}
                      </div>
                    </div>
                  )}
                  {reviewRecord.extreme_intervention_review && (
                    <div className="rounded-lg p-3" style={{ background: 'rgba(15,23,42,0.6)' }}>
                      <div className="text-sm font-semibold mb-2" style={{ color: '#EAECEF' }}>
                        {t('extremeReview', language)}
                      </div>
                      <div className="text-sm" style={{ color: '#CBD5E1' }}>
                        {reviewRecord.extreme_intervention_review}
                      </div>
                    </div>
                  )}
                </div>
              )}

              {reviewRecord.reasoning && (
                <div className="rounded-lg p-3" style={{ background: 'rgba(30,35,41,0.6)' }}>
                  <div className="text-sm font-semibold mb-2" style={{ color: '#EAECEF' }}>
                    {t('reasoningLabel', language)}
                  </div>
                  <div className="text-sm whitespace-pre-wrap" style={{ color: '#CBD5E1' }}>
                    {reviewRecord.reasoning}
                  </div>
                </div>
              )}

              {actionItems.length > 0 && (
                <div>
                  <div className="flex items-center gap-2 mb-2">
                    <ClipboardList className="w-4 h-4" style={{ color: '#F0B90B' }} />
                    <span className="text-sm font-semibold" style={{ color: '#EAECEF' }}>
                      {t('actionItems', language)}
                    </span>
                  </div>
                  <div className="overflow-x-auto rounded-lg border border-[#2B3139]">
                    <table className="w-full text-sm">
                      <thead style={{ background: 'rgba(30,35,41,0.8)', color: '#94A3B8' }}>
                        <tr>
                          <th className="text-left px-3 py-2">{t('owner', language)}</th>
                          <th className="text-left px-3 py-2">{t('due', language)}</th>
                          <th className="text-left px-3 py-2">{t('actionItems', language)}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {actionItems.map((item, idx) => (
                          <tr key={`action-${idx}`} className="border-t border-[#1F2937]">
                            <td className="px-3 py-2" style={{ color: '#EAECEF' }}>{item.owner || '--'}</td>
                            <td className="px-3 py-2" style={{ color: '#CBD5E1' }}>{formatTimestamp(item.due)}</td>
                            <td className="px-3 py-2" style={{ color: '#CBD5E1' }}>{item.item}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}
            </div>
          ) : (
            <div className="rounded-lg p-4 text-sm" style={{ background: 'rgba(248, 196, 113, 0.05)', color: '#FDE68A' }}>
              <div className="font-semibold mb-2">{t('closeReviewMissing', language)}</div>
              {hasRealTradeId && traderId ? (
                <div className="flex items-center gap-3">
                  <div className="flex-1">
                    {t('closeReviewActionHint', language, { tradeId: trade.trade_id || 'trade_id' })}
                  </div>
                  <button
                    onClick={handleGenerateReview}
                    disabled={generatingReview}
                    className="px-4 py-2 rounded-lg font-semibold text-sm transition-all disabled:opacity-50 disabled:cursor-not-allowed hover:opacity-80"
                    style={{
                      background: generatingReview ? 'rgba(129,140,248,0.2)' : 'rgba(129,140,248,0.3)',
                      color: '#818CF8',
                    }}
                  >
                    {generatingReview ? (
                      <span className="flex items-center gap-2">
                        <Loader2 className="w-4 h-4 animate-spin" />
                        {t('generating', language)}
                      </span>
                    ) : (
                      t('generateReview', language)
                    )}
                  </button>
                </div>
              ) : (
                <div>
                  {t('closeReviewActionHint', language, { tradeId: trade.trade_id || 'trade_id' })}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

export function formatDuration(duration: string | undefined): string {
  if (!duration) return '-';

  const match = duration.match(/(\d+h)?(\d+m)?(\d+\.?\d*s)?/);
  if (!match) return duration;

  const hours = match[1] || '';
  const minutes = match[2] || '';
  const seconds = match[3] || '';

  let result = '';
  if (hours) result += hours.replace('h', '小时');
  if (minutes) result += minutes.replace('m', '分');
  if (!hours && seconds) result += seconds.replace(/(\d+)\.?\d*s/, '$1秒');

  return result || duration;
}

export function ensureTradeId(trade: TradeOutcome): string {
  if (trade.trade_id && trade.trade_id.length > 0) return trade.trade_id;
  const closeTime = trade.close_time ? new Date(trade.close_time).getTime() : Date.now();
  return `synthetic-${trade.symbol}-${closeTime}`;
}

export function withSyntheticId(trade: TradeOutcome): TradeOutcome {
  if (trade.trade_id && trade.trade_id.length > 0) {
    return trade;
  }
  return { ...trade, trade_id: ensureTradeId(trade) };
}

export function getReviewStatusMeta(status: 'done' | 'pending' | 'missing', language: Language) {
  switch (status) {
    case 'done':
      return { label: t('reviewReady', language), color: '#0ECB81', background: 'rgba(14,203,129,0.15)' };
    case 'pending':
      return { label: t('reviewPending', language), color: '#F0B90B', background: 'rgba(240,185,11,0.15)' };
    default:
      return { label: t('reviewMissing', language), color: '#94A3B8', background: 'rgba(148,163,184,0.15)' };
  }
}

function buildSnapshotFromTrade(trade: TradeOutcome) {
  return {
    trade_id: trade.trade_id || '',
    symbol: trade.symbol,
    side: trade.side,
    entry_time: trade.open_time,
    exit_time: trade.close_time,
    entry_price: trade.open_price,
    exit_price: trade.close_price,
    quantity: trade.quantity,
    leverage: trade.leverage,
    risk_usd: trade.margin_used,
    pnl: trade.pn_l,
    pnl_pct: trade.pn_l_pct,
    holding_minutes: 0,
  };
}

function extractRequestSnapshot(detail: CloseReviewFile | null | undefined) {
  if (!detail) return null;
  if (detail.request_snapshot) return detail.request_snapshot;
  if (detail.additional_metadata && 'request_snapshot' in detail.additional_metadata) {
    return detail.additional_metadata.request_snapshot;
  }
  return null;
}

function extractTimelineEntries(detail: CloseReviewFile | null | undefined): PositionLifecycleEntry[] {
  if (!detail) return [];
  if (detail.major_decisions && detail.major_decisions.length > 0) return detail.major_decisions;
  if (detail.position_lifecycle && detail.position_lifecycle.length > 0) return detail.position_lifecycle;
  const extra = detail.additional_metadata?.major_decisions;
  if (Array.isArray(extra)) {
    return extra as PositionLifecycleEntry[];
  }
  return [];
}

function formatTimestamp(timestamp?: string | number | null): string {
  if (!timestamp) return '--';
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return String(timestamp);
  return date.toLocaleString();
}

function formatCurrency(value?: number): string {
  if (value === undefined || value === null) return '--';
  return `${value >= 0 ? '' : '-'}$${Math.abs(value).toFixed(2)}`;
}

function formatSignedCurrency(value?: number): string {
  if (value === undefined || value === null) return '--';
  const sign = value > 0 ? '+' : '';
  return `${sign}$${Math.abs(value).toFixed(2)}`;
}

function stringifyMetadata(data: unknown): string {
  if (typeof data === 'string') return data;
  try {
    return JSON.stringify(data, null, 2);
  } catch {
    return String(data);
  }
}


