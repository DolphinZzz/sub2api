//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestStudioRepositoryUserIsolationAndCascadeDelete(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	owner := mustCreateUser(t, client, &service.User{})
	other := mustCreateUser(t, client, &service.User{})
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM users WHERE id IN ($1, $2)`, owner.ID, other.ID)
	})

	repo := NewStudioRepository(integrationDB)
	now := time.Now().UTC()
	sessionID := fmt.Sprintf("studio-session-%d", owner.ID)
	session := &service.StudioSession{
		ID: sessionID, UserID: owner.ID, Title: "Studio", Mode: "image",
		Status: service.StudioSessionStatusActive, MetadataPath: "session.json",
		ExpiresAt: now.Add(30 * 24 * time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateSession(ctx, session))

	_, err := repo.GetSession(ctx, other.ID, sessionID)
	require.ErrorIs(t, err, service.ErrStudioSessionNotFound)

	turnID := "turn-1"
	message := &service.StudioMessage{
		ID: "message-1", SessionID: sessionID, UserID: owner.ID, TurnID: &turnID,
		Role: "assistant", MessageType: "images", Status: "completed",
		MetadataPath: "messages/message-1.json", CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateMessage(ctx, message))
	request := &service.StudioRequest{
		ID: "request-1", SessionID: sessionID, UserID: owner.ID, TurnID: turnID,
		Endpoint: "https://example.test", Model: "gpt-5.5", Status: service.StudioRequestCompleted,
		RequestPath: "requests/request-1.request.json", CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateRequest(ctx, request))
	generation := &service.StudioGenerationRecord{
		ID: "generation-1", SessionID: sessionID, UserID: owner.ID, RequestID: request.ID,
		MessageID: &message.ID, Status: "completed", MetadataPath: "generations/generation-1.json",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateGeneration(ctx, generation))
	asset := &service.StudioAsset{
		ID: "asset-1", SessionID: sessionID, UserID: owner.ID, RequestID: &request.ID,
		GenerationID: &generation.ID, Kind: "output", SHA256: fmt.Sprintf("%064x", owner.ID),
		MIMEType: "image/png", ByteSize: 3, RelativePath: "images/asset-1.png", CreatedAt: now,
	}
	require.NoError(t, repo.CreateAsset(ctx, asset))

	_, err = repo.GetRequest(ctx, other.ID, request.ID)
	require.ErrorIs(t, err, service.ErrStudioRequestNotFound)
	_, err = repo.GetAsset(ctx, other.ID, asset.ID)
	require.ErrorIs(t, err, service.ErrStudioAssetNotFound)

	marked, err := repo.MarkSessionDeleting(ctx, owner.ID, sessionID)
	require.NoError(t, err)
	require.True(t, marked)
	require.NoError(t, repo.DeleteSession(ctx, owner.ID, sessionID))

	for _, table := range []string{"studio_messages", "studio_requests", "studio_generation_records", "studio_assets"} {
		var count int
		err := integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE session_id = $1`, sessionID).Scan(&count)
		require.NoError(t, err)
		require.Zero(t, count, table)
	}
}

func TestStudioPersistenceChecksumCompatibilityAppliesAlignment(t *testing.T) {
	ctx := context.Background()

	_, err := integrationDB.ExecContext(ctx,
		`UPDATE schema_migrations SET checksum = $1 WHERE filename = $2`,
		"18e0f32da304b4309c86f606e8b2b641efa2a03896efd85283c67e7777c75341",
		"177_studio_persistence.sql",
	)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE filename = $1`,
		"178_studio_persistence_alignment.sql",
	)
	require.NoError(t, err)

	require.NoError(t, ApplyMigrations(ctx, integrationDB))

	var count int
	err = integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE filename = $1`,
		"178_studio_persistence_alignment.sql",
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
