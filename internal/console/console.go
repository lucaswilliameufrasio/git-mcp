// Package console renders the read-only local console from audit events.
package console

import (
	_ "embed"
	"html/template"
	"io"
	"sort"
	"strings"
	"time"

	"git-mcp/internal/audit"
	"git-mcp/internal/gitflow"
)

//go:embed console.html
var markup string

//go:embed console.css
var stylesheet string

var pageTemplate = template.Must(template.New("console").Parse(markup))

const (
	SectionOverview   = "overview"
	SectionMonitoring = "monitoring"
	SectionAudit      = "audit"
)

const timeLayout = "02/01/2006 15:04:05 UTC"

// barWidth is the diffstat length of the most called tool; the others scale to it.
const barWidth = 20

// statuses lists every result a flow can return, in the order the README presents them.
var statuses = []struct {
	status  gitflow.Status
	meaning string
}{
	{gitflow.StatusPreview, "Descreveu o efeito e devolveu a confirmação. Nada mudou."},
	{gitflow.StatusSuccess, "O fluxo terminou."},
	{gitflow.StatusBlocked, "Recusado antes de alterar qualquer branch."},
	{gitflow.StatusConflict, "Parou em conflito. O Git ficou com a operação em andamento."},
	{gitflow.StatusPartial, "Parte do fluxo aconteceu e o restante não foi enviado."},
}

// StatusCount is one row of the result breakdown.
type StatusCount struct {
	Name    string
	Tone    string
	Count   int
	Meaning string
}

// ToolStat aggregates the sampled calls of one tool.
type ToolStat struct {
	Tool    string
	Calls   int
	Stopped int
	Average string
	Slowest string
	Last    string
	// Plus and Minus draw the calls as a diffstat: finished against stopped.
	Plus  string
	Minus string
}

// Event is one audit row.
type Event struct {
	Tool     string
	Time     string
	Duration string
	Status   string
	Tone     string
}

// Page is everything the template shows. It summarizes the supplied sample, not the whole history.
type Page struct {
	Section       string
	Title         string
	GeneratedAt   string
	Unavailable   bool
	SampleLimit   int
	RetentionDays int
	Count         int
	Stopped       int
	Average       string
	Slowest       string
	SlowestTool   string
	Statuses      []StatusCount
	Tools         []ToolStat
	Events        []Event
	Style         template.CSS
}

// NewPage builds the page for one section. unavailable means the audit could
// not be read, which the page reports instead of showing zero activity.
func NewPage(section string, events []audit.Event, unavailable bool, now time.Time) Page {
	page := Page{
		Section:       section,
		Unavailable:   unavailable,
		SampleLimit:   audit.MaxList,
		RetentionDays: int(audit.Retention / (24 * time.Hour)),
		GeneratedAt:   now.UTC().Format(timeLayout),
		Style:         template.CSS(stylesheet),
	}
	switch section {
	case SectionMonitoring:
		page.Title = "Monitoramento"
	case SectionAudit:
		page.Title = "Auditoria"
	default:
		page.Section = SectionOverview
		page.Title = "Visão geral"
	}

	type toolTotals struct {
		calls, stopped int
		total, slowest time.Duration
		last           time.Time
	}
	byStatus := make(map[string]int)
	byTool := make(map[string]*toolTotals)
	var total, slowest time.Duration
	for _, event := range events {
		page.Count++
		total += event.Duration
		byStatus[event.Status]++
		totals := byTool[event.Tool]
		if totals == nil {
			totals = &toolTotals{}
			byTool[event.Tool] = totals
		}
		totals.calls++
		totals.total += event.Duration
		totals.slowest = max(totals.slowest, event.Duration)
		if event.StartedAt.After(totals.last) {
			totals.last = event.StartedAt
		}
		if stopped(event.Status) {
			page.Stopped++
			totals.stopped++
		}
		if event.Duration > slowest || page.SlowestTool == "" {
			slowest = event.Duration
			page.SlowestTool = event.Tool
		}
		page.Events = append(page.Events, Event{
			Tool:     event.Tool,
			Time:     event.StartedAt.UTC().Format(timeLayout),
			Duration: formatDuration(event.Duration),
			Status:   event.Status,
			Tone:     tone(event.Status),
		})
	}
	if page.Count > 0 {
		page.Average = formatDuration(total / time.Duration(page.Count))
		page.Slowest = formatDuration(slowest)
	}

	for _, known := range statuses {
		name := string(known.status)
		page.Statuses = append(page.Statuses, StatusCount{Name: name, Tone: tone(name), Count: byStatus[name], Meaning: known.meaning})
		delete(byStatus, name)
	}
	// A status this build does not know still counts; it came from another version's audit files.
	unknown := make([]string, 0, len(byStatus))
	for name := range byStatus {
		unknown = append(unknown, name)
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		page.Statuses = append(page.Statuses, StatusCount{Name: name, Tone: tone(name), Count: byStatus[name], Meaning: "Status que esta versão não conhece."})
	}

	busiest := 0
	for _, totals := range byTool {
		busiest = max(busiest, totals.calls)
	}
	for tool, totals := range byTool {
		plus, minus := diffstat(totals.calls, totals.stopped, busiest)
		page.Tools = append(page.Tools, ToolStat{
			Tool:    tool,
			Calls:   totals.calls,
			Stopped: totals.stopped,
			Average: formatDuration(totals.total / time.Duration(totals.calls)),
			Slowest: formatDuration(totals.slowest),
			Last:    totals.last.UTC().Format(timeLayout),
			Plus:    plus,
			Minus:   minus,
		})
	}
	sort.Slice(page.Tools, func(i, j int) bool {
		if page.Tools[i].Calls != page.Tools[j].Calls {
			return page.Tools[i].Calls > page.Tools[j].Calls
		}
		return page.Tools[i].Tool < page.Tools[j].Tool
	})
	return page
}

// Render writes the page as one self-contained HTML document.
func Render(w io.Writer, page Page) error {
	return pageTemplate.Execute(w, page)
}

// stopped reports whether the flow ended without doing what was asked.
func stopped(status string) bool {
	return status != string(gitflow.StatusPreview) && status != string(gitflow.StatusSuccess)
}

// tone names the state the repository was left in, which is what the color marks.
func tone(status string) string {
	switch gitflow.Status(status) {
	case gitflow.StatusSuccess:
		return "done"
	case gitflow.StatusBlocked:
		return "refused"
	case gitflow.StatusConflict, gitflow.StatusPartial:
		return "attention"
	default:
		return "unchanged"
	}
}

// diffstat scales one tool's calls against the busiest tool, the way git
// scales a diffstat to the widest file. A nonzero side never rounds to nothing.
func diffstat(calls, stoppedCalls, busiest int) (string, string) {
	if calls == 0 || busiest == 0 {
		return "", ""
	}
	finished := calls - stoppedCalls
	width := max(1, (calls*barWidth+busiest/2)/busiest)
	if finished > 0 && stoppedCalls > 0 {
		width = max(2, width)
	}
	minus := (stoppedCalls*width + calls/2) / calls
	if stoppedCalls > 0 {
		minus = max(1, minus)
	}
	if finished > 0 {
		minus = min(minus, width-1)
	}
	return strings.Repeat("+", width-minus), strings.Repeat("-", minus)
}

func formatDuration(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}
