package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git-mcp/internal/gitflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPListsToolsAndRunsUpdate(t *testing.T) {
	repo, origin := testRepository(t)
	var logs strings.Builder
	logger := log.New(&logs, "", 0)
	svc := gitflow.New(logger)
	svc.WorktreeRoot = t.TempDir()
	mcpServer := New(svc, logger)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Fatalf("ferramenta %s sem descrição", tool.Name)
		}
	}
	slices.Sort(names)
	want := []string{
		"clean_merged_branches",
		"create_release_branch",
		"create_release_from_tag",
		"delete_current_branch",
		"integrate_branch",
		"merge_into",
		"rebase_onto",
		"repository_inspect",
		"reset_and_push",
		"update_branch",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("ferramentas %v", names)
	}

	blocked, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "repository_inspect",
		Arguments: map[string]any{"repository": filepath.Join(t.TempDir(), "ausente")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !blocked.IsError {
		t.Fatal("caminho inválido deveria ser erro de ferramenta")
	}

	before := rev(t, repo, "main")
	remote := commitAndPush(t, origin, repo)
	inspected, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "repository_inspect",
		Arguments: map[string]any{
			"repository": repo,
			"base":       "main",
			"remote":     "origin",
			"fetch":      true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inspected.IsError {
		t.Fatal("inspeção falhou")
	}
	if rev(t, repo, "main") != before {
		t.Fatal("inspeção moveu a branch local")
	}

	previewCall, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "update_branch",
		Arguments: map[string]any{"repository": repo, "branch": "main", "remote": "origin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if previewCall.IsError {
		t.Fatal("prévia não é erro")
	}
	preview := decodeResult(t, previewCall)
	if preview.Status != gitflow.StatusPreview || preview.Confirmation == "" {
		t.Fatalf("prévia %+v", preview)
	}
	if rev(t, repo, "main") != before {
		t.Fatal("prévia moveu a branch")
	}

	updated, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "update_branch",
		Arguments: map[string]any{
			"repository":   repo,
			"branch":       "main",
			"remote":       "origin",
			"mode":         "execute",
			"confirmation": preview.Confirmation,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.IsError {
		t.Fatalf("execução: %s", textOf(updated))
	}
	done := decodeResult(t, updated)
	if done.Status != gitflow.StatusSuccess || rev(t, repo, "main") != remote {
		t.Fatalf("status %s oid %s remoto %s", done.Status, rev(t, repo, "main"), remote)
	}
	if !strings.Contains(logs.String(), "repository_inspect success") || !strings.Contains(logs.String(), "update_branch success") {
		t.Fatalf("log: %s", logs.String())
	}
}

func decodeResult(t *testing.T, result *mcp.CallToolResult) gitflow.Result {
	t.Helper()
	body, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out gitflow.Result
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status == "" {
		t.Fatalf("resultado vazio: %s", textOf(result))
	}
	return out
}

func textOf(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func testRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "repo")
	git(t, root, "init", "--bare", "-b", "main", origin)
	git(t, root, "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test")
	git(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "README")
	git(t, repo, "commit", "-m", "init")
	git(t, repo, "remote", "add", "origin", origin)
	git(t, repo, "push", "-u", "origin", "main")
	return repo, origin
}

func commitAndPush(t *testing.T, origin, repo string) string {
	t.Helper()
	other := filepath.Join(filepath.Dir(repo), "other")
	git(t, filepath.Dir(repo), "clone", "-o", "origin", origin, other)
	git(t, other, "config", "user.name", "Test")
	git(t, other, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(other, "README"), []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, other, "add", "README")
	git(t, other, "commit", "-m", "remote")
	git(t, other, "push", "origin", "main")
	return rev(t, other, "HEAD")
}

func rev(t *testing.T, dir, ref string) string {
	t.Helper()
	return strings.TrimSpace(git(t, dir, "rev-parse", ref))
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
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
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
