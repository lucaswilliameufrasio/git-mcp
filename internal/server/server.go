package server

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"git-mcp/internal/audit"
	"git-mcp/internal/gitflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is the MCP implementation version. Release builds replace it with -X.
var version = "0.3.0"

// Version is the version this binary reports to MCP clients.
func Version() string {
	return version
}

// Tools lists what the server exposes, by asking it over an in-memory session.
func Tools(ctx context.Context) ([]*mcp.Tool, error) {
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New(gitflow.New(nil), nil, nil).Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = serverSession.Close() }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "git-mcp", Version: version}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listed.Tools, nil
}

// New builds the stdio MCP server around a gitflow service.
// observe, when not nil, receives the metadata of every finished tool call.
func New(svc gitflow.Service, logger *log.Logger, observe func(audit.Event)) *mcp.Server {
	if logger == nil {
		logger = log.New(logDiscard{}, "", 0)
	}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "git-mcp", Version: version}, nil)
	add(mcpServer, logger, observe, "repository_inspect",
		"Lê o work tree: branch, alterações, operação em andamento e worktrees. Com fetch, compara uma branch ao remoto via ls-remote e não move refs locais.",
		true, false,
		svc.Inspect,
	)
	add(mcpServer, logger, observe, "update_branch",
		"Atualiza uma branch local com git pull --rebase somente quando ela está estritamente atrás do remoto. Não faz stash, não descarta alterações e não reescreve commits locais da base. O padrão é prévia; execute exige a confirmação exata.",
		false, false,
		svc.UpdateBranch,
	)
	add(mcpServer, logger, observe, "rebase_onto",
		"Atualiza a branch-base com a regra segura e faz git rebase não interativo da origem sobre ela. Não abre editor. Em conflito, o rebase permanece para resolução manual.",
		false, true,
		svc.RebaseOnto,
	)
	add(mcpServer, logger, observe, "integrate_branch",
		"Integra a origem no destino numa worktree em ~/git-worktrees, sem trocar o checkout atual. Atualiza o destino com git pull --rebase quando isso é seguro. publish envia a branch. Em conflito ou falha de envio, a worktree fica preservada.",
		false, false,
		svc.IntegrateBranch,
	)
	add(mcpServer, logger, observe, "merge_into",
		"Atualiza o destino no checkout principal, faz o merge e volta à branch inicial se tudo terminar. Em conflito, o checkout fica no destino com o merge em andamento. publish envia a branch.",
		false, false,
		svc.MergeInto,
	)
	add(mcpServer, logger, observe, "create_release_branch",
		"Atualiza a base informada e cria a branch de release nesse commit. Se a release já existe, replace_existing move a ponta local; a prévia lista commits que sairão da branch. publish envia sem sobrescrever o remoto.",
		false, true,
		svc.CreateReleaseBranch,
	)
	add(mcpServer, logger, observe, "create_release_from_tag",
		"Cria a branch de release no commit da tag informada. Valida a tag no remoto e não faz pull --rebase de branch-base. replace_existing e publish seguem a mesma autorização da release por branch.",
		false, true,
		svc.CreateReleaseFromTag,
	)
	add(mcpServer, logger, observe, "reset_and_push",
		"Substitui a branch de destino pelo commit da origem, no local e no remoto. Exige prévia e confirmação com os oids. Usa reset --hard e um lease preso ao oid observado; o lease sozinho não autoriza a operação. Se o remoto mudar, não envia.",
		false, true,
		svc.ResetAndPush,
	)
	add(mcpServer, logger, observe, "delete_current_branch",
		"Leva o checkout ao destino e remove a branch atual com git branch -d, só quando ela está mesclada nesse destino. Não usa -D. Recusa antes de trocar de branch quando a remoção não é segura.",
		false, true,
		svc.DeleteCurrentBranch,
	)
	add(mcpServer, logger, observe, "clean_merged_branches",
		"Remove, com git branch -d, somente as branches nomeadas na confirmação que continuam mescladas em HEAD e fora de outras worktrees. Não há lista protegida fixa além da branch atual e das worktrees.",
		false, true,
		svc.CleanMergedBranches,
	)
	return mcpServer
}

// Run serves MCP on stdin and stdout. Diagnostics belong on the logger, which must write to stderr.
func Run(ctx context.Context, svc gitflow.Service, logger *log.Logger, observe func(audit.Event)) error {
	return New(svc, logger, observe).Run(ctx, &mcp.StdioTransport{})
}

func add[I any](mcpServer *mcp.Server, logger *log.Logger, observe func(audit.Event), name, description string, readOnly, destructive bool, handle func(context.Context, I) gitflow.Result) {
	openWorld := true
	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        name,
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    readOnly,
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, any, error) {
		started := time.Now().UTC()
		result := handle(ctx, in)
		logger.Printf("%s %s", name, result.Status)
		if observe != nil {
			observe(describe(name, readOnly, in, result, started))
		}
		return callResult(result)
	})
}

func callResult(result gitflow.Result) (*mcp.CallToolResult, any, error) {
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	isError := result.Status != gitflow.StatusPreview && result.Status != gitflow.StatusSuccess
	return &mcp.CallToolResult{
		IsError: isError,
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, result, nil
}

// refNames are the arguments worth keeping in the audit, in the order they are shown.
// confirmation is left out on purpose: it is an authorization, not a description.
var refNames = []string{"branch", "source", "base", "target", "name", "tag", "remote", "branches"}

// describe turns one finished call into its audit record.
func describe(tool string, readOnly bool, in any, result gitflow.Result, started time.Time) audit.Event {
	event := audit.Event{
		Tool:       tool,
		StartedAt:  started,
		Duration:   time.Since(started),
		Status:     string(result.Status),
		Repository: result.Repository,
		Message:    result.Message,
		Reasons:    result.Reasons,
	}
	var arguments map[string]any
	if body, err := json.Marshal(in); err == nil {
		_ = json.Unmarshal(body, &arguments)
	}
	if event.Repository == "" {
		event.Repository, _ = arguments["repository"].(string)
	}
	if !readOnly {
		event.Mode = gitflow.ModePreview
		if mode, _ := arguments["mode"].(string); mode != "" {
			event.Mode = mode
		}
	}
	for _, name := range refNames {
		switch value := arguments[name].(type) {
		case string:
			if value != "" {
				event.Refs = append(event.Refs, audit.Ref{Name: name, Value: value})
			}
		case []any:
			names := make([]string, 0, len(value))
			for _, item := range value {
				if text, ok := item.(string); ok {
					names = append(names, text)
				}
			}
			if len(names) > 0 {
				event.Refs = append(event.Refs, audit.Ref{Name: name, Value: strings.Join(names, ", ")})
			}
		}
	}
	return event
}

type logDiscard struct{}

func (logDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
