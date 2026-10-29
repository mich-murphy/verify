package gitx

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// Line is one added or removed line with its path. Number is the new-file line
// for added lines and the old-file line for removed lines.
type Line struct {
	Path   string
	Number int
	Text   string
}

// LineDiff is a zero-context diff reduced to what the guards need.
type LineDiff struct {
	Added        []Line
	Removed      []Line
	DeletedFiles []string // paths removed entirely
	NewFiles     []string // paths created
	Modified     []string // paths that existed on both sides and changed
}

// DiffLines parses `git diff --unified=0 base sha` (no renames, no external
// diff or textconv).
func (r *Repo) DiffLines(ctx context.Context, base, sha string) (*LineDiff, error) {
	b, err := r.Resolve(ctx, base)
	if err != nil {
		return nil, err
	}
	s, err := r.Resolve(ctx, sha)
	if err != nil {
		return nil, err
	}
	out, err := r.run(ctx, "diff", "--unified=0", "--no-color", "--no-renames", "--no-ext-diff", "--no-textconv", b, s, "--")
	if err != nil {
		return nil, err
	}
	return ParseLineDiff(string(out))
}

// ParseLineDiff parses unified=0 diff text.
//
//nolint:gocognit,gocyclo // single-pass line state machine; splitting it would scatter shared cursor state.
func ParseLineDiff(text string) (*LineDiff, error) {
	d := &LineDiff{}
	var oldPath, newPath string
	var oldN, newN, oldLeft, newLeft int
	inFile, isNew, isDel := false, false, false
	var headPath string
	flush := func() {
		if !inFile {
			return
		}
		if oldPath == "" && newPath == "" { // binary or mode-only change
			oldPath, newPath = headPath, headPath
			if isNew {
				oldPath = ""
			}
			if isDel {
				newPath = ""
			}
		}
		switch {
		case headPath == "" && oldPath == "" && newPath == "":
		case newPath == "":
			d.DeletedFiles = append(d.DeletedFiles, oldPath)
		case oldPath == "":
			d.NewFiles = append(d.NewFiles, newPath)
		default:
			d.Modified = append(d.Modified, newPath)
		}
	}
	for line := range strings.SplitSeq(text, "\n") {
		if oldLeft > 0 || newLeft > 0 {
			switch {
			case strings.HasPrefix(line, "+") && newLeft > 0:
				d.Added = append(d.Added, Line{Path: newPath, Number: newN, Text: line[1:]})
				newN++
				newLeft--
				continue
			case strings.HasPrefix(line, "-") && oldLeft > 0:
				d.Removed = append(d.Removed, Line{Path: oldPath, Number: oldN, Text: line[1:]})
				oldN++
				oldLeft--
				continue
			case strings.HasPrefix(line, `\`):
				continue
			}
			return nil, errors.New("unparseable git diff hunk")
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			inFile, oldPath, newPath, isNew, isDel = true, "", "", false, false
			headPath = headerPath(line[len("diff --git "):])
			if headPath == "" {
				return nil, errors.New("unparseable git diff file header")
			}
		case strings.HasPrefix(line, "new file mode"):
			isNew = true
		case strings.HasPrefix(line, "deleted file mode"):
			isDel = true
		case strings.HasPrefix(line, "--- "):
			oldPath = diffPath(line[4:], "a/")
		case strings.HasPrefix(line, "+++ "):
			newPath = diffPath(line[4:], "b/")
		case strings.HasPrefix(line, "@@ "):
			var err error
			if oldN, oldLeft, newN, newLeft, err = parseHunk(line); err != nil {
				return nil, err
			}
		}
	}
	flush()
	return d, nil
}

func diffPath(s, prefix string) string {
	if s == "/dev/null" {
		return ""
	}
	// Git appends a literal tab after unquoted paths containing spaces. Remove
	// that delimiter before decoding, never a tab belonging to a quoted name.
	s = strings.TrimSuffix(s, "\t")
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			s = u
		}
	}
	return strings.TrimPrefix(s, prefix)
}

// parseHunk reads "@@ -a[,b] +c[,d] @@". With unified=0 a zero count means the
// start line is the line before the change; the counter is then unused.
func parseHunk(h string) (oldStart, oldCount, newStart, newCount int, err error) {
	f := strings.Fields(h)
	if len(f) < 3 || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return 0, 0, 0, 0, errors.New("unparseable git hunk header")
	}
	if oldStart, oldCount, err = rangeOf(f[1][1:]); err != nil {
		return 0, 0, 0, 0, err
	}
	newStart, newCount, err = rangeOf(f[2][1:])
	return oldStart, oldCount, newStart, newCount, err
}

func rangeOf(s string) (start, count int, err error) {
	a, b, hasCount := strings.Cut(s, ",")
	count = 1
	if hasCount {
		if count, err = strconv.Atoi(b); err != nil {
			return 0, 0, err
		}
	}
	start, err = strconv.Atoi(a)
	return start, count, err
}

// headerPath extracts the identical old/new path from a no-renames header.
func headerPath(h string) string {
	if strings.HasPrefix(h, `"`) {
		// Git C-quotes both paths when either needs quoting. Find the closing
		// quote while respecting escaped backslashes and escaped quotes.
		for i := 1; i < len(h); i++ {
			if h[i] == '\\' {
				i++
				continue
			}
			if h[i] != '"' {
				continue
			}
			old, err := strconv.Unquote(h[:i+1])
			if err != nil || i+2 >= len(h) || h[i+1] != ' ' {
				return ""
			}
			newPath, err := strconv.Unquote(h[i+2:])
			if err != nil || !strings.HasPrefix(old, "a/") || newPath != "b/"+old[2:] {
				return ""
			}
			return old[2:]
		}
		return ""
	}
	n := (len(h) - len("a/ b/")) / 2
	if n <= 0 || len(h) != 2*n+len("a/ b/") {
		return ""
	}
	p := h[2 : 2+n]
	if h != "a/"+p+" b/"+p {
		return ""
	}
	return p
}
