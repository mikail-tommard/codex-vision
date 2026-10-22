package mcpserver

import (
	"context"
	"example.com/codex-vision/internal/advisor"
	"example.com/codex-vision/internal/config"
	"example.com/codex-vision/internal/observation"
	"example.com/codex-vision/internal/platform"
	"example.com/codex-vision/internal/safety"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"log/slog"
	"sync"
	"time"
)

const serverVersion = "0.14.0"

type Service struct {
	views    agentViews
	metrics  usageMetrics
	started  time.Time
	Desktop  platform.Desktop
	Session  *safety.Session
	Config   config.Config
	Log      *slog.Logger
	recovery recoveryGuard
	Observer *observation.Manager
	Advisor  *advisor.Manager
	uiMu     sync.Mutex
	ui       *uiSnapshot
	uiOps    chan struct{}
	inputs   chan struct{}
	reads    chan struct{}
	mu       sync.Mutex
	recent   []time.Time
	total    int
}

func New(c config.Config, d platform.Desktop, l *slog.Logger) (*mcp.Server, *Service) {
	if c.ToolProfile == "" {
		c.ToolProfile = "compact"
	}
	svc := &Service{started: time.Now(), Desktop: d, Config: c, Session: safety.New(c, d, l), Log: l, uiOps: make(chan struct{}, 1), inputs: make(chan struct{}, 1), reads: make(chan struct{}, 1)}
	svc.Observer = observation.New(nil, svc.Session.ObservationError)
	svc.Advisor = advisor.New(svc.advisorSource)
	s := mcp.NewServer(&mcp.Implementation{Name: "codex-vision", Version: serverVersion}, &mcp.ServerOptions{Instructions: agentInstructions})
	svc.register(s)
	svc.registerObservation(s)
	svc.registerActionWait(s)
	svc.registerDesktopBatch(s)
	svc.registerControls(s)
	svc.registerUI(s)
	svc.registerUIBatch(s)
	svc.registerUIWait(s)
	svc.registerAgent(s)
	svc.registerAdvisor(s)
	return s, svc
}
func (s *Service) Run(ctx context.Context, server *mcp.Server) error {
	go s.Session.Watch(ctx)
	defer s.Desktop.Release()
	defer s.Close()
	return server.Run(ctx, &mcp.StdioTransport{})
}

func (s *Service) Close() {
	s.Session.Stop()
	s.Observer.Close()
	if s.Advisor != nil {
		s.Advisor.Close()
	}
	s.uiMu.Lock()
	s.ui = nil
	s.uiMu.Unlock()
	s.views.mu.Lock()
	s.views.current = nil
	s.views.mu.Unlock()
}
