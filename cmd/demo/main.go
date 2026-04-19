package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/demo/weatherdemo"
	"os"
)

func main() {
	var configPath string
	var prompt string
	var verbose bool

	flag.StringVar(&configPath, "config", "./configs/config.yaml", "配置文件路径")
	flag.StringVar(&prompt, "prompt", "帮我查一下我当前位置的天气，并告诉我要不要带伞。", "用户输入")
	flag.BoolVar(&verbose, "verbose", false, "输出完整的路由上下文、tool 注册载荷和状态快照")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	cfg.AI.RoutingMode = "route_and_plan"

	result, err := weatherdemo.Run(context.Background(), cfg, prompt, verbose)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "天气 demo 执行失败: %v\n", err)
		os.Exit(1)
	}

	raw, err := weatherdemo.Marshal(result)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "序列化输出失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(raw))
}
