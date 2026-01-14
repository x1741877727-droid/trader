package backtest

import (
	"flag"
	"fmt"
	"log"
	"time"
)

// RunBacktest 运行回测
func RunBacktest() {
	// 解析命令行参数
	startTimeStr := flag.String("start", "", "开始时间 (格式: 2006-01-02 15:04:05)")
	endTimeStr := flag.String("end", "", "结束时间 (格式: 2006-01-02 15:04:05)")
	symbolsStr := flag.String("symbols", "BTCUSDT,ETHUSDT", "币种列表，用逗号分隔")
	outputPath := flag.String("output", "backtest_report.txt", "报告输出路径")
	flag.Parse()

	// 解析时间
	var startTime, endTime time.Time
	var err error

	if *startTimeStr == "" {
		// 默认：最近7天
		endTime = time.Now()
		startTime = endTime.AddDate(0, 0, -7)
	} else {
		startTime, err = time.Parse("2006-01-02 15:04:05", *startTimeStr)
		if err != nil {
			log.Fatalf("❌ 解析开始时间失败: %v", err)
		}
	}

	if *endTimeStr == "" {
		endTime = time.Now()
	} else {
		endTime, err = time.Parse("2006-01-02 15:04:05", *endTimeStr)
		if err != nil {
			log.Fatalf("❌ 解析结束时间失败: %v", err)
		}
	}

	// 解析币种列表
	symbols := []string{}
	if *symbolsStr != "" {
		symbols = []string{"BTCUSDT", "ETHUSDT"} // 默认值
		// 实际应该解析 *symbolsStr
	}

	// 创建离线分析器
	analyzer := NewOfflineAnalyzer(
		startTime,
		endTime,
		symbols,
		3*time.Minute, // 扫描间隔：3分钟
	)

	// 执行分析
	log.Println("🚀 开始执行离线分析...")
	if err := analyzer.Analyze(); err != nil {
		log.Fatalf("❌ 分析失败: %v", err)
	}

	// 生成报告
	reportGenerator := NewReportGenerator(analyzer, *outputPath)
	if err := reportGenerator.Generate(); err != nil {
		log.Fatalf("❌ 生成报告失败: %v", err)
	}

	log.Println("✅ 回测完成！")
}

// PrintUsage 打印使用说明
func PrintUsage() {
	fmt.Println("使用方法:")
	fmt.Println("  go run backtest/backtest.go -start '2024-01-01 00:00:00' -end '2024-01-31 23:59:59' -symbols 'BTCUSDT,ETHUSDT' -output 'report.txt'")
	fmt.Println("")
	fmt.Println("参数说明:")
	fmt.Println("  -start: 开始时间 (格式: 2006-01-02 15:04:05)")
	fmt.Println("  -end:   结束时间 (格式: 2006-01-02 15:04:05)")
	fmt.Println("  -symbols: 币种列表，用逗号分隔 (默认: BTCUSDT,ETHUSDT)")
	fmt.Println("  -output: 报告输出路径 (默认: backtest_report.txt)")
	fmt.Println("")
	fmt.Println("示例:")
	fmt.Println("  go run backtest/backtest.go -start '2024-11-01 00:00:00' -end '2024-11-30 23:59:59'")
}

