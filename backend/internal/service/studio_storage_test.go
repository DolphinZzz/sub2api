package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newTestStudioStorage(t *testing.T) *LocalStudioFileStorage {
	t.Helper()
	cfg := &config.Config{Studio: config.StudioConfig{StorageRoot: t.TempDir()}}
	store, err := NewStudioFileStorage(cfg)
	require.NoError(t, err)
	return store
}

func TestStudioFileStorageLayoutAndPermissions(t *testing.T) {
	store := newTestStudioStorage(t)

	rel, err := store.WriteMessage(context.Background(), 42, "session-1", "message-1", map[string]any{"content": "hello"})
	require.NoError(t, err)
	require.Equal(t, "users/42/sessions/session-1/messages/message-1.json", rel)

	info, err := os.Stat(filepath.Join(store.Root(), filepath.FromSlash(rel)))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	var got map[string]any
	require.NoError(t, store.ReadJSON(context.Background(), rel, &got))
	require.Equal(t, "hello", got["content"])
}

func TestStudioFileStorageDefersUnwritableRootFailureUntilFirstWrite(t *testing.T) {
	parent := t.TempDir()
	blockingFile := filepath.Join(parent, "not-a-directory")
	require.NoError(t, os.WriteFile(blockingFile, []byte("x"), 0o600))
	store, err := NewStudioFileStorage(&config.Config{Studio: config.StudioConfig{StorageRoot: filepath.Join(blockingFile, "studio")}})
	require.NoError(t, err, "storage construction must not make the whole backend fail")

	_, err = store.WriteSession(context.Background(), 1, "session-1", map[string]string{"id": "session-1"})
	require.ErrorContains(t, err, "create studio storage root")
}

func TestStudioFileStorageWritesAndVerifiesAssets(t *testing.T) {
	store := newTestStudioStorage(t)
	data := []byte("image bytes")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	inputRel, err := store.WriteInput(context.Background(), 1, "s1", hash, ".PNG", data)
	require.NoError(t, err)
	require.Equal(t, "users/1/sessions/s1/inputs/"+hash+".png", inputRel)
	_, err = store.WriteInput(context.Background(), 1, "s1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "png", data)
	require.ErrorContains(t, err, "sha256 mismatch")

	outputRel, err := store.WriteOutput(context.Background(), 1, "s1", "asset-1", "jpg", data)
	require.NoError(t, err)
	require.Equal(t, "users/1/sessions/s1/images/asset-1.jpeg", outputRel)
	f, err := store.OpenAsset(context.Background(), outputRel)
	require.NoError(t, err)
	defer f.Close()
	got, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestStudioFileStorageRejectsPathTraversal(t *testing.T) {
	store := newTestStudioStorage(t)

	_, err := store.WriteSession(context.Background(), 1, "../escape", map[string]string{})
	require.Error(t, err)
	_, err = store.WriteOutput(context.Background(), 1, "safe", "../../asset", "png", []byte("x"))
	require.Error(t, err)
	_, err = store.OpenAsset(context.Background(), "../../etc/passwd")
	require.ErrorContains(t, err, "escapes storage root")
	require.Error(t, store.DeleteQuarantine(context.Background(), "users/1/sessions/safe"))
}

func TestStudioFileStorageRejectsSymlinkEscape(t *testing.T) {
	store := newTestStudioStorage(t)
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(store.Root(), "linked")))

	_, err := store.OpenAsset(context.Background(), "linked/secret")
	require.ErrorContains(t, err, "through symlink")
	_, err = store.WriteOutput(context.Background(), 1, "s1", "asset", "png", []byte("safe"))
	require.NoError(t, err)
}

func TestStudioFileStorageRejectsWriteThroughSymlinkWithoutSideEffects(t *testing.T) {
	store := newTestStudioStorage(t)
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(store.Root(), "users")))

	_, err := store.WriteSession(context.Background(), 7, "session-1", map[string]string{"id": "session-1"})
	require.ErrorContains(t, err, "symlink component")
	_, statErr := os.Stat(filepath.Join(outside, "7"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestStudioFileStorageRejectsQuarantineSymlink(t *testing.T) {
	store := newTestStudioStorage(t)
	_, err := store.WriteSession(context.Background(), 7, "session-1", map[string]string{"id": "session-1"})
	require.NoError(t, err)
	out := t.TempDir()
	require.NoError(t, os.Symlink(out, filepath.Join(store.Root(), ".trash")))

	_, err = store.QuarantineSession(context.Background(), 7, "session-1")
	require.ErrorContains(t, err, "symlink component")
	_, err = os.Stat(filepath.Join(store.Root(), "users", "7", "sessions", "session-1", "session.json"))
	require.NoError(t, err, "rejected quarantine must leave the source intact")
	require.Error(t, store.DeleteQuarantine(context.Background(), ".trash/users/7/sessions/session-1"))
}

func TestStudioFileStorageQuarantineIsIdempotent(t *testing.T) {
	store := newTestStudioStorage(t)
	_, err := store.WriteSession(context.Background(), 7, "s-7", map[string]string{"id": "s-7"})
	require.NoError(t, err)

	trashRel, err := store.QuarantineSession(context.Background(), 7, "s-7")
	require.NoError(t, err)
	require.Equal(t, ".trash/users/7/sessions/s-7", trashRel)
	_, err = os.Stat(filepath.Join(store.Root(), filepath.FromSlash(trashRel), "session.json"))
	require.NoError(t, err)

	// A retry after the move returns the same deterministic location.
	retryRel, err := store.QuarantineSession(context.Background(), 7, "s-7")
	require.NoError(t, err)
	require.Equal(t, trashRel, retryRel)
	require.NoError(t, store.DeleteQuarantine(context.Background(), trashRel))
	require.NoError(t, store.DeleteQuarantine(context.Background(), trashRel))
}
