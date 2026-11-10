package main

import (
	"context"
	"log"
	"os"

	"git-mcp/internal/gitflow"
	"git-mcp/internal/server"
)

func main() {
	logger := log.New(os.Stderr, "git-mcp: ", log.LstdFlags)
	svc := gitflow.New(logger)
	if err := server.Run(context.Background(), svc, logger); err != nil {
		logger.Fatal(err)
	}
}
