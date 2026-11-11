// Package console renders the read-only local console from audit events.
package console

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"sort"
	"strconv"
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
	Tool       string
	Mode       string
	Repository string
	Refs       []audit.Ref
	Time       string
	Duration   string
	Status     string
	Tone       string
	Message    string
	Reasons    []string
}

// Option is one choice of a filter.
type Option struct {
	Value    string
	Label    string
	Selected bool
}

// AuditView is the filterable history. Unlike the indicators, it covers
// every retained call, one page at a time.
type AuditView struct {
	Tools    []Option
	Statuses []Option
	Periods  []Option
	Text     string
	Filtered bool
	Total    int
	From     int
	To       int
	Previous string
	Next     string
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
	Audit         AuditView
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

// PageSize is how many calls one audit page lists.
const PageSize = 50

// periods are the choices of the period filter, shortest first.
var periods = []struct {
	value string
	label string
	span  time.Duration
}{
	{"24h", "Últimas 24 horas", 24 * time.Hour},
	{"7d", "Últimos 7 dias", 7 * 24 * time.Hour},
}

// Query is the audit page's address: which calls, and which page of them.
type Query struct {
	Tool   string
	Status string
	Period string
	Text   string
	Page   int
}

// ParseQuery reads the address. A value the page does not offer is dropped, not rejected.
func ParseQuery(values url.Values) Query {
	query := Query{
		Tool:   strings.TrimSpace(values.Get("tool")),
		Status: strings.TrimSpace(values.Get("status")),
		Text:   strings.TrimSpace(values.Get("q")),
		Page:   1,
	}
	if len([]rune(query.Text)) > 200 {
		query.Text = string([]rune(query.Text)[:200])
	}
	for _, period := range periods {
		if values.Get("period") == period.value {
			query.Period = period.value
		}
	}
	if page, err := strconv.Atoi(values.Get("page")); err == nil && page > 1 && page <= 100000 {
		query.Page = page
	}
	return query
}

// Filter is the search the store runs for this query.
func (q Query) Filter(now time.Time) audit.Filter {
	filter := audit.Filter{Tool: q.Tool, Status: q.Status, Text: q.Text}
	for _, period := range periods {
		if q.Period == period.value {
			filter.Since = now.Add(-period.span)
		}
	}
	return filter
}

// Offset is how many matching calls come before this page.
func (q Query) Offset() int {
	return (q.Page - 1) * PageSize
}

func (q Query) filtered() bool {
	return q.Tool != "" || q.Status != "" || q.Period != "" || q.Text != ""
}

// address is the audit URL for the same filters on another page.
func (q Query) address(page int) string {
	values := url.Values{}
	for name, value := range map[string]string{"tool": q.Tool, "status": q.Status, "period": q.Period, "q": q.Text} {
		if value != "" {
			values.Set(name, value)
		}
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	if len(values) == 0 {
		return "/web/audit"
	}
	return "/web/audit?" + values.Encode()
}

// NewAuditPage builds the history page from one search result.
func NewAuditPage(query Query, found audit.Found, unavailable bool, now time.Time) Page {
	page := NewPage(SectionAudit, nil, unavailable, now)
	view := AuditView{Text: query.Text, Filtered: query.filtered(), Total: found.Total}

	view.Tools = []Option{{Label: "Todas", Selected: query.Tool == ""}}
	known := false
	for _, tool := range found.Tools {
		known = known || tool == query.Tool
		view.Tools = append(view.Tools, Option{Value: tool, Label: tool, Selected: tool == query.Tool})
	}
	if query.Tool != "" && !known {
		view.Tools = append(view.Tools, Option{Value: query.Tool, Label: query.Tool, Selected: true})
	}

	view.Statuses = []Option{{Label: "Todos", Selected: query.Status == ""}}
	known = false
	for _, status := range statuses {
		name := string(status.status)
		known = known || name == query.Status
		view.Statuses = append(view.Statuses, Option{Value: name, Label: name, Selected: name == query.Status})
	}
	if query.Status != "" && !known {
		view.Statuses = append(view.Statuses, Option{Value: query.Status, Label: query.Status, Selected: true})
	}

	view.Periods = []Option{{Label: fmt.Sprintf("Últimos %d dias", page.RetentionDays), Selected: query.Period == ""}}
	for _, period := range periods {
		view.Periods = append(view.Periods, Option{Value: period.value, Label: period.label, Selected: period.value == query.Period})
	}

	for _, event := range found.Events {
		page.Events = append(page.Events, Event{
			Tool:       event.Tool,
			Mode:       event.Mode,
			Repository: event.Repository,
			Refs:       event.Refs,
			Time:       event.StartedAt.UTC().Format(timeLayout),
			Duration:   formatDuration(event.Duration),
			Status:     event.Status,
			Tone:       tone(event.Status),
			Message:    event.Message,
			Reasons:    event.Reasons,
		})
	}
	if len(found.Events) > 0 {
		view.From = query.Offset() + 1
		view.To = query.Offset() + len(found.Events)
	}
	if query.Page > 1 {
		// A page past the end still offers the way back to calls that exist.
		view.Previous = query.address(min(query.Page-1, max(1, (found.Total+PageSize-1)/PageSize)))
	}
	if query.Offset()+len(found.Events) < found.Total {
		view.Next = query.address(query.Page + 1)
	}
	page.Audit = view
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
