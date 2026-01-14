#!/bin/bash

# M2.2 执行链路验证脚本
# 用于在真实环境验证限价订单生命周期管理是否正常工作

echo "🔍 M2.2 执行链路验证工具"
echo "=========================="

# 检查是否有决策日志目录
if [ ! -d "decision_logs" ]; then
    echo "❌ 未找到 decision_logs 目录"
    echo "💡 请先运行系统并生成一些交易决策"
    exit 1
fi

echo "📂 扫描 decision_logs 目录中的执行链路..."

# 查找最近的日志文件
LATEST_LOG=$(find decision_logs -name "*.log" -type f -printf '%T@ %p\n' | sort -n | tail -1 | cut -d' ' -f2-)

if [ -z "$LATEST_LOG" ]; then
    echo "❌ 未找到日志文件"
    exit 1
fi

echo "📋 检查最新日志文件: $LATEST_LOG"
echo ""

# 搜索执行链路模式
echo "🔎 搜索 M2.2 执行链路模式..."
echo ""

# 模式1: ExecutionGate 判断
if grep -q "\[ExecutionGate\] mode=limit_only" "$LATEST_LOG"; then
    echo "✅ 找到 ExecutionGate 判断:"
    grep "\[ExecutionGate\] mode=limit_only" "$LATEST_LOG"
    echo ""
else
    echo "❌ 未找到 ExecutionGate limit_only 判断"
fi

# 模式2: 限价开仓尝试
if grep -q "限价.*开.*生命周期管理" "$LATEST_LOG"; then
    echo "✅ 找到生命周期管理触发:"
    grep "限价.*开.*生命周期管理" "$LATEST_LOG"
    echo ""
else
    echo "❌ 未找到生命周期管理触发"
fi

# 模式3: 订单尝试
if grep -q "限价.*尝试.*#" "$LATEST_LOG"; then
    echo "✅ 找到订单尝试记录:"
    grep "限价.*尝试.*#" "$LATEST_LOG"
    echo ""
else
    echo "❌ 未找到订单尝试记录"
fi

# 模式4: 订单挂单
if grep -q "订单已挂.*等待成交" "$LATEST_LOG"; then
    echo "✅ 找到订单挂单记录:"
    grep "订单已挂.*等待成交" "$LATEST_LOG"
    echo ""
else
    echo "❌ 未找到订单挂单记录"
fi

# 模式5: 状态轮询
if grep -q "poll status=" "$LATEST_LOG"; then
    echo "✅ 找到状态轮询记录:"
    grep "poll status=" "$LATEST_LOG"
    echo ""
else
    echo "❌ 未找到状态轮询记录"
fi

# 模式6: 超时取消
if grep -q "超时.*取消订单" "$LATEST_LOG"; then
    echo "✅ 找到超时取消记录:"
    grep "超时.*取消订单" "$LATEST_LOG"
    echo ""
else
    echo "⚠️ 未找到超时取消记录（可能直接成交）"
fi

# 模式7: 重试准备
if grep -q "准备重试" "$LATEST_LOG"; then
    echo "✅ 找到重试记录:"
    grep "准备重试" "$LATEST_LOG"
    echo ""
else
    echo "⚠️ 未找到重试记录（可能第一次就成功）"
fi

# 模式8: 重新定价
if grep -q "重新定价" "$LATEST_LOG"; then
    echo "✅ 找到重新定价记录:"
    grep "重新定价" "$LATEST_LOG"
    echo ""
else
    echo "⚠️ 未找到重新定价记录（可能第一次就成功）"
fi

# 模式9: 最终成交
if grep -q "订单完全成交" "$LATEST_LOG"; then
    echo "✅ 找到最终成交记录:"
    grep "订单完全成交" "$LATEST_LOG"
    echo ""
else
    echo "❌ 未找到最终成交记录"
fi

# 模式10: 生命周期完成
if grep -q "生命周期管理完成" "$LATEST_LOG"; then
    echo "✅ 找到生命周期完成记录:"
    grep "生命周期管理完成" "$LATEST_LOG"
    echo ""
else
    echo "❌ 未找到生命周期完成记录"
fi

echo "🎯 验证总结:"
echo "=============="

# 统计找到的模式数量
patterns_found=$(grep -c -E "(ExecutionGate.*limit_only|限价.*生命周期管理|限价.*尝试.*#|订单已挂.*等待成交|poll status=|超时.*取消订单|准备重试|重新定价|订单完全成交|生命周期管理完成)" "$LATEST_LOG")

if [ "$patterns_found" -ge 3 ]; then
    echo "✅ 执行链路验证通过！找到 $patterns_found 个关键模式"
    echo "🚀 M2.2 限价订单生命周期管理已正常工作"
else
    echo "⚠️ 执行链路验证部分通过，仅找到 $patterns_found 个关键模式"
    echo "💡 可能原因："
    echo "   - 还未触发 limit_only 模式"
    echo "   - 订单直接成交，未经历重试"
    echo "   - 日志级别设置不足"
fi

echo ""
echo "💡 使用建议:"
echo "   1. 确保市场条件触发 ExecutionGate limit_only（低流动性）"
echo "   2. 使用小仓位测试，避免实际成交过大金额"
echo "   3. 检查日志级别，确保 DEBUG 级别日志被记录"
echo "   4. 观察 slippage_bps 和 fee 计算是否正确"
