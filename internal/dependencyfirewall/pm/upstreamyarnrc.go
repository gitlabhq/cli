// The .yarnrc lockfile tokenizer, parser and YAML fallback in this file are
// translated from Yarn 1.22.19's src/lockfile/parse.js:
// https://github.com/yarnpkg/yarn/blob/v1.22.19/src/lockfile/parse.js
//
// BSD 2-Clause License
//
// For Yarn software
//
// Copyright (c) 2016-present, Yarn Contributors. All rights reserved.
//
// Redistribution and use in source and binary forms, with or without modification,
// are permitted provided that the following conditions are met:
//
//  * Redistributions of source code must retain the above copyright notice, this
//    list of conditions and the following disclaimer.
//
//  * Redistributions in binary form must reproduce the above copyright notice,
//    this list of conditions and the following disclaimer in the documentation
//    and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
// ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
// WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR
// ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
// (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
// LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON
// ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package pm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// readYarnrc reads the top-level settings of a Yarn Classic .yarnrc as yarn
// 1.22 does (src/lockfile/parse.js): in yarn's lockfile syntax, else, when
// that fails, the whole file as YAML, which yields settings only when the
// file is a mapping. A file that fails both fails the run, as it fails yarn.
// Errors name the line only, because the file can hold credentials.
func readYarnrc(files configFiles, path string) (map[string]configSetting, error) {
	data, ok, err := files.read(path)
	if !ok {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	values, err := parseYarnLockfile(string(data))
	if err != nil {
		var yamlErr error
		if values, yamlErr = yarnrcYAML(data); yamlErr != nil {
			return nil, parseError(path, err)
		}
	}
	layer := make(map[string]configSetting, len(values))
	for k, v := range values {
		layer[k] = configSetting{source: fileSource(path, k), value: v}
	}
	return layer, nil
}

// yarnrcYAML reads data as yarn's YAML fallback does, with js-yaml's
// failsafe schema: the top-level keys of a mapping, every scalar a string.
// Any other document yields no settings.
func yarnrcYAML(data []byte) (map[string]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	values := map[string]string{}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return values, nil
	}
	m := doc.Content[0].Content
	seen := map[string]struct{}{}
	for i := 0; i+1 < len(m); i += 2 {
		if _, dup := seen[m[i].Value]; dup {
			// js-yaml rejects a duplicated key.
			return nil, fmt.Errorf("line %d", m[i].Line)
		}
		seen[m[i].Value] = struct{}{}
		if m[i+1].Kind == yaml.ScalarNode {
			values[m[i].Value] = m[i+1].Value
		}
	}
	return values, nil
}

type yarnTokenKind int

const (
	yarnInvalid yarnTokenKind = iota
	yarnBoolean
	yarnString
	yarnNumber
	yarnColon
	yarnComma
	yarnNewline
	yarnComment
	yarnIndent
	yarnEOF
	// yarnBadIndent stands for the error yarn's tokenizer throws on an odd
	// indent, which yarn only meets when the parser reads that far.
	yarnBadIndent
)

type yarnToken struct {
	kind   yarnTokenKind
	value  string
	indent int
	line   int
}

// yarnTokens splits input as yarn 1.22's lockfile tokenizer does. It stops
// after a token the parser always rejects.
func yarnTokens(input string) []yarnToken {
	var tokens []yarnToken
	line, lastNewline := 1, false
	emit := func(kind yarnTokenKind, value string) {
		tokens = append(tokens, yarnToken{kind: kind, value: value, line: line})
	}
	for input != "" {
		chop := 0
		c := input[0]
		switch {
		case c == '\n' || c == '\r':
			chop = 1
			if len(input) > 1 && input[1] == '\n' {
				chop = 2
			}
			line++
			emit(yarnNewline, "")
		case c == '#':
			end := strings.IndexByte(input[1:], '\n') + 1
			if end == 0 {
				end = len(input)
			}
			emit(yarnComment, input[1:end])
			chop = end
		case c == ' ' && lastNewline:
			chop = len(input) - len(strings.TrimLeft(input, " "))
			if chop%2 == 1 {
				emit(yarnBadIndent, "")
				return tokens
			}
			tokens = append(tokens, yarnToken{kind: yarnIndent, indent: chop / 2, line: line})
		case c == ' ':
			chop = 1
		case c == '"':
			chop = len(input)
			for i := 1; i < len(input); i++ {
				if input[i] == '"' && (input[i-1] != '\\' || (i >= 2 && input[i-2] == '\\')) {
					chop = i + 1
					break
				}
			}
			var s string
			if json.Unmarshal([]byte(input[:chop]), &s) != nil {
				emit(yarnInvalid, "")
				return tokens
			}
			emit(yarnString, s)
		case c >= '0' && c <= '9':
			chop = len(input) - len(strings.TrimLeft(input, "0123456789"))
			n := strings.TrimLeft(input[:chop], "0")
			if n == "" {
				n = "0"
			}
			emit(yarnNumber, n)
		case strings.HasPrefix(input, "true"):
			emit(yarnBoolean, "true")
			chop = 4
		case strings.HasPrefix(input, "false"):
			emit(yarnBoolean, "false")
			chop = 5
		case c == ':':
			emit(yarnColon, "")
			chop = 1
		case c == ',':
			emit(yarnComma, "")
			chop = 1
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '/', c == '.', c == '-':
			chop = strings.IndexAny(input, ": \n\r,")
			if chop == -1 {
				chop = len(input)
			}
			emit(yarnString, input[:chop])
		default:
			emit(yarnInvalid, "")
			return tokens
		}
		lastNewline = c == '\n' || (c == '\r' && len(input) > 1 && input[1] == '\n')
		input = input[chop:]
	}
	emit(yarnEOF, "")
	return tokens
}

// yarnSyntaxError is a .yarnrc that yarn's lockfile parser rejects, at line.
type yarnSyntaxError struct{ line int }

func (e yarnSyntaxError) Error() string { return "line " + strconv.Itoa(e.line) }

type yarnParser struct {
	tokens []yarnToken
	pos    int
	tok    yarnToken
}

func parseYarnLockfile(text string) (map[string]string, error) {
	p := &yarnParser{tokens: yarnTokens(text)}
	if err := p.next(); err != nil {
		return nil, err
	}
	values := map[string]string{}
	if err := p.parse(0, values); err != nil {
		return nil, err
	}
	return values, nil
}

func (p *yarnParser) next() error {
	for p.pos < len(p.tokens) {
		t := p.tokens[p.pos]
		p.pos++
		switch t.kind {
		case yarnComment:
			continue
		case yarnBadIndent:
			return yarnSyntaxError{t.line}
		}
		p.tok = t
		return nil
	}
	return yarnSyntaxError{p.tok.line}
}

// parse reads the settings at indent, as yarn's Parser.parse does, and
// records the plain values in values when it is not nil. A line indented
// less than indent, or any indented line at the top level, ends it.
func (p *yarnParser) parse(indent int, values map[string]string) error {
	for {
		switch t := p.tok; t.kind {
		case yarnNewline:
			if err := p.next(); err != nil {
				return err
			}
			if indent == 0 {
				continue
			}
			if p.tok.kind != yarnIndent || p.tok.indent != indent {
				return nil
			}
			if err := p.next(); err != nil {
				return err
			}
		case yarnIndent:
			if t.indent != indent {
				return nil
			}
			if err := p.next(); err != nil {
				return err
			}
		case yarnEOF:
			return nil
		case yarnString:
			nested, err := p.parseEntry(indent, values)
			if err != nil {
				return err
			}
			if nested && indent > 0 && p.tok.kind != yarnIndent {
				return nil
			}
		default:
			return yarnSyntaxError{t.line}
		}
	}
}

// parseEntry reads one "key value", "key, key value" or "key:" entry, and
// reports whether it was the last kind, a nested block whose settings are
// not recorded.
func (p *yarnParser) parseEntry(indent int, values map[string]string) (bool, error) {
	keys := []string{p.tok.value}
	if err := p.next(); err != nil {
		return false, err
	}
	for p.tok.kind == yarnComma {
		if err := p.next(); err != nil {
			return false, err
		}
		if p.tok.kind != yarnString {
			return false, yarnSyntaxError{p.tok.line}
		}
		keys = append(keys, p.tok.value)
		if err := p.next(); err != nil {
			return false, err
		}
	}
	if slices.Contains(keys, "") {
		return false, yarnSyntaxError{p.tok.line}
	}
	wasColon := p.tok.kind == yarnColon
	if wasColon {
		if err := p.next(); err != nil {
			return false, err
		}
	}
	switch {
	case p.tok.kind == yarnBoolean || p.tok.kind == yarnString || p.tok.kind == yarnNumber:
		if values != nil {
			for _, k := range keys {
				values[k] = p.tok.value
			}
		}
		return false, p.next()
	case wasColon:
		for _, k := range keys {
			delete(values, k)
		}
		return true, p.parse(indent+1, nil)
	default:
		return false, yarnSyntaxError{p.tok.line}
	}
}
