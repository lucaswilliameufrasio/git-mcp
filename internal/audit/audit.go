// Package audit records each finished MCP tool call in a private per-user directory.
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

// Event is one finished tool call: what was asked, where, and how it ended.
// The confirmation string and commit oids are deliberately absent.
type Event struct {
	Tool      string        `json:"tool"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration_ns"`
	Status    string        `json:"status"`
	// Mode is preview or execute; empty for a tool that only reads.
	Mode       string `json:"mode,omitempty"`
	Repository string `json:"repository,omitempty"`
	// Refs are the branch, tag and remote names the call named, in argument order.
	Refs    []Ref    `json:"refs,omitempty"`
	Message string   `json:"message,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
}

// Ref is one named argument of a call, such as target=main.
type Ref struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Filter narrows a search. A zero field does not filter.
type Filter struct {
	Tool   string
	Status string
	// Since keeps calls recorded at or after this instant.
	Since time.Time
	// Text is matched, ignoring case, against repository, refs, message and reasons.
	Text string
}

// Found is one page of a search over every retained event.
type Found struct {
	Events []Event
	// Total counts the events that match the filter, not only this page.
	Total int
	// Tools names every tool with a retained event, whatever the filter.
	Tools []string
}

const (
	maxText    = 300
	maxReasons = 5
	maxRefs    = 12
	maxFile    = 32 << 10
)

// bounded keeps an event file small whatever a tool returned.
func bounded(event Event) Event {
	event.Mode = clip(event.Mode, 20)
	event.Repository = clip(event.Repository, 4*maxText)
	event.Message = clip(event.Message, maxText)
	if len(event.Reasons) > maxReasons {
		event.Reasons = event.Reasons[:maxReasons]
	}
	event.Reasons = append([]string(nil), event.Reasons...)
	for i, reason := range event.Reasons {
		event.Reasons[i] = clip(reason, maxText)
	}
	if len(event.Refs) > maxRefs {
		event.Refs = event.Refs[:maxRefs]
	}
	event.Refs = append([]Ref(nil), event.Refs...)
	for i, ref := range event.Refs {
		event.Refs[i] = Ref{Name: clip(ref.Name, 40), Value: clip(ref.Value, maxText)}
	}
	return event
}

func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func (e Event) matches(filter Filter) bool {
	if filter.Tool != "" && e.Tool != filter.Tool {
		return false
	}
	if filter.Status != "" && e.Status != filter.Status {
		return false
	}
	if filter.Text == "" {
		return true
	}
	needle := strings.ToLower(filter.Text)
	fields := append([]string{e.Repository, e.Message}, e.Reasons...)
	for _, ref := range e.Refs {
		fields = append(fields, ref.Value)
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
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
	writeErr := json.NewEncoder(file).Encode(bounded(event))
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
	if _, err := s.retained(ctx); err != nil {
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
	files, err := s.retained(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Event, 0)
	for _, file := range files {
		if len(result) >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event, err := readEvent(file.path)
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

// Search reads every retained event, newest first, and returns the matching
// ones from offset on, at most limit of them.
func (s *FileStore) Search(ctx context.Context, filter Filter, offset, limit int) (Found, error) {
	if offset < 0 || limit < 1 || limit > MaxList {
		return Found{}, fmt.Errorf("a página de auditoria vai de 1 a %d chamadas", MaxList)
	}
	files, err := s.retained(ctx)
	if err != nil {
		return Found{}, err
	}
	found := Found{Events: make([]Event, 0, limit)}
	tools := make(map[string]bool)
	since := filter.Since.UnixNano()
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return Found{}, err
		}
		event, err := readEvent(file.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return Found{}, err
		}
		tools[event.Tool] = true
		if (!filter.Since.IsZero() && file.stamp < since) || !event.matches(filter) {
			continue
		}
		if found.Total >= offset && len(found.Events) < limit {
			found.Events = append(found.Events, event)
		}
		found.Total++
	}
	for tool := range tools {
		found.Tools = append(found.Tools, tool)
	}
	sort.Strings(found.Tools)
	return found, nil
}

type eventFile struct {
	stamp int64
	path  string
}

// retained lists this package's event files, newest first, and removes the ones past the retention.
func (s *FileStore) retained(ctx context.Context) ([]eventFile, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	// The zero-padded timestamp makes name order equal to time order.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	files := make([]eventFile, 0, len(entries))
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
		files = append(files, eventFile{stamp: stamp, path: path})
	}
	return files, nil
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
	err = json.NewDecoder(io.LimitReader(file, maxFile)).Decode(&event)
	if closeErr := file.Close(); err != nil || closeErr != nil {
		return Event{}, errors.Join(err, closeErr)
	}
	return event, nil
}
