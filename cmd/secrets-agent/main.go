// Command secrets-agent fetches this host's environment variables from a secrets
// proxy and applies them to the local consumers: docker compose, grafana alloy, and
// per-variable files for images that read *_FILE.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/obervinov/secrets-agent/internal/agent"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	configPath := flag.String("config", envOr("SECRETS_AGENT_CONF", "/etc/secrets-agent.conf"),
		"path to the agent configuration")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	log := agent.Logger{Out: os.Stdout, Err: os.Stderr}

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Warnf("%v", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agent.Run(ctx, cfg, log); err != nil {
		log.Warnf("%v", err)
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
