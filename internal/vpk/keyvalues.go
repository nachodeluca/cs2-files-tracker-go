package vpk

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

type kvTokenKind int

const (
	kvTokenEOF kvTokenKind = iota
	kvTokenString
	kvTokenLeftBrace
	kvTokenRightBrace
)

type kvToken struct {
	kind  kvTokenKind
	value string
}

type kvParser struct {
	data []byte
	pos  int
}

func parseKeyValues(data []byte) (map[string]any, error) {
	p := &kvParser{data: data}
	return p.parseDocument()
}

func (p *kvParser) parseDocument() (map[string]any, error) {
	result := make(map[string]any)

	for {
		tok, err := p.nextToken()
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return nil, err
		}

		if tok.kind == kvTokenRightBrace {
			return nil, fmt.Errorf("unexpected closing brace at top level")
		}

		if tok.kind != kvTokenString {
			return nil, fmt.Errorf("expected key, got %q", tok.value)
		}

		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}

		appendParsedValue(result, tok.value, value)
	}
}

func (p *kvParser) parseObject() (map[string]any, error) {
	result := make(map[string]any)

	for {
		tok, err := p.nextToken()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("unexpected end of file inside object")
		}
		if err != nil {
			return nil, err
		}

		if tok.kind == kvTokenRightBrace {
			return result, nil
		}

		if tok.kind != kvTokenString {
			return nil, fmt.Errorf("expected key inside object, got %q", tok.value)
		}

		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}

		appendParsedValue(result, tok.value, value)
	}
}

func (p *kvParser) parseValue() (any, error) {
	tok, err := p.nextToken()
	if err != nil {
		return nil, err
	}

	switch tok.kind {
	case kvTokenString:
		return tok.value, nil
	case kvTokenLeftBrace:
		return p.parseObject()
	case kvTokenRightBrace:
		return nil, errors.New("unexpected closing brace")
	default:
		return nil, fmt.Errorf("unexpected token %q", tok.value)
	}
}

func appendParsedValue(result map[string]any, key string, value any) {
	if existing, ok := result[key]; ok {
		switch current := existing.(type) {
		case []any:
			result[key] = append(current, value)
		default:
			result[key] = []any{current, value}
		}

		return
	}

	result[key] = value
}

func (p *kvParser) nextToken() (kvToken, error) {
	p.skipWhitespaceAndComments()

	if p.pos >= len(p.data) {
		return kvToken{kind: kvTokenEOF}, io.EOF
	}

	switch p.data[p.pos] {
	case '{':
		p.pos++
		return kvToken{kind: kvTokenLeftBrace, value: "{"}, nil
	case '}':
		p.pos++
		return kvToken{kind: kvTokenRightBrace, value: "}"}, nil
	case '"':
		value, err := p.readQuotedString()
		if err != nil {
			return kvToken{}, err
		}

		return kvToken{kind: kvTokenString, value: value}, nil
	default:
		value := p.readBareWord()
		if value == "" {
			return kvToken{}, fmt.Errorf("unexpected byte 0x%02X", p.data[p.pos])
		}

		return kvToken{kind: kvTokenString, value: value}, nil
	}
}

func (p *kvParser) skipWhitespaceAndComments() {
	for p.pos < len(p.data) {
		if isWhitespace(p.data[p.pos]) {
			p.pos++
			continue
		}

		if p.data[p.pos] == '/' && p.pos+1 < len(p.data) {
			switch p.data[p.pos+1] {
			case '/':
				p.pos += 2
				for p.pos < len(p.data) && p.data[p.pos] != '\n' {
					p.pos++
				}
				continue
			case '*':
				p.pos += 2
				for p.pos+1 < len(p.data) && !(p.data[p.pos] == '*' && p.data[p.pos+1] == '/') {
					p.pos++
				}
				if p.pos+1 < len(p.data) {
					p.pos += 2
				}
				continue
			}
		}

		return
	}
}

func (p *kvParser) readQuotedString() (string, error) {
	if p.pos >= len(p.data) || p.data[p.pos] != '"' {
		return "", errors.New("expected quoted string")
	}

	p.pos++
	var value bytes.Buffer

	for p.pos < len(p.data) {
		ch := p.data[p.pos]
		p.pos++

		if ch == '"' {
			return value.String(), nil
		}

		if ch == '\\' {
			if p.pos >= len(p.data) {
				return "", errors.New("unterminated escape sequence")
			}

			switch p.data[p.pos] {
			case 'n':
				value.WriteByte('\n')
			case 'r':
				value.WriteByte('\r')
			case 't':
				value.WriteByte('\t')
			case 'b':
				value.WriteByte('\b')
			case 'f':
				value.WriteByte('\f')
			case '\\':
				value.WriteByte('\\')
			case '"':
				value.WriteByte('"')
			default:
				value.WriteByte(p.data[p.pos])
			}
			p.pos++
			continue
		}

		value.WriteByte(ch)
	}

	return "", errors.New("unterminated quoted string")
}

func (p *kvParser) readBareWord() string {
	start := p.pos

	for p.pos < len(p.data) {
		ch := p.data[p.pos]
		if isWhitespace(ch) || ch == '{' || ch == '}' {
			break
		}

		if ch == '/' && p.pos+1 < len(p.data) && (p.data[p.pos+1] == '/' || p.data[p.pos+1] == '*') {
			break
		}

		p.pos++
	}

	return strings.TrimSpace(string(p.data[start:p.pos]))
}

func isWhitespace(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}
