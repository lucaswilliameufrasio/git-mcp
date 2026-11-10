package gitflow

import (
	"context"
	"fmt"
)

// UpdateBranchInput updates one local branch with git pull --rebase
// when it is strictly behind its remote.
type UpdateBranchInput struct {
	Repository     string `json:"repository" jsonschema:"caminho do work tree Git"`
	Mode           string `json:"mode,omitempty" jsonschema:"preview ou execute; o padrão é preview"`
	Confirmation   string `json:"confirmation,omitempty" jsonschema:"confirmação exata devolvida pela prévia"`
	Branch         string `json:"branch" jsonschema:"branch local que será atualizada"`
	Remote         string `json:"remote" jsonschema:"remoto usado no pull --rebase"`
	RemainOnTarget bool   `json:"remain_on_target,omitempty" jsonschema:"ao terminar, deixa o checkout nesta branch"`
}

// UpdateBranch é o fluxo de pull-branch e de checkout com pull.
func (s Service) UpdateBranch(ctx context.Context, in UpdateBranchInput) Result {
	const op = "update_branch"
	preview, expected, stop := s.prepareUpdate(ctx, in)
	if stop != nil {
		return *stop
	}
	decision, run := gate(in.Mode, in.Confirmation, expected, preview)
	if !run {
		return decision
	}
	_, expectedAgain, stop := s.prepareUpdate(ctx, in)
	if stop != nil {
		return *stop
	}
	if expectedAgain != expected {
		return blocked(op, preview.Repository, "O estado mudou depois da prévia. Nada foi alterado. Gere outra prévia.")
	}
	return s.performUpdate(ctx, in)
}

func (s Service) prepareUpdate(ctx context.Context, in UpdateBranchInput) (Result, string, *Result) {
	const op = "update_branch"
	if err := ctx.Err(); err != nil {
		result := blocked(op, in.Repository, "Operação cancelada.")
		return Result{}, "", &result
	}
	repo, bad := s.openResult(ctx, op, in.Repository)
	if bad != nil {
		return Result{}, "", bad
	}
	if ready := s.ensureReady(ctx, op, repo); ready != nil {
		return Result{}, "", ready
	}
	obs, err := s.observeBranch(ctx, repo, in.Branch, in.Remote)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if !obs.LocalExists {
		result := blocked(op, repo.Root, "A branch local não existe.")
		return Result{}, "", &result
	}
	other, err := s.occupiedElsewhere(ctx, repo, in.Branch)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if other != nil {
		result := blocked(op, repo.Root, fmt.Sprintf("A branch %s está em uso na worktree %s.", in.Branch, other.Path))
		return Result{}, "", &result
	}
	if obs.BlockReason != "" {
		result := Result{
			Status:     StatusBlocked,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A base não foi atualizada.",
			Reasons:    []string{obs.BlockReason},
			LocalOID:   obs.LocalOID,
			RemoteOID:  obs.RemoteOID,
		}
		return Result{}, "", &result
	}
	expected := token(
		"operation="+op,
		"repository="+repo.Root,
		"branch="+in.Branch,
		"remote="+in.Remote,
		"local_oid="+obs.LocalOID,
		"remote_oid="+obs.RemoteOID,
		fmt.Sprintf("ahead=%d", obs.Ahead),
		fmt.Sprintf("behind=%d", obs.Behind),
		"remain_on_target="+flag(in.RemainOnTarget),
	)
	effects := []string{}
	if obs.Behind > 0 {
		effects = append(effects, "A branch local será atualizada com git pull --rebase "+in.Remote+" "+in.Branch+".")
	} else {
		effects = append(effects, "A branch local já corresponde ao remoto. Nenhum pull será feito.")
	}
	if in.RemainOnTarget {
		effects = append(effects, "Ao terminar, o checkout fica em "+in.Branch+".")
	} else {
		effects = append(effects, "Ao terminar, o checkout volta à branch de origem quando ela for outra.")
	}
	preview := Result{
		Status:     StatusPreview,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "Prévia da atualização. Nenhuma branch local foi movida.",
		Effects:    effects,
		LocalOID:   obs.LocalOID,
		RemoteOID:  obs.RemoteOID,
	}
	return preview, expected, nil
}

func (s Service) performUpdate(ctx context.Context, in UpdateBranchInput) Result {
	const op = "update_branch"
	repo, err := s.open(ctx, in.Repository)
	if err != nil {
		return blocked(op, in.Repository, err.Error())
	}
	obs, err := s.observeBranch(ctx, repo, in.Branch, in.Remote)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	if !obs.LocalExists {
		return blocked(op, repo.Root, "A branch local não existe.")
	}
	if obs.BlockReason != "" {
		return Result{
			Status:     StatusBlocked,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A base não foi atualizada.",
			Reasons:    []string{obs.BlockReason},
			LocalOID:   obs.LocalOID,
			RemoteOID:  obs.RemoteOID,
		}
	}
	before := obs.LocalOID
	if obs.Behind > 0 {
		if stopped := s.fastForward(ctx, op, repo, in.Branch, in.Remote); stopped != nil {
			return *stopped
		}
	}
	local, remoteOID, err := s.syncedOID(ctx, repo, in.Branch, in.Remote)
	if err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A atualização não deixou a branch local igual ao remoto.",
			Reasons:    []string{err.Error()},
			LocalOID:   local,
			RemoteOID:  remoteOID,
			NextSteps:  []string{"Inspecione a branch antes de repetir o fluxo.", "Commits anteriores continuam no reflog se a ponta se moveu."},
		}
	}
	if local == "" {
		local = before
	}
	if in.RemainOnTarget {
		head, _, detached, err := repo.head(ctx)
		if err != nil {
			return blocked(op, repo.Root, err.Error())
		}
		if detached || head != in.Branch {
			if err := repo.switchTo(ctx, in.Branch); err != nil {
				return Result{
					Status:     StatusPartial,
					Operation:  op,
					Repository: repo.Root,
					Head:       repo.headLabel(ctx),
					Message:    "A branch foi atualizada, mas o checkout não mudou.",
					Reasons:    []string{err.Error()},
					LocalOID:   local,
					RemoteOID:  remoteOID,
				}
			}
		}
	}
	return Result{
		Status:     StatusSuccess,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "A branch local corresponde ao remoto.",
		LocalOID:   local,
		RemoteOID:  remoteOID,
	}
}

func (s Service) syncedOID(ctx context.Context, repo Repository, branch, remote string) (string, string, error) {
	local, exists, err := repo.branchOID(ctx, branch)
	if err != nil {
		return "", "", err
	}
	if !exists {
		return "", "", fmt.Errorf("a branch local %s desapareceu durante a operação", branch)
	}
	remoteOID, err := repo.remoteBranchOID(ctx, remote, branch)
	if err != nil {
		return local, "", err
	}
	if local != remoteOID {
		return local, remoteOID, fmt.Errorf("a base local %s difere do remoto %s", shortOID(local), shortOID(remoteOID))
	}
	return local, remoteOID, nil
}

func (s Service) fastForward(ctx context.Context, op string, repo Repository, branch, remote string) *Result {
	head, _, detached, err := repo.head(ctx)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return &result
	}
	before, _, err := repo.branchOID(ctx, branch)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return &result
	}
	if !detached && head == branch {
		if err := repo.pullRebase(ctx, remote, branch); err != nil {
			return s.afterPull(ctx, op, repo, repo, branch, before, "", err)
		}
		return nil
	}
	path, err := s.addWorktree(ctx, repo, branch, "base-update")
	if err != nil {
		result := blocked(op, repo.Root, "Não foi possível preparar a worktree da base: "+err.Error())
		return &result
	}
	worktree := repo.at(path)
	if err := worktree.pullRebase(ctx, remote, branch); err != nil {
		return s.afterPull(ctx, op, repo, worktree, branch, before, path, err)
	}
	if err := repo.removeWorktree(ctx, path); err != nil {
		result := Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A base foi atualizada, mas a worktree temporária permanece.",
			Worktree:   path,
			NextSteps:  []string{"Revise " + path + " e remova a worktree quando não for mais necessária."},
		}
		return &result
	}
	return nil
}

func (s Service) afterPull(ctx context.Context, op string, main, affected Repository, branch, before, worktree string, pullErr error) *Result {
	progress, _ := affected.inProgress(ctx)
	current, _, _ := main.branchOID(ctx, branch)
	if progress == "rebase" || progress == "merge" {
		result := Result{
			Status:     StatusConflict,
			Operation:  op,
			Repository: main.Root,
			Head:       affected.headLabel(ctx),
			Message:    "O pull --rebase parou em conflito. Nada foi resolvido nem guardado em stash.",
			Reasons:    []string{pullErr.Error()},
			Worktree:   worktree,
			LocalOID:   current,
			NextSteps:  conflictSteps(progress, worktree),
		}
		return &result
	}
	if current == before {
		if worktree != "" {
			_ = main.removeWorktree(ctx, worktree)
		}
		result := blocked(op, main.Root, "O pull --rebase falhou e a branch local não se moveu: "+pullErr.Error())
		return &result
	}
	result := Result{
		Status:     StatusPartial,
		Operation:  op,
		Repository: main.Root,
		Head:       main.headLabel(ctx),
		Message:    "O pull --rebase falhou depois de mover a branch.",
		Reasons:    []string{pullErr.Error()},
		Worktree:   worktree,
		LocalOID:   current,
		NextSteps:  []string{"A ponta anterior está no reflog.", "Não repita a operação até inspecionar o estado."},
	}
	return &result
}

func conflictSteps(kind, worktree string) []string {
	place := "neste checkout"
	if worktree != "" {
		place = "na worktree " + worktree
	}
	if kind == "merge" {
		return []string{
			"Os conflitos estão " + place + ".",
			"Resolva os arquivos e faça o commit, ou rode git merge --abort para voltar ao estado anterior do merge.",
			"O servidor não resolve conflitos nem faz stash.",
		}
	}
	return []string{
		"O rebase está em andamento " + place + ".",
		"Resolva os arquivos e rode git rebase --continue, ou git rebase --abort para voltar ao estado anterior.",
		"O servidor não resolve conflitos nem faz stash.",
	}
}
