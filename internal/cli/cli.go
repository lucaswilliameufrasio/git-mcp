// Package cli is the command line of the git-mcp binary.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"git-mcp/internal/audit"
	"git-mcp/internal/console"
	"git-mcp/internal/gitflow"
	"git-mcp/internal/localconsole"
	"git-mcp/internal/server"
)

const usage = `git-mcp: servidor MCP local para fluxos Git

Uso:
  git-mcp            Inicia o servidor MCP por stdio
  git-mcp doctor     Confere git, worktrees, auditoria, o servidor e os clientes MCP
  git-mcp tools      Lista as ferramentas expostas e o tipo de efeito de cada uma
  git-mcp stats      Mostra no terminal as métricas das chamadas recentes
  git-mcp console    Abre o painel local de métricas em 127.0.0.1
  git-mcp version    Mostra a versão (também --version)
  git-mcp help       Mostra esta página (também --help e -h)
`

// App holds what a command reads and writes, so tests can replace each part.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	// Home is where client configurations and ~/git-worktrees are looked up.
	Home string
	// ServerCommand starts this same binary as a stdio server, for the doctor's handshake.
	ServerCommand func() (*exec.Cmd, error)
}

// New returns the app wired to the real process.
func New() App {
	home, _ := os.UserHomeDir()
	return App{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Home:   home,
		ServerCommand: func() (*exec.Cmd, error) {
			path, err := os.Executable()
			if err != nil {
				return nil, err
			}
			return exec.Command(path), nil
		},
	}
}

// Run executes one command line, without the program name, and returns the exit code.
// Code 1 is a failed command and code 2 is a command line this binary does not know.
func (a App) Run(args []string) int {
	logger := log.New(a.Stderr, "git-mcp: ", log.LstdFlags)
	if len(args) == 0 {
		svc := gitflow.New(logger)
		if err := server.Run(context.Background(), svc, logger, observer(logger)); err != nil {
			logger.Print(err)
			return 1
		}
		return 0
	}
	if len(args) > 1 {
		fmt.Fprintf(a.Stderr, "git-mcp: %s não recebe argumentos\n\n%s", args[0], usage)
		return 2
	}
	var err error
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, usage)
	case "version", "--version", "-version":
		fmt.Fprintf(a.Stdout, "git-mcp %s\n", server.Version())
	case "doctor":
		err = a.doctor()
	case "tools":
		err = a.tools()
	case "stats":
		err = a.stats()
	case "console":
		err = a.console(logger)
	default:
		fmt.Fprintf(a.Stderr, "git-mcp: comando desconhecido: %s\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "git-mcp: %v\n", err)
		return 1
	}
	return 0
}

func openAudit() (*audit.FileStore, error) {
	dir, err := audit.DefaultDirectory()
	if err != nil {
		return nil, err
	}
	return audit.NewFileStore(dir, audit.Retention)
}

// observer records each finished call for the console.
// The audit never decides a call: when it cannot be opened or written, the
// tools keep working and the failure goes to stderr.
func observer(logger *log.Logger) func(audit.Event) {
	store, err := openAudit()
	if err != nil {
		logger.Printf("auditoria local desligada: %v", err)
		return nil
	}
	return func(event audit.Event) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := store.Append(ctx, event); err != nil {
			logger.Printf("auditoria: chamada de %s não gravada: %v", event.Tool, err)
		}
	}
}

// console serves the panel until interrupted. It is a separate process from
// the stdio server, so stdout is free for the address.
func (a App) console(logger *log.Logger) error {
	store, err := openAudit()
	if err != nil {
		return fmt.Errorf("auditoria local indisponível: %w", err)
	}
	stopped := make(chan error, 1)
	web, err := localconsole.Start(store, func(err error) { stopped <- err })
	if err != nil {
		return fmt.Errorf("o console não iniciou: %w", err)
	}
	fmt.Fprintln(a.Stdout, web.URL)
	logger.Print("console local aberto; Ctrl+C encerra")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-stopped:
		return fmt.Errorf("o console parou: %w", err)
	case <-ctx.Done():
	}
	if err := web.Close(); err != nil {
		return fmt.Errorf("o console não fechou: %w", err)
	}
	return nil
}

func (a App) tools() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tools, err := server.Tools(ctx)
	if err != nil {
		return err
	}
	for i, tool := range tools {
		if i > 0 {
			fmt.Fprintln(a.Stdout)
		}
		effect := "altera"
		switch {
		case tool.Annotations != nil && tool.Annotations.ReadOnlyHint:
			effect = "só leitura"
		case tool.Annotations != nil && tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint:
			effect = "destrutiva"
		}
		fmt.Fprintf(a.Stdout, "%s (%s)\n  %s\n", tool.Name, effect, tool.Description)
	}
	return nil
}

// stats prints the same numbers as the console pages, from the same sample.
func (a App) stats() error {
	store, err := openAudit()
	if err != nil {
		return fmt.Errorf("auditoria local indisponível: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := store.List(ctx, audit.MaxList)
	if err != nil {
		return fmt.Errorf("auditoria local ilegível: %w", err)
	}
	page := console.NewPage(console.SectionMonitoring, events, false, time.Now())
	if page.Count == 0 {
		fmt.Fprintln(a.Stdout, "Nenhuma chamada gravada.")
		return nil
	}
	fmt.Fprintf(a.Stdout, "%d chamadas na amostra (as %d mais recentes dos últimos %d dias)\n", page.Count, page.SampleLimit, page.RetentionDays)
	fmt.Fprintf(a.Stdout, "%d interrompidas, duração média %s, mais lenta %s em %s\n\n", page.Stopped, page.Average, page.Slowest, page.SlowestTool)

	byStatus := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(byStatus, "STATUS\tCHAMADAS")
	for _, status := range page.Statuses {
		fmt.Fprintf(byStatus, "%s\t%d\n", status.Name, status.Count)
	}
	if err := byStatus.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout)

	byTool := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(byTool, "FERRAMENTA\tCHAMADAS\tINTERROMPIDAS\tMÉDIA\tMAIS LENTA\tÚLTIMA\t")
	for _, tool := range page.Tools {
		fmt.Fprintf(byTool, "%s\t%d\t%d\t%s\t%s\t%s\t%s%s\n", tool.Tool, tool.Calls, tool.Stopped, tool.Average, tool.Slowest, tool.Last, tool.Plus, tool.Minus)
	}
	return byTool.Flush()
}

var errProblems = errors.New("o doctor encontrou problemas")
