package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/admin/ai_project/internal/app"
	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/state"
	"github.com/admin/ai_project/plugins/builtin"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "./configs/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	application, err := app.New(ctx, cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "init app: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = application.Close(context.Background())
	}()
	if err := builtin.RegisterAll(application.Registry); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "register builtin nodes: %v\n", err)
		os.Exit(1)
	}

	st, err := state.New("demo-task", platform.NewTraceID(), state.UserInput{
		Text:     "Summarize this framework execution path.",
		Keywords: []string{"summarize", "framework", "execution"},
		Ext:      map[string]any{},
	}, map[string]string{"mode": "demo"})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "build demo state: %v\n", err)
		os.Exit(1)
	}
	summary, err := application.Engine.Run(ctx, st)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "run demo: %v\n", err)
		os.Exit(1)
	}
	raw, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Println(string(raw))
}
