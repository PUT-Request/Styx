package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/styx-ai/styx/internal/agent"
	"github.com/styx-ai/styx/internal/config"
)

func main() {
	var (
		configPath string
		verbose    bool
		jsonOutput bool
	)

	flag.StringVar(&configPath, "config", "", "Path to styx.yaml config file")
	flag.StringVar(&configPath, "c", "", "Path to styx.yaml config file (shorthand)")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose output")
	flag.BoolVar(&verbose, "v", false, "Enable verbose output (shorthand)")
	flag.BoolVar(&jsonOutput, "json", false, "Output events as JSON lines")
	flag.Parse()

	if configPath == "" {
		fmt.Fprintln(os.Stderr, "Error: config file required (-config flag)")
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nReceived interrupt, shutting down...")
		cancel()
	}()

	agt := agent.New(cfg)

	// Set up streaming event handler
	evtFn := func(evt agent.Event) {
		switch evt.Type {
		case agent.EventText:
			if verbose {
				fmt.Print(evt.Content)
			}
		case agent.EventToolCall:
			if jsonOutput {
				fmt.Printf("{\"type\":\"tool_call\",\"tool\":\"%s\",\"args\":%s}\n", evt.Tool, evt.Content)
			} else {
				// Truncate args for display
				args := evt.Content
				if len(args) > 100 {
					args = args[:97] + "..."
				}
				fmt.Fprintf(os.Stderr, "\033[90m[tool] %s %s\033[0m\n", evt.Tool, args)
			}
		case agent.EventToolResult:
			if jsonOutput {
				// Escape the result for JSON
				escaped := strings.ReplaceAll(evt.Content, "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				escaped = strings.ReplaceAll(escaped, "\n", "\\n")
				fmt.Printf("{\"type\":\"tool_result\",\"tool\":\"%s\",\"result\":\"%s\"}\n", evt.Tool, escaped)
			} else if verbose {
				// Show abbreviated result
				result := evt.Content
				if len(result) > 200 {
					result = result[:197] + "..."
				}
				fmt.Fprintf(os.Stderr, "\033[90m[result] %s\033[0m\n", result)
			}
		case agent.EventError:
			fmt.Fprintf(os.Stderr, "\033[31m[error] %s\033[0m\n", evt.Content)
		}
	}
	agt.SetEventFunc(evtFn)

	startTime := time.Now()
	result, err := agt.Run(ctx)
	duration := time.Since(startTime)

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n\033[31mAgent failed: %v\033[0m\n", err)
		os.Exit(1)
	}

	// Final output
	if jsonOutput {
		// Escape result for JSON
		escaped := strings.ReplaceAll(result, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		escaped = strings.ReplaceAll(escaped, "\n", "\\n")
		fmt.Printf("{\"type\":\"summary\",\"status\":\"success\",\"duration_ms\":%d,\"result\":\"%s\"}\n", duration.Milliseconds(), escaped)
	} else {
		fmt.Fprintf(os.Stderr, "\n\033[32m[done in %s]\033[0m\n", duration.Round(time.Second))
		fmt.Println(result)
	}
}
