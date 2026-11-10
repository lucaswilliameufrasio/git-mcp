package gitflow

import (
	"context"
	"fmt"
)

// RebaseInput rebases one branch onto a base that was updated with git pull --rebase.
// The rebase itself is not interactive.
type RebaseInput struct {
	Repository   string `json:"repository" jsonschema:"caminho do work tree Git"`
	Mode         string `json:"mode,omitempty" jsonschema:"preview ou execute; o padrão é preview"`
	Confirmation string `json:"confirmation,omitempty" jsonschema:"confirmação exata devolvida pela prévia"`
	Source       string `json:"source,omitempty" jsonschema:"branch que será rebaseada; padrão é a branch atual"`
	Base         string `json:"base" jsonschema:"branch-base que será atualizada e usada como novo upstream"`
	Remote       string `json:"remote" jsonschema:"remoto da branch-base"`
}

// RebaseOnto atualiza a base e faz git rebase não interativo.
func (s Service) RebaseOnto(ctx context.Context, in RebaseInput) Result {
	const op = "rebase_onto"
	preview, expected, stop := s.prepareRebase(ctx, in)
	if stop != nil {
		return *stop
	}
	decision, run := gate(in.Mode, in.Confirmation, expected, preview)
	if !run {
		return decision
	}
	_, expectedAgain, stop := s.prepareRebase(ctx, in)
	if stop != nil {
		return *stop
	}
	if expectedAgain != expected {
		return blocked(op, preview.Repository, "O estado mudou depois da prévia. O rebase não começou. Gere outra prévia.")
	}
	return s.performRebase(ctx, in)
}

func (s Service) prepareRebase(ctx context.Context, in RebaseInput) (Result, string, *Result) {
	const op = "rebase_onto"
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
	source, stop := s.namedSource(ctx, op, repo, in.Source)
	if stop != nil {
		return Result{}, "", stop
	}
	if source == in.Base {
		result := blocked(op, repo.Root, "A branch de origem e a base precisam ser diferentes.")
		return Result{}, "", &result
	}
	obs, err := s.observeBranch(ctx, repo, in.Base, in.Remote)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if !obs.LocalExists {
		result := blocked(op, repo.Root, "A branch-base local não existe.")
		return Result{}, "", &result
	}
	other, err := s.occupiedElsewhere(ctx, repo, in.Base)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if other != nil {
		result := blocked(op, repo.Root, fmt.Sprintf("A base %s está em uso na worktree %s.", in.Base, other.Path))
		return Result{}, "", &result
	}
	sourceOther, err := s.occupiedElsewhere(ctx, repo, source)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if sourceOther != nil {
		result := blocked(op, repo.Root, fmt.Sprintf("A branch %s está em uso na worktree %s.", source, sourceOther.Path))
		return Result{}, "", &result
	}
	if obs.BlockReason != "" {
		result := Result{
			Status:     StatusBlocked,
			Operation:  op,
			Repository: repo.Root,
			Message:    "A base não foi atualizada, então o rebase não começa.",
			Reasons:    []string{obs.BlockReason},
			LocalOID:   obs.LocalOID,
			RemoteOID:  obs.RemoteOID,
		}
		return Result{}, "", &result
	}
	sourceOID, _, err := repo.branchOID(ctx, source)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	expected := token(
		"operation="+op,
		"repository="+repo.Root,
		"source="+source,
		"base="+in.Base,
		"remote="+in.Remote,
		"source_oid="+sourceOID,
		"base_local_oid="+obs.LocalOID,
		"base_remote_oid="+obs.RemoteOID,
		fmt.Sprintf("ahead=%d", obs.Ahead),
		fmt.Sprintf("behind=%d", obs.Behind),
	)
	effects := []string{
		"A base " + in.Base + " será atualizada com git pull --rebase quando estiver atrás do remoto.",
		"Depois, " + source + " recebe git rebase " + in.Base + ", sem editor.",
		"Squash e reword continuam no terminal.",
	}
	preview := Result{
		Status:     StatusPreview,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "Prévia do rebase. Nenhuma branch foi movida.",
		Effects:    effects,
		SourceOID:  sourceOID,
		LocalOID:   obs.LocalOID,
		RemoteOID:  obs.RemoteOID,
	}
	return preview, expected, nil
}

func (s Service) performRebase(ctx context.Context, in RebaseInput) Result {
	const op = "rebase_onto"
	repo, err := s.open(ctx, in.Repository)
	if err != nil {
		return blocked(op, in.Repository, err.Error())
	}
	source, stop := s.namedSource(ctx, op, repo, in.Source)
	if stop != nil {
		return *stop
	}
	obs, err := s.observeBranch(ctx, repo, in.Base, in.Remote)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	if !obs.LocalExists || obs.BlockReason != "" {
		reason := obs.BlockReason
		if reason == "" {
			reason = "A branch-base local não existe."
		}
		return Result{
			Status:     StatusBlocked,
			Operation:  op,
			Repository: repo.Root,
			Message:    "A base não foi atualizada, então o rebase não começa.",
			Reasons:    []string{reason},
			LocalOID:   obs.LocalOID,
			RemoteOID:  obs.RemoteOID,
		}
	}
	if obs.Behind > 0 {
		if stopped := s.fastForward(ctx, op, repo, in.Base, in.Remote); stopped != nil {
			return *stopped
		}
	}
	if _, _, err := s.syncedOID(ctx, repo, in.Base, in.Remote); err != nil {
		return Result{
			Status:     StatusBlocked,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A base não corresponde ao remoto atualizado. O rebase não começou.",
			Reasons:    []string{err.Error()},
		}
	}
	head, _, detached, err := repo.head(ctx)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	if detached || head != source {
		if err := repo.switchTo(ctx, source); err != nil {
			return Result{
				Status:     StatusPartial,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "A base foi atualizada, mas não foi possível voltar à branch de origem.",
				Reasons:    []string{err.Error()},
			}
		}
	}
	if err := repo.rebaseOnto(ctx, in.Base); err != nil {
		progress, _ := repo.inProgress(ctx)
		if progress == "rebase" {
			return Result{
				Status:     StatusConflict,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "O rebase parou em conflito. O estado foi preservado.",
				Reasons:    []string{err.Error()},
				NextSteps:  conflictSteps("rebase", ""),
			}
		}
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "O rebase falhou.",
			Reasons:    []string{err.Error()},
			NextSteps:  []string{"Inspecione o repositório antes de tentar de novo."},
		}
	}
	return Result{
		Status:     StatusSuccess,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "Rebase não interativo concluído sobre a base atualizada.",
		SourceOID:  mustOID(ctx, repo, source),
		LocalOID:   mustOID(ctx, repo, in.Base),
	}
}

func (s Service) namedSource(ctx context.Context, op string, repo Repository, requested string) (string, *Result) {
	if requested != "" {
		if err := repo.checkBranch(ctx, requested); err != nil {
			result := blocked(op, repo.Root, err.Error())
			return "", &result
		}
		_, exists, err := repo.branchOID(ctx, requested)
		if err != nil {
			result := blocked(op, repo.Root, err.Error())
			return "", &result
		}
		if !exists {
			result := blocked(op, repo.Root, "A branch de origem não existe.")
			return "", &result
		}
		return requested, nil
	}
	branch, _, detached, err := repo.head(ctx)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return "", &result
	}
	if detached || branch == "" {
		result := blocked(op, repo.Root, "HEAD está destacado. Informe a branch de origem.")
		return "", &result
	}
	return branch, nil
}

func mustOID(ctx context.Context, repo Repository, branch string) string {
	oid, _, err := repo.branchOID(ctx, branch)
	if err != nil {
		return ""
	}
	return oid
}
