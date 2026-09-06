package openaicompat

import "strings"

type reviewerJSONFrame struct {
	kind         byte
	expectingKey bool
}

// repairReviewerJSON extracts one complete object and repairs damage commonly
// introduced when a reviewer puts prose directly in JSON strings. It does not
// synthesize fields or close truncated strings/containers; callers must still
// validate the returned candidate with json.Unmarshal.
func repairReviewerJSON(raw string) string {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end < start {
		return ""
	}

	text := raw[start : end+1]
	var out strings.Builder
	out.Grow(len(text) + 64)

	stack := make([]reviewerJSONFrame, 0, 4)
	inString := false
	escaped := false
	stringIsKey := false

	for i := 0; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				out.WriteByte(ch)
				escaped = false
				continue
			}
			if ch == '\\' {
				// A backslash cannot legally escape a raw control byte. Preserve
				// the backslash as data, then let the next iteration escape the
				// control byte itself.
				if i+1 < len(text) && text[i+1] < 0x20 {
					out.WriteString(`\\`)
					continue
				}
				out.WriteByte(ch)
				escaped = true
				continue
			}
			if ch < 0x20 {
				writeReviewerJSONControlEscape(&out, ch)
				continue
			}
			if ch != '"' {
				out.WriteByte(ch)
				continue
			}

			if reviewerQuoteClosesString(text, i, stringIsKey, stack) {
				out.WriteByte(ch)
				inString = false
				continue
			}
			out.WriteString(`\"`)
			continue
		}

		switch ch {
		case '"':
			if len(stack) == 0 {
				return ""
			}
			out.WriteByte(ch)
			inString = true
			escaped = false
			top := stack[len(stack)-1]
			stringIsKey = top.kind == '{' && top.expectingKey
		case '{':
			out.WriteByte(ch)
			stack = append(stack, reviewerJSONFrame{kind: '{', expectingKey: true})
		case '[':
			out.WriteByte(ch)
			stack = append(stack, reviewerJSONFrame{kind: '['})
		case '}':
			if len(stack) == 0 || stack[len(stack)-1].kind != '{' {
				return ""
			}
			out.WriteByte(ch)
			stack = stack[:len(stack)-1]
			if len(stack) == 0 && i != len(text)-1 {
				return ""
			}
		case ']':
			if len(stack) == 0 || stack[len(stack)-1].kind != '[' {
				return ""
			}
			out.WriteByte(ch)
			stack = stack[:len(stack)-1]
		case ':':
			out.WriteByte(ch)
			if len(stack) > 0 && stack[len(stack)-1].kind == '{' {
				stack[len(stack)-1].expectingKey = false
			}
		case ',':
			out.WriteByte(ch)
			if len(stack) > 0 && stack[len(stack)-1].kind == '{' {
				stack[len(stack)-1].expectingKey = true
			}
		default:
			out.WriteByte(ch)
		}
	}

	if inString || escaped || len(stack) != 0 {
		return ""
	}
	return out.String()
}

func reviewerQuoteClosesString(text string, quote int, isKey bool, stack []reviewerJSONFrame) bool {
	next := skipReviewerJSONSpace(text, quote+1)
	if isKey {
		return next < len(text) && text[next] == ':'
	}
	if len(stack) == 0 || next >= len(text) {
		return false
	}

	top := stack[len(stack)-1].kind
	switch text[next] {
	case '}':
		return top == '{'
	case ']':
		return top == '['
	case ',':
		afterComma := skipReviewerJSONSpace(text, next+1)
		if afterComma >= len(text) {
			return false
		}
		if top == '{' {
			return looksLikeReviewerJSONMember(text, afterComma)
		}
		return top == '[' && startsReviewerJSONValue(text[afterComma])
	default:
		return false
	}
}

func looksLikeReviewerJSONMember(text string, start int) bool {
	if start >= len(text) || text[start] != '"' {
		return false
	}
	escaped := false
	for i := start + 1; i < len(text); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch text[i] {
		case '\\':
			escaped = true
		case '"':
			next := skipReviewerJSONSpace(text, i+1)
			return next < len(text) && text[next] == ':'
		case '\n', '\r':
			return false
		}
	}
	return false
}

func startsReviewerJSONValue(ch byte) bool {
	return ch == '"' || ch == '{' || ch == '[' || ch == '-' ||
		(ch >= '0' && ch <= '9') || ch == 't' || ch == 'f' || ch == 'n'
}

func skipReviewerJSONSpace(text string, i int) int {
	for i < len(text) {
		switch text[i] {
		case ' ', '\n', '\r', '\t':
			i++
		default:
			return i
		}
	}
	return i
}

func writeReviewerJSONControlEscape(out *strings.Builder, ch byte) {
	switch ch {
	case '\b':
		out.WriteString(`\b`)
	case '\f':
		out.WriteString(`\f`)
	case '\n':
		out.WriteString(`\n`)
	case '\r':
		out.WriteString(`\r`)
	case '\t':
		out.WriteString(`\t`)
	default:
		const hex = "0123456789abcdef"
		out.WriteString(`\u00`)
		out.WriteByte(hex[ch>>4])
		out.WriteByte(hex[ch&0x0f])
	}
}
