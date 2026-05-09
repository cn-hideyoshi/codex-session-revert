package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func (a *App) ConfigModelProvider() (string, bool, error) {
	data, err := os.ReadFile(a.ConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultProvider, false, nil
		}
		return "", false, fmt.Errorf("read ~/.codex/config.toml: %w", err)
	}
	provider, ok, err := parseModelProviderFromTOML(data)
	if err != nil {
		return "", false, fmt.Errorf("parse ~/.codex/config.toml model_provider: %w", err)
	}
	if !ok {
		return defaultProvider, false, nil
	}
	return provider, true, nil
}

func parseModelProviderFromTOML(data []byte) (string, bool, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(stripTomlComment(scanner.Text()))
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if strings.HasPrefix(key, `"`) || strings.HasPrefix(key, `'`) {
			unquoted, err := strconv.Unquote(key)
			if err == nil {
				key = unquoted
			}
		}
		if key != targetField {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", false, fmt.Errorf("line %d has empty model_provider value", lineNo)
		}
		if strings.HasPrefix(value, `"`) {
			parsed, err := strconv.Unquote(value)
			if err != nil {
				return "", false, fmt.Errorf("line %d has invalid quoted model_provider: %w", lineNo, err)
			}
			if strings.TrimSpace(parsed) == "" {
				return "", false, fmt.Errorf("line %d has empty model_provider value", lineNo)
			}
			return parsed, true, nil
		}
		if strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`) && len(value) >= 2 {
			parsed := value[1 : len(value)-1]
			if strings.TrimSpace(parsed) == "" {
				return "", false, fmt.Errorf("line %d has empty model_provider value", lineNo)
			}
			return parsed, true, nil
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return "", false, fmt.Errorf("line %d has empty model_provider value", lineNo)
		}
		return fields[0], true, nil
	}
	if err := scanner.Err(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

func stripTomlComment(line string) string {
	var out strings.Builder
	inString := false
	var quote rune
	escaped := false
	for _, r := range line {
		if inString {
			out.WriteRune(r)
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				inString = false
			}
			continue
		}
		if r == '#' {
			break
		}
		if r == '"' || r == '\'' {
			inString = true
			quote = r
		}
		out.WriteRune(r)
	}
	return out.String()
}
