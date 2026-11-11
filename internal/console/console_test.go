package console

import (
	"net/url"
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

func TestParseQueryKeepsOnlyWhatThePageOffers(t *testing.T) {
	query := ParseQuery(url.Values{"tool": {" merge_into "}, "status": {"conflict"}, "period": {"7d"}, "q": {" loja "}, "page": {"3"}})
	if query != (Query{Tool: "merge_into", Status: "conflict", Period: "7d", Text: "loja", Page: 3}) {
		t.Fatalf("consulta %+v", query)
	}
	if query.Offset() != 2*PageSize {
		t.Fatalf("deslocamento %d", query.Offset())
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	if since := query.Filter(now).Since; !since.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("início do período %s", since)
	}

	loose := ParseQuery(url.Values{"period": {"1ano"}, "page": {"-4"}, "q": {strings.Repeat("a", 500)}})
	if loose.Period != "" || loose.Page != 1 || len(loose.Text) != 200 {
		t.Fatalf("valores fora do oferecido: %+v", loose)
	}
	if !loose.Filter(now).Since.IsZero() {
		t.Fatal("sem período, a busca cobre toda a retenção")
	}
}

func TestAuditPageShowsDetailsFiltersAndNeighbours(t *testing.T) {
	query := Query{Tool: "merge_into", Text: "loja & cia", Page: 2}
	found := audit.Found{
		Total: 120,
		Tools: []string{"merge_into", "update_branch"},
		Events: []audit.Event{{
			Tool: "merge_into", Mode: "execute", Status: "conflict", Repository: "/srv/loja",
			Refs:    []audit.Ref{{Name: "target", Value: "develop"}},
			Message: "Merge parou em conflito", Reasons: []string{"<b>go.mod</b>"},
			StartedAt: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC), Duration: 2 * time.Second,
		}},
	}
	page := NewAuditPage(query, found, false, time.Now())
	if page.Audit.From != 51 || page.Audit.To != 51 || page.Audit.Total != 120 || !page.Audit.Filtered {
		t.Fatalf("faixa %+v", page.Audit)
	}
	if page.Audit.Previous != "/web/audit?q=loja+%26+cia&tool=merge_into" || page.Audit.Next != "/web/audit?page=3&q=loja+%26+cia&tool=merge_into" {
		t.Fatalf("vizinhas: %q e %q", page.Audit.Previous, page.Audit.Next)
	}

	var out strings.Builder
	if err := Render(&out, page); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		`<option value="merge_into" selected>`, `value="loja &amp; cia"`, "/srv/loja", "<code>execute</code>",
		"<code>develop</code>", "Merge parou em conflito", "&lt;b&gt;go.mod&lt;/b&gt;", "Chamadas 51 a 51 de 120 com estes filtros",
		`rel="prev"`, `rel="next"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("faltou %q", want)
		}
	}
}

func TestAuditPageEmptyStates(t *testing.T) {
	render := func(query Query, found audit.Found) string {
		var out strings.Builder
		if err := Render(&out, NewAuditPage(query, found, false, time.Now())); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if html := render(Query{Page: 1}, audit.Found{}); !strings.Contains(html, "Nenhuma chamada gravada") || strings.Contains(html, "Limpar filtros") {
		t.Error("sem chamadas e sem filtro")
	}
	if html := render(Query{Status: "partial", Page: 1}, audit.Found{}); !strings.Contains(html, "Nenhuma chamada com estes filtros") || !strings.Contains(html, "Limpar filtros") {
		t.Error("filtro sem acerto")
	}
	past := NewAuditPage(Query{Page: 9}, audit.Found{Total: 60}, false, time.Now())
	if past.Audit.Previous != "/web/audit?page=2" {
		t.Errorf("página além do fim deveria voltar à última: %q", past.Audit.Previous)
	}
	old := NewAuditPage(Query{Page: 1}, audit.Found{Total: 1, Events: []audit.Event{{Tool: "update_branch", Status: "success"}}}, false, time.Now())
	var out strings.Builder
	if err := Render(&out, old); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "<td>—") {
		t.Error("evento gravado pela 0.2.0, sem repositório, mostra travessão")
	}
}
