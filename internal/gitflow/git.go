package gitflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Service runs Git flows. It does not speak MCP.
type Service struct {
	Git          string
	WorktreeRoot string
	Logger       *log.Logger
}

// New returns a service that uses the git executable on PATH
// and stores temporary worktrees under ~/git-worktrees.
func New(logger *log.Logger) Service {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	root := ""
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		root = filepath.Join(home, "git-worktrees")
	}
	return Service{Git: "git", WorktreeRoot: root, Logger: logger}
}

// Repository is one Git work tree.
type Repository struct {
	Root string
	Git  string
}

type gitFailure struct {
	Args   []string
	Stderr string
	Code   int
}

func (e *gitFailure) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("git saiu com código %d", e.Code)
	}
	return redact(msg)
}

var userinfo = regexp.MustCompile(`://[^\s/@]+(?::[^\s/@]*)?@`)

func redact(s string) string {
	s = userinfo.ReplaceAllString(s, "://***@")
	if len(s) > 2000 {
		return s[:2000] + "…"
	}
	return s
}

func (s Service) gitBinary() string {
	if s.Git == "" {
		return "git"
	}
	return s.Git
}

func (r Repository) at(path string) Repository {
	return Repository{Root: path, Git: r.Git}
}

func (r Repository) run(ctx context.Context, args ...string) (string, error) {
	full := make([]string, 0, len(args)+6)
	full = append(full,
		"-c", "rebase.autoStash=false",
		"-c", "rebase.autosquash=false",
		"-c", "commit.gpgsign=false",
	)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, r.Git, full...)
	cmd.Dir = r.Root
	cmd.Env = gitEnviron()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.String(), &gitFailure{
			Args:   args,
			Stderr: stderr.String(),
			Code:   exit.ExitCode(),
		}
	}
	return stdout.String(), err
}

func gitEnviron() []string {
	skip := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
		"GIT_PREFIX": true, "GIT_COMMON_DIR": true, "GIT_OBJECT_DIRECTORY": true,
		"GIT_NAMESPACE": true, "GIT_CONFIG_PARAMETERS": true,
		"GIT_EDITOR": true, "GIT_SEQUENCE_EDITOR": true, "GIT_TERMINAL_PROMPT": true,
	}
	out := make([]string, 0, 32)
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found || skip[key] {
			continue
		}
		out = append(out, entry)
	}
	out = append(out,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_EDITOR=false",
		"GIT_SEQUENCE_EDITOR=false",
	)
	return out
}

func missingRef(err error) bool {
	var failure *gitFailure
	return errors.As(err, &failure) && failure.Code == 1
}

func (s Service) open(ctx context.Context, path string) (Repository, error) {
	if strings.TrimSpace(path) == "" {
		return Repository{}, errors.New("o caminho do repositório é obrigatório")
	}
	if strings.ContainsAny(path, "\x00\n") {
		return Repository{}, errors.New("o caminho do repositório é inválido")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Repository{}, errors.New("não foi possível resolver o caminho do repositório")
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return Repository{}, errors.New("o caminho não é um diretório")
	}
	repo := Repository{Root: abs, Git: s.gitBinary()}
	inside, err := repo.run(ctx, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return Repository{}, errors.New("o caminho não está dentro de um work tree Git")
	}
	top, err := repo.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return Repository{}, errors.New("não foi possível identificar a raiz do work tree")
	}
	repo.Root = filepath.Clean(strings.TrimSpace(top))
	return repo, nil
}

func (s Service) openResult(ctx context.Context, op, path string) (Repository, *Result) {
	repo, err := s.open(ctx, path)
	if err != nil {
		result := blocked(op, path, err.Error())
		return Repository{}, &result
	}
	return repo, nil
}

func validateBranchName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t\n\x00~^:?*[\\") {
		return errors.New("nome de branch inválido")
	}
	if strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, "/") {
		return errors.New("nome de branch inválido")
	}
	return nil
}

func (r Repository) checkBranch(ctx context.Context, name string) error {
	if err := validateBranchName(name); err != nil {
		return err
	}
	_, err := r.run(ctx, "check-ref-format", "--branch", name)
	if err != nil {
		return errors.New("nome de branch inválido")
	}
	return nil
}

func validateRemoteName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") {
		return errors.New("nome de remoto inválido")
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return errors.New("nome de remoto inválido")
	}
	return nil
}

func (r Repository) checkRemote(ctx context.Context, name string) error {
	if err := validateRemoteName(name); err != nil {
		return err
	}
	_, err := r.run(ctx, "remote", "get-url", "--", name)
	if err != nil {
		return fmt.Errorf("o remoto %s não está configurado neste repositório", name)
	}
	return nil
}

func validateTagName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t\n\x00~^:?*[\\") {
		return errors.New("nome de tag inválido")
	}
	if strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.HasSuffix(name, ".lock") {
		return errors.New("nome de tag inválido")
	}
	return nil
}

func (r Repository) checkTag(ctx context.Context, name string) error {
	if err := validateTagName(name); err != nil {
		return err
	}
	_, err := r.run(ctx, "check-ref-format", "refs/tags/"+name)
	if err != nil {
		return errors.New("nome de tag inválido")
	}
	return nil
}

func (r Repository) head(ctx context.Context) (string, string, bool, error) {
	branch, branchErr := r.run(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	oid, oidErr := r.run(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	if oidErr != nil {
		if missingRef(oidErr) {
			if branchErr == nil {
				return strings.TrimSpace(branch), "", false, nil
			}
			return "", "", false, errors.New("o repositório ainda não tem commits")
		}
		return "", "", false, oidErr
	}
	if branchErr != nil {
		if missingRef(branchErr) {
			return "", strings.TrimSpace(oid), true, nil
		}
		return "", "", false, branchErr
	}
	return strings.TrimSpace(branch), strings.TrimSpace(oid), false, nil
}

func (r Repository) branchOID(ctx context.Context, name string) (string, bool, error) {
	out, err := r.run(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	if err != nil {
		if missingRef(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(out), true, nil
}

func (r Repository) remoteBranchOID(ctx context.Context, remote, branch string) (string, error) {
	if err := r.checkRemote(ctx, remote); err != nil {
		return "", err
	}
	ref := "refs/heads/" + branch
	out, err := r.run(ctx, "ls-remote", "--heads", remote, ref)
	if err != nil {
		return "", fmt.Errorf("não foi possível ler a branch %s no remoto %s: %s", branch, remote, err.Error())
	}
	oid, found := findRef(out, ref)
	if !found {
		return "", fmt.Errorf("a branch %s não existe no remoto %s", branch, remote)
	}
	return oid, nil
}

func (r Repository) remoteTagOID(ctx context.Context, remote, tag string) (string, error) {
	if err := r.checkRemote(ctx, remote); err != nil {
		return "", err
	}
	out, err := r.run(ctx, "ls-remote", "--tags", remote, "refs/tags/"+tag)
	if err != nil {
		return "", fmt.Errorf("não foi possível ler a tag %s no remoto %s: %s", tag, remote, err.Error())
	}
	peeled, found := findRef(out, "refs/tags/"+tag+"^{}")
	if found {
		return peeled, nil
	}
	oid, found := findRef(out, "refs/tags/"+tag)
	if !found {
		return "", fmt.Errorf("a tag %s não existe no remoto %s", tag, remote)
	}
	return oid, nil
}

func findRef(output, ref string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		oid, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if name == ref {
			return oid, true
		}
	}
	return "", false
}

func (r Repository) hasCommit(ctx context.Context, oid string) bool {
	if oid == "" {
		return false
	}
	_, err := r.run(ctx, "cat-file", "-e", oid+"^{commit}")
	return err == nil
}

// fetchBranchObjects downloads the remote branch so ahead/behind can be counted.
// The objects land under refs/git-mcp/observe and that ref is removed before return.
// heads, tags, and remote-tracking refs stay where they were.
func (r Repository) fetchBranchObjects(ctx context.Context, remote, branch string) error {
	dest := "refs/git-mcp/observe/" + branch
	spec := "+refs/heads/" + branch + ":" + dest
	if _, err := r.run(ctx, "fetch", "--no-tags", "--no-write-fetch-head", "--refmap=", remote, spec); err != nil {
		return fmt.Errorf("não foi possível obter os commits de %s em %s: %s", branch, remote, err.Error())
	}
	if _, err := r.run(ctx, "update-ref", "-d", dest); err != nil {
		return err
	}
	return nil
}

func (r Repository) counts(ctx context.Context, localOID, remoteOID string) (int, int, error) {
	aheadText, err := r.run(ctx, "rev-list", "--count", remoteOID+".."+localOID)
	if err != nil {
		return 0, 0, err
	}
	behindText, err := r.run(ctx, "rev-list", "--count", localOID+".."+remoteOID)
	if err != nil {
		return 0, 0, err
	}
	ahead, err := strconv.Atoi(strings.TrimSpace(aheadText))
	if err != nil {
		return 0, 0, err
	}
	behind, err := strconv.Atoi(strings.TrimSpace(behindText))
	if err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

func relationOf(ahead, behind int) (string, string) {
	if ahead > 0 && behind > 0 {
		return "diverged", "a base local divergiu do remoto; git pull --rebase reescreveria os commits locais"
	}
	if ahead > 0 {
		return "ahead", "a base local tem commits que o remoto não tem; git pull --rebase reescreveria esses commits"
	}
	if behind > 0 {
		return "behind", ""
	}
	return "equal", ""
}

func (r Repository) commitsBetween(ctx context.Context, fromOID, toOID string) ([]string, error) {
	if fromOID == "" || toOID == "" || fromOID == toOID {
		return nil, nil
	}
	out, err := r.run(ctx, "log", "--format=%h %s", "-n", "20", fromOID+".."+toOID)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func (r Repository) gitPath(ctx context.Context, name string) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(out)
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.Root, path)
	}
	return path, nil
}

func (r Repository) inProgress(ctx context.Context) (string, error) {
	checks := []struct {
		kind string
		path string
	}{
		{kind: "rebase", path: "rebase-merge"},
		{kind: "rebase", path: "rebase-apply"},
		{kind: "merge", path: "MERGE_HEAD"},
		{kind: "cherry-pick", path: "CHERRY_PICK_HEAD"},
		{kind: "revert", path: "REVERT_HEAD"},
		{kind: "bisect", path: "BISECT_LOG"},
	}
	for _, check := range checks {
		path, err := r.gitPath(ctx, check.path)
		if err != nil {
			return "", err
		}
		_, statErr := os.Stat(path)
		if statErr == nil {
			return check.kind, nil
		}
	}
	return "", nil
}

func (r Repository) changes(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) == 20 {
			lines = append(lines, "…")
			break
		}
	}
	return lines, nil
}

func (r Repository) worktrees(ctx context.Context) ([]WorktreeInfo, error) {
	out, err := r.run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var items []WorktreeInfo
	var current *WorktreeInfo
	flush := func() {
		if current != nil {
			items = append(items, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &WorktreeInfo{Path: filepath.Clean(strings.TrimPrefix(line, "worktree "))}
		case line == "detached":
			if current != nil {
				current.Detached = true
			}
		case strings.HasPrefix(line, "branch "):
			if current != nil {
				current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			}
		case strings.HasPrefix(line, "HEAD "):
			if current != nil {
				current.Head = strings.TrimPrefix(line, "HEAD ")
			}
		}
	}
	flush()
	return items, nil
}

func (r Repository) worktreeOf(ctx context.Context, branch string) (*WorktreeInfo, error) {
	items, err := r.worktrees(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Branch == branch {
			copy := item
			return &copy, nil
		}
	}
	return nil, nil
}

func (r Repository) pullRebase(ctx context.Context, remote, branch string) error {
	_, err := r.run(ctx, "pull", "--rebase", remote, branch)
	return err
}

func (r Repository) switchTo(ctx context.Context, branch string) error {
	_, err := r.run(ctx, "switch", "--", branch)
	return err
}

func (r Repository) switchNew(ctx context.Context, branch, start string) error {
	_, err := r.run(ctx, "switch", "-c", branch, "--", start)
	return err
}

func (r Repository) forceBranch(ctx context.Context, branch, start string) error {
	_, err := r.run(ctx, "branch", "-f", "--", branch, start)
	return err
}

func (r Repository) createBranch(ctx context.Context, branch, start string) error {
	_, err := r.run(ctx, "branch", "--", branch, start)
	return err
}

func (r Repository) deleteMerged(ctx context.Context, branch string) error {
	_, err := r.run(ctx, "branch", "-d", "--", branch)
	return err
}

func (r Repository) mergeOID(ctx context.Context, oid string) error {
	_, err := r.run(ctx, "merge", "--ff", "--no-edit", "--", oid)
	return err
}

func (r Repository) rebaseOnto(ctx context.Context, upstream string) error {
	_, err := r.run(ctx, "rebase", upstream)
	return err
}

func (r Repository) pushRef(ctx context.Context, remote, branch string) error {
	ref := "refs/heads/" + branch + ":refs/heads/" + branch
	_, err := r.run(ctx, "push", remote, ref)
	return err
}

func (r Repository) pushLease(ctx context.Context, remote, branch, expected string) error {
	lease := "refs/heads/" + branch + ":" + expected
	dest := "HEAD:refs/heads/" + branch
	_, err := r.run(ctx, "push", "--force-with-lease="+lease, remote, dest)
	return err
}

func (r Repository) fetchTag(ctx context.Context, remote, tag string) error {
	ref := "refs/tags/" + tag + ":refs/tags/" + tag
	_, err := r.run(ctx, "fetch", remote, ref)
	return err
}

func (r Repository) tagCommit(ctx context.Context, tag string) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--verify", "--quiet", "refs/tags/"+tag+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (r Repository) ancestor(ctx context.Context, commit, ancestorOf string) (bool, error) {
	_, err := r.run(ctx, "merge-base", "--is-ancestor", commit, ancestorOf)
	if err != nil {
		if missingRef(err) {
			return false, nil
		}
		var failure *gitFailure
		if errors.As(err, &failure) && failure.Code == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r Repository) mergedBranches(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "for-each-ref", "--merged=HEAD", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

func (s Service) addWorktree(ctx context.Context, repo Repository, branch, prefix string) (string, error) {
	if s.WorktreeRoot == "" {
		return "", errors.New("o diretório de worktrees não está configurado")
	}
	parent := filepath.Join(s.WorktreeRoot, repoDirName(repo.Root))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(parent, prefix+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	_, err := repo.run(ctx, "worktree", "add", "--", path, branch)
	if err != nil {
		return "", err
	}
	return path, nil
}

func (r Repository) removeWorktree(ctx context.Context, path string) error {
	_, err := r.run(ctx, "worktree", "remove", "--", path)
	return err
}

func repoDirName(root string) string {
	base := filepath.Base(root)
	var b strings.Builder
	for _, char := range base {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			b.WriteRune(char)
			continue
		}
		b.WriteByte('-')
	}
	if b.Len() == 0 {
		return "repository"
	}
	return b.String()
}

func blocked(op, repository, message string) Result {
	return Result{Status: StatusBlocked, Operation: op, Repository: repository, Message: message}
}

func token(lines ...string) string {
	return strings.Join(lines, "\n")
}

func flag(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func gate(mode, given, expected string, preview Result) (Result, bool) {
	if mode == "" || mode == ModePreview {
		preview.Status = StatusPreview
		preview.Confirmation = expected
		return preview, false
	}
	if mode != ModeExecute {
		preview.Status = StatusBlocked
		preview.Message = "mode deve ser preview ou execute."
		preview.Confirmation = ""
		return preview, false
	}
	if given != expected {
		preview.Status = StatusBlocked
		preview.Confirmation = expected
		preview.Message = "A confirmação não corresponde ao estado atual. Nada foi alterado. Gere outra prévia."
		return preview, false
	}
	return Result{}, true
}

func (r Repository) headLabel(ctx context.Context) string {
	branch, oid, detached, err := r.head(ctx)
	if err != nil {
		return ""
	}
	if detached {
		if len(oid) > 12 {
			return "detached@" + oid[:12]
		}
		return "detached@" + oid
	}
	return branch
}

func containsName(names []string, name string) bool {
	for _, item := range names {
		if item == name {
			return true
		}
	}
	return false
}
