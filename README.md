# git-mcp

Servidor MCP local, por stdio, para os fluxos Git que valem uma ferramenta. O binário fala o protocolo só em stdout. Diagnóstico vai para stderr. Não há ferramenta de shell, nem um alias por função.

A linguagem é Go 1.27.4, fixada em `mise.toml` e em `go.mod`. O processo só orquestra `git` com argumentos explícitos, usando o SDK oficial `github.com/modelcontextprotocol/go-sdk` v1.8.0. Quem for usar o servidor adiciona o comando na própria configuração. As versões publicadas estão em [docs/releases.md](docs/releases.md).

## O que o servidor faz

| Ferramenta | Efeito |
| --- | --- |
| `repository_inspect` | Lê branch, alterações, operação em andamento e worktrees. Com `fetch`, compara uma branch ao remoto. Não move branches, tags nem remote-tracking. |
| `update_branch` | `git pull --rebase` só quando a branch local está estritamente atrás. `remain_on_target` fica desligado por padrão e devolve o checkout anterior. |
| `rebase_onto` | Atualiza a base com a regra acima e faz `git rebase` não interativo. Não abre editor. |
| `integrate_branch` | Merge numa worktree nova, sem trocar o checkout da sessão. O destino já precisa existir no remoto. |
| `merge_into` | Merge no checkout principal. Volta à branch inicial se tudo termina. Em conflito, fica no destino. |
| `create_release_branch` | Atualiza a base informada e cria a release nesse commit. O nome da release é obrigatório. |
| `create_release_from_tag` | Cria a release no commit da tag. Não faz `pull --rebase` de uma branch-base. |
| `reset_and_push` | Substitui o destino pelo commit da origem, no local e no remoto. |
| `delete_current_branch` | Vai ao destino e remove a branch atual com `git branch -d`, só se ela estiver mesclada. |
| `clean_merged_branches` | Remove, com `git branch -d`, só os nomes autorizados que continuam mesclados em HEAD. |

Não há remoto, base nem release padrão. `origin`, `main`, `develop` e `o` não são assumidos.

## O que ficou de fora

Atalhos de digitação e comandos que não são um fluxo Git para um agente:

- `rename-origin-to-o`
- `set-upstream-to-current-branch`
- aliases de status, log, commit, amend e contagem
- reescrita de data de commit
- rebase interativo, squash e reword
- funções que não são Git

## Prévia e autorização

O padrão é `mode=preview`. A prévia descreve o efeito e devolve `confirmation`. `mode=execute` só age se essa string for exatamente a da prévia e o repositório ainda estiver no mesmo estado. O servidor observa o remoto de novo imediatamente antes de mudar qualquer coisa. Confirmação diferente, ou estado diferente, devolve `blocked` e não altera branches.

Uma prévia não autoriza a execução. `--force-with-lease` sozinho também não: no `reset_and_push` o lease fica preso ao oid que a confirmação nomeou. Se o remoto mudar antes do reset, nada é reescrito. Se mudar depois do reset e antes do envio, o status é `partial`, o remoto não é atualizado e a ponta anterior fica no reflog.

Bloqueios de segurança não são contornáveis pela confirmação: árvore suja, operação Git em andamento, base à frente ou divergente, branch ocupada por outra worktree, nome inválido, remoto ausente. `clean_merged_branches` é a exceção da árvore suja: pode apagar branches já mescladas com alterações locais, mas continua bloqueado se houver rebase, merge, cherry-pick, revert ou bisect em andamento.

Os status são `preview`, `success`, `blocked`, `conflict` e `partial`.

## Atualização da base

Antes de rebase, release a partir de branch, integração ou merge, a base local é atualizada assim:

1. A branch, o remoto e a origem vêm nos argumentos.
2. O servidor recusa alterações locais, operação em andamento e branch ocupada em outra worktree.
3. `git pull --rebase` só roda quando a base está estritamente atrás. Não há stash, descarte nem reescrita de commit que exista só na base.
4. Se a base não é o checkout atual e não está em outra worktree, o pull acontece numa worktree temporária, removida ao terminar.
5. Depois do pull, a ponta local precisa ser igual à ponta remota. Fetch sozinho não substitui esse passo.

A comparação da prévia usa `git ls-remote`. Se o commit remoto ainda não existe no repositório, os objetos são baixados para `refs/git-mcp/observe/<branch>` e esse ref é apagado em seguida. Branches, tags e remote-tracking não se movem nessa leitura. `--refmap=` evita que o Git atualize `refs/remotes` por conta própria.

Fluxo de tag não atualiza branch-base. A tag é lida com `ls-remote` e, só na execução, copiada para `refs/tags` sem força. Se a tag local divergir, a execução para e a tag local permanece.

## Worktrees

Integração e atualização de base fora do checkout usam `~/git-worktrees/<repositório>/`. O caminho exato só aparece no resultado da execução, não na confirmação. Em conflito ou falha de envio, a worktree fica no disco e o resultado traz o caminho. O checkout da sessão não muda na integração.

## Consequências

- `reset_and_push` faz `git reset --hard` para o oid autorizado e envia com lease. O checkout volta à branch inicial só se o envio termina. Falha de envio deixa o checkout no destino.
- `delete_current_branch` usa apenas `git branch -d`. Se a branch não está mesclada no destino, a recusa acontece antes de trocar de checkout. O shell equivalente troca primeiro e falha depois; este servidor não troca.
- `clean_merged_branches` apaga a lista exata, já ordenada, que a confirmação nomeou. Lista vazia é só uma prévia dos candidatos. A branch atual e qualquer branch em worktree ficam de fora. Não há proteção fixa de `main`, `master`, `develop` ou `release`: o que não estiver na confirmação não é apagado. Se um nome não puder ser removido, nenhum é.
- `create_release_branch` e `create_release_from_tag` terminam com HEAD na release. Se ela já existe, `replace_existing` precisa estar na confirmação. A prévia lista commits que deixarão a branch. A troca usa `git branch -f` e recusa se a release estiver em qualquer worktree, inclusive a atual. O envio é um push comum, sem força. Se o remoto não for um ancestral da release local, nada é enviado.
- `integrate_branch` não cria no remoto um destino que não existe. Se a branch local do destino falta, ela é criada na ponta remota já observada. `publish` faz parte da confirmação. Sem ele, nada é enviado.
- `merge_into` exige o destino local. Em conflito, o checkout fica no destino com o merge em andamento.

Conflito não é resolvido e nada é guardado em stash. O resultado diz como continuar ou abortar no Git.

O servidor desliga `rebase.autoStash`, `rebase.autosquash` e `commit.gpgsign` em cada comando. Não define `user.name` nem `user.email` e não assina commits. `GIT_EDITOR` e `GIT_SEQUENCE_EDITOR` ficam em `false` para um editor não prender o stdio.

## Configuração

O binário não recebe argumentos, variáveis de ambiente nem um repositório fixo: cada chamada informa `repository`. Um exemplo para o Grok, em `~/.grok/config.toml`:

```toml
[mcp_servers.git-mcp]
command = "/caminho/para/git-mcp"
enabled = true
```

Claude Code, Codex e OpenCode usam o mesmo executável, sem argumentos, como servidor stdio. Uma sessão já aberta só passa a ver o servidor depois de recarregar o cliente.

## Verificação

Os testes criam repositórios Git locais em diretório temporário. Não usam remoto de rede, não empurram para um remoto real e não escrevem em `~/git-worktrees`.

Com Go 1.27.4, mise e Git disponíveis:

```sh
gofmt -l .
go vet ./...
go test ./...
go build -o /tmp/git-mcp ./cmd/git-mcp
```

`gofmt -l` sem saída significa que o código está formatado. O binário de exemplo vai para `/tmp` para não ficar na árvore do projeto.
