package gitflow

import (
	"context"
	"fmt"
)

// ReleaseBranchInput creates a release branch from an updated base.
type ReleaseBranchInput struct {
	Repository      string `json:"repository" jsonschema:"caminho do work tree Git"`
	Mode            string `json:"mode,omitempty" jsonschema:"preview ou execute; o padrão é preview"`
	Confirmation    string `json:"confirmation,omitempty" jsonschema:"confirmação exata devolvida pela prévia"`
	Base            string `json:"base" jsonschema:"branch-base que será atualizada antes de criar a release"`
	Remote          string `json:"remote" jsonschema:"remoto da base e, se publish, da release"`
	Name            string `json:"name" jsonschema:"nome da branch de release"`
	Publish         bool   `json:"publish,omitempty" jsonschema:"envia a release ao terminar"`
	ReplaceExisting bool   `json:"replace_existing,omitempty" jsonschema:"move uma release local já existente para a nova ponta"`
}

// ReleaseTagInput creates a release branch from a tag commit.
type ReleaseTagInput struct {
	Repository      string `json:"repository" jsonschema:"caminho do work tree Git"`
	Mode            string `json:"mode,omitempty" jsonschema:"preview ou execute; o padrão é preview"`
	Confirmation    string `json:"confirmation,omitempty" jsonschema:"confirmação exata devolvida pela prévia"`
	Tag             string `json:"tag" jsonschema:"tag que aponta para o commit da release"`
	Remote          string `json:"remote" jsonschema:"remoto de onde a tag é lida"`
	Name            string `json:"name" jsonschema:"nome da branch de release"`
	Publish         bool   `json:"publish,omitempty" jsonschema:"envia a release ao terminar"`
	ReplaceExisting bool   `json:"replace_existing,omitempty" jsonschema:"move uma release local já existente para o commit da tag"`
}

// CreateReleaseBranch atualiza a base e cria a branch de release a partir dela.
func (s Service) CreateReleaseBranch(ctx context.Context, in ReleaseBranchInput) Result {
	const op = "create_release_branch"
	preview, expected, stop := s.prepareReleaseBranch(ctx, in)
	if stop != nil {
		return *stop
	}
	decision, run := gate(in.Mode, in.Confirmation, expected, preview)
	if !run {
		return decision
	}
	_, expectedAgain, stop := s.prepareReleaseBranch(ctx, in)
	if stop != nil {
		return *stop
	}
	if expectedAgain != expected {
		return blocked(op, preview.Repository, "O estado mudou depois da prévia. A release não foi criada. Gere outra prévia.")
	}
	return s.performReleaseBranch(ctx, in)
}

// CreateReleaseFromTag cria a release no commit da tag, sem atualizar uma branch-base.
func (s Service) CreateReleaseFromTag(ctx context.Context, in ReleaseTagInput) Result {
	const op = "create_release_from_tag"
	preview, expected, stop := s.prepareReleaseTag(ctx, in)
	if stop != nil {
		return *stop
	}
	decision, run := gate(in.Mode, in.Confirmation, expected, preview)
	if !run {
		return decision
	}
	_, expectedAgain, stop := s.prepareReleaseTag(ctx, in)
	if stop != nil {
		return *stop
	}
	if expectedAgain != expected {
		return blocked(op, preview.Repository, "O estado mudou depois da prévia. A release não foi criada. Gere outra prévia.")
	}
	return s.performReleaseTag(ctx, in)
}

func (s Service) prepareReleaseBranch(ctx context.Context, in ReleaseBranchInput) (Result, string, *Result) {
	const op = "create_release_branch"
	repo, bad := s.openResult(ctx, op, in.Repository)
	if bad != nil {
		return Result{}, "", bad
	}
	if ready := s.ensureReady(ctx, op, repo); ready != nil {
		return Result{}, "", ready
	}
	if err := repo.checkBranch(ctx, in.Name); err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if in.Name == in.Base {
		result := blocked(op, repo.Root, "O nome da release precisa ser diferente da base.")
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
	if obs.BlockReason != "" {
		result := Result{
			Status:     StatusBlocked,
			Operation:  op,
			Repository: repo.Root,
			Message:    "A base não foi atualizada, então a release não foi criada.",
			Reasons:    []string{obs.BlockReason},
			LocalOID:   obs.LocalOID,
			RemoteOID:  obs.RemoteOID,
		}
		return Result{}, "", &result
	}
	releaseOID, dropped, stop := s.prepareReplacement(ctx, op, repo, in.Name, obs.LocalOID, in.ReplaceExisting)
	if stop != nil {
		return Result{}, "", stop
	}
	expected := token(
		"operation="+op,
		"repository="+repo.Root,
		"base="+in.Base,
		"remote="+in.Remote,
		"name="+in.Name,
		"base_local_oid="+obs.LocalOID,
		"base_remote_oid="+obs.RemoteOID,
		fmt.Sprintf("ahead=%d", obs.Ahead),
		fmt.Sprintf("behind=%d", obs.Behind),
		"release_oid="+emptyOID(releaseOID),
		"publish="+flag(in.Publish),
		"replace_existing="+flag(in.ReplaceExisting),
	)
	effects := []string{"A base será atualizada com git pull --rebase quando estiver atrás do remoto.", "A branch " + in.Name + " passará a apontar para essa base."}
	effects = append(effects, replacementEffects(in.Name, in.ReplaceExisting, releaseOID, dropped)...)
	effects = append(effects, publishEffect(in.Publish))
	preview := Result{
		Status:     StatusPreview,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "Prévia da release. Nenhuma branch foi movida.",
		Effects:    effects,
		Dropped:    dropped,
		LocalOID:   obs.LocalOID,
		RemoteOID:  obs.RemoteOID,
	}
	return preview, expected, nil
}

func (s Service) prepareReleaseTag(ctx context.Context, in ReleaseTagInput) (Result, string, *Result) {
	const op = "create_release_from_tag"
	repo, bad := s.openResult(ctx, op, in.Repository)
	if bad != nil {
		return Result{}, "", bad
	}
	if ready := s.ensureReady(ctx, op, repo); ready != nil {
		return Result{}, "", ready
	}
	if err := repo.checkTag(ctx, in.Tag); err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	if err := repo.checkBranch(ctx, in.Name); err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	tagOID, err := repo.remoteTagOID(ctx, in.Remote, in.Tag)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return Result{}, "", &result
	}
	releaseOID, dropped, stop := s.prepareReplacement(ctx, op, repo, in.Name, tagOID, in.ReplaceExisting)
	if stop != nil {
		return Result{}, "", stop
	}
	expected := token(
		"operation="+op,
		"repository="+repo.Root,
		"tag="+in.Tag,
		"remote="+in.Remote,
		"name="+in.Name,
		"tag_oid="+tagOID,
		"release_oid="+emptyOID(releaseOID),
		"publish="+flag(in.Publish),
		"replace_existing="+flag(in.ReplaceExisting),
	)
	effects := []string{
		"A tag " + in.Tag + " será validada no commit " + shortOID(tagOID) + ".",
		"Nenhuma branch-base será atualizada com pull --rebase.",
		"A branch " + in.Name + " passará a apontar para esse commit.",
	}
	effects = append(effects, replacementEffects(in.Name, in.ReplaceExisting, releaseOID, dropped)...)
	effects = append(effects, publishEffect(in.Publish))
	preview := Result{
		Status:     StatusPreview,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    "Prévia da release a partir da tag. Nenhuma branch foi movida.",
		Effects:    effects,
		Dropped:    dropped,
		SourceOID:  tagOID,
		LocalOID:   releaseOID,
	}
	return preview, expected, nil
}

func (s Service) prepareReplacement(ctx context.Context, op string, repo Repository, name, start string, replace bool) (string, []string, *Result) {
	holder, err := repo.worktreeOf(ctx, name)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return "", nil, &result
	}
	oid, exists, err := repo.branchOID(ctx, name)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return "", nil, &result
	}
	if !exists {
		return "", nil, nil
	}
	if holder != nil {
		result := blocked(op, repo.Root, fmt.Sprintf("A branch %s está em uso na worktree %s e não será movida.", name, holder.Path))
		return "", nil, &result
	}
	dropped, err := repo.commitsBetween(ctx, start, oid)
	if err != nil {
		result := blocked(op, repo.Root, err.Error())
		return "", nil, &result
	}
	if !replace {
		result := Result{
			Status:     StatusBlocked,
			Operation:  op,
			Repository: repo.Root,
			Message:    "A branch de release já existe. A execução exige replace_existing na prévia.",
			Dropped:    dropped,
			LocalOID:   oid,
		}
		return "", nil, &result
	}
	return oid, dropped, nil
}

func replacementEffects(name string, replace bool, oid string, dropped []string) []string {
	if oid == "" {
		return []string{"A branch " + name + " ainda não existe e será criada."}
	}
	line := "A branch " + name + " já existe"
	if len(dropped) > 0 {
		line += fmt.Sprintf(" e tem %d commit(s) que deixarão de estar nela", len(dropped))
	}
	line += ". replace_existing move a ponta; o reflog guarda a anterior."
	if !replace {
		line += " Sem esse campo a execução para."
	}
	return []string{line}
}

func publishEffect(publish bool) string {
	if publish {
		return "A branch de release será enviada ao remoto, sem sobrescrever história remota."
	}
	return "Nada será enviado ao remoto."
}

func (s Service) performReleaseBranch(ctx context.Context, in ReleaseBranchInput) Result {
	const op = "create_release_branch"
	repo, err := s.open(ctx, in.Repository)
	if err != nil {
		return blocked(op, in.Repository, err.Error())
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
			Message:    "A base não foi atualizada, então a release não foi criada.",
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
	local, _, err := s.syncedOID(ctx, repo, in.Base, in.Remote)
	if err != nil {
		return Result{
			Status:     StatusBlocked,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A base não corresponde ao remoto. A release não foi criada.",
			Reasons:    []string{err.Error()},
		}
	}
	if err := s.pointRelease(ctx, repo, in.Name, local, in.ReplaceExisting); err != nil {
		return Result{
			Status:     StatusPartial,
			Operation:  op,
			Repository: repo.Root,
			Head:       repo.headLabel(ctx),
			Message:    "A base está atualizada, mas a branch de release não ficou no commit esperado.",
			Reasons:    []string{err.Error()},
			LocalOID:   local,
		}
	}
	return s.finishRelease(ctx, op, repo, in.Name, in.Remote, in.Publish)
}

func (s Service) performReleaseTag(ctx context.Context, in ReleaseTagInput) Result {
	const op = "create_release_from_tag"
	repo, err := s.open(ctx, in.Repository)
	if err != nil {
		return blocked(op, in.Repository, err.Error())
	}
	tagOID, err := repo.remoteTagOID(ctx, in.Remote, in.Tag)
	if err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	if err := repo.fetchTag(ctx, in.Remote, in.Tag); err != nil {
		return blocked(op, repo.Root, "A tag local não foi atualizada a partir do remoto: "+err.Error())
	}
	localTag, err := repo.tagCommit(ctx, in.Tag)
	if err != nil || localTag != tagOID {
		return blocked(op, repo.Root, "A tag local não corresponde ao commit observado no remoto.")
	}
	if err := s.pointRelease(ctx, repo, in.Name, tagOID, in.ReplaceExisting); err != nil {
		return blocked(op, repo.Root, err.Error())
	}
	return s.finishRelease(ctx, op, repo, in.Name, in.Remote, in.Publish)
}

func (s Service) pointRelease(ctx context.Context, repo Repository, name, start string, replace bool) error {
	oid, exists, err := repo.branchOID(ctx, name)
	if err != nil {
		return err
	}
	if exists && !replace {
		return fmt.Errorf("a branch %s já existe", name)
	}
	if exists && oid == start {
		return repo.switchTo(ctx, name)
	}
	if exists {
		if err := repo.forceBranch(ctx, name, start); err != nil {
			return err
		}
		return repo.switchTo(ctx, name)
	}
	return repo.switchNew(ctx, name, start)
}

func (s Service) finishRelease(ctx context.Context, op string, repo Repository, name, remote string, publish bool) Result {
	if publish {
		remoteOID, err := repo.remoteBranchOID(ctx, remote, name)
		if err == nil {
			local := mustOID(ctx, repo, name)
			ok, ancestorErr := repo.ancestor(ctx, remoteOID, local)
			if ancestorErr != nil || !ok {
				return Result{
					Status:     StatusPartial,
					Operation:  op,
					Repository: repo.Root,
					Head:       repo.headLabel(ctx),
					Message:    "A release local foi criada, mas o remoto tem uma ponta que o envio não avança. Nada foi enviado.",
					LocalOID:   local,
					RemoteOID:  remoteOID,
				}
			}
		}
		if err := repo.pushRef(ctx, remote, name); err != nil {
			return Result{
				Status:     StatusPartial,
				Operation:  op,
				Repository: repo.Root,
				Head:       repo.headLabel(ctx),
				Message:    "A release local foi criada e o envio falhou.",
				Reasons:    []string{err.Error()},
				LocalOID:   mustOID(ctx, repo, name),
				NextSteps:  []string{"O checkout está na release.", "Envie a branch quando o remoto aceitar."},
			}
		}
	}
	message := "Release criada localmente."
	if publish {
		message = "Release criada e enviada."
	}
	return Result{
		Status:     StatusSuccess,
		Operation:  op,
		Repository: repo.Root,
		Head:       repo.headLabel(ctx),
		Message:    message,
		LocalOID:   mustOID(ctx, repo, name),
	}
}
