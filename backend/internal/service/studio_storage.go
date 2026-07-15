package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const defaultStudioStorageRoot = "./data/studio"

var (
	studioPathIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	studioExtPattern    = regexp.MustCompile(`^[a-z0-9]{1,10}$`)
)

// LocalStudioFileStorage owns the server-generated file layout for Studio history.
// Paths returned by this type are relative to Root and are safe to persist in DB.
type LocalStudioFileStorage struct {
	root string
}

func NewStudioFileStorage(cfg *config.Config) (*LocalStudioFileStorage, error) {
	root := defaultStudioStorageRoot
	if cfg != nil && strings.TrimSpace(cfg.Studio.StorageRoot) != "" {
		root = strings.TrimSpace(cfg.Studio.StorageRoot)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve studio storage root: %w", err)
	}
	return &LocalStudioFileStorage{root: filepath.Clean(absRoot)}, nil
}

// ProvideStudioFileStorage exposes the concrete local store through the service port for Wire.
func ProvideStudioFileStorage(cfg *config.Config) (StudioFileStorage, error) {
	return NewStudioFileStorage(cfg)
}

func (s *LocalStudioFileStorage) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *LocalStudioFileStorage) WriteSession(_ context.Context, userID int64, sessionID string, value any) (string, error) {
	rel, err := studioSessionRelativePath(userID, sessionID, "session.json")
	if err != nil {
		return "", err
	}
	return rel, s.writeJSON(rel, value)
}

func (s *LocalStudioFileStorage) WriteMessage(_ context.Context, userID int64, sessionID, messageID string, value any) (string, error) {
	if err := validateStudioPathID("message", messageID); err != nil {
		return "", err
	}
	rel, err := studioSessionRelativePath(userID, sessionID, filepath.Join("messages", messageID+".json"))
	if err != nil {
		return "", err
	}
	return rel, s.writeJSON(rel, value)
}

func (s *LocalStudioFileStorage) WriteRequest(_ context.Context, userID int64, sessionID, requestID string, value any) (string, error) {
	return s.writeRequestDocument(userID, sessionID, requestID, "request", value)
}

func (s *LocalStudioFileStorage) WriteResponse(_ context.Context, userID int64, sessionID, requestID string, value any) (string, error) {
	return s.writeRequestDocument(userID, sessionID, requestID, "response", value)
}

func (s *LocalStudioFileStorage) writeRequestDocument(userID int64, sessionID, requestID, suffix string, value any) (string, error) {
	if err := validateStudioPathID("request", requestID); err != nil {
		return "", err
	}
	rel, err := studioSessionRelativePath(userID, sessionID, filepath.Join("requests", requestID+"."+suffix+".json"))
	if err != nil {
		return "", err
	}
	return rel, s.writeJSON(rel, value)
}

func (s *LocalStudioFileStorage) WriteGeneration(_ context.Context, userID int64, sessionID, generationID string, value any) (string, error) {
	if err := validateStudioPathID("generation", generationID); err != nil {
		return "", err
	}
	rel, err := studioSessionRelativePath(userID, sessionID, filepath.Join("generations", generationID+".json"))
	if err != nil {
		return "", err
	}
	return rel, s.writeJSON(rel, value)
}

func (s *LocalStudioFileStorage) WriteInput(_ context.Context, userID int64, sessionID, expectedSHA256, extension string, data []byte) (string, error) {
	actual := sha256.Sum256(data)
	actualHex := hex.EncodeToString(actual[:])
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if expectedSHA256 == "" {
		expectedSHA256 = actualHex
	}
	if len(expectedSHA256) != sha256.Size*2 || expectedSHA256 != actualHex {
		return "", errors.New("studio input asset sha256 mismatch")
	}
	ext, err := normalizeStudioExtension(extension)
	if err != nil {
		return "", err
	}
	rel, err := studioSessionRelativePath(userID, sessionID, filepath.Join("inputs", expectedSHA256+"."+ext))
	if err != nil {
		return "", err
	}
	return rel, s.writeAtomic(rel, data)
}

func (s *LocalStudioFileStorage) WriteOutput(_ context.Context, userID int64, sessionID, assetID, extension string, data []byte) (string, error) {
	if err := validateStudioPathID("asset", assetID); err != nil {
		return "", err
	}
	ext, err := normalizeStudioExtension(extension)
	if err != nil {
		return "", err
	}
	rel, err := studioSessionRelativePath(userID, sessionID, filepath.Join("images", assetID+"."+ext))
	if err != nil {
		return "", err
	}
	return rel, s.writeAtomic(rel, data)
}

func (s *LocalStudioFileStorage) ReadJSON(ctx context.Context, relativePath string, value any) error {
	f, err := s.OpenAsset(ctx, relativePath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(value); err != nil {
		return fmt.Errorf("decode studio json: %w", err)
	}
	return nil
}

func (s *LocalStudioFileStorage) OpenAsset(_ context.Context, relativePath string) (io.ReadCloser, error) {
	abs, err := s.safePath(relativePath)
	if err != nil {
		return nil, err
	}
	if err := s.ensureResolvedWithinRoot(abs); err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("open studio file: %w", err)
	}
	return f, nil
}

// QuarantineSession atomically moves a session to a deterministic trash path.
// It is idempotent: when already moved or absent it still returns that path.
func (s *LocalStudioFileStorage) QuarantineSession(_ context.Context, userID int64, sessionID string) (string, error) {
	sourceRel, err := studioSessionRelativePath(userID, sessionID, "")
	if err != nil {
		return "", err
	}
	trashRel := filepath.Join(".trash", "users", strconv.FormatInt(userID, 10), "sessions", sessionID)
	source, err := s.safePath(sourceRel)
	if err != nil {
		return "", err
	}
	trash, err := s.safePath(trashRel)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return filepath.ToSlash(trashRel), nil
		}
		return "", fmt.Errorf("stat studio session: %w", err)
	}
	if err := s.rejectSymlinkComponents(source); err != nil {
		return "", err
	}
	if err := s.ensureResolvedWithinRoot(source); err != nil {
		return "", err
	}
	trashParent := filepath.Dir(trash)
	if err := s.rejectSymlinkComponents(trashParent); err != nil {
		return "", err
	}
	if err := os.MkdirAll(trashParent, 0o750); err != nil {
		return "", fmt.Errorf("create studio trash parent: %w", err)
	}
	if err := s.ensureResolvedWithinRoot(trashParent); err != nil {
		return "", err
	}
	if err := s.rejectSymlinkComponents(trash); err != nil {
		return "", err
	}
	if _, err := os.Stat(trash); err == nil {
		return "", fmt.Errorf("studio quarantine already contains session %q", sessionID)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat studio quarantine: %w", err)
	}
	if err := os.Rename(source, trash); err != nil {
		return "", fmt.Errorf("quarantine studio session: %w", err)
	}
	return filepath.ToSlash(trashRel), nil
}

func (s *LocalStudioFileStorage) DeleteQuarantine(_ context.Context, relativePath string) error {
	clean := filepath.Clean(filepath.FromSlash(relativePath))
	trashPrefix := ".trash" + string(filepath.Separator)
	if !strings.HasPrefix(clean, trashPrefix) {
		return errors.New("studio delete path is outside quarantine")
	}
	abs, err := s.safePath(clean)
	if err != nil {
		return err
	}
	if err := s.rejectSymlinkComponents(abs); err != nil {
		return err
	}
	if err := os.RemoveAll(abs); err != nil {
		return fmt.Errorf("delete quarantined studio session: %w", err)
	}
	return nil
}

func (s *LocalStudioFileStorage) writeJSON(relativePath string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal studio json: %w", err)
	}
	data = append(data, '\n')
	return s.writeAtomic(relativePath, data)
}

func (s *LocalStudioFileStorage) writeAtomic(relativePath string, data []byte) error {
	if err := s.ensureRoot(); err != nil {
		return err
	}
	abs, err := s.safePath(relativePath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	if err := s.rejectSymlinkComponents(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create studio directory: %w", err)
	}
	if err := s.ensureResolvedWithinRoot(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".studio-*")
	if err != nil {
		return fmt.Errorf("create studio temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod studio temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write studio temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync studio temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close studio temp file: %w", err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return fmt.Errorf("commit studio file: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func (s *LocalStudioFileStorage) ensureRoot() error {
	if s == nil || s.root == "" {
		return errors.New("studio storage is not initialized")
	}
	if err := os.MkdirAll(s.root, 0o750); err != nil {
		return fmt.Errorf("create studio storage root %q: %w", s.root, err)
	}
	if err := os.Chmod(s.root, 0o750); err != nil {
		return fmt.Errorf("chmod studio storage root %q: %w", s.root, err)
	}
	return nil
}

func (s *LocalStudioFileStorage) safePath(relativePath string) (string, error) {
	if s == nil || s.root == "" {
		return "", errors.New("studio storage is not initialized")
	}
	if filepath.IsAbs(relativePath) {
		return "", errors.New("studio path must be relative")
	}
	clean := filepath.Clean(filepath.FromSlash(relativePath))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("studio path escapes storage root")
	}
	abs := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("studio path escapes storage root")
	}
	return abs, nil
}

func (s *LocalStudioFileStorage) ensureResolvedWithinRoot(path string) error {
	resolvedRoot, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return fmt.Errorf("resolve studio storage root symlinks: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve studio path symlinks: %w", err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("studio path escapes storage root through symlink")
	}
	return nil
}

func (s *LocalStudioFileStorage) rejectSymlinkComponents(path string) error {
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("studio path escapes storage root")
	}
	current := s.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect studio path component: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("studio path contains a symlink component")
		}
	}
	return nil
}

func studioSessionRelativePath(userID int64, sessionID, suffix string) (string, error) {
	if userID <= 0 {
		return "", errors.New("studio user id must be positive")
	}
	if err := validateStudioPathID("session", sessionID); err != nil {
		return "", err
	}
	parts := []string{"users", strconv.FormatInt(userID, 10), "sessions", sessionID}
	if suffix != "" {
		parts = append(parts, suffix)
	}
	return filepath.ToSlash(filepath.Join(parts...)), nil
}

func validateStudioPathID(kind, value string) error {
	if !studioPathIDPattern.MatchString(value) || value == "." || value == ".." {
		return fmt.Errorf("invalid studio %s id", kind)
	}
	return nil
}

func normalizeStudioExtension(extension string) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(extension), "."))
	if ext == "jpg" {
		ext = "jpeg"
	}
	if !studioExtPattern.MatchString(ext) {
		return "", errors.New("invalid studio asset extension")
	}
	return ext, nil
}

var _ StudioFileStorage = (*LocalStudioFileStorage)(nil)
