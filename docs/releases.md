# Releases

O git-mcp usa Semantic Versioning. As tags publicadas têm a forma `vX.Y.Z`.

## 1. Preparar a versão

No GitHub Actions, execute **Prepare Release** em `main` com uma versão estável,
como `0.1.0` ou `v0.1.0`:

```sh
gh workflow run prepare-release.yml --ref main -f version=0.1.0
```

O workflow valida a versão, gera `CHANGELOG.md` com git-cliff 2.14.2, abre a
branch `release/vX.Y.Z` e cria o pull request para `main`. O corpo de cada
commit entra no changelog, então a revisão mostra o que mudou, não só o
assunto. Não cria tag nem release. O `GITHUB_TOKEN` não dispara o CI do push
nem do pull request, então a preparação dispara o CI na branch. Uma nova
execução reutiliza o pull request aberto. A versão precisa ser maior que as
tags estáveis já existentes.

O mesmo resultado pode ser feito localmente, com a árvore limpa e `main`
atualizada:

```sh
git cliff --tag vX.Y.Z -o CHANGELOG.md
git switch -c release/vX.Y.Z
git add CHANGELOG.md
git commit -m "chore(release): prepare for vX.Y.Z"
git push -u origin HEAD
gh pr create --base main --head release/vX.Y.Z --title "Release vX.Y.Z"
```

## 2. Revisar e fazer merge

Confira o changelog e aguarde o CI. O merge usa um merge commit. Não há
auto-merge.

## 3. Criar a tag do commit revisado

Depois do merge, atualize `main` e confira o commit antes de taguear:

```sh
git switch main
git pull --ff-only
git log -1 --oneline
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

Use a versão preparada. Não mova uma tag já publicada.

## 4. Publicação

A tag dispara o workflow **Release**. Ele exige tag estável, a entrada
correspondente no changelog e um commit que já está em `main`. Roda o mesmo
CI e publica a GitHub Release com GoReleaser 2.20.0.

A página da release abre com a instalação para Linux, macOS e Windows. O
instalador baixa o arquivo da plataforma e confere o `.sha256` publicado ao
lado dele. Em seguida vem o que entrou na versão, agrupado a partir dos
commits desde a tag anterior. Merge e `chore(release)` ficam de fora.

Os arquivos são seis binários, o código-fonte, os dois instaladores e um
`.sha256` para cada arquivo. Releases já publicadas antes dessa mudança
continuam com o `checksums.txt` único e sem instalador.

O binário local permanece na versão declarada no código. O build de release
substitui essa versão pela tag.
