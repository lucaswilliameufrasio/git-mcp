package cli

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git-mcp/internal/audit"
	"git-mcp/internal/gitflow"
	"git-mcp/internal/server"
)

const serveEnv = "GIT_MCP_TEST_SERVE"

// TestMain lets the test binary stand in for git-mcp when the doctor starts a stdio server.
func TestMain(m *testing.M) {
	if os.Getenv(serveEnv) == "1" {
		logger := log.New(os.Stderr, "", 0)
		if err := server.Run(context.Background(), gitflow.New(logger), logger, nil); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type harness struct {
	app    App
	stdout *strings.Builder
	stderr *strings.Builder
	state  string
}

func newHarness(t *testing.T) harness {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	return harness{
		stdout: stdout,
		stderr: stderr,
		state:  state,
		app: App{
			Stdout: stdout,
			Stderr: stderr,
			Home:   t.TempDir(),
			ServerCommand: func() (*exec.Cmd, error) {
				cmd := exec.Command(os.Args[0])
				cmd.Env = append(os.Environ(), serveEnv+"=1")
				return cmd, nil
			},
		},
	}
}

func (h harness) record(t *testing.T, events ...audit.Event) {
	t.Helper()
	dir, err := audit.DefaultDirectory()
	if err != nil {
		t.Fatal(err)
	}
	store, err := audit.NewFileStore(dir, audit.Retention)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if err := store.Append(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVersionAndHelp(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		h := newHarness(t)
		if code := h.app.Run([]string{arg}); code != 0 || h.stdout.String() != "git-mcp "+server.Version()+"\n" {
			t.Fatalf("%s: código %d, saída %q", arg, code, h.stdout.String())
		}
	}
	for _, arg := range []string{"help", "--help", "-h"} {
		h := newHarness(t)
		if code := h.app.Run([]string{arg}); code != 0 || !strings.Contains(h.stdout.String(), "git-mcp doctor") {
			t.Fatalf("%s: código %d, saída %q", arg, code, h.stdout.String())
		}
	}
}

func TestUnknownCommandLineExitsWithUsage(t *testing.T) {
	for _, args := range [][]string{{"consle"}, {"doctor", "sobrando"}} {
		h := newHarness(t)
		if code := h.app.Run(args); code != 2 || !strings.Contains(h.stderr.String(), "Uso:") || h.stdout.Len() != 0 {
			t.Fatalf("%v: código %d, stderr %q", args, code, h.stderr.String())
		}
	}
}

func TestToolsListsEveryToolWithItsEffect(t *testing.T) {
	h := newHarness(t)
	if code := h.app.Run([]string{"tools"}); code != 0 {
		t.Fatalf("código %d: %s", code, h.stderr.String())
	}
	out := h.stdout.String()
	for _, want := range []string{"repository_inspect (só leitura)", "update_branch (altera)", "reset_and_push (destrutiva)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("faltou %q em:\n%s", want, out)
		}
	}
}

func TestStatsPrintsTheSample(t *testing.T) {
	h := newHarness(t)
	if code := h.app.Run([]string{"stats"}); code != 0 || !strings.Contains(h.stdout.String(), "Nenhuma chamada gravada.") {
		t.Fatalf("sem eventos: código %d, saída %q", code, h.stdout.String())
	}

	h = newHarness(t)
	now := time.Now().UTC()
	h.record(t,
		audit.Event{Tool: "update_branch", Status: "success", StartedAt: now, Duration: 400 * time.Millisecond},
		audit.Event{Tool: "update_branch", Status: "blocked", StartedAt: now, Duration: 200 * time.Millisecond},
	)
	if code := h.app.Run([]string{"stats"}); code != 0 {
		t.Fatalf("código %d: %s", code, h.stderr.String())
	}
	out := h.stdout.String()
	for _, want := range []string{"2 chamadas na amostra", "1 interrompidas, duração média 300ms", "blocked", "update_branch"} {
		if !strings.Contains(out, want) {
			t.Fatalf("faltou %q em:\n%s", want, out)
		}
	}
}

func TestDoctorChecksTheEnvironmentWithoutCreatingIt(t *testing.T) {
	h := newHarness(t)
	write(t, filepath.Join(h.app.Home, ".codex", "config.toml"), "[mcp_servers.git-mcp]\ncommand = \"/opt/bin/git-mcp\"\n")
	write(t, filepath.Join(h.app.Home, ".claude.json"), `{"mcpServers":{"jira":{"command":"jira-mcp"}}}`)

	if code := h.app.Run([]string{"doctor"}); code != 0 {
		t.Fatalf("código %d\n%s\n%s", code, h.stdout.String(), h.stderr.String())
	}
	out := h.stdout.String()
	for _, want := range []string{
		"git version",
		"ainda não existe; é criado na primeira integração",
		"ainda não existe; é criado quando o servidor inicia",
		"handshake com git-mcp " + server.Version() + "; 10 ferramentas",
		"configurado      Codex",
		"-> /opt/bin/git-mcp",
		"não configurado  Claude Code",
		"sem o cliente    Grok",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("faltou %q em:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FALHA") {
		t.Fatalf("ambiente saudável não deveria falhar:\n%s", out)
	}
	for _, path := range []string{filepath.Join(h.app.Home, "git-worktrees"), filepath.Join(h.state, "git-mcp")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("o doctor criou %s", path)
		}
	}
}

func TestDoctorFailsWhenTheServerDoesNotAnswer(t *testing.T) {
	h := newHarness(t)
	h.app.ServerCommand = func() (*exec.Cmd, error) {
		return exec.Command(filepath.Join(t.TempDir(), "ausente")), nil
	}
	if code := h.app.Run([]string{"doctor"}); code != 1 || !strings.Contains(h.stdout.String(), "FALHA") {
		t.Fatalf("código %d\n%s", code, h.stdout.String())
	}
}

func TestClientDetection(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"git":{"command":"C:\\tools\\git-mcp.exe"}}}`)
	write(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{"mcp":{"git-mcp":{"type":"local","command":["/usr/local/bin/git-mcp"]}}}`)
	write(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.jira]\ncommand = \"jira-mcp\"\n\n[other]\ncommand = \"git-mcp\"\n")
	write(t, filepath.Join(home, ".grok", "config.toml"), "[mcp_servers.git]\ncommand = 'git-mcp'\n")

	got := make(map[string]clientConfig)
	for _, client := range detectClients(home) {
		got[client.Name] = client
	}
	if got["Claude Code"].Command != `C:\tools\git-mcp.exe` {
		t.Errorf("Claude Code: %+v", got["Claude Code"])
	}
	if got["OpenCode"].Command != "/usr/local/bin/git-mcp" {
		t.Errorf("OpenCode: %+v", got["OpenCode"])
	}
	if !got["Codex"].Present || got["Codex"].Command != "" {
		t.Errorf("Codex só tem o comando fora de mcp_servers: %+v", got["Codex"])
	}
	if got["Grok"].Command != "git-mcp" {
		t.Errorf("Grok: %+v", got["Grok"])
	}

	write(t, filepath.Join(home, ".claude.json"), "{")
	for _, client := range detectClients(home) {
		if client.Name == "Claude Code" && !client.Unreadable {
			t.Errorf("JSON inválido deveria ser ilegível: %+v", client)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
