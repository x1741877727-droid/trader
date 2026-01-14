# 模块化提示词 — 全面修复与优化方案

说明：本文件给出对现有模块化提示词（prompts/modules）的一致化、职责划分、冲突点清理与逐步实施方案。目标是让每个模块职责单一、相互引用而不冲突，并把“机器可读字段/校验”集中到唯一位置供后端与流程层使用。

一、总体设计原则
- 单一事实源（Single Source of Truth）：所有机器可读输出字段（JSON schema）与所有量化阈值必须集中管理（建议放在 `OutputFormat` 的 schema 或独立 `MachineOutputSchema.txt` 与 `RiskManagement.txt`）。其他模块仅引用字段名和规则 id，不重复定义数值或必填逻辑。
- 模块职责清晰：每个模块只做其标注的事（SystemCore=角色/哲学，CoreTradingRules=高层原则，RiskManagement=硬阈值与校验，OpenSetup/HoldPlaybook=流程层决策流程，TechnicalIndicators=证据字典，TradingStrategyMatrix=语义策略框架，DecisionChecklist=思维自检），并在头部写明“本模块不重复定义机器字段”。
- 流程层优先：buildModularSystemPrompt 的拼接顺序即执行优先级；模块内容应假设自己会被拼接在合适的位置，不自行切换分支逻辑。
- 授权与例外：把“可推翻的教练建议”明确为 `coach_advice`，把“后端强检”明确为 `hard_check`，AI 若要例外须在 JSON 填写 `exception_reason` 与 `additional_risk_controls`，并由后端决定是否允许执行。

二、当前需要修复的代码点（已完成/待完成）
- 已完成：在 `decision/engine.go` 中移除对已删除模块 `ChanTheory` 与 `MarketStateAndTrend` 的 append（对应模块文件已被删除）。证据字段仍通过 `TechnicalIndicators`、`RiskManagement` 提供给 AI。
- 待完成（建议优先级）：
  1. 在 `prompts/modules/OutputFormat.txt` 或新建 `prompts/modules/MachineOutputSchema.txt` 中放置权威 JSON schema（versioned），并在所有模块中改为“引用 schema 字段”。
  2. 清理所有模块中重复的 JSON 示例，所有示例应引用 schema 并标注版本号。
  3. 优化 `CoreTradingRules` 的规则6（急跌/急涨后反弹）为更明确的判断要素与例外流程（见下）。
  4. 在 `RiskManagement.txt` 中加入“阈值使用指南”：每个常量配一行“如何被后端/AI 使用”的简单示例（计算流程、何时触发 wait）。
  5. 将 `TechnicalIndicators` 中 137-184 行的“证据组合与输出模板”转为只引用 `OutputFormat` 的 `strategy_evidence` 字段，并把“probe”与“sizing_hint”语义写入 TechnicalIndicators 的解读部分而非重复模板定义。

三、各模块职责（建议的最终划分）
- `SystemCore.txt`：系统定位、AI 身份与高层哲学（不写流程逻辑、只说明输出风格与权责）。  
- `CoreTradingRules.txt`：高阶原则（趋势阶段、共振、逆势例外、高风险触发）与哪些行为属于“硬禁/高风险”——不包含数值阈值。  
- `RiskManagement.txt`：唯一的数值阈值与计算公式（MAX_MARGIN_PCT、MIN_RR、ATR 校准、连续止损触发、仓位/频率限制、JSON 必填项清单）；并附“阈值使用示例”段（如何计算 final_margin_usd、如何判定 stop_vs_atr 超标并返回 wait）。  
- `OutputFormat.txt`（或 `MachineOutputSchema.txt`）：权威的机器输出 JSON schema（versioned），列出必填/可选/后端强检字段，并给出最小化示例（token 友好）。所有模块引用该文件中字段名与必填规则。  
- `DecisionChecklist.txt`：AI 的思维自检流程（顺序化），并把每一步映射到 `OutputFormat` 字段（例如 step4 → `position_size_usd`、`stop_vs_atr` 等）。不重复定义 JSON。  
- `OpenSetup.txt`：无持仓时的分析步骤与必须输出的 evidence list；引用 `OutputFormat` schema 填充；强调 execution_assumptions（流动性/slippage/order_type）。保留 minimal self‑check。  
- `HoldPlaybook.txt`、`PositionManagement.txt`：有持仓时的行为指南与 JSON action 映射（close/partial_close/update_stop_loss），引用 `OutputFormat`。  
- `TradingStrategyMatrix.txt`：策略语义框架（A~E）与所需证据字段列表（只列字段路径，不写 JSON 模版）。策略匹配输出需填 `strategy_choice`、`evidence`（字段路径形式）与 `confidence`，但真正的 action 由流程层决定。  
- `TechnicalIndicators.txt`：证据字典（字段路径、语义、典型场景、解读写法），并给出证据组合建议；移除任何重复的 machine‑readable 模板（只引用 schema）。  
- `StructureRules.txt`：结构识别规则（HH/HL/BOS/CHoCH/中枢定义），供 TechnicalIndicators 与 StrategyMatrix 使用。  
- `TradeReview.txt` / `BestPractices.txt` / `MultiAssetOpportunityScan.txt`：辅助模块，非流程核心，保留为参考内容。

四、关于 `RiskManagement` 的阈值如何“被使用”的说明（对你目前混淆点的解答）
- 用法说明（一步步）：
  1) AI 在输出 JSON 时只填写 semantic/technicals 与期望的 `position_size_usd`、`stop_anchor`、`stop_price`、`tp1/2/3` 等字段（参见 schema）。  
  2) 后端在接到决策前先运行 `RiskManagement` 中的流程：例如计算 `max_margin_usd = floor(total_equity × MAX_MARGIN_PCT)`，然后根据 AI 提供的 `target_pct` 或 `position_size_usd` 做最终 `final_margin_usd = min(plan_margin_usd, max_margin_usd, available_balance)`。若 mismatch → 返回 error/拒绝（或以 action=wait）。  
  3) 对于 stop_vs_atr、R:R 等验证，后端会用 AI 填写的 `stop_price`/`tpX` 与市场数据计算实际比率并判断是否满足 `MIN_RR` 或 `stop_vs_atr` 的上下限；若不满足 → 拒绝或让 AI 重试（engine 会触发格式修复/重试流程）。  
  4) 因此：AI 不需要在自然语言提示词中重新定义阈值，只需按 `RiskManagement` 的字段要求输出数值（并在 reasoning 中写出关键计算项，例如 `equity=XX available=YY max_margin=ZZ plan_margin=AA final_margin=BB`）。  

五、对 `CoreTradingRules` 中规则6（急跌/急涨后反弹）的具体优化建议（可直接替换段落）
- 建议把规则6 拆为三部分：A) 场景识别条件（语义式，举例 2–3 条）；B) 参与决策流程（需要哪些短期确认）；C) 例外与风控（小仓/更紧止损/限价）。并在末尾加入“后端处理流程”说明（若 AI 判定参与，后端将强校验 position_size_usd ≤ final_margin 和 stop_vs_atr 限制）。  
- 示例（可直接替换到 CoreTradingRules）：
  * 识别要点（任意 2 条满足 → 倾向参与）：
    - 4h 出现 ≥ 2% 的短期急跌/急涨（可配置为 4h 最近 N 根 K 线幅度阈值）；
    - 1h/15m 出现明确的结构性回调或反弹（BOS/CHoCH/回踩到关键 OB）；
    - 成交量或衍生品出现配合信号（TradeCount/VolumePercentile 提升 或 OI/Funding 无明显反向冲突）。
  * 参与决策（必须至少满足 2–3 项确认）：
    - 1h 出现回调确认（价格稳定在支撑/OB 附近并出现 15m 的右侧确认信号）；  
    - 15m 动能回归或短周期形成 HL/LH 支撑（用于触发入场）；  
    - 衍生品与流动性条件未放大风险（large_taker_volume 未异常放大或 OI 与价格方向共振）。
  * 风控与执行：  
    - 只允许小仓或 probe（sizing_hint="probe"），并在 JSON 填写 `sizing_hint` 与 `execution_assumptions`；  
    - 强制更紧 stop_vs_atr（由 RiskManagement 校验）；  
    - 若任一后端校验失败 → action=wait。

六、实施步骤（优先级与时间表）
1) 立即（已完成）：从 `decision/engine.go` 中移除对已删除模块的拼接引用。  
2) 本轮（高优先）：在 `prompts/modules` 中建立 `MachineOutputSchema.txt`（或把 schema 加入 `OutputFormat.txt` 的开头），并把所有模块内的 JSON 示例替换为“引用 schema v1” 的说明。  
3) 本轮（中优先）：在 `RiskManagement.txt` 里为每个阈值增加“使用示例 + 触发后端动作”的短语。  
4) 下一步：把 `TechnicalIndicators` 中证据组合/输出模板统一改为仅“证据解释”，把输出模板完全移至 schema。  
5) 验证：运行一次端到端流程（AI 模拟输出 → parse → validateDecisions）并修复任何字段不一致问题。  
6) 收尾：把 `CoreTradingRules` 规则6 改为上述新版，QA 测试并在 docs 中保留变更日志。

七、交付物（我会给你的）
- `prompts/modules/MachineOutputSchema.txt`（或更新 `OutputFormat.txt`）— 权威 JSON schema（v1）。  
- `docs/modular_prompt_fix_plan.md`（本文件）— 你现在看到的修复计划。  
- 若你同意：我会把各模块中重复的 JSON 定义替换为对 schema 的引用（逐文件提交 edits），并把 `RiskManagement` 的“阈值使用示例”添加到文件中。

----  
若你同意此方向，我将下一步：  
  A) 创建 `MachineOutputSchema.txt`（或把 schema 写入 `OutputFormat.txt` 开头），并提交修改；  
  B) 把 `TechnicalIndicators` / `TradingStrategyMatrix` 中的重复模板删除并替换为引用；  
  C) 将 `CoreTradingRules` 中规则6 更新为建议文本并提交。  

请确认或指出优先级调整（例如先做 schema 或先修规则6）。  



八、附录：什么是 schema？以及 MachineOutputSchema 字段中文说明（便于理解）

什么是 schema？
- 简单来说，schema 就是“机器可读的字段规范”——定义哪些字段可以出现在 AI 输出的 JSON 中、字段的数据类型、哪些是必须的、哪些是可选的以及字段语义。后端和解析器会根据这个 schema 去解析、校验和执行 AI 的决策。把 schema 作为唯一事实源可以避免不同模块对同一字段产生语义冲突。

八、附录（更新版）：最小必填 schema 与字段中文说明

为什么改为最小必填？
- 为了减少 token、降低 AI 输出对格式的依赖，以及避免 AI 被“格式完备”卡住，让思维链（reasoning）保留详细计算与证据，JSON 只提供后端**绝对必须**的执行字段。

最小必填字段（后端校验所需，必须出现在 JSON）：
- `version`：schema 版本号（例如 "v1"）
- `action`：动作，示例 "open_long" / "open_short" / "limit_open_long" / "wait" / "partial_close_long" / "close_long" / "update_stop_loss"
- `symbol`：交易对，如 "BTCUSDT"
- `leverage`：杠杆（仅开仓时必填，整数）
- `position_size_usd`：本笔单实际占用的保证金（USDT，开仓时必填）
- `stop_price`：止损价格（开仓时必填；若仅提供 stop_anchor，则后端需能解析）
- `tp1`,`tp2`,`tp3`：三个分批止盈价位（开仓时必填，TP3 为最终 take_profit）
- `confidence`：置信度（0-100，整数）
- `reasoning`：简洁人类可读决策理由（必须，包含关键计算摘要）

可选字段（仅在特定动作或需要时出现，默认放入 reasoning 也可）：
- `limit_price`（限价挂单时使用）
- `order_id`（取消限价单时使用）
- `close_quantity` 或 `close_ratio`（部分平仓）
- `intervention_level`（"extreme" 时需严格理由）
- `execution_assumptions`（如流动性、预期滑点，建议放在 reasoning）
- `evidence`（可选；若需要快速自动审计可提供，否则放在 reasoning）

最小 JSON 示例：
{
  "version":"v1",
  "action":"open_long",
  "symbol":"BTCUSDT",
  "leverage":65,
  "position_size_usd":1000,
  "stop_price":88300,
  "tp1":92000,
  "tp2":93000,
  "tp3":94000,
  "confidence":80,
  "reasoning":"4h BOS 成立；15m 动能回归；计算：equity=10000 available=8000 plan_margin=1000 final_margin=1000"
}

说明与注意事项：
- 详细计算（如 stop_vs_atr、rr_ratio、equity/available/final_margin 的逐步计算）放在 reasoning；后端会从 reasoning 或市场数据中再次验证关键数值（例如 final_margin_usd）。  
- 若后端校验失败（例如 position_size_usd 超过 max margin），后端会拒绝执行并要求 AI 重试（engine 已实现格式纠错/重试流程）。  
- schema 仍需要版本化（`version` 字段），便于后端同步校验规则的变更。

如果你同意，我会：
 A) 把 `prompts/modules/OutputFormat.txt` 中的 JSON 结构改为上述“最小必填版”；  
 B) 更新 `docs/modular_prompt_fix_plan.md`（已完成）并保持两者一致。  
 请确认我现在执行 A。  
