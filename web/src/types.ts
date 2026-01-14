export interface SystemStatus {
  trader_id: string;
  trader_name: string;
  ai_model: string;
  is_running: boolean;
  start_time: string;
  runtime_minutes: number;
  call_count: number;
  initial_balance: number;
  scan_interval: string;
  stop_until: string;
  last_reset_time: string;
  ai_provider: string;
}

export interface AccountInfo {
  total_equity: number;
  wallet_balance: number;
  unrealized_profit: number;
  available_balance: number;
  total_pnl: number;
  total_pnl_pct: number;
  total_unrealized_pnl: number;
  initial_balance: number;
  daily_pnl: number;
  position_count: number;
  margin_used: number;
  margin_used_pct: number;
}

export interface Position {
  symbol: string;
  side: string;
  entry_price: number;
  mark_price: number;
  quantity: number;
  leverage: number;
  unrealized_pnl: number;
  unrealized_pnl_pct: number;
  liquidation_price: number;
  margin_used: number;
  // 止损止盈字段（可选，可能为空）
  stop_loss?: number;
  tp1?: number;
  tp2?: number;
  tp3?: number;
}

export interface PendingOrder {
  symbol: string;
  side: string;
  limit_price: number;
  quantity: number;
  leverage: number;
  order_id: number;
  tp1?: number;
  tp2?: number;
  tp3?: number;
  stop_loss?: number;
  take_profit?: number;
  create_time?: number;
  duration_min?: number;
  confidence?: number;
  reasoning?: string;
}

export interface DecisionAction {
  action: string;
  symbol: string;
  quantity: number;
  leverage: number;
  price: number;
  order_id: number;
  timestamp: string;
  success: boolean;
  error?: string;
}

export interface AccountSnapshot {
  total_balance: number;
  available_balance: number;
  total_unrealized_profit: number;
  position_count: number;
  margin_used_pct: number;
}

// 验证错误详情
export interface ValidationError {
  symbol: string;
  action: string;
  reason: string;
}

export interface DecisionRecord {
  timestamp: string;
  cycle_number: number;
  input_prompt: string;
  cot_trace: string;
  decision_json: string;
  account_state: AccountSnapshot;
  positions: any[];
  candidate_coins: string[];
  decisions: DecisionAction[];
  execution_log: string[];
  success: boolean;
  error_message?: string;
  status?: string; // "ok" | "warning" | "error"
  error_type?: string;
  error_severity?: string; // "warning" | "error"
  validation_errors?: ValidationError[];
}

export interface Statistics {
  total_cycles: number;
  successful_cycles: number;
  failed_cycles: number;
  total_open_positions: number;
  total_close_positions: number;
}

// AI Trading相关类型
export interface TraderInfo {
  trader_id: string;
  trader_name: string;
  ai_model: string;
  exchange_id?: string;
  is_running?: boolean;
  custom_prompt?: string;
  candidate_coins?: string[];
}

export interface AIModel {
  id: string;
  name: string;
  provider: string;
  enabled: boolean;
  apiKey?: string;
  customApiUrl?: string;
  customModelName?: string;
}

export interface Exchange {
  id: string;
  name: string;
  type: 'cex' | 'dex';
  enabled: boolean;
  apiKey?: string;
  secretKey?: string;
  testnet?: boolean;
  // Hyperliquid 特定字段
  hyperliquidWalletAddr?: string;
  // Aster 特定字段
  asterUser?: string;
  asterSigner?: string;
  asterPrivateKey?: string;
}

export interface CreateTraderRequest {
  name: string;
  ai_model_id: string;
  exchange_id: string;
  initial_balance: number;
  btc_eth_leverage?: number;
  altcoin_leverage?: number;
  trading_symbols?: string;
  custom_prompt?: string;
  override_base_prompt?: boolean;
  system_prompt_template?: string;
  is_cross_margin?: boolean;
  use_coin_pool?: boolean;
  use_oi_top?: boolean;
}

export interface UpdateModelConfigRequest {
  models: {
    [key: string]: {
      enabled: boolean;
      api_key: string;
      custom_api_url?: string;
      custom_model_name?: string;
    };
  };
}

export interface UpdateExchangeConfigRequest {
  exchanges: {
    [key: string]: {
      enabled: boolean;
      api_key: string;
      secret_key: string;
      testnet?: boolean;
      // Hyperliquid 特定字段
      hyperliquid_wallet_addr?: string;
      // Aster 特定字段
      aster_user?: string;
      aster_signer?: string;
      aster_private_key?: string;
    };
  };
}

// Competition related types
export interface CompetitionTraderData {
  trader_id: string;
  trader_name: string;
  ai_model: string;
  exchange: string;
  total_equity: number;
  total_pnl: number;
  total_pnl_pct: number;
  position_count: number;
  margin_used_pct: number;
  is_running: boolean;
}

export interface CompetitionData {
  traders: CompetitionTraderData[];
  count: number;
}

export interface CloseReviewActionItem {
  owner: string;
  item: string;
  due: string;
}

export interface CloseReviewRecord {
  trade_id: string;
  symbol: string;
  side: string;
  pnl: number;
  pnl_pct: number;
  holding_minutes: number;
  risk_score: number;
  execution_score: number;
  signal_score: number;
  summary: string;
  what_went_well: string[];
  improvements: string[];
  root_cause: string;
  extreme_intervention_review: string;
  action_items: CloseReviewActionItem[];
  confidence: number;
  reasoning: string;
}

export interface CloseReviewSummary extends CloseReviewRecord {
  trader_id: string;
  file_path: string;
  created_at: string;
}

export interface PositionLifecycleEntry {
  cycle_number: number;
  timestamp: string;
  action: string;
  reasoning: string;
  metadata?: Record<string, unknown>;
}

export interface MarketContextAtClose {
  account_state?: Record<string, unknown>;
  market_data?: Record<string, unknown>;
  runtime_meta?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface TradeSnapshot {
  trade_id: string;
  symbol: string;
  side: string;
  entry_time: string;
  exit_time: string;
  entry_price: number;
  exit_price: number;
  quantity: number;
  leverage: number;
  risk_usd: number;
  pnl: number;
  pnl_pct: number;
  holding_minutes: number;
  stop_loss?: number;
  take_profit?: number;
}

export interface CloseReviewFile {
  version: number;
  timestamp: string;
  trade_snapshot: TradeSnapshot;
  request_snapshot?: Record<string, unknown>;
  position_lifecycle: PositionLifecycleEntry[];
  major_decisions?: PositionLifecycleEntry[];
  market_context: MarketContextAtClose;
  cot_trace: string;
  review: CloseReviewRecord;
  additional_metadata?: Record<string, unknown>;
}

// Trader Configuration Data for View Modal
export interface TraderConfigData {
  trader_id?: string;
  trader_name: string;
  ai_model: string;
  exchange_id: string;
  btc_eth_leverage: number;
  altcoin_leverage: number;
  trading_symbols: string;
  custom_prompt: string;
  override_base_prompt: boolean;
  system_prompt_template: string; // 添加系统提示词模板字段
  is_cross_margin: boolean;
  use_coin_pool: boolean;
  use_oi_top: boolean;
  initial_balance: number;
  is_running: boolean;
}
