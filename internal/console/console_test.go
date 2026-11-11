package console

import (
	"strings"
	"testing"
	"time"

	"git-mcp/internal/audit"
)

func sample() []audit.Event {
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	call := func(tool, status string, minutesAgo int, duration time.Duration) audit.Event {
		return audit.Event{Tool: tool, Status: status, StartedAt: start.Add(-time.Duration(minutesAgo) * time.Minute), Duration: duration}
	}
	return []audit.Event{
		call("update_branch", "success", 1, 900*time.Millisecond),
		call("update_branch", "preview", 2, 300*time.Millisecond),
		call("merge_into", "conflict", 3, 1500*time.Millisecond),
		call("update_branch", "blocked", 4, 100*time.Millisecond),
		call("reset_and_push", "partial", 5, 200*time.Millisecond),
	}
}

func TestNewPageSummarizesTheSample(t *testing.T) {
	page := NewPage(SectionMonitoring, sample(), false, time.Date(2026, 11, 11, 12, 30, 0, 0, time.UTC))

	if page.Count != 5 || page.Stopped != 3 || page.Average != "600ms" {
		t.Fatalf("resumo: %d chamadas, %d interrompidas, média %s", page.Count, page.Stopped, page.Average)
	}
	if page.Slowest != "1.5s" || page.SlowestTool != "merge_into" {
		t.Fatalf("mais lenta: %s em %s", page.Slowest, page.SlowestTool)
	}
	counts := make(map[string]int)
	for _, status := range page.Statuses {
		counts[status.Name] = status.Count
	}
	if len(page.Statuses) != 5 || counts["preview"] != 1 || counts["success"] != 1 || counts["blocked"] != 1 || counts["conflict"] != 1 || counts["partial"] != 1 {
		t.Fatalf("status: %+v", page.Statuses)
	}
	if len(page.Tools) != 3 {
		t.Fatalf("ferramentas: %+v", page.Tools)
	}
	busiest := page.Tools[0]
	if busiest.Tool != "update_branch" || busiest.Calls != 3 || busiest.Stopped != 1 || busiest.Slowest != "900ms" {
		t.Fatalf("ferramenta mais chamada: %+v", busiest)
	}
	if busiest.Last != "11/11/2026 11:59:00 UTC" {
		t.Fatalf("última chamada: %s", busiest.Last)
	}
	if len(busiest.Plus)+len(busiest.Minus) != barWidth || busiest.Minus == "" || busiest.Plus == "" {
		t.Fatalf("barra da mais chamada: %q %q", busiest.Plus, busiest.Minus)
	}
}

func TestDiffstatKeepsEachSideVisible(t *testing.T) {
	cases := []struct {
		calls, stopped, busiest int
		plus, minus             int
	}{
		{calls: 20, stopped: 0, busiest: 20, plus: 20, minus: 0},
		{calls: 20, stopped: 20, busiest: 20, plus: 0, minus: 20},
		{calls: 20, stopped: 5, busiest: 20, plus: 15, minus: 5},
		{calls: 1, stopped: 0, busiest: 200, plus: 1, minus: 0},
		{calls: 2, stopped: 1, busiest: 200, plus: 1, minus: 1},
		{calls: 200, stopped: 1, busiest: 200, plus: 19, minus: 1},
		{calls: 200, stopped: 199, busiest: 200, plus: 1, minus: 19},
	}
	for _, c := range cases {
		plus, minus := diffstat(c.calls, c.stopped, c.busiest)
		if len(plus) != c.plus || len(minus) != c.minus {
			t.Errorf("diffstat(%d, %d, %d) = %d+ %d-, esperado %d+ %d-", c.calls, c.stopped, c.busiest, len(plus), len(minus), c.plus, c.minus)
		}
	}
}

func TestRenderEscapesStoredTextAndKeepsUnknownStatus(t *testing.T) {
	events := []audit.Event{{Tool: `<script>alert(1)</script>`, Status: "adiado", StartedAt: time.Now(), Duration: time.Second}}
	for _, section := range []string{SectionOverview, SectionMonitoring, SectionAudit} {
		var out strings.Builder
		page := NewPage(section, events, false, time.Now())
		if err := Render(&out, page); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "<script") {
			t.Fatalf("%s: texto gravado saiu sem escape", section)
		}
		if page.Stopped != 1 {
			t.Fatalf("%s: status desconhecido não conta como concluído", section)
		}
	}
	var overview strings.Builder
	if err := Render(&overview, NewPage(SectionOverview, events, false, time.Now())); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(overview.String(), "adiado") {
		t.Fatal("status desconhecido deveria aparecer na visão geral")
	}
}

func TestRenderReportsUnavailableAuditInsteadOfZero(t *testing.T) {
	var out strings.Builder
	if err := Render(&out, NewPage(SectionOverview, nil, true, time.Now())); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, "Auditoria indisponível") || strings.Contains(html, "Chamadas na amostra") {
		t.Fatal("leitura com falha não pode aparecer como zero chamadas")
	}
}

func TestUnknownSectionFallsBackToOverview(t *testing.T) {
	if page := NewPage("outra", nil, false, time.Now()); page.Section != SectionOverview {
		t.Fatalf("seção %q", page.Section)
	}
}
