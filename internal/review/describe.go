package review

import (
	"strings"
	"unicode/utf8"
)

// Task is the role and answer shape of one reviewer CLI call. The zero value is a review.
type Task struct{ System, Schema string }

func (t Task) orReview() Task {
	if t.System == "" {
		return Task{SystemPrompt, Schema}
	}
	return t
}

// MaxTitle is the longest PR title Verify accepts.
const MaxTitle = 72

// DescribeTask asks for a PR title and description. It is not a review.
var DescribeTask = Task{System: DescribePrompt, Schema: DescribeSchema}

// DescribePrompt is the role and rules for composing a PR title and description.
const DescribePrompt = `You write the title and description of a pull request. You receive a packet with the commit messages and a unified diff of the whole branch. You have no tools and cannot run anything.

The title is under 72 characters and in the imperative mood, for example "Add retry to the sync worker". It names the work as a whole, never the first commit alone.

The description is three to eight sentences of plain prose. State what changed for the user of the repository and why. Then state what the maintainer inherits, such as new behaviour to keep working, new configuration or a removed capability. Use no bullet lists, no headings, no em dashes and no colons inside sentences. Describe only what the commit messages and diff show.

Answer only with JSON matching the supplied schema.`

// DescribeSchema is the answer shape for DescribeTask, valid for Claude and for codex strict outputs.
const DescribeSchema = `{"type":"object","additionalProperties":false,"required":["title","description"],"properties":{"title":{"type":"string"},"description":{"type":"string"}}}`

// WithTask returns a copy of r that runs t instead of a review.
func WithTask(r Reviewer, t Task) Reviewer {
	switch v := r.(type) {
	case *Claude:
		c := *v
		c.Task = t
		return &c
	case *Codex:
		c := *v
		c.Task = t
		return &c
	}
	return r
}

// ParseDescription strictly decodes the answer to DescribeTask.
func ParseDescription(raw []byte) (title, description string, err error) {
	var out struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := decodeStrict(raw, &out); err != nil {
		return "", "", err
	}
	title, description = strings.TrimSpace(out.Title), strings.TrimSpace(out.Description)
	switch {
	case title == "" || description == "":
		return "", "", invalid("description needs a title and a body")
	case strings.ContainsAny(title, "\r\n"):
		return "", "", invalid("title must be one line")
	case utf8.RuneCountInString(title) > MaxTitle:
		return "", "", invalid("title is longer than %d characters", MaxTitle)
	}
	return title, description, nil
}
