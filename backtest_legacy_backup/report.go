package backtest

import (
	"fmt"
	"os"
	"time"
)

// ReportGenerator 报告生成器
type ReportGenerator struct {
	analyzer   *OfflineAnalyzer
	outputPath string
}

// NewReportGenerator 创建报告生成器
func NewReportGenerator(analyzer *OfflineAnalyzer, outputPath string) *ReportGenerator {
	return &ReportGenerator{
		analyzer:   analyzer,
		outputPath: outputPath,
	}
}

// Generate 生成测试报告
func (rg *ReportGenerator) Generate() error {
	stats := rg.analyzer.GetStatistics()

	report := rg.buildReport(stats)

	// 写入文件
	file, err := os.Create(rg.outputPath)
	if err != nil {
		return fmt.Errorf("创建报告文件失败: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString(report)
	if err != nil {
		return fmt.Errorf("写入报告失败: %w", err)
	}

	fmt.Printf("✅ 测试报告已生成: %s\n", rg.outputPath)
	return nil
}

// buildReport 构建报告内容
func (rg *ReportGenerator) buildReport(stats *Statistics) string {
	var report string

	report += "═══════════════════════════════════════════════════════════════\n"
	report += "           策略测试报告（规则引擎 + 离线分析）\n"
	report += "═══════════════════════════════════════════════════════════════\n\n"

	// 总体统计
	report += "【总体统计】\n"
	report += fmt.Sprintf("总周期数: %d\n", stats.TotalCycles)
	report += fmt.Sprintf("总决策数: %d\n", stats.TotalDecisions)
	report += fmt.Sprintf("Wait决策数: %d (%.2f%%)\n", stats.WaitDecisions, stats.GetWaitRate())
	report += fmt.Sprintf("开仓决策数: %d (%.2f%%)\n", stats.OpenDecisions, stats.GetOpenRate())
	report += "\n"

	// Top Wait原因
	report += "【Top 10 Wait原因】\n"
	topReasons := stats.GetTopWaitReasons(10)
	for i, reason := range topReasons {
		report += fmt.Sprintf("%d. %s: %d次 (%.2f%%)\n", i+1, reason.Reason, reason.Count, reason.Pct)
	}
	report += "\n"

	// Top 规则失败
	report += "【Top 10 规则失败】\n"
	topFailures := stats.GetTopRuleFailures(10)
	for i, failure := range topFailures {
		report += fmt.Sprintf("%d. %s: %d次 (%.2f%%)\n", i+1, failure.Rule, failure.Count, failure.Pct)
	}
	report += "\n"

	// 按币种统计
	report += "【按币种统计】\n"
	for symbol, symbolStats := range stats.BySymbol {
		report += fmt.Sprintf("\n%s:\n", symbol)
		report += fmt.Sprintf("  总决策数: %d\n", symbolStats.TotalDecisions)
		report += fmt.Sprintf("  Wait决策数: %d (%.2f%%)\n", symbolStats.WaitDecisions, float64(symbolStats.WaitDecisions)/float64(symbolStats.TotalDecisions)*100)
		report += fmt.Sprintf("  开仓决策数: %d (%.2f%%)\n", symbolStats.OpenDecisions, float64(symbolStats.OpenDecisions)/float64(symbolStats.TotalDecisions)*100)
		
		if len(symbolStats.WaitReasons) > 0 {
			report += "  Top Wait原因:\n"
			type reasonCount struct {
				Reason string
				Count  int
			}
			reasons := make([]reasonCount, 0, len(symbolStats.WaitReasons))
			for reason, count := range symbolStats.WaitReasons {
				reasons = append(reasons, reasonCount{Reason: reason, Count: count})
			}
			// 简单排序
			for i := 0; i < len(reasons)-1; i++ {
				for j := i + 1; j < len(reasons); j++ {
					if reasons[i].Count < reasons[j].Count {
						reasons[i], reasons[j] = reasons[j], reasons[i]
					}
				}
			}
			for i := 0; i < 5 && i < len(reasons); i++ {
				report += fmt.Sprintf("    - %s: %d次\n", reasons[i].Reason, reasons[i].Count)
			}
		}
	}
	report += "\n"

	// 错过的机会统计
	report += "【错过的机会统计】\n"
	report += fmt.Sprintf("总错过机会数: %d\n", len(stats.OpportunityMissed))
	if len(stats.OpportunityMissed) > 0 {
		report += "前10个错过的机会:\n"
		for i := 0; i < 10 && i < len(stats.OpportunityMissed); i++ {
			opp := stats.OpportunityMissed[i]
			report += fmt.Sprintf("  %d. %s %s @ %.2f - %s (%s)\n", 
				i+1, opp.Time.Format("2006-01-02 15:04:05"), opp.Symbol, opp.Price, opp.Reason, opp.MarketState)
		}
	}
	report += "\n"

	// 分析结论
	report += "【分析结论】\n"
	waitRate := stats.GetWaitRate()
	if waitRate > 90 {
		report += "⚠️  开仓严格程度: 非常高（wait率>90%），可能过于严格，错过很多机会\n"
	} else if waitRate > 70 {
		report += "⚠️  开仓严格程度: 较高（wait率70-90%），可能偏严格\n"
	} else if waitRate > 50 {
		report += "✅ 开仓严格程度: 中等（wait率50-70%），较为合理\n"
	} else {
		report += "✅ 开仓严格程度: 较低（wait率<50%），较为宽松\n"
	}

	report += "\n"
	report += "═══════════════════════════════════════════════════════════════\n"
	report += fmt.Sprintf("报告生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	report += "═══════════════════════════════════════════════════════════════\n"

	return report
}

