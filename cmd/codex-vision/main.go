package main

import (
	"context"
	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/mcpserver"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/platform/mock"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
)

func run() error {
	path := flag.String("config", "", "local JSON configuration (defaults to observe)")
	sim := flag.Bool("mock", false, "synthetic desktop; never controls host")
	doctor := flag.Bool("doctor", false, "report native platform permissions and availability (JSON; no MCP session)")
	request := flag.Bool("request-permissions", false, "request OS privacy permissions locally, report status, then exit")
	flag.Parse()
	if *doctor || *request {
		return nativeDoctor(*request)
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(c.AuditFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("open audit log: %w", e)
	}
	defer f.Close()
	log := slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stderr, f), nil))
	var d platform.Desktop
	if *sim {
		d = &mock.Desktop{}
	} else {
		d, e = newNative()
		if e != nil {
			return e
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	server, svc := mcpserver.New(c, d, log)
	return svc.Run(ctx, server)
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "codex-vision:", e)
		os.Exit(1)
	}
}
