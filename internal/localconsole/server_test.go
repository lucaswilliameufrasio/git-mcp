package localconsole

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git-mcp/internal/audit"
)

type fakeReader struct {
	events []audit.Event
	err    error
	// searched receives the filter and offset of each search.
	searched func(audit.Filter, int)
}

func (f fakeReader) Search(_ context.Context, filter audit.Filter, offset, _ int) (audit.Found, error) {
	if f.searched != nil {
		f.searched(filter, offset)
	}
	return audit.Found{Events: f.events, Total: len(f.events)}, f.err
}

func (f fakeReader) List(context.Context, int) ([]audit.Event, error) {
	return f.events, f.err
}

const testHost = "127.0.0.1:4100"

func serve(reader Reader, change func(*http.Request)) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/web/overview", nil)
	request.RemoteAddr = "127.0.0.1:50000"
	if change != nil {
		change(request)
	}
	recorder := httptest.NewRecorder()
	newHandler(reader, testHost).ServeHTTP(recorder, request)
	return recorder
}

func TestHandlerServesEachPage(t *testing.T) {
	reader := fakeReader{events: []audit.Event{{Tool: "update_branch", Status: "success", StartedAt: time.Now(), Duration: time.Second}}}
	for path, title := range map[string]string{"/web/overview": "Visão geral", "/web/monitoring": "Monitoramento", "/web/audit": "Auditoria"} {
		response := serve(reader, func(r *http.Request) { r.URL.Path = path })
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, response.Code)
		}
		if !strings.Contains(response.Body.String(), "<h1>"+title+"</h1>") {
			t.Fatalf("%s: título ausente", path)
		}
		if response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "default-src 'none'") {
			t.Fatalf("%s: cabeçalhos de proteção ausentes", path)
		}
	}
}

func TestHandlerRefusesRequestsFromOutsideTheLocalBrowser(t *testing.T) {
	cases := map[string]func(*http.Request){
		"peer fora do loopback": func(r *http.Request) { r.RemoteAddr = "192.168.0.10:50000" },
		"host de outro nome":    func(r *http.Request) { r.Host = "exemplo.test:4100" },
		"origem de outro site":  func(r *http.Request) { r.Header.Set("Origin", "https://exemplo.test") },
		"fetch entre sites":     func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	}
	for name, change := range cases {
		if response := serve(fakeReader{}, change); response.Code != http.StatusForbidden {
			t.Errorf("%s: status %d", name, response.Code)
		}
	}
}

func TestHandlerIsReadOnly(t *testing.T) {
	response := serve(fakeReader{}, func(r *http.Request) { r.Method = http.MethodPost })
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", response.Code)
	}
}

func TestHandlerRoutes(t *testing.T) {
	if response := serve(fakeReader{}, func(r *http.Request) { r.URL.Path = "/" }); response.Code != http.StatusFound || response.Header().Get("Location") != "/web/overview" {
		t.Fatalf("raiz: status %d para %q", response.Code, response.Header().Get("Location"))
	}
	if response := serve(fakeReader{}, func(r *http.Request) { r.URL.Path = "/web/outra" }); response.Code != http.StatusNotFound {
		t.Fatalf("caminho desconhecido: status %d", response.Code)
	}
}

func TestHandlerShowsUnreadableAuditAsUnavailable(t *testing.T) {
	response := serve(fakeReader{err: errors.New("disco")}, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Auditoria indisponível") {
		t.Fatalf("status %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "disco") {
		t.Fatal("o texto do erro não vai para a página")
	}
}

func TestStartListensOnLoopbackUntilClosed(t *testing.T) {
	web, err := Start(fakeReader{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(web.URL, "http://127.0.0.1:") {
		t.Fatalf("endereço %q", web.URL)
	}
	response, err := http.Get(web.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "git-mcp") {
		t.Fatalf("status %d", response.StatusCode)
	}
	if err := web.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := http.Get(web.URL); err == nil {
		t.Fatal("o console deveria parar de responder depois de fechado")
	}
}

func TestAuditPageSearchesWithTheAddressFilters(t *testing.T) {
	var got audit.Filter
	var gotOffset int
	reader := fakeReader{
		events:   []audit.Event{{Tool: "merge_into", Status: "conflict", Repository: "/srv/loja", StartedAt: time.Now(), Duration: time.Second}},
		searched: func(filter audit.Filter, offset int) { got, gotOffset = filter, offset },
	}
	response := serve(reader, func(r *http.Request) {
		r.URL.Path = "/web/audit"
		r.URL.RawQuery = "tool=merge_into&status=conflict&period=24h&q=loja&page=3"
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status %d", response.Code)
	}
	if got.Tool != "merge_into" || got.Status != "conflict" || got.Text != "loja" || got.Since.IsZero() || gotOffset != 100 {
		t.Fatalf("filtro %+v, deslocamento %d", got, gotOffset)
	}
	body := response.Body.String()
	if !strings.Contains(body, "/srv/loja") || !strings.Contains(body, `value="loja"`) {
		t.Fatal("a página deveria mostrar o repositório e manter o texto buscado")
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Fatal("o formulário de filtros precisa poder enviar para o próprio console")
	}
}
