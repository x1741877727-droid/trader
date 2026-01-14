import type {
  SystemStatus,
  AccountInfo,
  Position,
  PendingOrder,
  DecisionRecord,
  Statistics,
  TraderInfo,
  AIModel,
  Exchange,
  CreateTraderRequest,
  UpdateModelConfigRequest,
  UpdateExchangeConfigRequest,
  CompetitionData,
  CloseReviewSummary,
  CloseReviewFile,
} from '../types';

const API_BASE = '/api';

// Helper function to get auth headers
function getAuthHeaders(): Record<string, string> {
  const token = localStorage.getItem('auth_token');
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };
  
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }
  
  return headers;
}

export const api = {
  // AI交易员管理接口
  async getTraders(): Promise<TraderInfo[]> {
    const res = await fetch(`${API_BASE}/traders`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取trader列表失败');
    return res.json();
  },

  async createTrader(request: CreateTraderRequest): Promise<TraderInfo> {
    const res = await fetch(`${API_BASE}/traders`, {
      method: 'POST',
      headers: getAuthHeaders(),
      body: JSON.stringify(request),
    });
    if (!res.ok) throw new Error('创建交易员失败');
    return res.json();
  },

  async deleteTrader(traderId: string): Promise<void> {
    const res = await fetch(`${API_BASE}/traders/${traderId}`, {
      method: 'DELETE',
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('删除交易员失败');
  },

  async startTrader(traderId: string): Promise<void> {
    const res = await fetch(`${API_BASE}/traders/${traderId}/start`, {
      method: 'POST',
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('启动交易员失败');
  },

  async stopTrader(traderId: string): Promise<void> {
    const res = await fetch(`${API_BASE}/traders/${traderId}/stop`, {
      method: 'POST',
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('停止交易员失败');
  },

  async updateTraderPrompt(traderId: string, customPrompt: string): Promise<void> {
    const res = await fetch(`${API_BASE}/traders/${traderId}/prompt`, {
      method: 'PUT',
      headers: getAuthHeaders(),
      body: JSON.stringify({ custom_prompt: customPrompt }),
    });
    if (!res.ok) throw new Error('更新自定义策略失败');
  },

  async getTraderConfig(traderId: string): Promise<any> {
    const res = await fetch(`${API_BASE}/traders/${traderId}/config`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取交易员配置失败');
    return res.json();
  },

  async updateTrader(traderId: string, request: CreateTraderRequest): Promise<TraderInfo> {
    const res = await fetch(`${API_BASE}/traders/${traderId}`, {
      method: 'PUT',
      headers: getAuthHeaders(),
      body: JSON.stringify(request),
    });
    if (!res.ok) throw new Error('更新交易员失败');
    return res.json();
  },

  // AI模型配置接口
  async getModelConfigs(): Promise<AIModel[]> {
    const res = await fetch(`${API_BASE}/models`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取模型配置失败');
    return res.json();
  },

  // 获取系统支持的AI模型列表（无需认证）
  async getSupportedModels(): Promise<AIModel[]> {
    const res = await fetch(`${API_BASE}/supported-models`);
    if (!res.ok) throw new Error('获取支持的模型失败');
    return res.json();
  },

  async updateModelConfigs(request: UpdateModelConfigRequest): Promise<void> {
    const res = await fetch(`${API_BASE}/models`, {
      method: 'PUT',
      headers: getAuthHeaders(),
      body: JSON.stringify(request),
    });
    if (!res.ok) throw new Error('更新模型配置失败');
  },

  // 交易所配置接口
  async getExchangeConfigs(): Promise<Exchange[]> {
    const res = await fetch(`${API_BASE}/exchanges`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取交易所配置失败');
    return res.json();
  },

  // 获取系统支持的交易所列表（无需认证）
  async getSupportedExchanges(): Promise<Exchange[]> {
    const res = await fetch(`${API_BASE}/supported-exchanges`);
    if (!res.ok) throw new Error('获取支持的交易所失败');
    return res.json();
  },

  async updateExchangeConfigs(request: UpdateExchangeConfigRequest): Promise<void> {
    const res = await fetch(`${API_BASE}/exchanges`, {
      method: 'PUT',
      headers: getAuthHeaders(),
      body: JSON.stringify(request),
    });
    if (!res.ok) throw new Error('更新交易所配置失败');
  },

  // 获取系统状态（支持trader_id）
  async getStatus(traderId?: string): Promise<SystemStatus> {
    const url = traderId
      ? `${API_BASE}/status?trader_id=${traderId}`
      : `${API_BASE}/status`;
    const res = await fetch(url, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取系统状态失败');
    return res.json();
  },

  // 获取账户信息（支持trader_id）
  async getAccount(traderId?: string): Promise<AccountInfo> {
    const url = traderId
      ? `${API_BASE}/account?trader_id=${traderId}`
      : `${API_BASE}/account`;
    const res = await fetch(url, {
      cache: 'no-store',
      headers: {
        ...getAuthHeaders(),
        'Cache-Control': 'no-cache',
      },
    });
    if (!res.ok) throw new Error('获取账户信息失败');
    const data = await res.json();
    console.log('Account data fetched:', data);
    return data;
  },

  // 获取持仓列表（支持trader_id）
  async getPositions(traderId?: string): Promise<Position[]> {
    const url = traderId
      ? `${API_BASE}/positions?trader_id=${traderId}`
      : `${API_BASE}/positions`;
    const res = await fetch(url, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取持仓列表失败');
    return res.json();
  },

  async getPendingOrders(traderId?: string): Promise<PendingOrder[]> {
    if (!traderId) {
      return [];
    }
    const params = new URLSearchParams();
    params.append('trader_id', traderId);
    const res = await fetch(`${API_BASE}/pending-orders?${params.toString()}`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取限价单列表失败');
    const data = await res.json();
    return data?.pending_orders ?? [];
  },

  // 获取决策日志（支持trader_id）
  async getDecisions(traderId?: string): Promise<DecisionRecord[]> {
    const url = traderId
      ? `${API_BASE}/decisions?trader_id=${traderId}`
      : `${API_BASE}/decisions`;
    const res = await fetch(url, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取决策日志失败');
    return res.json();
  },

  // 获取最新决策（支持trader_id）
  async getLatestDecisions(traderId?: string): Promise<DecisionRecord[]> {
    const url = traderId
      ? `${API_BASE}/decisions/latest?trader_id=${traderId}`
      : `${API_BASE}/decisions/latest`;
    const res = await fetch(url, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取最新决策失败');
    return res.json();
  },

  // 获取统计信息（支持trader_id）
  async getStatistics(traderId?: string): Promise<Statistics> {
    const url = traderId
      ? `${API_BASE}/statistics?trader_id=${traderId}`
      : `${API_BASE}/statistics`;
    const res = await fetch(url, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取统计信息失败');
    return res.json();
  },

  // 获取收益率历史数据（支持trader_id）
  async getEquityHistory(traderId?: string): Promise<any[]> {
    const url = traderId
      ? `${API_BASE}/equity-history?trader_id=${traderId}`
      : `${API_BASE}/equity-history`;
    const res = await fetch(url, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取历史数据失败');
    return res.json();
  },

  // 获取AI学习表现分析（支持trader_id）
  async getPerformance(traderId?: string): Promise<any> {
    const url = traderId
      ? `${API_BASE}/performance?trader_id=${traderId}`
      : `${API_BASE}/performance`;
    const res = await fetch(url, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取AI学习数据失败');
    return res.json();
  },

  // 获取竞赛数据
  async getCompetition(): Promise<CompetitionData> {
    const res = await fetch(`${API_BASE}/competition`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取竞赛数据失败');
    return res.json();
  },

  // 用户信号源配置接口
  async getUserSignalSource(): Promise<{coin_pool_url: string, oi_top_url: string}> {
    const res = await fetch(`${API_BASE}/user/signal-sources`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取用户信号源配置失败');
    return res.json();
  },

  async saveUserSignalSource(coinPoolUrl: string, oiTopUrl: string): Promise<void> {
    const res = await fetch(`${API_BASE}/user/signal-sources`, {
      method: 'POST',
      headers: getAuthHeaders(),
      body: JSON.stringify({
        coin_pool_url: coinPoolUrl,
        oi_top_url: oiTopUrl,
      }),
    });
    if (!res.ok) throw new Error('保存用户信号源配置失败');
  },

  // K线数据接口
  async getKlines(symbol: string, interval: string = '4h', limit: number = 100): Promise<{
    symbol: string;
    interval: string;
    klines: Array<{
      time: number;
      open: number;
      high: number;
      low: number;
      close: number;
      volume: number;
      ema20?: number;
      ema50?: number;
      ema200?: number;
      macd?: number;
      rsi?: number;
      bb_upper?: number;
      bb_middle?: number;
      bb_lower?: number;
    }>;
  }> {
    const res = await fetch(`${API_BASE}/klines?symbol=${symbol}&interval=${interval}&limit=${limit}`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取K线数据失败');
    return res.json();
  },

  // 回测接口（规则层）
  async runBacktestStart(body: {
    symbols: string[];
    start: string;
    end: string;
    interval_minutes: number;
  }): Promise<any> {
    const res = await fetch(`${API_BASE}/backtest`, {
      method: 'POST',
      headers: getAuthHeaders(),
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || '回测失败');
    }
    return res.json();
  },

  async getBacktestStatus(jobId: string): Promise<any> {
    const params = new URLSearchParams();
    params.append('job_id', jobId);
    const res = await fetch(`${API_BASE}/backtest/status?${params.toString()}`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text || '获取回测状态失败: ' + text);
    }
    return res.json();
  },

  async getCycleChecks(traderId?: string, limit: number = 15): Promise<{
    trader_id: string;
    limit: number;
    records: DecisionRecord[];
  }> {
    const params = new URLSearchParams();
    if (traderId) params.append('trader_id', traderId);
    if (limit) params.append('limit', String(limit));
    const res = await fetch(`${API_BASE}/cycle-check?${params.toString()}`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取cycle check记录失败');
    return res.json();
  },

  async getCloseReviews(traderId?: string, limit: number = 50): Promise<CloseReviewSummary[]> {
    const params = new URLSearchParams();
    if (traderId) params.append('trader_id', traderId);
    if (limit) params.append('limit', String(limit));
    const res = await fetch(`${API_BASE}/close-reviews?${params.toString()}`, {
      headers: getAuthHeaders(),
    });
    if (!res.ok) throw new Error('获取close review列表失败');
    const data = await res.json();
    return data.items ?? [];
  },

  async getCloseReview(tradeId: string, traderId?: string): Promise<{
    summary: CloseReviewSummary | null;
    detail: CloseReviewFile | null;
  }> {
    const params = new URLSearchParams();
    if (traderId) params.append('trader_id', traderId);
    const res = await fetch(`${API_BASE}/trades/${tradeId}/close-review?${params.toString()}`, {
      headers: getAuthHeaders(),
    });
    if (res.status === 404) {
      return { summary: null, detail: null };
    }
    if (!res.ok) throw new Error('获取close review详情失败');
    return res.json();
  },

  async saveCloseReview(tradeId: string, payload: CloseReviewFile, traderId?: string): Promise<CloseReviewSummary> {
    const params = new URLSearchParams();
    if (traderId) params.append('trader_id', traderId);
    const res = await fetch(`${API_BASE}/trades/${tradeId}/close-review?${params.toString()}`, {
      method: 'POST',
      headers: getAuthHeaders(),
      body: JSON.stringify(payload),
    });
    if (!res.ok) throw new Error('保存close review失败');
    const data = await res.json();
    return data.summary;
  },

  async generateReview(tradeId: string, traderId?: string, force: boolean = false): Promise<{
    summary: CloseReviewSummary;
    detail?: CloseReviewFile;
  }> {
    const params = new URLSearchParams();
    if (traderId) params.append('trader_id', traderId);
    if (force) params.append('force', 'true');
    const res = await fetch(`${API_BASE}/trades/${tradeId}/review?${params.toString()}`, {
      method: 'POST',
      headers: getAuthHeaders(),
    });
    if (!res.ok) {
      const errorData = await res.json().catch(() => ({ error: '生成复盘失败' }));
      throw new Error(errorData.error || '生成复盘失败');
    }
    return res.json();
  },

  // AI 实时思考流（SSE）
  createAIStream(traderId: string, onMessage: (data: any) => void, onError?: (error: Error) => void): EventSource {
    const token = localStorage.getItem('auth_token');
    const url = `${API_BASE}/ai/stream?trader_id=${traderId}${token ? `&token=${token}` : ''}`;
    
    const eventSource = new EventSource(url);
    
    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        onMessage(data);
      } catch (err) {
        console.error('解析 SSE 消息失败:', err);
      }
    };
    
    eventSource.onerror = (error) => {
      console.error('SSE 连接错误:', error);
      if (onError) {
        onError(new Error('SSE 连接失败'));
      }
      eventSource.close();
    };
    
    return eventSource;
  },
};
