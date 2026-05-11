package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

type LineProblem struct {
	Path string
	Line int
	Err  error
}

type RewritePlan struct {
	Path    string
	Content []byte
	Changed int
}

func (a *App) Revert(provider string) (string, int, int, error) {
	if strings.TrimSpace(provider) == "" {
		return "", 0, 0, errors.New("model_provider cannot be empty")
	}
	backupName, _, err := a.Backup()
	if err != nil {
		return "", 0, 0, err
	}

	files, err := a.SessionFiles()
	if err != nil {
		return backupName, 0, 0, err
	}
	plans, problems, err := a.buildRewritePlans(files, provider)
	if err != nil {
		return backupName, 0, 0, err
	}
	totalChanged := 0
	activePlans := make([]RewritePlan, 0, len(plans))
	for _, plan := range plans {
		if plan.Changed > 0 {
			totalChanged += plan.Changed
			activePlans = append(activePlans, plan)
		}
	}
	if len(problems) > 0 {
		return backupName, len(files), 0, formatLineProblems(problems, backupName)
	}

	stateChanged, err := a.UpdateStateProvider(provider)
	if err != nil {
		return backupName, len(files), totalChanged, err
	}
	totalChanged += stateChanged

	for _, plan := range activePlans {
		info, err := os.Stat(plan.Path)
		if err != nil {
			return backupName, len(files), totalChanged, fmt.Errorf("stat before write %s: %w", plan.Path, err)
		}
		if err := os.WriteFile(plan.Path, plan.Content, info.Mode().Perm()); err != nil {
			return backupName, len(files), totalChanged, fmt.Errorf("write %s: %w", plan.Path, err)
		}
	}
	return backupName, len(files), totalChanged, nil
}

func (a *App) buildRewritePlans(files []string, provider string) ([]RewritePlan, []LineProblem, error) {
	type job struct {
		Index int
		Path  string
	}
	type result struct {
		Index    int
		Plan     RewritePlan
		Problems []LineProblem
		Err      error
	}

	jobs := make(chan job)
	results := make(chan result, len(files))
	var wg sync.WaitGroup
	for i := 0; i < a.workerCount(len(files)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				plan, problems, err := buildRewritePlan(j.Path, provider)
				results <- result{Index: j.Index, Plan: plan, Problems: problems, Err: err}
			}
		}()
	}
	go func() {
		for i, path := range files {
			jobs <- job{Index: i, Path: path}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	plans := make([]RewritePlan, len(files))
	problemsByFile := make([][]LineProblem, len(files))
	for r := range results {
		if r.Err != nil {
			return nil, nil, r.Err
		}
		plans[r.Index] = r.Plan
		problemsByFile[r.Index] = r.Problems
	}

	var problems []LineProblem
	for _, fileProblems := range problemsByFile {
		problems = append(problems, fileProblems...)
	}
	return plans, problems, nil
}

func formatLineProblems(problems []LineProblem, backupName string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "JSONL validation failed; no session files were modified. Backup already created: %s. Fix these lines and retry:\n", backupName)
	for _, problem := range problems {
		fmt.Fprintf(&b, "- %s:%d: %v\n", problem.Path, problem.Line, problem.Err)
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}
