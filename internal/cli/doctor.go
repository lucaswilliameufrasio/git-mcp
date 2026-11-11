package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"git-mcp/internal/audit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// doctor reads the environment the flows depend on. The only thing it can
// create is the audit directory, through the server it starts for the handshake.
// Client configuration is informative: a missing client is not a problem.
func (a App) doctor() error {
	healthy := true
	report := func(ok bool, format string, values ...any) {
		mark := "OK   "
		if !ok {
			mark = "FALHA"
			healthy = false
		}
		fmt.Fprintf(a.Stdout, "  %s  %s\n", mark, fmt.Sprintf(format, values...))
	}

	fmt.Fprintln(a.Stdout, "Git")
	if path, err := exec.LookPath("git"); err != nil {
		report(false, "git não está no PATH")
	} else if out, err := exec.Command(path, "--version").Output(); err != nil {
		report(false, "%s --version: %v", path, err)
	} else {
		report(true, "%s (%s)", strings.TrimSpace(string(out)), path)
	}

	fmt.Fprintln(a.Stdout, "\nWorktrees")
	root := filepath.Join(a.Home, "git-worktrees")
	switch info, err := os.Stat(root); {
	case os.IsNotExist(err):
		report(true, "%s ainda não existe; é criado na primeira integração", root)
	case err != nil:
		report(false, "%s: %v", root, err)
	case !info.IsDir():
		report(false, "%s existe e não é um diretório", root)
	default:
		report(true, "%s", root)
	}

	fmt.Fprintln(a.Stdout, "\nAuditoria")
	if dir, err := audit.DefaultDirectory(); err != nil {
		report(false, "%v", err)
	} else if _, err := os.Lstat(dir); os.IsNotExist(err) {
		report(true, "%s ainda não existe; é criado quando o servidor inicia", dir)
	} else if count, err := countEvents(dir); err != nil {
		report(false, "%s: %v", dir, err)
	} else {
		report(true, "%s (%d chamadas na amostra)", dir, count)
	}

	fmt.Fprintln(a.Stdout, "\nMCP stdio")
	if version, tools, err := a.handshake(); err != nil {
		report(false, "%v", err)
	} else {
		report(true, "handshake com git-mcp %s; %d ferramentas", version, tools)
	}

	fmt.Fprintln(a.Stdout, "\nClientes MCP (configuração do usuário)")
	for _, client := range detectClients(a.Home) {
		switch {
		case !client.Present:
			fmt.Fprintf(a.Stdout, "  %-16s %-12s %s\n", "sem o cliente", client.Name, client.Path)
		case client.Unreadable:
			fmt.Fprintf(a.Stdout, "  %-16s %-12s %s\n", "ilegível", client.Name, client.Path)
		case client.Command == "":
			fmt.Fprintf(a.Stdout, "  %-16s %-12s %s\n", "não configurado", client.Name, client.Path)
		default:
			fmt.Fprintf(a.Stdout, "  %-16s %-12s %s -> %s\n", "configurado", client.Name, client.Path, client.Command)
		}
	}

	if !healthy {
		return errProblems
	}
	return nil
}

func countEvents(dir string) (int, error) {
	store, err := audit.NewFileStore(dir, audit.Retention)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := store.List(ctx, audit.MaxList)
	return len(events), err
}

// handshake starts this binary as a client would and lists its tools.
func (a App) handshake() (string, int, error) {
	cmd, err := a.ServerCommand()
	if err != nil {
		return "", 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "git-mcp-doctor", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return "", 0, fmt.Errorf("o servidor não respondeu: %w", err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return "", 0, fmt.Errorf("tools/list: %w", err)
	}
	version := ""
	if info := session.InitializeResult(); info != nil && info.ServerInfo != nil {
		version = info.ServerInfo.Version
	}
	return version, len(listed.Tools), nil
}

// clientConfig is one MCP client's user-level configuration file.
type clientConfig struct {
	Name       string
	Path       string
	Present    bool
	Unreadable bool
	// Command is the executable the client starts for git-mcp, empty when there is no entry.
	Command string
}

// detectClients looks only at user-level files. A server registered per
// project, or by a plugin, is not seen.
func detectClients(home string) []clientConfig {
	clients := []struct {
		name, path string
		find       func([]byte) (string, error)
	}{
		{"Claude Code", filepath.Join(home, ".claude.json"), jsonServerCommand("mcpServers")},
		{"Codex", filepath.Join(home, ".codex", "config.toml"), tomlServerCommand},
		{"Grok", filepath.Join(home, ".grok", "config.toml"), tomlServerCommand},
		{"OpenCode", filepath.Join(home, ".config", "opencode", "opencode.json"), jsonServerCommand("mcp")},
	}
	result := make([]clientConfig, 0, len(clients))
	for _, client := range clients {
		config := clientConfig{Name: client.name, Path: client.path}
		content, err := os.ReadFile(client.path)
		if err == nil {
			config.Present = true
			config.Command, err = client.find(content)
		}
		if err != nil && !os.IsNotExist(err) {
			config.Present = true
			config.Unreadable = true
		}
		result = append(result, config)
	}
	return result
}

func isGitMCP(command string) bool {
	base := filepath.Base(strings.ReplaceAll(command, `\`, "/"))
	return base == "git-mcp" || base == "git-mcp.exe"
}

// jsonServerCommand finds, under the given key, a server that runs the git-mcp executable.
// command may be a string or, in OpenCode, an array whose first item is the executable.
func jsonServerCommand(key string) func([]byte) (string, error) {
	return func(content []byte) (string, error) {
		var file map[string]json.RawMessage
		if err := json.Unmarshal(content, &file); err != nil {
			return "", err
		}
		var servers map[string]struct {
			Command json.RawMessage `json:"command"`
		}
		if len(file[key]) == 0 {
			return "", nil
		}
		if err := json.Unmarshal(file[key], &servers); err != nil {
			return "", err
		}
		for _, entry := range servers {
			var command string
			var parts []string
			if json.Unmarshal(entry.Command, &command) != nil && json.Unmarshal(entry.Command, &parts) == nil && len(parts) > 0 {
				command = parts[0]
			}
			if isGitMCP(command) {
				return command, nil
			}
		}
		return "", nil
	}
}

// tomlServerCommand reads only what it needs: the command line of each [mcp_servers.<name>] table.
func tomlServerCommand(content []byte) (string, error) {
	inServer := false
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inServer = strings.HasPrefix(line, "[mcp_servers.")
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !inServer || !found || strings.TrimSpace(name) != "command" {
			continue
		}
		command := strings.Trim(strings.TrimSpace(value), `"'`)
		if isGitMCP(command) {
			return command, nil
		}
	}
	return "", nil
}
