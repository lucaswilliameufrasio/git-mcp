# Design system do console

Fonte de verdade para a interface do `git-mcp console`. Toda decisão visual vem
de um token abaixo, da referência A ou da camada de identidade. O que não vier
de nenhum dos três é padrão de fábrica e sai.

## Quadro de referências

| Ref | Fonte | Papel | Consultada |
| --- | --- | --- | --- |
| A | Console local do jira-mcp v1.0.0 (`jira-mcp console`, `internal/console/console.{html,css}`), capturado em 1440×900 e 390×844 | Tudo o que é estrutura: trilho, cabeçalho, faixa de métricas, superfícies, tabela, cores, tipografia, espaçamento, estados | 2026-11-11 |

Os valores de A foram lidos do CSS do próprio produto, não estimados por olho.
Só há uma referência, então não houve conflito a resolver.

Não levado de A: o nome, os textos, a seção de conta conectada e o botão de
sair, que pertencem ao modo remoto do jira-mcp.

## Camada de identidade

Movimentos que vêm do git-mcp, não da referência.

| Movimento | Origem no produto | Função | Onde pode aparecer |
| --- | --- | --- | --- |
| Linha de commits | O grafo de `git log --graph` | Marca a página atual: cada página é um nó na linha, a atual é o nó cheio | Navegação do trilho, só na vertical |
| Diffstat | O resumo `++++----` de `git diff --stat` | Mostra, por ferramenta, chamadas concluídas contra interrompidas, em escala com a ferramenta mais chamada | Tabela por ferramenta do Monitoramento, uma barra por linha |
| Palavra literal em monoespaçada | Os identificadores que o servidor devolve (`update_branch`, `blocked`) | Separa o que é identificador do protocolo do que é texto da interface | Marca, nomes de ferramenta, status |

## Tokens

Implementados em `internal/console/console.css`, no bloco `:root`.

| Token | Valor | De | Notas |
| --- | --- | --- | --- |
| `--color-bg` | `#edf3f8` | A | fundo da página |
| `--color-surface` | `#fff` | A | faixa de métricas e seções |
| `--color-line` | `#cbd9e6` | A | borda de superfície e divisor de métrica |
| `--color-line-row` | `#dbe5ee` | A | divisor de linha de tabela |
| `--color-rule` | `#93afc7` | A | régua de 2px sob o cabeçalho |
| `--color-text` | `#18304d` | A | 13,39:1 sobre a superfície |
| `--color-muted` | `#50647a` | A | 6,10:1 sobre a superfície, 5,45:1 sobre o fundo |
| `--color-link` | `#1757a6` | A | 7,12:1 |
| `--color-rail` | `#163353` | A | fundo do trilho |
| `--color-rail-text` | `#d8e7f3` | A | 10,2:1 sobre o trilho |
| `--color-rail-muted` | `#c4d8eb` | A | subtítulo da marca |
| `--color-rail-hover` | `#284e73` | A | item sob o ponteiro |
| `--color-rail-current` | `#e9f3ff` | A | item da página atual |
| `--color-rail-graph` | `#7f9fbe` | identidade | linha e nós; 4,66:1 sobre o trilho |
| `--color-done` | `#126448` | A (`--ok`) | `success` e `+` do diffstat; 7,14:1 |
| `--color-refused` | `#b75d1c` | A (borda do aviso) | `blocked` e borda do aviso; 4,57:1 |
| `--color-attention` | `#a92838` | A (`--fail`) | `conflict`, `partial` e `-` do diffstat; 6,89:1 |
| `--color-notice-bg` | `#fff4e9` | A | fundo do aviso |
| `--color-focus` | `#c27400` | A, ajustado | ver desvios de acessibilidade |
| `--font-body` | `"Trebuchet MS", Verdana, sans-serif` | A | 16px, linha 1,5 |
| `--font-mono` | `ui-monospace, "Cascadia Mono", "DejaVu Sans Mono", "Noto Sans Mono", monospace` | identidade | só identificadores |
| `--size-h1` | `clamp(2rem, 4vw, 3.1rem)` | A | peso 700, linha 1,1, tracking -0,04em |
| `--size-metric` | `2rem` | A | peso 700 |
| `--size-h2` | `1.2rem` | A | |
| `--size-brand` | `1.25rem` | A | |
| `--size-small` | `.85rem` | A | cabeçalho de tabela, rodapé, bloco do trilho |
| `--radius-control` | `5px` | A | itens de navegação |
| `--radius-surface` | `7px` | A | faixa de métricas e seções |
| `--rail-width` | `240px` | A | |
| `--target-min` | `44px` | A | altura mínima de link |
| `--node` | `12px` | identidade | diâmetro do nó, borda de 2px |
| `--pad-rail`, `--pad-main`, `--pad-surface`, `--pad-metric`, `--pad-cell` | `30px 22px`, `42px clamp(20px, 5vw, 74px)`, `clamp(18px, 3vw, 28px)`, `24px`, `12px 10px` | A | |
| `--gap-nav`, `--gap-rail`, `--gap-header`, `--gap-section` | `6px`, `36px`, `34px`, `28px` | A | |

A cor marca como o repositório ficou, não a ferramenta: sem cor para `preview`
(nada mudou), `done` para `success`, `refused` para `blocked` (recusado antes de
mudar), `attention` para `conflict` e `partial` (ficou no meio do caminho).

## Componentes

### Trilho
- Anatomia: marca em monoespaçada com subtítulo, navegação, bloco "Modo local" no pé.
- Navegação: linha vertical de 2px com um nó por página. Estados: padrão (anel), hover (fundo `rail-hover`), atual (pílula `rail-current`, texto escuro em 700, nó cheio), foco (contorno de 3px).
- Até 760px vira barra horizontal sem linha nem nós; fica só a pílula de A.
- De: A, mais a linha de commits.

### Cabeçalho da página
- `h1` grande com tracking fechado, uma linha de descrição, link "Atualizar dados" à direita, régua de 2px embaixo.
- De: A.

### Faixa de métricas
- Uma superfície só, com três células separadas por linha de 1px. Rótulo em `muted`, valor em 2rem/700. Uma célula pode ter um identificador em monoespaçada sob o valor.
- Vazio: o valor é `—`, nunca `0` para uma medida que não existe.
- De: A.

### Seção
- Superfície com borda de 1px e raio de 7px, sem sombra. `h2`, texto, tabela ou links.
- De: A.

### Tabela
- Cabeçalho pequeno em `muted`, linhas com divisor de 1px, sem zebra. Largura mínima de 620px com rolagem horizontal no contêiner.
- Identificadores não quebram no meio.
- Vazio: uma frase em `muted` no lugar da tabela.
- De: A.

### Linha do histórico
- Cada célula tem um valor principal e, embaixo, linhas secundárias em `size-small` e `muted`: o modo sob a ferramenta, as branches sob o repositório, a mensagem e os motivos sob o status.
- Caminho e nomes de branch em monoespaçada. Início e duração não quebram. A tabela tem largura mínima de 900px e rola dentro do contêiner.
- Chamada sem repositório gravado mostra `—`.
- De: A (tabela), mais a palavra literal.

### Filtros
- Formulário GET numa linha que quebra: rótulo pequeno em `muted` sobre cada controle, três seleções, um campo de texto que ocupa o resto, botão "Filtrar". Com filtro ativo aparece o link "Limpar filtros".
- Controles: 44px de altura, borda de 1px, raio `radius-control`, fundo `surface`. A referência não tem campo de formulário; a anatomia é a do botão "Sair" de A (borda de 1px, raio de 5px, 44px, fonte herdada) desenhada sobre a superfície clara.
- Borda dos campos em `muted` (6,10:1) e não em `line` (1,44:1), porque a borda é o que delimita o controle.
- Botão: borda e texto em `link`, peso 700; hover com fundo `rail-current`. Sem botão preenchido.
- As opções das seleções são identificadores, por isso em monoespaçada.
- Estados: padrão, hover (botão), foco (contorno de 3px), vazio com filtro ("Nenhuma chamada com estes filtros"), vazio sem filtro.
- De: tokens e botão de A.

### Paginação
- Dois links de texto, "Mais recentes" à esquerda e "Mais antigas" à direita, com 44px de alvo. Acima da tabela, uma frase diz a faixa e o total. Sem números de página.
- De: os links de texto de A.

### Status
- A palavra literal do servidor, monoespaçada em 700, na cor do tom. Sem pílula nem fundo.
- De: A (texto colorido em negrito), mais a palavra literal.

### Diffstat
- Contagem em texto, depois `+` e `-` em monoespaçada. No máximo 20 caracteres na ferramenta mais chamada. Um lado com chamadas nunca some no arredondamento.
- A barra é `aria-hidden`; os números das colunas "Chamadas" e "Interrompidas" dizem o mesmo.
- De: identidade.

### Aviso
- Borda esquerda de 4px em `refused`, fundo `notice-bg`. Usado quando a auditoria não pôde ser lida, no lugar das métricas.
- De: A.

## Regras

DEVE
1. O trilho é escuro, com 240px, e a página atual é uma pílula clara com o nó cheio.
2. O título da página é grande, de tracking fechado, sobre uma régua de 2px.
3. As métricas ficam numa faixa única dividida por linhas.
4. Status aparece como a palavra literal do servidor, em texto colorido.
5. Nomes de ferramenta e status usam a fonte monoespaçada; o resto do texto não.
6. Filtro e paginação são endereço: formulário GET e links.
7. A página funciona sem JavaScript e sem recurso externo, por causa da política de conteúdo do console.

NÃO DEVE
1. Sombra, gradiente, vidro ou desfoque.
2. Uma métrica por cartão solto, ou ícone em quadrado arredondado.
3. Pílula ou selo colorido para status.
4. Cor que não marca nada, ou a identidade visual do Git (logo, laranja da marca).
5. Emoji ou ícone como elemento de interface.
6. Número inventado: sem auditoria, a página diz que está indisponível ou vazia.
7. Mais de um movimento de identidade na mesma região da tela.

## Superfícies

| Superfície | Leva igual | Adapta |
| --- | --- | --- |
| Console local (web) | tudo | abaixo de 760px o trilho vira barra e perde a linha de commits |
| Terminal (`git-mcp`, stderr) | as palavras literais de status | sem cor nem layout; não há interface |

Não há back office nem aplicativo móvel.

## Desvios de acessibilidade em relação à referência

- Contorno de foco: A usa `#d88610`, com 2,86:1 sobre branco e 2,56:1 sobre o fundo. Aqui é `#c27400`: 3,63:1 sobre branco, 3,24:1 sobre o fundo, 3,55:1 sobre o trilho.
- Links "Ver por ferramenta" e "Ver cada chamada": A deixa a altura da linha de texto; aqui têm 44px de alvo, como o "Atualizar dados" de A.
- Identificadores na tabela: A permite quebra em qualquer ponto na primeira coluna; aqui não quebram, e a tabela rola dentro do contêiner.

## Lista de revisão

- [ ] Toda cor, tamanho, raio e espaçamento vem de um token.
- [ ] Os movimentos de A e os de identidade estão presentes, cada um com sua função.
- [ ] Nenhum item de NÃO DEVE aparece.
- [ ] Tela comparada lado a lado com A no mesmo viewport (1440×900 e 390×844).
- [ ] Contraste, foco, rótulos e alvos conferidos. Não há animação.

## Histórico

- 2026-11-11: a Auditoria ganhou linha com detalhes, filtros e paginação. Os controles de formulário não existem na referência e foram derivados do botão de A e dos tokens.
- 2026-11-11: primeira versão, extraída do console do jira-mcp v1.0.0, com a camada de identidade do git-mcp.
