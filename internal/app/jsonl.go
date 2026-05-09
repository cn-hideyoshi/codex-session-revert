package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

func buildRewritePlan(path, provider string) (RewritePlan, []LineProblem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RewritePlan{}, nil, fmt.Errorf("read %s: %w", path, err)
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	var out bytes.Buffer
	var problems []LineProblem
	changed := 0
	for i, rawLine := range lines {
		lineNo := i + 1
		hasNewline := bytes.HasSuffix(rawLine, []byte("\n"))
		line := bytes.TrimSuffix(rawLine, []byte("\n"))
		if bytes.HasSuffix(line, []byte("\r")) {
			line = bytes.TrimSuffix(line, []byte("\r"))
		}
		if len(bytes.TrimSpace(line)) == 0 {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: errors.New("empty line is not a JSON object")})
			out.Write(rawLine)
			continue
		}
		updated, fieldChanges, err := updateProviderInLine(line, provider)
		if err != nil {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: err})
			out.Write(rawLine)
			continue
		}
		changed += fieldChanges
		out.Write(updated)
		if bytes.HasSuffix(rawLine, []byte("\r\n")) {
			out.WriteString("\r\n")
		} else if hasNewline {
			out.WriteByte('\n')
		}
	}
	return RewritePlan{Path: path, Content: out.Bytes(), Changed: changed}, problems, nil
}

func inspectJSONL(path string) ([]LineProblem, map[string]int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	providers := map[string]int{}
	var problems []LineProblem
	for i, rawLine := range lines {
		lineNo := i + 1
		line := bytes.TrimRight(rawLine, "\r\n")
		if len(bytes.TrimSpace(line)) == 0 {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: errors.New("empty line is not a JSON object")})
			continue
		}
		if !json.Valid(line) {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: errors.New("invalid JSON")})
			continue
		}
		providersOnLine, err := stringFieldValues(line, targetField)
		if err != nil {
			problems = append(problems, LineProblem{Path: path, Line: lineNo, Err: err})
			continue
		}
		for _, provider := range providersOnLine {
			providers[provider]++
		}
	}
	return problems, providers, len(lines), nil
}

func updateProviderInLine(line []byte, provider string) ([]byte, int, error) {
	if !json.Valid(line) {
		return nil, 0, errors.New("invalid JSON")
	}
	spans, err := fieldValueSpans(line, targetField)
	if err != nil || len(spans) == 0 {
		return line, 0, err
	}
	quoted, err := json.Marshal(provider)
	if err != nil {
		return nil, 0, err
	}
	changes := 0
	out := append([]byte(nil), line...)
	for i := len(spans) - 1; i >= 0; i-- {
		span := spans[i]
		if bytes.Equal(bytes.TrimSpace(out[span.Start:span.End]), quoted) {
			continue
		}
		next := make([]byte, 0, len(out)-(span.End-span.Start)+len(quoted))
		next = append(next, out[:span.Start]...)
		next = append(next, quoted...)
		next = append(next, out[span.End:]...)
		out = next
		changes++
	}
	if changes == 0 {
		return line, 0, nil
	}
	if !json.Valid(out) {
		return nil, 0, errors.New("internal error: rewritten line is invalid JSON")
	}
	return out, changes, nil
}

type valueSpan struct {
	Start int
	End   int
}

func stringFieldValues(line []byte, field string) ([]string, error) {
	spans, err := fieldValueSpans(line, field)
	if err != nil || len(spans) == 0 {
		return nil, err
	}
	values := make([]string, 0, len(spans))
	for _, span := range spans {
		value := bytes.TrimSpace(line[span.Start:span.End])
		if len(value) == 0 || value[0] != '"' {
			return nil, fmt.Errorf("field %q exists but is not a JSON string", field)
		}
		var parsed string
		if err := json.Unmarshal(value, &parsed); err != nil {
			return nil, fmt.Errorf("parse field %q: %w", field, err)
		}
		values = append(values, parsed)
	}
	return values, nil
}
