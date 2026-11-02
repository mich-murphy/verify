package gitx

import (
	"errors"
	"strconv"
	"strings"
)

// parseChangeModes joins Git's raw metadata and numstat sections from one
// bounded diff. NUL framing preserves tabs/newlines in paths. --no-renames
// makes renames separate deletions/additions, preserving both policy paths.
func parseChangeModes(out []byte) ([]Change, error) {
	if len(out) == 0 {
		return nil, nil
	}
	if out[len(out)-1] != 0 {
		return nil, errors.New("unterminated git change record")
	}
	records := strings.Split(string(out[:len(out)-1]), "\x00")
	changes, numstats, err := rawChanges(records)
	if err != nil {
		return nil, err
	}
	if err := joinNumstats(changes, numstats); err != nil {
		return nil, err
	}
	return changes, nil
}

func rawChanges(records []string) ([]Change, []string, error) {
	var changes []Change
	i := 0
	for i < len(records) && strings.HasPrefix(records[i], ":") {
		fields := strings.Fields(records[i])
		if len(fields) != 5 || !validChangeMode(strings.TrimPrefix(fields[0], ":")) || !validChangeMode(fields[1]) || len(fields[4]) != 1 || !strings.ContainsAny(fields[4], "ADMT") || i+1 >= len(records) || records[i+1] == "" {
			return nil, nil, errors.New("unparseable git raw change record")
		}
		changes = append(changes, Change{Path: records[i+1], OldMode: strings.TrimPrefix(fields[0], ":"), NewMode: fields[1]})
		i += 2
	}
	return changes, records[i:], nil
}

func joinNumstats(changes []Change, records []string) error {
	byPath := make(map[string]int, len(changes))
	for i, c := range changes {
		if _, exists := byPath[c.Path]; exists {
			return errors.New("duplicate git raw change path")
		}
		byPath[c.Path] = i
	}
	for _, rec := range records {
		counts, err := numstatChange(rec)
		if err != nil {
			return err
		}
		index, exists := byPath[counts.Path]
		if !exists {
			return errors.New("git change metadata/numstat mismatch")
		}
		changes[index].Added, changes[index].Deleted, changes[index].Binary = counts.Added, counts.Deleted, counts.Binary
		delete(byPath, counts.Path)
	}
	if len(byPath) != 0 {
		return errors.New("missing git numstat record")
	}
	return nil
}

func numstatChange(rec string) (Change, error) {
	parts := strings.SplitN(rec, "\t", 3)
	if len(parts) != 3 {
		return Change{}, errors.New("unparseable git numstat record")
	}
	c := Change{Path: parts[2]}
	if parts[0] == "-" && parts[1] == "-" {
		c.Binary = true
		return c, nil
	}
	add, errA := strconv.Atoi(parts[0])
	del, errD := strconv.Atoi(parts[1])
	if errA != nil || errD != nil || add < 0 || del < 0 {
		return Change{}, errors.New("invalid git numstat counts")
	}
	c.Added, c.Deleted = add, del
	return c, nil
}

func validChangeMode(mode string) bool {
	switch mode {
	case "000000", "100644", "100755", "120000", "160000":
		return true
	}
	return false
}
