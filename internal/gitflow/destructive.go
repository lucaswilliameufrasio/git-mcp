package gitflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ResetInput replaces the target branch with the source commit, locally and on the remote.
type ResetInput struct {
	Repository   string `json:"repository" jsonschema:"caminho do work tree Git"`
	Mode         string `json:"mode,omitempty" jsonschema:"preview ou execute; o padrão é preview"`
	Confirmation string `json:"confirmation,omitempty" jsonschema:"confirmação exata devolvida pela prévia"`
	Source       string `json:"source,omitempty" jsonschema:"branch cuja ponta substitui o destino; padrão é a branch atual"`
	Target       string `json:"target" jsonschema:"branch local e remota que será substituída"`
	Remote       string `json:"remote" jsonschema:"remoto do destino"`
}

// DeleteInput switches to the target and deletes the current branch with git branch -d.
type DeleteInput struct {
	Repository   string `json:"repository" jsonschema:"caminho do work tree Git"`
	Mode         string `json:"mode,omitempty" jsonschema:"preview ou execute; o padrão é preview"`
	Confirmation string `json:"confirmation,omitempty" jsonschema:"confirmação exata devolvida pela prévia"`
	Branch       string `json:"branch" jsonschema:"branch atual que será removida se estiver mesclada no destino"`
	Target       string `json:"target" jsonschema:"branch para a qual o checkout vai antes da remoção"`
}

// CleanInput deletes only the authorized local branches that are already merged into HEAD.
type CleanInput struct {
	Repository   string   `json:"repository" jsonschema:"caminho do work tree Git"`
	Mode         string   `json:"mode,omitempty" jsonschema:"preview ou execute; o padrão é preview"`
	Confirmation string   `json:"confirmation,omitempty" jsonschema:"confirmação exata devolvida pela prévia"`
	Branches     []string `json:"branches,omitempty" jsonschema:"lista exata de branches locais já mescladas que podem ser removidas"`
}

// ResetAndPush moves the target to the source commit and publishes that with a scoped lease.
func (s Service) ResetAndPush(ctx context.Context, in ResetInput) Result {
	const op = "reset_and_push"
	preview, expected, stop := s.prepareReset(ctx, in)
	if stop != nil {
		return *stop
	}
	decision, run := gate(in.Mode, in.Confirmation, expected, preview)
	if !run {
		return decision
	}
	_, expectedAgain, stop := s.prepareReset(ctx, in)
	if stop != nil {
		return *stop
	}
	if expectedAgain != expected {
		return blocked(op, preview.Repository, "O estado mudou depois da prévia. Nada foi substituído. Gere outra prévia.")
	}
	return s.performReset(ctx, in)
}

// DeleteCurrentBranch removes the named current branch when it is merged into the target.
func (s Service) DeleteCurrentBranch(ctx context.Context, in DeleteInput) Result {
	const op = "delete_current_branch"
	preview, expected, stop := s.prepareDelete(ctx, in)
	if stop != nil {
		return *stop
	}
	decision, run := gate(in.Mode, in.Confirmation, expected, preview)
	if !run {
		return decision
	}
	_, expectedAgain, stop := s.prepareDelete(ctx, in)
	if stop != nil {
		return *stop
	}
	if expectedAgain != expected {
		return blocked(op, preview.Repository, "O estado mudou depois da prévia. Nenhuma branch foi removida. Gere outra prévia.")
	}
	return s.performDelete(ctx, in)
}

// CleanMergedBranches deletes the exact authorized list of merged local branches.
func (s Service) CleanMergedBranches(ctx context.Context, in CleanInput) Result {
	const op = "clean_merged_branches"
	preview, expected, stop := s.prepareClean(ctx, in)
	if stop != nil {
		return *stop
	}
	decision, run := gate(in.Mode, in.Confirmation, expected, preview)
	if !run {
		return decision
	}
	_, expectedAgain, stop := s.prepareClean(ctx, in)
	if stop != nil {
		return *stop
	}
	if expectedAgain != expected {
		return blocked(op, preview.Repository, "O estado mudou depois da prévia. Nenhuma branch foi removida. Gere outra prévia.")
	}
	return s.performClean(ctx, in)
}

func (s Service) prepareReset(ctx context.Context, in ResetInput) (Result, string, *Result) {
	const op = "reset_and_push"
	repo, bad := s.openResult(ctx, op, in.Repository)
	if bad != nil {
		return Result{}, "", bad
	}
	if ready := s.ensureReady(ctx, op, repo); ready != nil {
		return Result{}, "", ready
	}
	source, stop := s.namedSource(ctx, op, repo, in.Source)
	if stop != nil {
		return Result{}, "", stop
	}
	if err := repo.checkBranch(ctx, in.Target); err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if source == in.Target {
		result := blocked(op, repo.Root, "A origem e o destino precisam ser branches diferentes.")
		return Result{}, "", &result
	}
	targetOID, exists, err := repo.branchOID(ctx, in.Target)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if !exists {
		result := blocked(op, repo.Root, "A branch de destino não existe localmente.")
		return Result{}, "", &result
	}
	other, err := s.occupiedElsewhere(ctx, repo, in.Target)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if other != nil {
		result := blocked(op, repo.Root, fmt.Sprintf("A branch %s está em uso na worktree %s.", in.Target, other.Path))
		return Result{}, "", &result
	}
	remoteOID, err := repo.remoteBranchOID(ctx, in.Remote, in.Target)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	sourceOID, _, err := repo.branchOID(ctx, source)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	dropped, err := repo.commitsBetween(ctx, sourceOID, targetOID)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	expected := token(
		"operation="+op,
		"repository="+repo.Root,
		"source="+source,
		"target="+in.Target,
		"remote="+in.Remote,
		"source_oid="+sourceOID,
		"target_local_oid="+targetOID,
		"target_remote_oid="+remoteOID,
	)
	preview := Result{
		Status:     StatusPreview,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "Prévia da substituição. Nada foi alterado. --force-with-lease sozinho não autoriza esta operação.",
		Effects: []string{
			"A branch local " + in.Target + " passará a apontar para " + shortOID(sourceOID) + " com git reset --hard.",
			"O remoto " + in.Remote + "/" + in.Target + " será atualizado só se ainda estiver em " + shortOID(remoteOID) + ".",
			"Commits que ficarem fora da branch permanecem no reflog.",
		},
		Dropped:   dropped,
		SourceOID: sourceOID,
		LocalOID:  targetOID,
		RemoteOID: remoteOID,
	}
	return preview, expected, nil
}

func (s Service) performReset(ctx context.Context, in ResetInput) Result {
	const op = "reset_and_push"
	repo, err := s.open(ctx, in.Repository)
	if err != nil {
		return blocked(op, in.Repository, err.Error())
	}
	source, stop := s.namedSource(ctx, op, repo, in.Source)
	if stop != nil {
		return *stop
	}
	sourceOID, _, err := repo.branchOID(ctx, source)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	remoteOID, err := repo.remoteBranchOID(ctx, in.Remote, in.Target)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	start, startOID, detached, err := repo.head(ctx)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	if detached || start != in.Target {
		if err := repo.switchTo(ctx, in.Target); err != nil {
			return blocked(op, repo.Root, "Não foi possível ir para o destino: "+err.Error())
		}
	}
	remoteNow, err := repo.remoteBranchOID(ctx, in.Remote, in.Target)
	if err != nil || remoteNow != remoteOID {
		_ = restoreStart(ctx, repo, start, startOID, detached)
		return blocked(op, repo.Root, "O remoto mudou imediatamente antes da substituição. Nada foi reescrito.")
	}
	if ready := s.ensureReady(ctx, op, repo); ready != nil {
		if restoreErr := restoreStart(ctx, repo, start, startOID, detached); restoreErr != nil {
			return Result{
				Status:     StatusPartial,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "A substituição foi interrompida e o checkout não voltou ao início.",
				Reasons:    append(ready.Reasons, restoreErr.Error()),
			}
		}
		ready.Message = "O repositório mudou antes da substituição. Nada foi reescrito."
		ready.Head = repo.headLabel(ctx)
		return *ready
	}
	// Words after -- are paths, so the authorized oid stays positional.
	if _, err := repo.run(ctx, "reset", "--hard", sourceOID); err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "O checkout está no destino e o reset falhou.",
			Reasons:    []string{err.Error()},
		}
	}
	remoteAfter, err := repo.remoteBranchOID(ctx, in.Remote, in.Target)
	if err != nil || remoteAfter != remoteOID {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A branch local foi substituída e o remoto mudou antes do envio. Nada foi enviado.",
			LocalOID:   sourceOID,
			RemoteOID:  remoteAfter,
			NextSteps:  []string{"O checkout está no destino.", "A ponta anterior está no reflog.", "Gere outra prévia antes de publicar."},
		}
	}
	if err := repo.pushLease(ctx, in.Remote, in.Target, remoteOID); err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A branch local foi substituída e o envio falhou. O remoto não foi alterado por esta operação se o lease foi rejeitado.",
			Reasons:    []string{err.Error()},
			LocalOID:   sourceOID,
			RemoteOID:  remoteOID,
			NextSteps:  []string{"O checkout está no destino.", "A ponta anterior está no reflog."},
		}
	}
	if err := restoreStart(ctx, repo, start, startOID, detached); err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A substituição foi enviada, mas o checkout não voltou à branch inicial.",
			Reasons:    []string{err.Error()},
			LocalOID:   sourceOID,
		}
	}
	return Result{
		Status:     StatusSuccess,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "O destino local e o remoto agora apontam para o commit da origem.",
		SourceOID:  sourceOID,
		LocalOID:   mustOID(ctx, repo, in.Target),
		RemoteOID:  sourceOID,
	}
}

func (s Service) prepareDelete(ctx context.Context, in DeleteInput) (Result, string, *Result) {
	const op = "delete_current_branch"
	repo, bad := s.openResult(ctx, op, in.Repository)
	if bad != nil {
		return Result{}, "", bad
	}
	if ready := s.ensureReady(ctx, op, repo); ready != nil {
		return Result{}, "", ready
	}
	if err := repo.checkBranch(ctx, in.Branch); err != nil || repo.checkBranch(ctx, in.Target) != nil {
		result := blocked(op, repo.Root, "Nome de branch inválido.")
		return Result{}, "", &result
	}
	head, _, detached, err := repo.head(ctx)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if detached || head != in.Branch {
		result := blocked(op, repo.Root, "Este fluxo só remove a branch que está no checkout, e o nome informado precisa ser essa branch.")
		return Result{}, "", &result
	}
	if in.Branch == in.Target {
		result := blocked(op, repo.Root, "A branch removida e o destino precisam ser diferentes.")
		return Result{}, "", &result
	}
	if _, exists, err := repo.branchOID(ctx, in.Target); err != nil || !exists {
		result := blocked(op, repo.Root, "A branch de destino não existe.")
		return Result{}, "", &result
	}
	other, err := s.occupiedElsewhere(ctx, repo, in.Target)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if other != nil {
		result := blocked(op, repo.Root, fmt.Sprintf("O destino %s está em uso na worktree %s.", in.Target, other.Path))
		return Result{}, "", &result
	}
	merged, err := repo.ancestor(ctx, in.Branch, in.Target)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if !merged {
		result := blocked(op, repo.Root, "A branch não está mesclada no destino. Ela não será removida e o checkout permanece.")
		return Result{}, "", &result
	}
	branchOID, _, _ := repo.branchOID(ctx, in.Branch)
	targetOID, _, _ := repo.branchOID(ctx, in.Target)
	expected := token(
		"operation="+op,
		"repository="+repo.Root,
		"branch="+in.Branch,
		"target="+in.Target,
		"branch_oid="+branchOID,
		"target_oid="+targetOID,
	)
	preview := Result{
		Status:     StatusPreview,
		Operation:  op,
		Repository: repo.Root,
		Head:       head,
		Message:    "Prévia da remoção. Nada foi alterado.",
		Effects:    []string{"O checkout passa para " + in.Target + ".", "git branch -d remove " + in.Branch + " porque ela está mesclada nesse destino."},
		LocalOID:   branchOID,
		Branches:   []string{in.Branch},
	}
	return preview, expected, nil
}

func (s Service) performDelete(ctx context.Context, in DeleteInput) Result {
	const op = "delete_current_branch"
	repo, err := s.open(ctx, in.Repository)
	if err != nil {
		return blocked(op, in.Repository, err.Error())
	}
	head, _, detached, err := repo.head(ctx)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	if detached || head != in.Branch {
		return blocked(op, repo.Root, "Este fluxo só remove a branch que está no checkout. O checkout permanece.")
	}
	merged, err := repo.ancestor(ctx, in.Branch, in.Target)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	if !merged {
		return blocked(op, repo.Root, "A branch não está mesclada no destino. Ela não será removida e o checkout permanece.")
	}
	if err := repo.switchTo(ctx, in.Target); err != nil {
		return blocked(op, repo.Root, "Não foi possível ir para o destino: "+err.Error())
	}
	if err := repo.deleteMerged(ctx, in.Branch); err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "O checkout está no destino e a branch não foi removida.",
			Reasons:    []string{err.Error()},
			Branches:   []string{in.Branch},
		}
	}
	return Result{
		Status:     StatusSuccess,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "A branch foi removida com git branch -d.",
		Branches:   []string{in.Branch},
	}
}

func (s Service) prepareClean(ctx context.Context, in CleanInput) (Result, string, *Result) {
	const op = "clean_merged_branches"
	repo, bad := s.openResult(ctx, op, in.Repository)
	if bad != nil {
		return Result{}, "", bad
	}
	if busy := s.ensureNoOperation(ctx, op, repo); busy != nil {
		return Result{}, "", busy
	}
	requested, err := normalizeNames(in.Branches)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	deletable, err := s.deletableBranches(ctx, repo)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if len(requested) == 0 {
		expected := token("operation="+op, "repository="+repo.Root, "branches=")
		preview := Result{
			Status:     StatusPreview,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "Estas branches locais estão mescladas em HEAD e não estão em outra worktree. Informe essa lista, ou um subconjunto, e gere outra prévia para autorizar a remoção.",
			Branches:   deletable,
			Effects:    []string{"Nada é apagado nesta resposta.", "A execução só remove os nomes presentes na confirmação.", "A branch atual e as branches em outras worktrees ficam de fora."},
		}
		return preview, expected, nil
	}
	var missing []string
	for _, name := range requested {
		if !containsName(deletable, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		result := blocked(op, repo.Root, "Estas branches não podem ser removidas: "+strings.Join(missing, ", ")+".")
		result.Branches = deletable
		return Result{}, "", &result
	}
	expected := token("operation="+op, "repository="+repo.Root, "branches="+strings.Join(requested, ","))
	preview := Result{
		Status:     StatusPreview,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "Prévia da limpeza. Nada foi alterado.",
		Effects:    []string{"git branch -d será usado em cada nome autorizado.", "Branches não mescladas não entram nesta lista."},
		Branches:   requested,
	}
	return preview, expected, nil
}

func (s Service) performClean(ctx context.Context, in CleanInput) Result {
	const op = "clean_merged_branches"
	repo, err := s.open(ctx, in.Repository)
	if err != nil {
		return blocked(op, in.Repository, err.Error())
	}
	requested, err := normalizeNames(in.Branches)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	if len(requested) == 0 {
		return blocked(op, repo.Root, "Nenhuma branch foi autorizada. Informe a lista na prévia.")
	}
	deletable, err := s.deletableBranches(ctx, repo)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	for _, name := range requested {
		if !containsName(deletable, name) {
			return blocked(op, repo.Root, "A lista não é mais só de branches mescladas e livres. Nada foi removido.")
		}
	}
	var removed []string
	for _, name := range requested {
		if err := repo.deleteMerged(ctx, name); err != nil {
			return Result{
				Status:     StatusPartial,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "A limpeza parou depois de remover parte da lista.",
				Reasons:    []string{err.Error()},
				Branches:   removed,
				NextSteps:  []string{"As branches já removidas não serão recriadas por este fluxo.", "Gere outra prévia para o restante."},
			}
		}
		removed = append(removed, name)
	}
	return Result{
		Status:     StatusSuccess,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "As branches autorizadas foram removidas com git branch -d.",
		Branches:   removed,
	}
}

func (s Service) deletableBranches(ctx context.Context, repo Repository) ([]string, error) {
	merged, err := repo.mergedBranches(ctx)
	if err != nil {
		return nil, err
	}
	items, err := repo.worktrees(ctx)
	if err != nil {
		return nil, err
	}
	occupied := map[string]bool{}
	for _, item := range items {
		if item.Branch != "" {
			occupied[item.Branch] = true
		}
	}
	var names []string
	for _, name := range merged {
		if occupied[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func normalizeNames(names []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, name := range names {
		if err := validateBranchName(name); err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("branch repetida: %s", name)
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
