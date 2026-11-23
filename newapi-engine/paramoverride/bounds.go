package paramoverride

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MoeForge/moego/ojson"
	"github.com/MoeForge/moego/regexp"
	"github.com/tidwall/gjson"
)

const (
	MaxBodyBytes     = 1 << 20
	MaxRuleBytes     = 64 << 10
	maxOperations    = 128
	maxDepth         = 64
	maxNodes         = 16384
	maxPatternBytes  = 4096
	maxCaptureGroups = 32
)

var errLimit = errors.New("parameter override exceeds host limits")

type boundedDoc struct{ *ojson.Doc }

func newBoundedDoc(body []byte) *boundedDoc       { return &boundedDoc{ojson.NewDoc(body)} }
func newBoundedDocString(body string) *boundedDoc { return &boundedDoc{ojson.NewDocString(body)} }

func (d *boundedDoc) Set(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return d.SetRawBytes(path, raw)
}

func (d *boundedDoc) SetRaw(path, raw string) error { return d.SetRawBytes(path, []byte(raw)) }

func (d *boundedDoc) SetRawBytes(path string, raw []byte) error {
	depth, err := boundedPath(path)
	if err != nil {
		return err
	}
	if err = boundedJSON(raw, maxDepth-depth); err != nil {
		return err
	}
	parts := strings.Split(path, ".")
	for i, part := range parts {
		parent := "@this"
		if i > 0 {
			parent = strings.Join(parts[:i], ".")
		}
		// Existing object keys may be numeric (for example a date). Only
		// arrays or missing containers can turn this component into padding.
		if d.Get(parent).IsObject() {
			continue
		}
		digits := strings.TrimPrefix(strings.TrimPrefix(part, "-"), "+")
		if digits != "" && strings.Trim(digits, "0123456789") == "" {
			n, err := strconv.ParseInt(part, 10, 64)
			if err != nil || n > maxNodes || n < -maxNodes {
				return errLimit
			}
		}
	}
	if err = d.Doc.SetRawBytes(path, raw); err != nil {
		return err
	}
	if d.Len() > MaxBodyBytes {
		return errLimit
	}
	return nil
}

func (d *boundedDoc) Delete(path string) error {
	if _, err := boundedPath(path); err != nil {
		return err
	}
	return d.Doc.Delete(path)
}

func boundedPath(path string) (int, error) {
	if len(path) > 8192 {
		return 0, errLimit
	}
	parts := strings.Split(path, ".")
	if len(parts) > maxDepth {
		return 0, errLimit
	}
	return len(parts), nil
}

func boundedJSON(raw []byte, depthLimit int) error {
	if len(raw) > MaxBodyBytes || !json.Valid(raw) || depthLimit < 0 {
		return errLimit
	}
	nodes := 0
	var walk func(gjson.Result, int) bool
	walk = func(v gjson.Result, depth int) bool {
		nodes++
		if nodes > maxNodes || depth > depthLimit {
			return false
		}
		valid := true
		if v.IsArray() || v.IsObject() {
			v.ForEach(func(_, child gjson.Result) bool { valid = walk(child, depth+1); return valid })
		}
		return valid
	}
	if !walk(gjson.ParseBytes(raw), 0) {
		return errLimit
	}
	return nil
}

func encodeJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }

func boundedReplacementSize(input, from, to string) error {
	count := strings.Count(input, from)
	if len(to) > len(from) && count > (MaxBodyBytes-len(input))/(len(to)-len(from)) {
		return errLimit
	}
	return nil
}

func appendBounded(out *strings.Builder, value string) error {
	if len(value) > MaxBodyBytes-out.Len() {
		return errLimit
	}
	out.WriteString(value)
	return nil
}

// Expand each match with a bounded writer. The source regex engine uses Go's
// $name/${name}/$$ syntax; a long capture cannot amplify into an unbounded
// intermediate allocation before the document size check.
func boundedRegexReplace(re *regexp.Regexp, input, template string) (string, error) {
	if re.NumSubexp() > maxCaptureGroups {
		return "", errLimit
	}
	matches := re.FindAllStringSubmatchIndex(input, maxNodes+1)
	if len(matches) > maxNodes {
		return "", errLimit
	}
	var out strings.Builder
	last := 0
	names := re.SubexpNames()
	for _, match := range matches {
		if err := appendBounded(&out, input[last:match[0]]); err != nil {
			return "", err
		}
		remaining := template
		for len(remaining) > 0 {
			before, after, found := strings.Cut(remaining, "$")
			if !found {
				if err := appendBounded(&out, remaining); err != nil {
					return "", err
				}
				break
			}
			if err := appendBounded(&out, before); err != nil {
				return "", err
			}
			remaining = after
			if strings.HasPrefix(remaining, "$") {
				if err := appendBounded(&out, "$"); err != nil {
					return "", err
				}
				remaining = remaining[1:]
				continue
			}
			name, num, rest, valid := replacementName(remaining)
			if !valid {
				if err := appendBounded(&out, "$"); err != nil {
					return "", err
				}
				continue
			}
			remaining = rest
			if num < 0 {
				for i, n := range names {
					if n == name && 2*i+1 < len(match) && match[2*i] >= 0 {
						num = i
						break
					}
				}
			}
			if num >= 0 && 2*num+1 < len(match) && match[2*num] >= 0 {
				if err := appendBounded(&out, input[match[2*num]:match[2*num+1]]); err != nil {
					return "", err
				}
			}
		}
		last = match[1]
	}
	if err := appendBounded(&out, input[last:]); err != nil {
		return "", err
	}
	return out.String(), nil
}

func replacementName(text string) (name string, num int, rest string, ok bool) {
	if text == "" {
		return
	}
	brace := text[0] == '{'
	if brace {
		text = text[1:]
	}
	i := 0
	for i < len(text) {
		r, n := utf8.DecodeRuneInString(text[i:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		i += n
	}
	if i == 0 {
		return
	}
	name = text[:i]
	if brace {
		if i >= len(text) || text[i] != '}' {
			return
		}
		i++
	}
	num = 0
	for _, c := range []byte(name) {
		if c < '0' || c > '9' || num >= 1e8 {
			num = -1
			break
		}
		num = num*10 + int(c-'0')
	}
	if name[0] == '0' && len(name) > 1 {
		num = -1
	}
	return name, num, text[i:], true
}
