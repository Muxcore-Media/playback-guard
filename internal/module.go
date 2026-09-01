package internal

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/Muxcore-Media/core/sdk/go/client"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
	_ "modernc.org/sqlite"
)

const moduleVersion = "0.1.0"

type Module struct {
	guardv1.UnimplementedPlaybackGuardServiceServer
	monitorOverride   monitorv1.PlaybackMonitorServiceClient
	terminateHook     func(context.Context, *guardv1.TerminateSessionRequest) (*guardv1.TerminateSessionResponse, error)
	grpcLis           net.Listener
	stopCh            chan struct{}
	mc                *client.Client
	db                *sql.DB
	grpcSrv           *grpc.Server
	id                string
	dbPath            string
	grpcAddr          string
	moduleToken       string
	mu                sync.RWMutex
	cfgMu             sync.RWMutex
	notifyOnViolation bool
}

type Config struct {
	ID          string
	DBPath      string
	GRPCAddr    string
	ModuleToken string
}

func NewModule(cfg Config) *Module {
	if cfg.ID == "" {
		cfg.ID = "playback-guard"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "/var/lib/muxcore-playback-guard/guard.db"
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = "127.0.0.1:9561"
	}
	if v := os.Getenv("PLAYBACK_GUARD_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("PLAYBACK_GUARD_GRPC_ADDR"); v != "" {
		cfg.GRPCAddr = v
	}
	notifyViolation := envTruthy(os.Getenv("PLAYBACK_GUARD_NOTIFY_ON_VIOLATION"))
	return &Module{
		id:                cfg.ID,
		dbPath:            cfg.DBPath,
		grpcAddr:          cfg.GRPCAddr,
		moduleToken:       cfg.ModuleToken,
		stopCh:            make(chan struct{}),
		notifyOnViolation: notifyViolation,
	}
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:          m.id,
		Name:        "Playback Guard",
		Version:     moduleVersion,
		Roles:       []string{"security"},
		Description: "Sharing detection, trust scores, and guard rules (Tracearr parity)",
		Author:      "MuxCore",
		Capabilities: []string{
			"playback.guard",
			"settings",
		},
		Contracts: []contracts.ContractDeclaration{
			{
				Repo:      "github.com/Muxcore-Media/contracts-playback",
				Interface: "PlaybackGuardEvents",
				Version:   "v0.1.0",
			},
			{
				Repo:      "github.com/Muxcore-Media/contracts-notification",
				Interface: "NotificationProvider",
				Version:   "v0.1.0",
			},
		},
		MinCoreVersion: "0.5.0",
		HTTPAddr:       m.grpcAddr,
	}
}

func (m *Module) Init(ctx context.Context) error {
	if err := m.initDB(ctx); err != nil {
		return err
	}
	if err := m.loadPersistedSettings(ctx); err != nil {
		return err
	}
	if m.moduleToken == "" {
		m.moduleToken = moduleTokenFromEnv()
	}
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", m.grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC %s: %w", m.grpcAddr, err)
	}
	m.grpcLis = lis
	slog.Info("playback-guard initialized", "db", m.dbPath, "grpc", m.grpcAddr)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	m.grpcSrv = grpc.NewServer(grpc.UnaryInterceptor(authUnaryInterceptor(m.moduleToken)))
	guardv1.RegisterPlaybackGuardServiceServer(m.grpcSrv, m)
	modulesdk.RegisterSettings(m.grpcSrv, m.id, m)
	go func() {
		slog.Info("playback-guard gRPC started", "addr", m.grpcAddr)
		if err := m.grpcSrv.Serve(m.grpcLis); err != nil {
			slog.Error("playback-guard gRPC error", "error", err)
		}
	}()
	go m.connectCoreAndSubscribe(ctx)
	go m.accountInactivityLoop(ctx)
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	m.mu.Lock()
	mc := m.mc
	if m.db != nil {
		_ = m.db.Close()
		m.db = nil
	}
	m.mc = nil
	m.mu.Unlock()
	if mc != nil {
		_ = mc.Close()
	}
	slog.Info("playback-guard stopped")
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("not initialized")
	}
	return db.PingContext(ctx)
}

func (m *Module) connectCoreAndSubscribe(ctx context.Context) {
	addr := os.Getenv("MUXCORE_GRPC_ADDR")
	if addr == "" {
		return
	}
	var opts []client.Option
	if meshInsecure() {
		opts = append(opts, client.WithInsecure())
	}
	backoff := time.Second
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}
		c, err := client.Dial(addr, opts...)
		if err != nil {
			slog.Warn("playback-guard: dial core failed, retrying", "error", err, "backoff", backoff)
			if !sleepUntilStop(m.stopCh, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		m.mu.Lock()
		if m.mc != nil {
			_ = m.mc.Close()
		}
		m.mc = c
		m.mu.Unlock()
		slog.Info("playback-guard: connected to core mesh", "addr", addr)
		backoff = time.Second
		m.subscribePlaybackEvents(ctx)
		select {
		case <-m.stopCh:
			return
		default:
		}
		slog.Warn("playback-guard: playback event subscriptions ended, reconnecting")
		if !sleepUntilStop(m.stopCh, backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func sleepUntilStop(stopCh <-chan struct{}, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-stopCh:
		return false
	case <-t.C:
		return true
	}
}
