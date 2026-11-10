package gitflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type lab struct {
	t       *testing.T
	root    string
	origin  string
	repo    string
	other   string
	service Service
}

func newLab(t *testing.T) *lab {
	t.Helper()
	root := t.TempDir()
	l := &lab{
		t:      t,
		root:   root,
		origin: filepath.Join(root, "origin.git"),
		repo:   filepath.Join(root, "repo"),
		other:  filepath.Join(root, "other"),
	}
	git(t, root, "init", "--bare", "-b", "main", l.origin)
	git(t, l.origin, "config", "core.hooksPath", filepath.Join(l.origin, "hooks"))
	git(t, l.origin, "config", "receive.denyCurrentBranch", "ignore")
	git(t, l.origin, "config", "receive.denyNonFastForwards", "false")
	git(t, root, "init", "-b", "main", l.repo)
	configure(t, l.repo)
	writeFile(t, filepath.Join(l.repo, "README"), "one\n")
	git(t, l.repo, "add", "README")
	git(t, l.repo, "commit", "-m", "init")
	git(t, l.repo, "remote", "add", "origin", l.origin)
	git(t, l.repo, "push", "-u", "origin", "main")
	git(t, root, "clone", "-o", "origin", l.origin, l.other)
	configure(t, l.other)
	l.service = New(nil)
	l.service.WorktreeRoot = filepath.Join(root, "worktrees")
	return l
}

func configure(t *testing.T, repo string) {
	t.Helper()
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.com")
	git(t, repo, "config", "commit.gpgsign", "false")
	git(t, repo, "config", "core.hooksPath", filepath.Join(repo, ".git", "hooks"))
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (l *lab) ctx() context.Context {
	return context.Background()
}

func (l *lab) commit(repo, name, content, message string) string {
	l.t.Helper()
	writeFile(l.t, filepath.Join(repo, name), content)
	git(l.t, repo, "add", "--", name)
	git(l.t, repo, "commit", "-m", message)
	return l.oid(repo, "HEAD")
}

func (l *lab) oid(repo, ref string) string {
	l.t.Helper()
	return strings.TrimSpace(git(l.t, repo, "rev-parse", ref))
}

func (l *lab) hasRef(repo, ref string) bool {
	l.t.Helper()
	_, err := gitOutput(repo, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

func (l *lab) head(repo string) string {
	l.t.Helper()
	return strings.TrimSpace(git(l.t, repo, "symbolic-ref", "--short", "HEAD"))
}

func (l *lab) switchTo(repo, branch string) {
	l.t.Helper()
	git(l.t, repo, "switch", "--", branch)
}

func (l *lab) push(repo, branch string) {
	l.t.Helper()
	git(l.t, repo, "push", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
}

func (l *lab) advanceRemoteMain() string {
	l.t.Helper()
	content := "remote-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "\n"
	oid := l.commit(l.other, "README", content, "remote")
	l.push(l.other, "main")
	return oid
}

func (l *lab) rejectPush() {
	l.t.Helper()
	path := filepath.Join(l.origin, "hooks", "pre-receive")
	script := []byte("#!/bin/sh\nexit 1\n")
	if err := os.WriteFile(path, script, 0o755); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) worktreeList() string {
	l.t.Helper()
	return git(l.t, l.repo, "worktree", "list")
}

func (l *lab) assertNoHomeWorktree() {
	l.t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	if strings.Contains(l.worktreeList(), filepath.Join(home, "git-worktrees")) {
		l.t.Fatalf("worktree criada em casa: %s", l.worktreeList())
	}
}

func executed(t *testing.T, preview Result, run func(string) Result) Result {
	t.Helper()
	if preview.Status != StatusPreview {
		t.Fatalf("status %s, mensagem %s, motivos %v", preview.Status, preview.Message, preview.Reasons)
	}
	if strings.TrimSpace(preview.Confirmation) == "" {
		t.Fatal("prévia sem confirmação")
	}
	return run(preview.Confirmation)
}

func requireStatus(t *testing.T, got Result, status Status) {
	t.Helper()
	if got.Status != status {
		t.Fatalf("status %s, want %s; mensagem %s; motivos %v", got.Status, status, got.Message, got.Reasons)
	}
}
