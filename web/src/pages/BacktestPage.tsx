import { useState } from 'react'
import { api } from '../lib/api'

interface BacktestWaitReason {
  reason: string
  count: number
  pct: number
}

interface BacktestRuleFailure {
  rule: string
  count: number
  pct: number
}

export function BacktestPage() {
  const [symbols, setSymbols] = useState('BTCUSDT,ETHUSDT')
  const [days, setDays] = useState(7)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [data, setData] = useState<any | null>(null)
  const [progress, setProgress] = useState<{ current: number; total: number }>({ current: 0, total: 0 })
  const [status, setStatus] = useState<string>('idle')

  const handleRun = async () => {
    setLoading(true)
    setError(null)
    setData(null)
    setProgress({ current: 0, total: 0 })
    setStatus('running')
    try {
      const now = new Date()
      const end = now
      const start = new Date(now.getTime() - days * 24 * 60 * 60 * 1000)

      const format = (d: Date) =>
        `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(
          d.getHours(),
        ).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:00`

      const body = {
        symbols: symbols
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
        start: format(start),
        end: format(end),
        interval_minutes: 3,
      }

      // 1) 提交任务，获取 job_id
      const startRes = await api.runBacktestStart(body)
      const jid = startRes.job_id as string
      setProgress({
        current: startRes.current_cycle ?? 0,
        total: startRes.total_cycles ?? 0,
      })

      // 2) 轮询状态，直到完成/失败
      const poll = async () => {
        if (!jid) return
        try {
          const statusRes = await api.getBacktestStatus(jid)
          setStatus(statusRes.status)
          setProgress({
            current: statusRes.current_cycle ?? 0,
            total: statusRes.total_cycles ?? 0,
          })
          if (statusRes.status === 'completed' && statusRes.statistics) {
            setData(statusRes)
            setLoading(false)
            return
          }
          if (statusRes.status === 'failed') {
            setError(statusRes.error || '回测失败')
            setLoading(false)
            return
          }
          // 继续轮询
          setTimeout(poll, 1500)
        } catch (e: any) {
          setError(e?.message || '获取回测状态失败')
          setLoading(false)
        }
      }

      // 启动第一次轮询
      setTimeout(poll, 1000)
    } catch (e: any) {
      setError(e?.message || '回测请求失败')
      setLoading(false)
    }
  }

  return (
    <div className="binance-card p-6 space-y-6">
      <h2 className="text-xl font-semibold" style={{ color: '#EAECEF' }}>
        规则层回测（用于评估硬规则与模块化提示词的匹配度）
      </h2>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div>
          <label className="block text-sm mb-1" style={{ color: '#848E9C' }}>
            币种列表（逗号分隔）
          </label>
          <input
            value={symbols}
            onChange={(e) => setSymbols(e.target.value)}
            className="w-full px-3 py-2 rounded bg-[#1E2329] text-sm"
            style={{ color: '#EAECEF' }}
          />
        </div>
        <div>
          <label className="block text-sm mb-1" style={{ color: '#848E9C' }}>
            回测天数（向前）
          </label>
          <input
            type="number"
            min={1}
            max={60}
            value={days}
            onChange={(e) => setDays(Number(e.target.value) || 1)}
            className="w-full px-3 py-2 rounded bg-[#1E2329] text-sm"
            style={{ color: '#EAECEF' }}
          />
        </div>
        <div className="flex items-end">
          <button
            onClick={handleRun}
            disabled={loading}
            className="px-6 py-2 rounded font-semibold text-sm"
            style={{
              background: '#F0B90B',
              color: '#000',
              opacity: loading ? 0.7 : 1,
            }}
          >
            {loading ? '回测中…' : '开始回测'}
          </button>
        </div>
      </div>

      {status === 'running' && progress.total > 0 && (
        <div className="text-sm" style={{ color: '#EAECEF' }}>
          已处理周期 {progress.current} / {progress.total}{' '}
          ({((progress.current / progress.total) * 100).toFixed(1)}%)
        </div>
      )}

      {error && (
        <div className="text-sm" style={{ color: '#F6465D' }}>
          {error}
        </div>
      )}

      {data && (
        <div className="space-y-6">
          <div className="flex gap-6 text-sm">
            <div>
              <div style={{ color: '#848E9C' }}>总开仓比例</div>
              <div className="text-lg font-semibold" style={{ color: '#0ECB81' }}>
                {data.open_rate.toFixed(1)}%
              </div>
            </div>
            <div>
              <div style={{ color: '#848E9C' }}>总等待比例</div>
              <div className="text-lg font-semibold" style={{ color: '#F6465D' }}>
                {data.wait_rate.toFixed(1)}%
              </div>
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-6 text-sm">
            <div>
              <h3 className="mb-2 font-semibold" style={{ color: '#EAECEF' }}>
                Top Wait 原因
              </h3>
              {(!data.top_waitReasons || data.top_waitReasons.length === 0) ? (
                <div style={{ color: '#848E9C' }}>暂无数据</div>
              ) : (
                <ul className="space-y-1">
                  {data.top_waitReasons.map((w: BacktestWaitReason) => (
                    <li key={w.reason} className="flex justify-between gap-2">
                      <span style={{ color: '#EAECEF' }}>{w.reason}</span>
                      <span style={{ color: '#848E9C' }}>
                        {w.count} 次 ({w.pct.toFixed(1)}%)
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <div>
              <h3 className="mb-2 font-semibold" style={{ color: '#EAECEF' }}>
                Top 规则触发
              </h3>
              {(!data.top_ruleFails || data.top_ruleFails.length === 0) ? (
                <div style={{ color: '#848E9C' }}>暂无数据</div>
              ) : (
                <ul className="space-y-1">
                  {data.top_ruleFails.map((r: BacktestRuleFailure) => (
                    <li key={r.rule} className="flex justify-between gap-2">
                      <span style={{ color: '#EAECEF' }}>{r.rule}</span>
                      <span style={{ color: '#848E9C' }}>
                        {r.count} 次 ({r.pct.toFixed(1)}%)
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}


