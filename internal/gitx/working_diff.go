package gitx

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DiffWorkingTree compares the index and working files to HEAD, then includes
// untracked regular text files. It never stages or modifies user files.
func (r *Repo) DiffWorkingTree(ctx context.Context) (*LineDiff, error) {
	out, err := r.run(ctx, "diff", "--unified=0", "--no-color", "--no-renames", "--no-ext-diff", "--no-textconv", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	d, err := ParseLineDiff(string(out))
	if err != nil {
		return nil, err
	}
	files, err := r.run(ctx, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	for p := range strings.SplitSeq(string(files), "\x00") {
		if p == "" {
			continue
		}
		lines, err := r.readUntrackedLines(p)
		if err != nil {
			return nil, err
		}
		d.NewFiles = append(d.NewFiles, p)
		for i, line := range lines {
			d.Added = append(d.Added, Line{Path: p, Number: i + 1, Text: line})
		}
	}
	return d, nil
}

func (r *Repo) readUntrackedLines(p string) ([]string, error) {
	if !validPath(p) {
		return nil, fmt.Errorf("invalid untracked path %q", p)
	}
	// #nosec G304 -- Path is git ls-files output and checked repository-relative.
	info, err := os.Lstat(filepath.Join(r.Root, p))
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	// #nosec G304 -- Path is git ls-files output, validated and lstat checked.
	f, err := os.Open(filepath.Join(r.Root, p))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	if info.Size() > 16<<20 {
		return nil, fmt.Errorf("untracked file exceeds guard scan bound: %s", p)
	}
	data, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("untracked file grew beyond guard scan bound: %s", p)
	}
	if strings.ContainsRune(string(data), 0) {
		return nil, nil
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}
