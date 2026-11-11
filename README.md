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

O servidor MCP roda sem argumentos e sem repositório fixo: cada chamada informa `repository`. Os subcomandos estão em [Linha de comando](#linha-de-comando). Um exemplo para o Grok, em `~/.grok/config.toml`:

```toml
[mcp_servers.git-mcp]
command = "/caminho/para/git-mcp"
enabled = true
```

Claude Code, Codex e OpenCode usam o mesmo executável, sem argumentos, como servidor stdio. Uma sessão já aberta só passa a ver o servidor depois de recarregar o cliente.

## Linha de comando

| Comando | Efeito |
| --- | --- |
| `git-mcp` | Inicia o servidor MCP por stdio. |
| `git-mcp doctor` | Confere o ambiente. Sai com código 1 se algo falhar. |
| `git-mcp tools` | Lista as ferramentas, com a descrição e o tipo de efeito: só leitura, altera ou destrutiva. |
| `git-mcp stats` | Mostra no terminal os mesmos números do console. |
| `git-mcp console` | Abre o painel local de métricas. |
| `git-mcp version` | Mostra a versão. `--version` faz o mesmo. |
| `git-mcp help` | Mostra os comandos. `--help` e `-h` fazem o mesmo. |

Comando desconhecido, ou argumento a mais, encerra com a página de uso e código 2. Os subcomandos escrevem o resultado em stdout; o protocolo MCP só existe no modo sem argumentos.

`doctor` não toca em repositório nem em configuração de cliente. O handshake inicia o servidor de verdade, que cria o diretório de auditoria se ele faltar. Ele confere:

- `git` no `PATH` e a versão;
- `~/git-worktrees`, que pode ainda não existir;
- o diretório de auditoria e quantas chamadas há na amostra;
- o próprio binário como servidor stdio: faz o handshake e conta as ferramentas;
- a entrada do git-mcp na configuração de usuário do Claude Code (`~/.claude.json`), Codex (`~/.codex/config.toml`), Grok (`~/.grok/config.toml`) e OpenCode (`~/.config/opencode/opencode.json`), com o executável que cada um aponta.

A lista de clientes é informativa e não muda o código de saída. Servidor registrado por projeto ou por plugin não aparece nela. O `doctor` não grava configuração em nenhum cliente.

## Console local

```sh
git-mcp console
```

O comando abre um painel web somente leitura em `127.0.0.1`, numa porta escolhida pelo sistema, e imprime o endereço em stdout. Roda até receber Ctrl+C. É um processo separado do servidor MCP: a configuração dos clientes não muda, e parar um não para o outro.

| Página | Mostra |
| --- | --- |
| Visão geral | Chamadas na amostra, chamadas interrompidas, duração média e quantas terminaram em cada status. |
| Monitoramento | Por ferramenta: chamadas, interrompidas, duração média, a mais lenta e a última. |
| Auditoria | Cada chamada dos últimos 30 dias, 50 por página: ferramenta, modo, repositório, branches, início, duração, status, mensagem e motivos. Filtra por ferramenta, status, período e texto. |

Interrompida é a chamada que terminou em `blocked`, `conflict` ou `partial`. Os números da Visão geral e do Monitoramento cobrem as 200 chamadas mais recentes. A Auditoria percorre o histórico inteiro dentro da retenção.

Os filtros da Auditoria ficam no endereço (`/web/audit?tool=merge_into&status=conflict&period=7d&q=loja`), então uma busca pode ser guardada ou recarregada. O texto é procurado, sem diferenciar maiúsculas, em repositório, branches, mensagem e motivos.

Cada processo stdio grava um arquivo por chamada concluída em `$XDG_STATE_HOME/git-mcp/audit`, ou em `~/.local/state/git-mcp/audit` quando a variável não existe. O arquivo guarda nome da ferramenta, horário, duração, status, modo (`preview` ou `execute`), caminho do repositório, os nomes de branch, tag e remoto informados na chamada, a mensagem do resultado e até cinco motivos de bloqueio. A string de confirmação, os oids e a lista de efeitos não são gravados. Cada texto é cortado em 300 caracteres. Chamadas gravadas pela 0.2.0 só têm os quatro primeiros campos e aparecem com travessão no lugar do repositório. Arquivos com mais de 30 dias são apagados na gravação ou leitura seguinte. O diretório é criado com modo `0700`; se já existir com acesso para outros usuários, a auditoria fica desligada. Se a auditoria não puder ser aberta ou gravada, o aviso vai para stderr e as ferramentas continuam funcionando.

O painel não tem autenticação. Ele recusa conexão que não venha do loopback, cabeçalho `Host` ou `Origin` diferente do próprio endereço, requisição de outro site e qualquer método além de GET. Isso não protege contra outro processo ou usuário da mesma máquina: quem alcança a porta lê os metadados. Em máquina compartilhada, deixe o comando parado.

A aparência do painel está registrada em [docs/design-system.md](docs/design-system.md).

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
