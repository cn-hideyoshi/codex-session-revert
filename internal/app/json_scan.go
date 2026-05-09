package app

import (
	"encoding/json"
	"errors"
)

func fieldValueSpans(line []byte, field string) ([]valueSpan, error) {
	var spans []valueSpan
	end, err := collectFieldValueSpans(line, skipJSONSpace(line, 0), field, nil, &spans)
	if err != nil {
		return nil, err
	}
	if skipJSONSpace(line, end) != len(line) {
		return nil, errors.New("unexpected data after JSON value")
	}
	return spans, nil
}

func collectFieldValueSpans(line []byte, start int, field string, path []string, spans *[]valueSpan) (int, error) {
	if start >= len(line) {
		return 0, errors.New("missing JSON value")
	}
	switch line[start] {
	case '{':
		return collectObjectFieldValueSpans(line, start, field, path, spans)
	case '[':
		return collectArrayFieldValueSpans(line, start, field, path, spans)
	case '"':
		return scanJSONString(line, start)
	default:
		return scanJSONValue(line, start)
	}
}

func collectObjectFieldValueSpans(line []byte, start int, field string, path []string, spans *[]valueSpan) (int, error) {
	i := start + 1
	for {
		i = skipJSONSpace(line, i)
		if i >= len(line) {
			return 0, errors.New("unexpected end of JSON object")
		}
		if line[i] == '}' {
			return i + 1, nil
		}
		if line[i] != '"' {
			return 0, errors.New("expected object key")
		}
		keyStart := i
		keyEnd, err := scanJSONString(line, i)
		if err != nil {
			return 0, err
		}
		var key string
		if err := json.Unmarshal(line[keyStart:keyEnd], &key); err != nil {
			return 0, err
		}
		i = skipJSONSpace(line, keyEnd)
		if i >= len(line) || line[i] != ':' {
			return 0, errors.New("expected ':' after object key")
		}
		valueStart := skipJSONSpace(line, i+1)
		var valueEnd int
		if key == field && isSessionModelProviderPath(path) {
			valueEnd, err = scanJSONValue(line, valueStart)
			if err == nil {
				*spans = append(*spans, valueSpan{Start: valueStart, End: valueEnd})
			}
		} else {
			childPath := append(append([]string(nil), path...), key)
			valueEnd, err = collectFieldValueSpans(line, valueStart, field, childPath, spans)
		}
		if err != nil {
			return 0, err
		}
		i = skipJSONSpace(line, valueEnd)
		if i >= len(line) {
			return 0, errors.New("unexpected end after object value")
		}
		switch line[i] {
		case ',':
			i++
		case '}':
			return i + 1, nil
		default:
			return 0, errors.New("expected ',' or '}' after object value")
		}
	}
}

func collectArrayFieldValueSpans(line []byte, start int, field string, path []string, spans *[]valueSpan) (int, error) {
	i := start + 1
	for {
		i = skipJSONSpace(line, i)
		if i >= len(line) {
			return 0, errors.New("unexpected end of JSON array")
		}
		if line[i] == ']' {
			return i + 1, nil
		}
		valueEnd, err := collectFieldValueSpans(line, i, field, path, spans)
		if err != nil {
			return 0, err
		}
		i = skipJSONSpace(line, valueEnd)
		if i >= len(line) {
			return 0, errors.New("unexpected end after array value")
		}
		switch line[i] {
		case ',':
			i++
		case ']':
			return i + 1, nil
		default:
			return 0, errors.New("expected ',' or ']' after array value")
		}
	}
}

func isSessionModelProviderPath(path []string) bool {
	return len(path) == 0 || (len(path) == 1 && path[0] == "payload")
}

func scanJSONString(line []byte, start int) (int, error) {
	if start >= len(line) || line[start] != '"' {
		return 0, errors.New("expected JSON string")
	}
	escaped := false
	for i := start + 1; i < len(line); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch line[i] {
		case '\\':
			escaped = true
		case '"':
			return i + 1, nil
		}
	}
	return 0, errors.New("unterminated JSON string")
}

func scanJSONValue(line []byte, start int) (int, error) {
	if start >= len(line) {
		return 0, errors.New("missing JSON value")
	}
	switch line[start] {
	case '"':
		return scanJSONString(line, start)
	case '{', '[':
		return scanJSONContainer(line, start)
	default:
		i := start
		for i < len(line) {
			switch line[i] {
			case ',', '}', ']':
				return i, nil
			case ' ', '\t', '\r', '\n':
				return i, nil
			default:
				i++
			}
		}
		return i, nil
	}
}

func scanJSONContainer(line []byte, start int) (int, error) {
	stack := []byte{line[start]}
	inString := false
	escaped := false
	for i := start + 1; i < len(line); i++ {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch line[i] {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch line[i] {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, line[i])
		case '}', ']':
			if len(stack) == 0 {
				return 0, errors.New("unexpected container close")
			}
			open := stack[len(stack)-1]
			if (open == '{' && line[i] != '}') || (open == '[' && line[i] != ']') {
				return 0, errors.New("mismatched JSON container")
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, nil
			}
		}
	}
	return 0, errors.New("unterminated JSON container")
}

func skipJSONSpace(line []byte, i int) int {
	for i < len(line) {
		switch line[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}
