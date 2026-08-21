package internal

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	embyv1 "github.com/Muxcore-Media/emby/proto/embyv1"
	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
	plexv1 "github.com/Muxcore-Media/plex/proto/plexv1"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

func bridgeCapability(serverType string) string {
	switch strings.ToLower(strings.TrimSpace(serverType)) {
	case "plex":
		return "playback.plex"
	case "emby":
		return "playback.emby"
	default:
		return "playback.jellyfin"
	}
}

func (m *Module) TerminateSession(ctx context.Context, req *guardv1.TerminateSessionRequest) (*guardv1.TerminateSessionResponse, error) {
	sessionID := strings.TrimSpace(req.GetSessionId())
	if sessionID == "" {
		return &guardv1.TerminateSessionResponse{Ok: false, Error: "session_id required"}, nil
	}
	cap := bridgeCapability(req.GetServerType())
	addr, err := m.findCapabilityAddr(ctx, cap)
	if err != nil {
		return &guardv1.TerminateSessionResponse{Ok: false, Error: err.Error()}, nil
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return &guardv1.TerminateSessionResponse{Ok: false, Error: fmt.Sprintf("dial %s: %v", addr, err)}, nil
	}
	defer conn.Close()

	reason := strings.TrimSpace(req.GetReason())
	switch strings.ToLower(strings.TrimSpace(req.GetServerType())) {
	case "plex":
		resp, err := plexv1.NewPlexBridgeServiceClient(conn).TerminateSession(ctx, &plexv1.TerminateSessionRequest{
			SessionId: sessionID,
			Reason:    reason,
		})
		if err != nil {
			return &guardv1.TerminateSessionResponse{Ok: false, Error: err.Error()}, nil
		}
		return &guardv1.TerminateSessionResponse{Ok: resp.GetOk(), Error: resp.GetError()}, nil
	case "emby":
		resp, err := embyv1.NewEmbyBridgeServiceClient(conn).TerminateSession(ctx, &embyv1.TerminateSessionRequest{
			SessionId: sessionID,
		})
		if err != nil {
			return &guardv1.TerminateSessionResponse{Ok: false, Error: err.Error()}, nil
		}
		return &guardv1.TerminateSessionResponse{Ok: resp.GetOk(), Error: resp.GetError()}, nil
	default:
		resp, err := jellyfinv1.NewJellyfinBridgeClient(conn).TerminateSession(ctx, &jellyfinv1.TerminateSessionRequest{
			SessionId: sessionID,
			Reason:    reason,
		})
		if err != nil {
			return &guardv1.TerminateSessionResponse{Ok: false, Error: err.Error()}, nil
		}
		return &guardv1.TerminateSessionResponse{Ok: resp.GetOk(), Error: resp.GetError()}, nil
	}
}
