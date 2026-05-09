package app

import (
	"fmt"
	"sort"
	"sync"
)

func (a *App) Status() error {
	files, err := a.SessionFiles()
	if err != nil {
		return err
	}
	provider, configured, err := a.ConfigModelProvider()
	if err != nil {
		return err
	}

	problems, distribution, lines, err := a.inspectSessionFiles(files)
	if err != nil {
		return err
	}

	source := "default"
	if configured {
		source = "~/.codex/config.toml"
	}
	fmt.Fprintf(a.Out, "Session files: %d\n", len(files))
	fmt.Fprintf(a.Out, "JSONL lines: %d\n", lines)
	if len(problems) == 0 {
		fmt.Fprintln(a.Out, "JSONL parse: ok")
	} else {
		fmt.Fprintf(a.Out, "JSONL parse: %d problem(s)\n", len(problems))
		for _, problem := range problems {
			fmt.Fprintf(a.Out, "- %s:%d: %v\n", problem.Path, problem.Line, problem.Err)
		}
	}
	fmt.Fprintf(a.Out, "Target model_provider: %s (%s)\n", provider, source)
	fmt.Fprintln(a.Out, "Model provider distribution:")
	if len(distribution) == 0 {
		fmt.Fprintln(a.Out, "- <none>: 0")
		return nil
	}
	keys := make([]string, 0, len(distribution))
	for k := range distribution {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(a.Out, "- %s: %d\n", k, distribution[k])
	}
	return nil
}

func (a *App) inspectSessionFiles(files []string) ([]LineProblem, map[string]int, int, error) {
	type job struct {
		Index int
		Path  string
	}
	type result struct {
		Index    int
		Problems []LineProblem
		Counts   map[string]int
		Lines    int
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
				problems, counts, lines, err := inspectJSONL(j.Path)
				results <- result{Index: j.Index, Problems: problems, Counts: counts, Lines: lines, Err: err}
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

	problemsByFile := make([][]LineProblem, len(files))
	countsByFile := make([]map[string]int, len(files))
	linesByFile := make([]int, len(files))
	for r := range results {
		if r.Err != nil {
			return nil, nil, 0, r.Err
		}
		problemsByFile[r.Index] = r.Problems
		countsByFile[r.Index] = r.Counts
		linesByFile[r.Index] = r.Lines
	}

	distribution := map[string]int{}
	var problems []LineProblem
	lines := 0
	for i := range files {
		lines += linesByFile[i]
		problems = append(problems, problemsByFile[i]...)
		for k, v := range countsByFile[i] {
			distribution[k] += v
		}
	}
	return problems, distribution, lines, nil
}
