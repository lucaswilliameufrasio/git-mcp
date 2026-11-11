package audit

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAppendThenListReturnsNewestFirst(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "audit"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	ctx := context.Background()
	for i, tool := range []string{"repository_inspect", "update_branch", "merge_into"} {
		now = now.Add(time.Second)
		event := Event{Tool: tool, StartedAt: now, Duration: time.Duration(i+1) * time.Millisecond, Status: "preview"}
		if err := store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	events, err := store.List(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Tool != "merge_into" || events[1].Tool != "update_branch" {
		t.Fatalf("ordem ou limite inesperados: %+v", events)
	}
	if events[0].Duration != 3*time.Millisecond || events[0].Status != "preview" {
		t.Fatalf("evento não preservado: %+v", events[0])
	}
}

func TestListRemovesExpiredEventsAndIgnoresForeignFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	store, err := NewFileStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	ctx := context.Background()
	if err := store.Append(ctx, Event{Tool: "old", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "notas.json")
	if err := os.WriteFile(foreign, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	now = now.Add(2 * time.Hour)
	if err := store.Append(ctx, Event{Tool: "new", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	events, err := store.List(ctx, MaxList)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Tool != "new" {
		t.Fatalf("o evento vencido deveria sair da lista: %+v", events)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("esperava o evento novo e o arquivo alheio, havia %d entradas", len(entries))
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("arquivo alheio não pode ser removido: %v", err)
	}
}

func TestListRejectsLimitOutsideTheSample(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "audit"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, MaxList + 1} {
		if _, err := store.List(context.Background(), limit); err == nil {
			t.Fatalf("limite %d deveria ser recusado", limit)
		}
	}
}

func TestNewFileStoreRefusesSharedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sem bits de permissão POSIX")
	}
	dir := filepath.Join(t.TempDir(), "audit")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(dir, time.Hour); err == nil {
		t.Fatal("diretório legível por outros usuários deveria ser recusado")
	}
}

func TestDefaultDirectoryFollowsXDGStateHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	dir, err := DefaultDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "git-mcp", "audit"); dir != want {
		t.Fatalf("diretório %q, esperado %q", dir, want)
	}
	t.Setenv("XDG_STATE_HOME", "relativo")
	if _, err := DefaultDirectory(); err == nil {
		t.Fatal("caminho relativo deveria ser recusado")
	}
}
