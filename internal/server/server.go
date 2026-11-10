package server

import (
	"context"
	"encoding/json"
	"log"

	"git-mcp/internal/gitflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is the MCP implementation version. Release builds replace it with -X.
var version = "0.1.0"

// New builds the stdio MCP server around a gitflow service.
func New(svc gitflow.Service, logger *log.Logger) *mcp.Server {
	if logger == nil {
		logger = log.New(logDiscard{}, "", 0)
	}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "git-mcp", Version: version}, nil)
	add(mcpServer, logger, "repository_inspect",
		"Lê o work tree: branch, alterações, operação em andamento e worktrees. Com fetch, compara uma branch ao remoto via ls-remote e não move refs locais.",
		true, false,
		svc.Inspect,
	)
	add(mcpServer, logger, "update_branch",
		"Atualiza uma branch local com git pull --rebase somente quando ela está estritamente atrás do remoto. Não faz stash, não descarta alterações e não reescreve commits locais da base. O padrão é prévia; execute exige a confirmação exata.",
		false, false,
		svc.UpdateBranch,
	)
	add(mcpServer, logger, "rebase_onto",
		"Atualiza a branch-base com a regra segura e faz git rebase não interativo da origem sobre ela. Não abre editor. Em conflito, o rebase permanece para resolução manual.",
		false, true,
		svc.RebaseOnto,
	)
	add(mcpServer, logger, "integrate_branch",
		"Integra a origem no destino numa worktree em ~/git-worktrees, sem trocar o checkout atual. Atualiza o destino com git pull --rebase quando isso é seguro. publish envia a branch. Em conflito ou falha de envio, a worktree fica preservada.",
		false, false,
		svc.IntegrateBranch,
	)
	add(mcpServer, logger, "merge_into",
		"Atualiza o destino no checkout principal, faz o merge e volta à branch inicial se tudo terminar. Em conflito, o checkout fica no destino com o merge em andamento. publish envia a branch.",
		false, false,
		svc.MergeInto,
	)
	add(mcpServer, logger, "create_release_branch",
		"Atualiza a base informada e cria a branch de release nesse commit. Se a release já existe, replace_existing move a ponta local; a prévia lista commits que sairão da branch. publish envia sem sobrescrever o remoto.",
		false, true,
		svc.CreateReleaseBranch,
	)
	add(mcpServer, logger, "create_release_from_tag",
		"Cria a branch de release no commit da tag informada. Valida a tag no remoto e não faz pull --rebase de branch-base. replace_existing e publish seguem a mesma autorização da release por branch.",
		false, true,
		svc.CreateReleaseFromTag,
	)
	add(mcpServer, logger, "reset_and_push",
		"Substitui a branch de destino pelo commit da origem, no local e no remoto. Exige prévia e confirmação com os oids. Usa reset --hard e um lease preso ao oid observado; o lease sozinho não autoriza a operação. Se o remoto mudar, não envia.",
		false, true,
		svc.ResetAndPush,
	)
	add(mcpServer, logger, "delete_current_branch",
		"Leva o checkout ao destino e remove a branch atual com git branch -d, só quando ela está mesclada nesse destino. Não usa -D. Recusa antes de trocar de branch quando a remoção não é segura.",
		false, true,
		svc.DeleteCurrentBranch,
	)
	add(mcpServer, logger, "clean_merged_branches",
		"Remove, com git branch -d, somente as branches nomeadas na confirmação que continuam mescladas em HEAD e fora de outras worktrees. Não há lista protegida fixa além da branch atual e das worktrees.",
		false, true,
		svc.CleanMergedBranches,
	)
	return mcpServer
}

// Run serves MCP on stdin and stdout. Diagnostics belong on the logger, which must write to stderr.
func Run(ctx context.Context, svc gitflow.Service, logger *log.Logger) error {
	return New(svc, logger).Run(ctx, &mcp.StdioTransport{})
}

func add[I any](mcpServer *mcp.Server, logger *log.Logger, name, description string, readOnly, destructive bool, handle func(context.Context, I) gitflow.Result) {
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
		result := handle(ctx, in)
		logger.Printf("%s %s", name, result.Status)
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

type logDiscard struct{}

func (logDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
