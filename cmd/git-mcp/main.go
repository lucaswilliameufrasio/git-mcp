package main

import (
	"os"

	"git-mcp/internal/cli"
)

func main() {
	os.Exit(cli.New().Run(os.Args[1:]))
}
