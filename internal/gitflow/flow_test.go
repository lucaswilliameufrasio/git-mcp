package gitflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectFetchDoesNotMoveLocalBranch(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	before := l.oid(l.repo, "main")
	remote := l.advanceRemoteMain()
	got := l.service.Inspect(l.ctx(), InspectInput{
		Repository: l.repo,
		Base:       "main",
		Remote:     "origin",
		Fetch:      true,
	})
	requireStatus(t, got, StatusSuccess)
	if l.oid(l.repo, "main") != before {
		t.Fatal("ls-remote moveu a branch local")
	}
	if got.Details == nil || got.Details.Base == nil {
		t.Fatal("divergência ausente")
	}
	if got.Details.Base.RemoteOID != remote || got.Details.Base.Relation != "behind" {
		t.Fatalf("divergência %+v", got.Details.Base)
	}
	localOnly := l.service.Inspect(l.ctx(), InspectInput{Repository: l.repo, Base: "main", Remote: "origin"})
	requireStatus(t, localOnly, StatusSuccess)
	if localOnly.Details.Base.RemoteOID != "" {
		t.Fatal("sem fetch a leitura não consulta o remoto")
	}
}

func TestInspectRequiresBaseAndRemoteTogether(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	got := l.service.Inspect(l.ctx(), InspectInput{Repository: l.repo, Base: "main"})
	requireStatus(t, got, StatusBlocked)
	missing := l.service.Inspect(l.ctx(), InspectInput{Repository: filepath.Join(l.root, "ausente")})
	requireStatus(t, missing, StatusBlocked)
}

func TestUpdatePreviewDoesNotMoveAndExecuteFastForwards(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	before := l.oid(l.repo, "main")
	tracking := l.oid(l.repo, "refs/remotes/origin/main")
	remote := l.advanceRemoteMain()
	input := UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "origin"}
	preview := l.service.UpdateBranch(l.ctx(), input)
	requireStatus(t, preview, StatusPreview)
	if l.oid(l.repo, "main") != before {
		t.Fatal("prévia moveu a branch")
	}
	if l.oid(l.repo, "refs/remotes/origin/main") != tracking {
		t.Fatal("prévia moveu o remote-tracking")
	}
	if l.hasRef(l.repo, "refs/git-mcp/observe/main") {
		t.Fatal("ref temporária de observação permaneceu")
	}
	if strings.Contains(preview.Confirmation, l.service.WorktreeRoot) {
		t.Fatal("confirmação inclui caminho de worktree")
	}
	input.Mode = ModeExecute
	input.Confirmation = "não"
	refused := l.service.UpdateBranch(l.ctx(), input)
	requireStatus(t, refused, StatusBlocked)
	if l.oid(l.repo, "main") != before {
		t.Fatal("confirmação errada moveu a branch")
	}
	done := executed(t, preview, func(confirmation string) Result {
		input.Confirmation = confirmation
		return l.service.UpdateBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.oid(l.repo, "main") != remote || done.LocalOID != remote {
		t.Fatalf("local %s remoto %s resultado %s", l.oid(l.repo, "main"), remote, done.LocalOID)
	}
	l.assertNoHomeWorktree()
}

func TestUpdateOtherBranchReturnsToPrevious(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.commit(l.repo, "feature.txt", "f\n", "feature")
	remote := l.advanceRemoteMain()
	input := UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "origin"}
	preview := l.service.UpdateBranch(l.ctx(), input)
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.UpdateBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.head(l.repo) != "feature" {
		t.Fatalf("checkout ficou em %s", l.head(l.repo))
	}
	if l.oid(l.repo, "main") != remote {
		t.Fatal("main não acompanhou o remoto")
	}
	if strings.Contains(l.worktreeList(), "base-update") {
		t.Fatalf("worktree temporária permaneceu: %s", l.worktreeList())
	}
	l.assertNoHomeWorktree()
}

func TestUpdateCanRemainOnTarget(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	remote := l.advanceRemoteMain()
	input := UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "origin", RemainOnTarget: true}
	preview := l.service.UpdateBranch(l.ctx(), input)
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.UpdateBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.head(l.repo) != "main" || l.oid(l.repo, "main") != remote {
		t.Fatalf("head %s oid %s", l.head(l.repo), l.oid(l.repo, "main"))
	}
}

func TestLocalOnlyBaseCommitsAreNotRewritten(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	local := l.commit(l.repo, "README", "local-only\n", "local")
	got := l.service.UpdateBranch(l.ctx(), UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "origin"})
	requireStatus(t, got, StatusBlocked)
	if l.oid(l.repo, "main") != local {
		t.Fatal("commits locais da base foram reescritos")
	}
	remote := l.advanceRemoteMain()
	diverged := l.service.UpdateBranch(l.ctx(), UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "origin"})
	requireStatus(t, diverged, StatusBlocked)
	if l.oid(l.repo, "main") != local || l.oid(l.other, "main") != remote {
		t.Fatal("divergência alterou um dos lados")
	}
}

func TestDirtyTreeAndMissingRemoteBlockUpdate(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	before := l.oid(l.repo, "main")
	writeFile(t, filepath.Join(l.repo, "README"), "dirty\n")
	dirty := l.service.UpdateBranch(l.ctx(), UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "origin"})
	requireStatus(t, dirty, StatusBlocked)
	git(t, l.repo, "checkout", "--", "README")
	git(t, l.repo, "remote", "set-url", "origin", filepath.Join(l.root, "ausente.git"))
	offline := l.service.UpdateBranch(l.ctx(), UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "origin"})
	requireStatus(t, offline, StatusBlocked)
	if l.oid(l.repo, "main") != before {
		t.Fatal("falha de leitura do remoto moveu a branch")
	}
}

func TestNoDefaultRemoteName(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	before := l.oid(l.repo, "main")
	l.advanceRemoteMain()
	wrong := l.service.UpdateBranch(l.ctx(), UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "o"})
	requireStatus(t, wrong, StatusBlocked)
	if !strings.Contains(wrong.Message, "o") {
		t.Fatalf("mensagem: %s", wrong.Message)
	}
	git(t, l.repo, "remote", "rename", "origin", "upstream")
	git(t, l.other, "remote", "rename", "origin", "upstream")
	stillOrigin := l.service.UpdateBranch(l.ctx(), UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "origin"})
	requireStatus(t, stillOrigin, StatusBlocked)
	input := UpdateBranchInput{Repository: l.repo, Branch: "main", Remote: "upstream"}
	preview := l.service.UpdateBranch(l.ctx(), input)
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.UpdateBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.oid(l.repo, "refs/remotes/upstream/main") != l.oid(l.repo, "main") {
		t.Fatal("upstream não atualizou main")
	}
	if before == l.oid(l.repo, "main") {
		t.Fatal("a atualização com o remoto informado não aconteceu")
	}
}

func TestBranchCheckedOutElsewhereBlocksBaseUpdate(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "branch", "base")
	l.push(l.repo, "base")
	wt := filepath.Join(l.root, "occupied")
	git(t, l.repo, "worktree", "add", "--", wt, "base")
	before := l.oid(l.repo, "base")
	got := l.service.UpdateBranch(l.ctx(), UpdateBranchInput{Repository: l.repo, Branch: "base", Remote: "origin"})
	requireStatus(t, got, StatusBlocked)
	if !strings.Contains(got.Message, wt) {
		t.Fatalf("mensagem não cita a worktree: %s", got.Message)
	}
	if l.oid(l.repo, "base") != before {
		t.Fatal("branch ocupada foi movida")
	}
}

func TestInvalidBranchName(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	got := l.service.UpdateBranch(l.ctx(), UpdateBranchInput{Repository: l.repo, Branch: "bad name", Remote: "origin"})
	requireStatus(t, got, StatusBlocked)
	dots := l.service.RebaseOnto(l.ctx(), RebaseInput{Repository: l.repo, Source: "main", Base: "foo..bar", Remote: "origin"})
	requireStatus(t, dots, StatusBlocked)
}

func TestRebaseOntoUpdatedBase(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.commit(l.repo, "feature.txt", "f\n", "feature")
	featureBefore := l.oid(l.repo, "feature")
	remote := l.advanceRemoteMain()
	input := RebaseInput{Repository: l.repo, Source: "feature", Base: "main", Remote: "origin"}
	preview := l.service.RebaseOnto(l.ctx(), input)
	if l.oid(l.repo, "feature") != featureBefore || l.oid(l.repo, "main") == remote {
		t.Fatal("prévia do rebase moveu branch")
	}
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.RebaseOnto(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.head(l.repo) != "feature" {
		t.Fatalf("head %s", l.head(l.repo))
	}
	if l.oid(l.repo, "main") != remote {
		t.Fatal("base não foi atualizada antes do rebase")
	}
	if !l.hasRef(l.repo, remote) {
		t.Fatal("commit remoto ausente")
	}
	ancestor, err := gitOutput(l.repo, "merge-base", "--is-ancestor", "main", "feature")
	if err != nil {
		t.Fatalf("feature não contém a base: %v %s", err, ancestor)
	}
	l.assertNoHomeWorktree()
}

func TestRebaseConflictStaysInProgress(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.switchTo(l.repo, "main")
	l.commit(l.repo, "README", "mainline\n", "mainline")
	l.push(l.repo, "main")
	l.switchTo(l.repo, "feature")
	l.commit(l.repo, "README", "feature\n", "feature")
	input := RebaseInput{Repository: l.repo, Source: "feature", Base: "main", Remote: "origin"}
	preview := l.service.RebaseOnto(l.ctx(), input)
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.RebaseOnto(l.ctx(), input)
	})
	requireStatus(t, done, StatusConflict)
	progress := l.service.Inspect(l.ctx(), InspectInput{Repository: l.repo})
	if progress.Details.InProgress != "rebase" {
		t.Fatalf("operação %q", progress.Details.InProgress)
	}
}

func TestRebaseRefusesLocalBaseCommitsAndDirtyTree(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	feature := l.commit(l.repo, "feature.txt", "f\n", "feature")
	l.switchTo(l.repo, "main")
	main := l.commit(l.repo, "README", "local-base\n", "local-base")
	input := RebaseInput{Repository: l.repo, Source: "feature", Base: "main", Remote: "origin"}
	blocked := l.service.RebaseOnto(l.ctx(), input)
	requireStatus(t, blocked, StatusBlocked)
	if l.oid(l.repo, "feature") != feature || l.oid(l.repo, "main") != main {
		t.Fatal("recusa reescreveu a base ou a feature")
	}
	git(t, l.repo, "reset", "--hard", "origin/main")
	writeFile(t, filepath.Join(l.repo, "README"), "dirty\n")
	dirty := l.service.RebaseOnto(l.ctx(), input)
	requireStatus(t, dirty, StatusBlocked)
	if l.oid(l.repo, "feature") != feature {
		t.Fatal("árvore suja rebaseou a feature")
	}
}

func TestDetachedHeadNeedsSource(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "--detach", "HEAD")
	got := l.service.RebaseOnto(l.ctx(), RebaseInput{Repository: l.repo, Base: "main", Remote: "origin"})
	requireStatus(t, got, StatusBlocked)
}

func TestReleaseBranchUpdatesBaseThenPointsRelease(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	remote := l.advanceRemoteMain()
	input := ReleaseBranchInput{Repository: l.repo, Base: "main", Remote: "origin", Name: "release/1", Publish: true}
	preview := l.service.CreateReleaseBranch(l.ctx(), input)
	if l.hasRef(l.repo, "refs/heads/release/1") || l.oid(l.repo, "main") == remote {
		t.Fatal("prévia criou a release ou atualizou a base")
	}
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.CreateReleaseBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.head(l.repo) != "release/1" {
		t.Fatalf("head %s", l.head(l.repo))
	}
	if l.oid(l.repo, "main") != remote || l.oid(l.repo, "release/1") != remote {
		t.Fatal("release não ficou na base atualizada")
	}
	if l.oid(l.repo, "refs/remotes/origin/release/1") != remote {
		t.Fatal("publish não enviou a release")
	}
}

func TestReleaseReplaceListsDroppedCommits(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	first := ReleaseBranchInput{Repository: l.repo, Base: "main", Remote: "origin", Name: "release/1"}
	created := executed(t, l.service.CreateReleaseBranch(l.ctx(), first), func(confirmation string) Result {
		first.Mode = ModeExecute
		first.Confirmation = confirmation
		return l.service.CreateReleaseBranch(l.ctx(), first)
	})
	requireStatus(t, created, StatusSuccess)
	extra := l.commit(l.repo, "extra.txt", "x\n", "exclusive")
	l.switchTo(l.repo, "main")
	without := l.service.CreateReleaseBranch(l.ctx(), ReleaseBranchInput{
		Repository: l.repo, Base: "main", Remote: "origin", Name: "release/1",
	})
	requireStatus(t, without, StatusBlocked)
	if len(without.Dropped) == 0 || l.oid(l.repo, "release/1") != extra {
		t.Fatalf("dropped %v oid %s", without.Dropped, l.oid(l.repo, "release/1"))
	}
	input := ReleaseBranchInput{
		Repository: l.repo, Base: "main", Remote: "origin", Name: "release/1", ReplaceExisting: true,
	}
	done := executed(t, l.service.CreateReleaseBranch(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.CreateReleaseBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	main := l.oid(l.repo, "main")
	if l.oid(l.repo, "release/1") != main || l.head(l.repo) != "release/1" {
		t.Fatal("replace não moveu a release para a base")
	}
	reflog := git(t, l.repo, "reflog", "show", "release/1")
	if !strings.Contains(reflog, extra[:7]) {
		t.Fatal("reflog não guarda a ponta anterior")
	}
}

func TestReleaseCheckedOutIsNotReplaced(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	input := ReleaseBranchInput{Repository: l.repo, Base: "main", Remote: "origin", Name: "release/1"}
	_ = executed(t, l.service.CreateReleaseBranch(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.CreateReleaseBranch(l.ctx(), input)
	})
	extra := l.commit(l.repo, "extra.txt", "x\n", "exclusive")
	replace := ReleaseBranchInput{
		Repository: l.repo, Base: "main", Remote: "origin", Name: "release/1", ReplaceExisting: true,
	}
	got := l.service.CreateReleaseBranch(l.ctx(), replace)
	requireStatus(t, got, StatusBlocked)
	if l.oid(l.repo, "release/1") != extra {
		t.Fatal("release em uso foi movida")
	}
}

func TestReleasePublishFailureKeepsLocalBranch(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.rejectPush()
	input := ReleaseBranchInput{Repository: l.repo, Base: "main", Remote: "origin", Name: "release/1", Publish: true}
	done := executed(t, l.service.CreateReleaseBranch(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.CreateReleaseBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusPartial)
	if l.head(l.repo) != "release/1" || !l.hasRef(l.repo, "refs/heads/release/1") {
		t.Fatal("falha de envio não preservou a release local")
	}
	if l.hasRef(l.other, "refs/remotes/origin/release/1") {
		t.Fatal("remoto recebeu a release rejeitada")
	}
	remote, err := gitOutput(l.origin, "rev-parse", "--verify", "--quiet", "refs/heads/release/1")
	if err == nil {
		t.Fatalf("ref remota existe: %s", remote)
	}
}

func TestReleaseFromTagDoesNotFastForwardBase(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	base := l.oid(l.repo, "main")
	git(t, l.repo, "tag", "v1", base)
	l.pushTag(l.repo, "v1")
	remoteMain := l.advanceRemoteMain()
	input := ReleaseTagInput{Repository: l.repo, Tag: "v1", Remote: "origin", Name: "release/v1"}
	preview := l.service.CreateReleaseFromTag(l.ctx(), input)
	requireStatus(t, preview, StatusPreview)
	if l.oid(l.repo, "main") != base || l.hasRef(l.repo, "refs/heads/release/v1") {
		t.Fatal("prévia da tag moveu branch")
	}
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.CreateReleaseFromTag(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.oid(l.repo, "main") != base {
		t.Fatal("fluxo de tag atualizou a base com pull --rebase")
	}
	if l.oid(l.other, "main") != remoteMain {
		t.Fatal("remoto main mudou além do fixture")
	}
	if l.head(l.repo) != "release/v1" || l.oid(l.repo, "release/v1") != base {
		t.Fatalf("head %s release %s", l.head(l.repo), l.oid(l.repo, "release/v1"))
	}
}

func TestTagPreviewDoesNotFetch(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.other, "tag", "v1", "HEAD")
	git(t, l.other, "push", "origin", "refs/tags/v1:refs/tags/v1")
	preview := l.service.CreateReleaseFromTag(l.ctx(), ReleaseTagInput{
		Repository: l.repo, Tag: "v1", Remote: "origin", Name: "release/v1",
	})
	requireStatus(t, preview, StatusPreview)
	if l.hasRef(l.repo, "refs/tags/v1") {
		t.Fatal("prévia buscou a tag")
	}
}

func TestTagFetchDoesNotForceLocalTag(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	local := l.oid(l.repo, "main")
	git(t, l.repo, "tag", "v1", local)
	moved := l.advanceRemoteMain()
	git(t, l.other, "tag", "--force", "v1", moved)
	git(t, l.other, "push", "--force", "origin", "refs/tags/v1:refs/tags/v1")
	input := ReleaseTagInput{Repository: l.repo, Tag: "v1", Remote: "origin", Name: "release/v1"}
	preview := l.service.CreateReleaseFromTag(l.ctx(), input)
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.CreateReleaseFromTag(l.ctx(), input)
	})
	requireStatus(t, done, StatusBlocked)
	if l.oid(l.repo, "refs/tags/v1") != local {
		t.Fatal("fetch forçou a tag local")
	}
	if l.hasRef(l.repo, "refs/heads/release/v1") {
		t.Fatal("release foi criada depois da tag divergente")
	}
	if l.oid(l.repo, "main") != local {
		t.Fatal("falha da tag moveu main")
	}
}

func (l *lab) pushTag(repo, tag string) {
	l.t.Helper()
	git(l.t, repo, "push", "origin", "refs/tags/"+tag+":refs/tags/"+tag)
}

func TestIntegratePreservesCheckout(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.commit(l.repo, "feature.txt", "f\n", "feature")
	feature := l.oid(l.repo, "feature")
	remoteMain := l.oid(l.repo, "refs/remotes/origin/main")
	input := IntegrateInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin"}
	preview := l.service.IntegrateBranch(l.ctx(), input)
	if strings.Contains(preview.Confirmation, l.service.WorktreeRoot) {
		t.Fatal("confirmação inclui o caminho da worktree")
	}
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.IntegrateBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.head(l.repo) != "feature" {
		t.Fatalf("checkout mudou para %s", l.head(l.repo))
	}
	if strings.Contains(l.worktreeList(), "integration") {
		t.Fatalf("worktree permaneceu: %s", l.worktreeList())
	}
	if _, err := gitOutput(l.repo, "merge-base", "--is-ancestor", feature, "main"); err != nil {
		t.Fatal("main local não contém a feature")
	}
	if l.oid(l.repo, "refs/remotes/origin/main") != remoteMain {
		t.Fatal("integração sem publish enviou a branch")
	}
	l.assertNoHomeWorktree()
}

func TestIntegrateConflictKeepsWorktree(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.switchTo(l.repo, "main")
	l.commit(l.repo, "README", "mainline\n", "mainline")
	l.push(l.repo, "main")
	l.switchTo(l.repo, "feature")
	l.commit(l.repo, "README", "feature\n", "feature")
	input := IntegrateInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin"}
	done := executed(t, l.service.IntegrateBranch(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.IntegrateBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusConflict)
	if l.head(l.repo) != "feature" {
		t.Fatalf("checkout mudou para %s", l.head(l.repo))
	}
	if done.Worktree == "" || !strings.HasPrefix(done.Worktree, l.service.WorktreeRoot) {
		t.Fatalf("worktree %s", done.Worktree)
	}
	progress := l.service.Inspect(l.ctx(), InspectInput{Repository: done.Worktree})
	if progress.Details == nil || progress.Details.InProgress != "merge" {
		t.Fatalf("worktree sem merge: %+v", progress.Details)
	}
	l.assertNoHomeWorktree()
}

func TestIntegratePushFailureKeepsWorktree(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.commit(l.repo, "feature.txt", "f\n", "feature")
	remoteMain := l.oid(l.origin, "refs/heads/main")
	l.rejectPush()
	input := IntegrateInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin", Publish: true}
	done := executed(t, l.service.IntegrateBranch(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.IntegrateBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusPartial)
	if done.Worktree == "" || l.head(l.repo) != "feature" {
		t.Fatalf("head %s worktree %s", l.head(l.repo), done.Worktree)
	}
	if l.oid(l.origin, "refs/heads/main") != remoteMain {
		t.Fatal("push rejeitado alterou o remoto")
	}
}

func TestIntegrateDoesNotInventMissingRemoteTarget(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "branch", "ghost")
	before := l.worktreeList()
	got := l.service.IntegrateBranch(l.ctx(), IntegrateInput{
		Repository: l.repo, Source: "main", Target: "ghost", Remote: "origin",
	})
	requireStatus(t, got, StatusBlocked)
	if !strings.Contains(got.Message, "não existe no remoto") {
		t.Fatalf("mensagem: %s", got.Message)
	}
	if l.worktreeList() != before {
		t.Fatal("destino ausente criou worktree")
	}
}

func TestIntegrateCreatesLocalTargetAtRemoteOID(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.commit(l.repo, "feature.txt", "f\n", "feature")
	remoteMain := l.oid(l.origin, "refs/heads/main")
	git(t, l.repo, "branch", "-D", "main")
	input := IntegrateInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin"}
	preview := l.service.IntegrateBranch(l.ctx(), input)
	if !strings.Contains(preview.Confirmation, "create_local=true") {
		t.Fatalf("token:\n%s", preview.Confirmation)
	}
	done := executed(t, preview, func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.IntegrateBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if !l.hasRef(l.repo, "refs/heads/main") || l.head(l.repo) != "feature" {
		t.Fatal("main local não foi criada ou o checkout mudou")
	}
	if _, err := gitOutput(l.repo, "merge-base", "--is-ancestor", remoteMain, "main"); err != nil {
		t.Fatal("main local não começou na ponta remota observada")
	}
}

func TestMergeIntoReturnsOnSuccessAndStaysOnConflict(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	feature := l.commit(l.repo, "feature.txt", "f\n", "feature")
	remoteMain := l.oid(l.origin, "refs/heads/main")
	input := MergeInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin"}
	done := executed(t, l.service.MergeInto(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.MergeInto(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.head(l.repo) != "feature" {
		t.Fatalf("head %s", l.head(l.repo))
	}
	if _, err := gitOutput(l.repo, "merge-base", "--is-ancestor", feature, "main"); err != nil {
		t.Fatal("merge não entrou em main")
	}
	if l.oid(l.origin, "refs/heads/main") != remoteMain {
		t.Fatal("merge sem publish alterou o remoto")
	}

	l.switchTo(l.repo, "main")
	l.commit(l.repo, "README", "mainline\n", "mainline")
	l.push(l.repo, "main")
	l.switchTo(l.repo, "feature")
	l.commit(l.repo, "README", "feature\n", "feature")
	conflictInput := MergeInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin"}
	conflict := executed(t, l.service.MergeInto(l.ctx(), conflictInput), func(confirmation string) Result {
		conflictInput.Mode = ModeExecute
		conflictInput.Confirmation = confirmation
		return l.service.MergeInto(l.ctx(), conflictInput)
	})
	requireStatus(t, conflict, StatusConflict)
	if l.head(l.repo) != "main" {
		t.Fatalf("conflito devolveu o checkout para %s", l.head(l.repo))
	}
	progress := l.service.Inspect(l.ctx(), InspectInput{Repository: l.repo})
	if progress.Details.InProgress != "merge" {
		t.Fatalf("merge %q", progress.Details.InProgress)
	}
}

func TestMergePushFailureStaysOnTarget(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.commit(l.repo, "feature.txt", "f\n", "feature")
	remoteMain := l.oid(l.origin, "refs/heads/main")
	l.rejectPush()
	input := MergeInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin", Publish: true}
	done := executed(t, l.service.MergeInto(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.MergeInto(l.ctx(), input)
	})
	requireStatus(t, done, StatusPartial)
	if l.head(l.repo) != "main" {
		t.Fatalf("head %s", l.head(l.repo))
	}
	if l.oid(l.origin, "refs/heads/main") != remoteMain {
		t.Fatal("envio rejeitado alterou o remoto")
	}
}

func TestMergeRequiresLocalTarget(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	git(t, l.repo, "branch", "-D", "main")
	got := l.service.MergeInto(l.ctx(), MergeInput{
		Repository: l.repo, Source: "feature", Target: "main", Remote: "origin",
	})
	requireStatus(t, got, StatusBlocked)
	if l.head(l.repo) != "feature" {
		t.Fatal("recusa trocou o checkout")
	}
}

func TestResetRequiresMatchingConfirmation(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	feature := l.commit(l.repo, "extra.txt", "x\n", "extra")
	remote := l.advanceRemoteMain()
	mainBefore := l.oid(l.repo, "main")
	input := ResetInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin"}
	preview := l.service.ResetAndPush(l.ctx(), input)
	requireStatus(t, preview, StatusPreview)
	input.Mode = ModeExecute
	input.Confirmation = preview.Confirmation + "\nextra=1"
	refused := l.service.ResetAndPush(l.ctx(), input)
	requireStatus(t, refused, StatusBlocked)
	if l.oid(l.repo, "main") != mainBefore || l.head(l.repo) != "feature" {
		t.Fatal("confirmação diferente substituiu a branch")
	}
	if l.oid(l.origin, "refs/heads/main") != remote {
		t.Fatal("fixture remoto inesperado")
	}
	l.advanceRemoteMain()
	done := executed(t, preview, func(confirmation string) Result {
		fresh := ResetInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin", Mode: ModeExecute, Confirmation: confirmation}
		return l.service.ResetAndPush(l.ctx(), fresh)
	})
	requireStatus(t, done, StatusBlocked)
	if l.oid(l.repo, "main") != mainBefore || l.oid(l.repo, "feature") != feature {
		t.Fatal("remoto mudado entre prévia e execução ainda assim reescreveu")
	}
	if l.head(l.repo) != "feature" {
		t.Fatalf("head %s", l.head(l.repo))
	}
}

func TestResetAndPushThenRestoreCheckout(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	feature := l.commit(l.repo, "extra.txt", "x\n", "extra")
	l.advanceRemoteMain()
	git(t, l.repo, "fetch", "origin", "main")
	input := ResetInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin"}
	done := executed(t, l.service.ResetAndPush(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.ResetAndPush(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.head(l.repo) != "feature" {
		t.Fatalf("head %s", l.head(l.repo))
	}
	if l.oid(l.repo, "main") != feature || l.oid(l.origin, "refs/heads/main") != feature {
		t.Fatalf("local %s remoto %s feature %s", l.oid(l.repo, "main"), l.oid(l.origin, "refs/heads/main"), feature)
	}
}

func TestResetPushRejectionIsPartial(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	feature := l.commit(l.repo, "extra.txt", "x\n", "extra")
	remote := l.oid(l.origin, "refs/heads/main")
	l.rejectPush()
	input := ResetInput{Repository: l.repo, Source: "feature", Target: "main", Remote: "origin"}
	done := executed(t, l.service.ResetAndPush(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.ResetAndPush(l.ctx(), input)
	})
	requireStatus(t, done, StatusPartial)
	if l.head(l.repo) != "main" || l.oid(l.repo, "main") != feature {
		t.Fatalf("head %s main %s", l.head(l.repo), l.oid(l.repo, "main"))
	}
	if l.oid(l.origin, "refs/heads/main") != remote {
		t.Fatal("lease rejeitado alterou o remoto")
	}
}

func TestResetDirtyDoesNothing(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	before := l.oid(l.repo, "main")
	writeFile(t, filepath.Join(l.repo, "README"), "dirty\n")
	got := l.service.ResetAndPush(l.ctx(), ResetInput{Repository: l.repo, Source: "main", Target: "main", Remote: "origin"})
	requireStatus(t, got, StatusBlocked)
	if l.oid(l.repo, "main") != before {
		t.Fatal("árvore suja foi reescrita")
	}
}

func TestDeleteUnmergedRefusesBeforeSwitch(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	feature := l.commit(l.repo, "feature.txt", "f\n", "feature")
	got := l.service.DeleteCurrentBranch(l.ctx(), DeleteInput{Repository: l.repo, Branch: "feature", Target: "main"})
	requireStatus(t, got, StatusBlocked)
	if l.head(l.repo) != "feature" || l.oid(l.repo, "feature") != feature {
		t.Fatal("branch não mesclada mudou o checkout ou a ref")
	}
}

func TestDeleteMergedBranch(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.commit(l.repo, "feature.txt", "f\n", "feature")
	l.switchTo(l.repo, "main")
	git(t, l.repo, "merge", "--ff", "--no-edit", "feature")
	l.switchTo(l.repo, "feature")
	input := DeleteInput{Repository: l.repo, Branch: "feature", Target: "main"}
	done := executed(t, l.service.DeleteCurrentBranch(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.DeleteCurrentBranch(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.head(l.repo) != "main" || l.hasRef(l.repo, "refs/heads/feature") {
		t.Fatalf("head %s feature existe %v", l.head(l.repo), l.hasRef(l.repo, "refs/heads/feature"))
	}
}

func TestCleanDeletesOnlyAuthorizedMergedBranches(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "branch", "old")
	git(t, l.repo, "branch", "topic")
	l.switchTo(l.repo, "topic")
	l.commit(l.repo, "topic.txt", "t\n", "topic")
	l.switchTo(l.repo, "main")
	writeFile(t, filepath.Join(l.repo, "note.txt"), "dirty\n")
	empty := l.service.CleanMergedBranches(l.ctx(), CleanInput{Repository: l.repo})
	requireStatus(t, empty, StatusPreview)
	if !containsName(empty.Branches, "old") || containsName(empty.Branches, "main") || containsName(empty.Branches, "topic") {
		t.Fatalf("candidatos %v", empty.Branches)
	}
	if !l.hasRef(l.repo, "refs/heads/old") {
		t.Fatal("prévia apagou old")
	}
	emptyExec := l.service.CleanMergedBranches(l.ctx(), CleanInput{
		Repository: l.repo, Mode: ModeExecute, Confirmation: empty.Confirmation,
	})
	requireStatus(t, emptyExec, StatusBlocked)
	if !l.hasRef(l.repo, "refs/heads/old") {
		t.Fatal("lista vazia apagou old")
	}
	mixed := l.service.CleanMergedBranches(l.ctx(), CleanInput{Repository: l.repo, Branches: []string{"old", "topic"}})
	requireStatus(t, mixed, StatusBlocked)
	if !l.hasRef(l.repo, "refs/heads/old") {
		t.Fatal("nome inelegível apagou outra branch")
	}
	current := l.service.CleanMergedBranches(l.ctx(), CleanInput{Repository: l.repo, Branches: []string{"main"}})
	requireStatus(t, current, StatusBlocked)
	input := CleanInput{Repository: l.repo, Branches: []string{"old"}}
	done := executed(t, l.service.CleanMergedBranches(l.ctx(), input), func(confirmation string) Result {
		input.Mode = ModeExecute
		input.Confirmation = confirmation
		return l.service.CleanMergedBranches(l.ctx(), input)
	})
	requireStatus(t, done, StatusSuccess)
	if l.hasRef(l.repo, "refs/heads/old") || !l.hasRef(l.repo, "refs/heads/topic") || !l.hasRef(l.repo, "refs/heads/main") {
		t.Fatal("limpeza não ficou restrita à lista autorizada")
	}
	if _, err := os.Stat(filepath.Join(l.repo, "note.txt")); err != nil {
		t.Fatal("limpeza alterou a árvore suja")
	}
}

func TestCleanProtectsWorktreeBranch(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "branch", "old")
	wt := filepath.Join(l.root, "old-wt")
	git(t, l.repo, "worktree", "add", "--", wt, "old")
	got := l.service.CleanMergedBranches(l.ctx(), CleanInput{Repository: l.repo, Branches: []string{"old"}})
	requireStatus(t, got, StatusBlocked)
	if !l.hasRef(l.repo, "refs/heads/old") {
		t.Fatal("branch de worktree foi removida")
	}
}

func TestCleanRejectsInvalidAndDuplicateNames(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	invalid := l.service.CleanMergedBranches(l.ctx(), CleanInput{Repository: l.repo, Branches: []string{"bad name"}})
	requireStatus(t, invalid, StatusBlocked)
	dup := l.service.CleanMergedBranches(l.ctx(), CleanInput{Repository: l.repo, Branches: []string{"old", "old"}})
	requireStatus(t, dup, StatusBlocked)
}

func TestInProgressOperationBlocksClean(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	git(t, l.repo, "switch", "-c", "feature")
	l.commit(l.repo, "README", "feature\n", "feature")
	l.switchTo(l.repo, "main")
	l.commit(l.repo, "README", "mainline\n", "mainline")
	if _, err := gitOutput(l.repo, "merge", "--no-edit", "feature"); err == nil {
		t.Fatal("o merge de teste deveria conflitar")
	}
	got := l.service.CleanMergedBranches(l.ctx(), CleanInput{Repository: l.repo})
	requireStatus(t, got, StatusBlocked)
}
