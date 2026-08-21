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
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	_ "modernc.org/sqlite"
)

const moduleVersion = "0.1.0"

type Module struct {
	guardv1.UnimplementedPlaybackGuardServiceServer

	mu    sync.RWMutex
	cfgMu sync.RWMutex
	db    *sql.DB

	id       string
	dbPath   string
	grpcAddr string

	grpcSrv *grpc.Server
	grpcLis net.Listener

	monitorOverride monitorv1.PlaybackMonitorServiceClient // tests only

	mc     *client.Client
	stopCh chan struct{}

	notifyOnViolation bool
}

type Config struct {
	ID       string
	DBPath   string
	GRPCAddr string
}

func NewModule(cfg Config) *Module {
	if cfg.ID == "" {
		cfg.ID = "playback-guard"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "/var/lib/muxcore-playback-guard/guard.db"
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = ":9561"
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
		MinCoreVersion: "0.5.0",
	}
}

func (m *Module) Init(ctx context.Context) error {
	if err := m.initDB(ctx); err != nil {
		return err
	}
	lis, err := net.Listen("tcp", m.grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC %s: %w", m.grpcAddr, err)
	}
	m.grpcLis = lis
	slog.Info("playback-guard initialized", "db", m.dbPath, "grpc", m.grpcAddr)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	m.grpcSrv = grpc.NewServer()
	guardv1.RegisterPlaybackGuardServiceServer(m.grpcSrv, m)
	modulesdk.RegisterSettings(m.grpcSrv, m.id, m)
	go func() {
		slog.Info("playback-guard gRPC started", "addr", m.grpcAddr)
		if err := m.grpcSrv.Serve(m.grpcLis); err != nil {
			slog.Error("playback-guard gRPC error", "error", err)
		}
	}()
	go m.connectCoreAndSubscribe()
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
		mc.Close()
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

func (m *Module) connectCoreAndSubscribe() {
	addr := os.Getenv("MUXCORE_GRPC_ADDR")
	if addr == "" {
		return
	}
	var opts []client.Option
	if os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_GRPC_INSECURE") == "true" {
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
			select {
			case <-m.stopCh:
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		m.mu.Lock()
		if m.mc != nil {
			m.mc.Close()
		}
		m.mc = c
		m.mu.Unlock()
		slog.Info("playback-guard: connected to core mesh", "addr", addr)
		m.subscribePlaybackEvents()
		return
	}
}
