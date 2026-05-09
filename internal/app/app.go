package app

import (
	"io"
	"os"
	"runtime"
	"time"
)

const (
	defaultProvider    = "openai"
	targetField        = "model_provider"
	backupPrefix       = "csr-"
	legacyBackupPrefix = "codex-session-revert-"
)

type App struct {
	Home    string
	Now     func() time.Time
	Out     io.Writer
	Err     io.Writer
	Workers int
}

func NewApp() (*App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &App{
		Home:    home,
		Now:     time.Now,
		Out:     os.Stdout,
		Err:     os.Stderr,
		Workers: runtime.NumCPU(),
	}, nil
}

func (a *App) workerCount(fileCount int) int {
	workers := a.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers < 1 {
		workers = 1
	}
	if fileCount > 0 && workers > fileCount {
		workers = fileCount
	}
	return workers
}
