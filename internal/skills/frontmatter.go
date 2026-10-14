package skills

import "strings"

// parseFrontmatter extracts the keys of a leading YAML frontmatter block
// (--- ... ---). Only the keys a skill needs are interpreted; everything
// else is ignored. Values may be plain, single/double quoted, or block
// scalars: "|" keeps newlines, ">" folds continuation lines with spaces.
func parseFrontmatter(content string) map[string]string {
	body, fm := splitFrontmatter(content)
	_ = body
	return fm
}

// stripFrontmatter returns the content without its leading frontmatter
// block.
func stripFrontmatter(content string) string {
	body, _ := splitFrontmatter(content)
	return body
}

// splitFrontmatter splits a document into body and parsed frontmatter
// pairs. When no block is present the whole content is the body.
func splitFrontmatter(content string) (string, map[string]string) {
	fm := map[string]string{}
	text := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") && strings.TrimSpace(text) != "---" {
		return content, fm
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return content, fm
	}
	block := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")

	lines := strings.Split(block, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue // continuation line, handled by the block scalar below
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if value == "|" || value == ">" {
			// Block scalar: consume the following more-indented lines.
			var parts []string
			for i+1 < len(lines) {
				next := lines[i+1]
				if strings.TrimSpace(next) == "" {
					parts = append(parts, "")
					i++
					continue
				}
				if next[0] != ' ' && next[0] != '\t' {
					break
				}
				parts = append(parts, strings.TrimSpace(next))
				i++
			}
			if value == "|" {
				fm[key] = strings.TrimRight(strings.Join(parts, "\n"), "\n")
			} else {
				fm[key] = strings.Join(parts, " ")
			}
			continue
		}
		fm[key] = unquote(value)
	}
	return body, fm
}

// unquote strips one layer of single or double quotes.
func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
