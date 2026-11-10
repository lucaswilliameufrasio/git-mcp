package gitflow

import (
	"context"
	"fmt"
)

// IntegrateInput merges a source commit into a target branch in a side worktree.
type IntegrateInput struct {
	Repository   string `json:"repository" jsonschema:"caminho do work tree Git"`
	Mode         string `json:"mode,omitempty" jsonschema:"preview ou execute; o padrão é preview"`
	Confirmation string `json:"confirmation,omitempty" jsonschema:"confirmação exata devolvida pela prévia"`
	Source       string `json:"source,omitempty" jsonschema:"branch de origem; padrão é a branch atual"`
	Target       string `json:"target" jsonschema:"branch de destino, que já precisa existir no remoto"`
	Remote       string `json:"remote" jsonschema:"remoto do destino"`
	Publish      bool   `json:"publish,omitempty" jsonschema:"envia a branch de destino depois do merge"`
}

// MergeInput merges in the current checkout and returns to the starting branch on success.
type MergeInput struct {
	Repository   string `json:"repository" jsonschema:"caminho do work tree Git"`
	Mode         string `json:"mode,omitempty" jsonschema:"preview ou execute; o padrão é preview"`
	Confirmation string `json:"confirmation,omitempty" jsonschema:"confirmação exata devolvida pela prévia"`
	Source       string `json:"source,omitempty" jsonschema:"branch de origem; padrão é a branch atual"`
	Target       string `json:"target" jsonschema:"branch de destino, que já precisa existir localmente e no remoto"`
	Remote       string `json:"remote" jsonschema:"remoto do destino"`
	Publish      bool   `json:"publish,omitempty" jsonschema:"envia a branch de destino depois do merge"`
}

// IntegrateBranch integra sem trocar o checkout atual.
func (s Service) IntegrateBranch(ctx context.Context, in IntegrateInput) Result {
	const op = "integrate_branch"
	preview, expected, stop := s.prepareIntegrate(ctx, in)
	if stop != nil {
		return *stop
	}
	decision, run := gate(in.Mode, in.Confirmation, expected, preview)
	if !run {
		return decision
	}
	_, expectedAgain, stop := s.prepareIntegrate(ctx, in)
	if stop != nil {
		return *stop
	}
	if expectedAgain != expected {
		return blocked(op, preview.Repository, "O estado mudou depois da prévia. A integração não começou. Gere outra prévia.")
	}
	return s.performIntegrate(ctx, in)
}

// MergeInto integra no checkout principal.
func (s Service) MergeInto(ctx context.Context, in MergeInput) Result {
	const op = "merge_into"
	preview, expected, stop := s.prepareMerge(ctx, in)
	if stop != nil {
		return *stop
	}
	decision, run := gate(in.Mode, in.Confirmation, expected, preview)
	if !run {
		return decision
	}
	_, expectedAgain, stop := s.prepareMerge(ctx, in)
	if stop != nil {
		return *stop
	}
	if expectedAgain != expected {
		return blocked(op, preview.Repository, "O estado mudou depois da prévia. O merge não começou. Gere outra prévia.")
	}
	return s.performMerge(ctx, in)
}

type integratePlan struct {
	repo        Repository
	source      string
	sourceOID   string
	targetName  string
	remote      string
	target      branchObs
	createLocal bool
}

func (s Service) prepareIntegrate(ctx context.Context, in IntegrateInput) (Result, string, *Result) {
	const op = "integrate_branch"
	plan, stop := s.integrationPlan(ctx, op, in.Repository, in.Source, in.Target, in.Remote, true)
	if stop != nil {
		return Result{}, "", stop
	}
	expected := integrationToken(op, plan, in.Publish)
	effects := integrationEffects(plan, in.Publish, true)
	preview := Result{
		Status:     StatusPreview,
		Operation:  op,
		Repository: plan.repo.Root,
		Head:       plan.repo.headLabel(ctx),
		Message:    "Prévia da integração por worktree. O checkout atual não muda nesta prévia.",
		Effects:    effects,
		SourceOID:  plan.sourceOID,
		LocalOID:   plan.target.LocalOID,
		RemoteOID:  plan.target.RemoteOID,
	}
	return preview, expected, nil
}

func (s Service) prepareMerge(ctx context.Context, in MergeInput) (Result, string, *Result) {
	const op = "merge_into"
	plan, stop := s.integrationPlan(ctx, op, in.Repository, in.Source, in.Target, in.Remote, false)
	if stop != nil {
		return Result{}, "", stop
	}
	if !plan.target.LocalExists {
		result := blocked(op, plan.repo.Root, "A branch de destino não existe localmente.")
		return Result{}, "", &result
	}
	expected := integrationToken(op, plan, in.Publish)
	effects := integrationEffects(plan, in.Publish, false)
	preview := Result{
		Status:     StatusPreview,
		Operation:  op,
		Repository: plan.repo.Root,
		Head:       plan.repo.headLabel(ctx),
		Message:    "Prévia do merge no checkout principal. Nada foi alterado.",
		Effects:    effects,
		SourceOID:  plan.sourceOID,
		LocalOID:   plan.target.LocalOID,
		RemoteOID:  plan.target.RemoteOID,
	}
	return preview, expected, nil
}

func (s Service) integrationPlan(ctx context.Context, op, repository, sourceName, target, remote string, sideWorktree bool) (integratePlan, *Result) {
	if err := ctx.Err(); err != nil {
		result := blocked(op, repository, "Operação cancelada.")
		return integratePlan{}, &result
	}
	repo, bad := s.openResult(ctx, op, repository)
	if bad != nil {
		return integratePlan{}, bad
	}
	if ready := s.ensureReady(ctx, op, repo); ready != nil {
		return integratePlan{}, ready
	}
	source, stop := s.namedSource(ctx, op, repo, sourceName)
	if stop != nil {
		return integratePlan{}, stop
	}
	if source == target {
		result := blocked(op, repo.Root, "A origem e o destino precisam ser branches diferentes.")
		return integratePlan{}, &result
	}
	obs, err := s.observeBranch(ctx, repo, target, remote)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return integratePlan{}, &result
	}
	if obs.BlockReason != "" {
		result := Result{
			Status:     StatusBlocked,
			Operation:  op,
			Repository: repo.Root,
			Message:    "O destino não foi atualizado, então a integração não começa.",
			Reasons:    []string{obs.BlockReason},
			LocalOID:   obs.LocalOID,
			RemoteOID:  obs.RemoteOID,
		}
		return integratePlan{}, &result
	}
	var holder *WorktreeInfo
	if sideWorktree {
		holder, err = repo.worktreeOf(ctx, target)
	} else {
		holder, err = s.occupiedElsewhere(ctx, repo, target)
	}
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return integratePlan{}, &result
	}
	if holder != nil {
		result := blocked(op, repo.Root, fmt.Sprintf("A branch %s está em uso na worktree %s.", target, holder.Path))
		return integratePlan{}, &result
	}
	sourceOID, _, err := repo.branchOID(ctx, source)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return integratePlan{}, &result
	}
	return integratePlan{
		repo:        repo,
		source:      source,
		sourceOID:   sourceOID,
		targetName:  target,
		remote:      remote,
		target:      obs,
		createLocal: !obs.LocalExists,
	}, nil
}

func integrationToken(op string, plan integratePlan, publish bool) string {
	return token(
		"operation="+op,
		"repository="+plan.repo.Root,
		"source="+plan.source,
		"target="+plan.targetName,
		"remote="+plan.remote,
		"source_oid="+plan.sourceOID,
		"target_local_oid="+emptyOID(plan.target.LocalOID),
		"target_remote_oid="+plan.target.RemoteOID,
		fmt.Sprintf("ahead=%d", plan.target.Ahead),
		fmt.Sprintf("behind=%d", plan.target.Behind),
		"create_local="+flag(plan.createLocal),
		"publish="+flag(publish),
	)
}

func emptyOID(oid string) string {
	if oid == "" {
		return "-"
	}
	return oid
}

func integrationEffects(plan integratePlan, publish, side bool) []string {
	effects := []string{}
	if side {
		effects = append(effects, "A integração acontece numa worktree nova e o checkout atual permanece.")
	} else {
		effects = append(effects, "O checkout principal muda para o destino durante o merge e volta à branch inicial se tudo terminar.")
		effects = append(effects, "Em conflito, o checkout permanece no destino com o merge em andamento.")
	}
	if plan.createLocal {
		effects = append(effects, "A branch local de destino será criada na ponta remota já observada.")
	}
	if plan.target.Behind > 0 {
		effects = append(effects, "O destino será atualizado com git pull --rebase antes do merge.")
	}
	effects = append(effects, "O merge usa o commit "+shortOID(plan.sourceOID)+" da origem, sem editor.")
	if publish {
		effects = append(effects, "O destino será enviado ao remoto.")
	} else {
		effects = append(effects, "Nada será enviado ao remoto.")
	}
	return effects
}

func (s Service) performIntegrate(ctx context.Context, in IntegrateInput) Result {
	const op = "integrate_branch"
	plan, stop := s.integrationPlan(ctx, op, in.Repository, in.Source, in.Target, in.Remote, true)
	if stop != nil {
		return *stop
	}
	repo := plan.repo
	if plan.createLocal {
		if err := repo.createBranch(ctx, in.Target, plan.target.RemoteOID); err != nil {
			return blocked(op, repo.Root, "Não foi possível criar a branch local de destino: "+err.Error())
		}
	}
	path, err := s.addWorktree(ctx, repo, in.Target, "integration")
	if err != nil {
		if plan.createLocal {
			return Result{
				Status:     StatusPartial,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "A branch local de destino foi criada na ponta remota, mas a worktree de integração não.",
				Reasons:    []string{err.Error()},
				LocalOID:   plan.target.RemoteOID,
			}
		}
		return blocked(op, repo.Root, "Não foi possível criar a worktree de integração: "+err.Error())
	}
	worktree := repo.at(path)
	if plan.target.Behind > 0 {
		if err := worktree.pullRebase(ctx, in.Remote, in.Target); err != nil {
			progress, _ := worktree.inProgress(ctx)
			if progress != "" {
				return Result{
					Status:     StatusConflict,
					Operation:  op,
					Repository: repo.Root,
					Head:       repo.headLabel(ctx),
					Message:    "A atualização do destino parou em conflito. A worktree foi preservada.",
					Reasons:    []string{err.Error()},
					Worktree:   path,
					NextSteps:  conflictSteps(progress, path),
				}
			}
			return Result{
				Status:     StatusPartial,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "A atualização do destino falhou. A worktree foi preservada.",
				Reasons:    []string{err.Error()},
				Worktree:   path,
			}
		}
	}
	if _, _, err := s.syncedOID(ctx, worktree, in.Target, in.Remote); err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "O destino não corresponde ao remoto depois da atualização. O merge não começou.",
			Reasons:    []string{err.Error()},
			Worktree:   path,
		}
	}
	if err := worktree.mergeOID(ctx, plan.sourceOID); err != nil {
		progress, _ := worktree.inProgress(ctx)
		if progress == "merge" {
			return Result{
				Status:     StatusConflict,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "O merge parou em conflito. A worktree foi preservada e o checkout atual não mudou.",
				Reasons:    []string{err.Error()},
				Worktree:   path,
				SourceOID:  plan.sourceOID,
				NextSteps:  conflictSteps("merge", path),
			}
		}
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "O merge falhou. A worktree foi preservada.",
			Reasons:    []string{err.Error()},
			Worktree:   path,
		}
	}
	if in.Publish {
		if refused := refuseMovedRemote(ctx, op, worktree, in.Remote, in.Target, path); refused != nil {
			refused.Repository = repo.Root
			refused.Head = repo.headLabel(ctx)
			return *refused
		}
		if err := worktree.pushRef(ctx, in.Remote, in.Target); err != nil {
			return Result{
				Status:     StatusPartial,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "O merge local terminou e o envio falhou. A worktree foi preservada.",
				Reasons:    []string{err.Error()},
				Worktree:   path,
				LocalOID:   mustOID(ctx, worktree, in.Target),
				NextSteps:  []string{"O checkout de origem permanece.", "Envie a branch a partir da worktree quando o remoto estiver aceitando, ou remova a worktree depois de decidir o que fazer."},
			}
		}
	}
	if err := repo.removeWorktree(ctx, path); err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A integração terminou, mas a worktree permanece.",
			Worktree:   path,
			LocalOID:   mustOID(ctx, repo, in.Target),
		}
	}
	message := "Integração concluída na branch de destino."
	if !in.Publish {
		message = "Integração concluída localmente. Nada foi enviado ao remoto."
	}
	return Result{
		Status:     StatusSuccess,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    message,
		SourceOID:  plan.sourceOID,
		LocalOID:   mustOID(ctx, repo, in.Target),
	}
}

func (s Service) performMerge(ctx context.Context, in MergeInput) Result {
	const op = "merge_into"
	plan, stop := s.integrationPlan(ctx, op, in.Repository, in.Source, in.Target, in.Remote, false)
	if stop != nil {
		return *stop
	}
	repo := plan.repo
	start, startOID, detached, err := repo.head(ctx)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	if plan.target.LocalExists {
		head, _, headDetached, err := repo.head(ctx)
		if err != nil {
			return blocked(op, repo.Root, err.Error())
		}
		if headDetached || head != in.Target {
			if err := repo.switchTo(ctx, in.Target); err != nil {
				return blocked(op, repo.Root, "Não foi possível ir para o destino: "+err.Error())
			}
		}
	}
	if plan.target.Behind > 0 {
		if err := repo.pullRebase(ctx, in.Remote, in.Target); err != nil {
			progress, _ := repo.inProgress(ctx)
			if progress != "" {
				return Result{
					Status:     StatusConflict,
					Operation:  op,
					Repository: repo.Root,
					Head:       repo.headLabel(ctx),
					Message:    "A atualização do destino parou em conflito. O checkout ficou no destino.",
					Reasons:    []string{err.Error()},
					NextSteps:  conflictSteps(progress, ""),
				}
			}
			return Result{
				Status:     StatusPartial,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "A atualização do destino falhou. O checkout ficou no destino.",
				Reasons:    []string{err.Error()},
			}
		}
	}
	if _, _, err := s.syncedOID(ctx, repo, in.Target, in.Remote); err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "O destino não corresponde ao remoto. O merge não começou.",
			Reasons:    []string{err.Error()},
		}
	}
	if err := repo.mergeOID(ctx, plan.sourceOID); err != nil {
		progress, _ := repo.inProgress(ctx)
		if progress == "merge" {
			return Result{
				Status:     StatusConflict,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "O merge parou em conflito. O checkout permanece no destino.",
				Reasons:    []string{err.Error()},
				SourceOID:  plan.sourceOID,
				NextSteps:  conflictSteps("merge", ""),
			}
		}
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "O merge falhou. O checkout permanece no destino.",
			Reasons:    []string{err.Error()},
		}
	}
	if in.Publish {
		if refused := refuseMovedRemote(ctx, op, repo, in.Remote, in.Target, ""); refused != nil {
			return *refused
		}
		if err := repo.pushRef(ctx, in.Remote, in.Target); err != nil {
			return Result{
				Status:     StatusPartial,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "O merge local terminou e o envio falhou. O checkout permanece no destino.",
				Reasons:    []string{err.Error()},
				LocalOID:   mustOID(ctx, repo, in.Target),
				NextSteps:  []string{"O merge já está na branch local.", "Envie a branch quando o remoto aceitar, ou desfaça o merge se ele não deve permanecer."},
			}
		}
	}
	if err := restoreStart(ctx, repo, start, startOID, detached); err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "O merge terminou, mas não foi possível voltar ao checkout inicial.",
			Reasons:    []string{err.Error()},
			LocalOID:   mustOID(ctx, repo, in.Target),
		}
	}
	message := "Merge concluído e enviado."
	if !in.Publish {
		message = "Merge concluído localmente. Nada foi enviado ao remoto."
	}
	return Result{
		Status:     StatusSuccess,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    message,
		SourceOID:  plan.sourceOID,
		LocalOID:   mustOID(ctx, repo, in.Target),
	}
}

func restoreStart(ctx context.Context, repo Repository, branch, oid string, detached bool) error {
	if detached {
		_, err := repo.run(ctx, "switch", "--detach", oid)
		return err
	}
	if branch == "" {
		return nil
	}
	return repo.switchTo(ctx, branch)
}

func refuseMovedRemote(ctx context.Context, op string, repo Repository, remote, branch, worktree string) *Result {
	remoteOID, err := repo.remoteBranchOID(ctx, remote, branch)
	if err != nil {
		result := Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "O merge local terminou, mas o remoto não pôde ser relido. Nada foi enviado.",
			Reasons:    []string{err.Error()},
			Worktree:   worktree,
		}
		return &result
	}
	local := mustOID(ctx, repo, branch)
	ok, err := repo.ancestor(ctx, remoteOID, local)
	if err != nil || !ok {
		message := "O remoto mudou e o envio deixaria de ser um avanço. Nada foi enviado."
		if err != nil {
			message = err.Error()
		}
		result := Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    message,
			Worktree:   worktree,
			LocalOID:   local,
			RemoteOID:  remoteOID,
			NextSteps:  []string{"O merge permanece na branch local.", "Gere outra prévia se ainda quiser publicar."},
		}
		return &result
	}
	return nil
}
