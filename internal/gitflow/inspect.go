package gitflow

import (
	"context"
	"fmt"
)

// InspectInput selects a work tree and, optionally, one branch to compare with its remote.
type InspectInput struct {
	Repository string `json:"repository" jsonschema:"caminho do work tree Git"`
	Base       string `json:"base,omitempty" jsonschema:"branch local a comparar; exige remote"`
	Remote     string `json:"remote,omitempty" jsonschema:"nome do remoto; exige base"`
	Fetch      bool   `json:"fetch,omitempty" jsonschema:"consulta o remoto com ls-remote, sem atualizar refs locais"`
}

// Inspect reads the work tree. fetch consulta o remoto e não move branches locais.
func (s Service) Inspect(ctx context.Context, in InspectInput) Result {
	const op = "repository_inspect"
	if err := ctx.Err(); err != nil {
		return blocked(op, in.Repository, "Operação cancelada.")
	}
	repo, bad := s.openResult(ctx, op, in.Repository)
	if bad != nil {
		return *bad
	}
	details, err := s.snapshot(ctx, repo, in.Base, in.Remote, in.Fetch)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		result.Details = &details
		return result
	}
	return Result{
		Status:     StatusSuccess,
		Operation:  op,
		Repository: repo.Root,
		Head:       details.Head,
		Message:    "Leitura do repositório. Nenhuma branch local foi movida.",
		Changes:    details.Changes,
		Details:    &details,
	}
}

func (s Service) snapshot(ctx context.Context, repo Repository, base, remote string, fetch bool) (Inspection, error) {
	inspection := Inspection{Repository: repo.Root}
	branch, oid, detached, err := repo.head(ctx)
	if err != nil {
		return inspection, err
	}
	inspection.Detached = detached
	if detached {
		inspection.Head = "detached@" + shortOID(oid)
	} else {
		inspection.Head = branch
	}
	changes, err := repo.changes(ctx)
	if err != nil {
		return inspection, err
	}
	inspection.Changes = changes
	inspection.Dirty = len(changes) > 0
	progress, err := repo.inProgress(ctx)
	if err != nil {
		return inspection, err
	}
	inspection.InProgress = progress
	worktrees, err := repo.worktrees(ctx)
	if err != nil {
		return inspection, err
	}
	inspection.Worktrees = worktrees
	if base == "" && remote == "" {
		return inspection, nil
	}
	if base == "" || remote == "" {
		return inspection, fmt.Errorf("base e remote precisam ser informados juntos")
	}
	if err := repo.checkBranch(ctx, base); err != nil {
		return inspection, err
	}
	divergence := Divergence{Branch: base, Remote: remote, Relation: "local-only"}
	local, exists, err := repo.branchOID(ctx, base)
	if err != nil {
		return inspection, err
	}
	if exists {
		divergence.LocalOID = local
	}
	if !fetch {
		inspection.Base = &divergence
		return inspection, nil
	}
	remoteOID, err := repo.remoteBranchOID(ctx, remote, base)
	if err != nil {
		inspection.Base = &divergence
		return inspection, err
	}
	divergence.RemoteOID = remoteOID
	if !exists {
		divergence.Relation = "missing-local"
		inspection.Base = &divergence
		return inspection, nil
	}
	if !repo.hasCommit(ctx, remoteOID) {
		if err := repo.fetchBranchObjects(ctx, remote, base); err != nil {
			return inspection, err
		}
	}
	ahead, behind, err := repo.counts(ctx, local, remoteOID)
	if err != nil {
		return inspection, err
	}
	divergence.Ahead = ahead
	divergence.Behind = behind
	divergence.Relation, _ = relationOf(ahead, behind)
	inspection.Base = &divergence
	return inspection, nil
}

func (s Service) ensureReady(ctx context.Context, op string, repo Repository) *Result {
	changes, err := repo.changes(ctx)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return &result
	}
	progress, err := repo.inProgress(ctx)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return &result
	}
	var reasons []string
	if len(changes) > 0 {
		reasons = append(reasons, "há alterações locais sem commit")
	}
	if progress != "" {
		reasons = append(reasons, "há uma operação Git em andamento: "+progress)
	}
	if len(reasons) == 0 {
		return nil
	}
	result := Result{
		Status:     StatusBlocked,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "A operação parou para preservar o estado do repositório.",
		Reasons:    reasons,
		Changes:    changes,
	}
	return &result
}

func (s Service) ensureNoOperation(ctx context.Context, op string, repo Repository) *Result {
	progress, err := repo.inProgress(ctx)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return &result
	}
	if progress == "" {
		return nil
	}
	result := blocked(op, repo.Root, "Há uma operação Git em andamento: "+progress+".")
	return &result
}

type branchObs struct {
	LocalOID    string
	RemoteOID   string
	LocalExists bool
	Ahead       int
	Behind      int
	Relation    string
	BlockReason string
}

func (s Service) observeBranch(ctx context.Context, repo Repository, branch, remote string) (branchObs, error) {
	if err := repo.checkBranch(ctx, branch); err != nil {
		return branchObs{}, err
	}
	local, exists, err := repo.branchOID(ctx, branch)
	if err != nil {
		return branchObs{}, err
	}
	remoteOID, err := repo.remoteBranchOID(ctx, remote, branch)
	if err != nil {
		return branchObs{}, err
	}
	obs := branchObs{LocalOID: local, RemoteOID: remoteOID, LocalExists: exists, Relation: "missing-local"}
	if !exists {
		return obs, nil
	}
	if !repo.hasCommit(ctx, remoteOID) {
		if err := repo.fetchBranchObjects(ctx, remote, branch); err != nil {
			return branchObs{}, err
		}
	}
	ahead, behind, err := repo.counts(ctx, local, remoteOID)
	if err != nil {
		return branchObs{}, err
	}
	obs.Ahead = ahead
	obs.Behind = behind
	obs.Relation, obs.BlockReason = relationOf(ahead, behind)
	return obs, nil
}

func (s Service) occupiedElsewhere(ctx context.Context, repo Repository, branch string) (*WorktreeInfo, error) {
	item, err := repo.worktreeOf(ctx, branch)
	if err != nil || item == nil {
		return nil, err
	}
	if item.Path == repo.Root {
		return nil, nil
	}
	return item, nil
}

func shortOID(oid string) string {
	if len(oid) > 12 {
		return oid[:12]
	}
	return oid
}
