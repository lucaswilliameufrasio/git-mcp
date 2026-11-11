// Package audit stores metadata about MCP tool calls, never their arguments or results.
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Retention is how long an event file stays on disk.
const Retention = 30 * 24 * time.Hour

// MaxList bounds one read, so a page never summarizes more than this many events.
const MaxList = 200

// Event is one finished tool call. Repository paths, branch names, oids and
// messages are deliberately absent.
type Event struct {
	Tool      string        `json:"tool"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration_ns"`
	Status    string        `json:"status"`
}

// DefaultDirectory is the per-user state directory shared by every git-mcp process.
func DefaultDirectory() (string, error) {
	if root := os.Getenv("XDG_STATE_HOME"); root != "" {
		if !filepath.IsAbs(root) {
			return "", errors.New("XDG_STATE_HOME precisa ser um caminho absoluto")
		}
		return filepath.Join(root, "git-mcp", "audit"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "git-mcp", "audit"), nil
}

// FileStore keeps one file per event, so separate processes append without locking each other.
type FileStore struct {
	dir       string
	retention time.Duration
	clock     func() time.Time
	mu        sync.Mutex
	nextSweep time.Time
}

// NewFileStore creates the directory when missing and refuses one other users can read.
func NewFileStore(dir string, retention time.Duration) (*FileStore, error) {
	if dir == "" || retention < time.Second {
		return nil, errors.New("diretório ou retenção de auditoria inválidos")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("o caminho de auditoria não é um diretório")
	}
	// Windows does not report POSIX permission bits; the profile directory's ACL applies there.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("o diretório de auditoria precisa ser privado (0700)")
	}
	return &FileStore{dir: dir, retention: retention, clock: time.Now}, nil
}

// Append writes the event under a temporary name and renames it, so a reader never sees half a file.
func (s *FileStore) Append(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.sweep(ctx); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	name := fmt.Sprintf("event-%020d-%s.json", s.clock().UTC().UnixNano(), hex.EncodeToString(random[:]))
	file, err := os.CreateTemp(s.dir, ".pending-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()
	writeErr := json.NewEncoder(file).Encode(event)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(writeErr, closeErr)
	}
	return os.Rename(tempPath, filepath.Join(s.dir, name))
}

func (s *FileStore) sweep(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if now.Before(s.nextSweep) {
		return nil
	}
	if _, err := s.list(ctx, 0); err != nil {
		return err
	}
	s.nextSweep = now.Add(min(time.Hour, s.retention/2))
	return nil
}

// List returns the newest events first and removes files past the retention.
func (s *FileStore) List(ctx context.Context, limit int) ([]Event, error) {
	if limit < 1 || limit > MaxList {
		return nil, fmt.Errorf("o limite de auditoria vai de 1 a %d", MaxList)
	}
	return s.list(ctx, limit)
}

func (s *FileStore) list(ctx context.Context, limit int) ([]Event, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	// The zero-padded timestamp makes name order equal to time order.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	result := make([]Event, 0)
	cutoff := s.clock().Add(-s.retention).UnixNano()
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		stamp, ok := eventStamp(name)
		if !ok || entry.Type() != 0 {
			continue
		}
		path := filepath.Join(s.dir, name)
		if stamp <= cutoff {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			continue
		}
		if len(result) >= limit {
			continue
		}
		event, err := readEvent(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, nil
}

// eventStamp accepts only names this package writes: event-<20 digits>-<32 hex>.json.
func eventStamp(name string) (int64, bool) {
	const prefix, suffix = "event-", ".json"
	if len(name) != len(prefix)+20+1+32+len(suffix) || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) || name[26] != '-' {
		return 0, false
	}
	stamp, err := strconv.ParseInt(name[6:26], 10, 64)
	if err != nil {
		return 0, false
	}
	if _, err := hex.DecodeString(name[27:59]); err != nil {
		return 0, false
	}
	return stamp, true
}

func readEvent(path string) (Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return Event{}, err
	}
	var event Event
	err = json.NewDecoder(io.LimitReader(file, 4096)).Decode(&event)
	if closeErr := file.Close(); err != nil || closeErr != nil {
		return Event{}, errors.Join(err, closeErr)
	}
	return event, nil
}
