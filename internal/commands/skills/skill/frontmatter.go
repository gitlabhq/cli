package skill

import (
	"bytes"
	"errors"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Frontmatter holds the SKILL.md frontmatter keys glab reads.
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// FrontmatterParts is a SKILL.md split around its YAML frontmatter so a
// caller can edit Block and reassemble the file byte-for-byte otherwise.
type FrontmatterParts struct {
	// Head is any leading whitespace plus the opening `---` line.
	Head []byte
	// Block is the YAML between the delimiters, without the final line break.
	Block []byte
	// Tail starts at the line break before the closing `---` and runs to EOF.
	Tail []byte
}

var closingDelimiter = regexp.MustCompile(`\r?\n---`)

// SplitFrontmatter splits content at the first pair of `---` delimiters.
// LF and CRLF line endings are both preserved in Head and Tail.
func SplitFrontmatter(content []byte) (FrontmatterParts, error) {
	const delim = "---"
	trimmed := bytes.TrimLeft(content, " \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte(delim)) {
		return FrontmatterParts{}, errors.New("missing leading '---' delimiter")
	}
	afterDelim := trimmed[len(delim):]
	nl := bytes.IndexByte(afterDelim, '\n')
	if nl == -1 {
		return FrontmatterParts{}, errors.New("missing newline after opening '---'")
	}
	headLen := len(content) - len(afterDelim) + nl + 1
	rest := content[headLen:]
	loc := closingDelimiter.FindIndex(rest)
	if loc == nil {
		return FrontmatterParts{}, errors.New("missing closing '---' delimiter")
	}
	return FrontmatterParts{
		Head:  content[:headLen],
		Block: rest[:loc[0]],
		Tail:  rest[loc[0]:],
	}, nil
}

// Join reassembles the file from its parts.
func (p FrontmatterParts) Join() []byte {
	out := make([]byte, 0, len(p.Head)+len(p.Block)+len(p.Tail))
	out = append(out, p.Head...)
	out = append(out, p.Block...)
	return append(out, p.Tail...)
}

// ParseFrontmatter decodes the frontmatter of a SKILL.md.
func ParseFrontmatter(content []byte) (Frontmatter, error) {
	var fm Frontmatter
	parts, err := SplitFrontmatter(content)
	if err != nil {
		return fm, err
	}
	if err := yaml.Unmarshal(parts.Block, &fm); err != nil {
		return fm, err
	}
	fm.Description = strings.TrimSpace(fm.Description)
	return fm, nil
}
