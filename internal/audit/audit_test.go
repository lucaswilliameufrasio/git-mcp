package audit

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestSearchFiltersAndPagesTheWholeHistory(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "audit"), 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	ctx := context.Background()
	record := func(event Event) {
		t.Helper()
		now = now.Add(time.Minute)
		event.StartedAt = now
		if err := store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	record(Event{Tool: "update_branch", Status: "success", Repository: "/srv/loja", Refs: []Ref{{Name: "branch", Value: "main"}}})
	record(Event{Tool: "merge_into", Status: "conflict", Repository: "/srv/loja", Refs: []Ref{{Name: "target", Value: "develop"}}, Reasons: []string{"conflito em go.mod"}})
	record(Event{Tool: "update_branch", Status: "blocked", Repository: "/srv/site", Message: "Árvore suja"})
	recent := now
	record(Event{Tool: "update_branch", Status: "preview", Repository: "/srv/site"})

	all, err := store.Search(ctx, Filter{}, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 4 || len(all.Events) != 2 || all.Events[0].Status != "preview" || all.Events[1].Status != "blocked" {
		t.Fatalf("primeira página: total %d, %+v", all.Total, all.Events)
	}
	if len(all.Tools) != 2 || all.Tools[0] != "merge_into" || all.Tools[1] != "update_branch" {
		t.Fatalf("ferramentas: %v", all.Tools)
	}
	second, err := store.Search(ctx, Filter{}, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 2 || second.Events[0].Tool != "merge_into" || second.Events[1].Status != "success" {
		t.Fatalf("segunda página: %+v", second.Events)
	}

	cases := map[string]struct {
		filter Filter
		want   int
	}{
		"ferramenta":            {Filter{Tool: "update_branch"}, 3},
		"status":                {Filter{Status: "conflict"}, 1},
		"repositório":           {Filter{Text: "LOJA"}, 2},
		"branch":                {Filter{Text: "develop"}, 1},
		"motivo":                {Filter{Text: "go.mod"}, 1},
		"mensagem":              {Filter{Text: "árvore"}, 1},
		"período":               {Filter{Since: recent.Add(time.Second)}, 1},
		"combinação sem acerto": {Filter{Tool: "merge_into", Status: "success"}, 0},
	}
	for name, c := range cases {
		found, err := store.Search(ctx, c.filter, 0, MaxList)
		if err != nil {
			t.Fatal(err)
		}
		if found.Total != c.want || len(found.Events) != c.want {
			t.Errorf("%s: %d chamadas, esperado %d", name, found.Total, c.want)
		}
		if len(found.Tools) != 2 {
			t.Errorf("%s: a lista de ferramentas não depende do filtro: %v", name, found.Tools)
		}
	}
	if _, err := store.Search(ctx, Filter{}, -1, 10); err == nil {
		t.Error("deslocamento negativo deveria ser recusado")
	}
}

func TestAppendBoundsWhatAToolReturned(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "audit"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("ã", 5000)
	reasons := make([]string, 40)
	for i := range reasons {
		reasons[i] = long
	}
	if err := store.Append(context.Background(), Event{Tool: "merge_into", Status: "blocked", Message: long, Reasons: reasons}); err != nil {
		t.Fatal(err)
	}
	events, err := store.List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || len(events[0].Reasons) != maxReasons || len([]rune(events[0].Message)) != maxText+1 {
		t.Fatalf("evento não foi limitado: %d motivos, mensagem de %d caracteres", len(events[0].Reasons), len([]rune(events[0].Message)))
	}
}
