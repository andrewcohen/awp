package diff

import (
	"bufio"
	"strings"
)

// FileDiff is the diff for a single file including all hunks.
type FileDiff struct {
	OldPath string
	NewPath string
	Status  string
	Hunks   []Hunk
}

// Hunk is a parsed diff hunk.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []HunkLine
}

// HunkLine is a line within a hunk.
type HunkLine struct {
	Type    byte // '+', '-', ' '
	Content string
}

// ParseGitDiff parses git-format unified diff output from `jj diff --git`.
func ParseGitDiff(input string) []FileDiff {
	var files []FileDiff
	var current *FileDiff
	var currentHunk *Hunk

	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "diff --git "):
			if current != nil {
				if currentHunk != nil {
					current.Hunks = append(current.Hunks, *currentHunk)
					currentHunk = nil
				}
				files = append(files, *current)
			}
			current = &FileDiff{Status: "M"}
			parts := strings.SplitN(line[len("diff --git "):], " ", 2)
			if len(parts) == 2 {
				current.OldPath = strings.TrimPrefix(parts[0], "a/")
				current.NewPath = strings.TrimPrefix(parts[1], "b/")
			}

		case strings.HasPrefix(line, "--- "):
			if current == nil {
				break
			}
			p := strings.TrimPrefix(line, "--- ")
			if p == "/dev/null" {
				current.OldPath = ""
				current.Status = "A"
			} else {
				current.OldPath = strings.TrimPrefix(p, "a/")
			}

		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				break
			}
			p := strings.TrimPrefix(line, "+++ ")
			if p == "/dev/null" {
				current.NewPath = ""
				current.Status = "D"
			} else {
				current.NewPath = strings.TrimPrefix(p, "b/")
			}

		case strings.HasPrefix(line, "new file mode"):
			if current != nil {
				current.Status = "A"
			}

		case strings.HasPrefix(line, "deleted file mode"):
			if current != nil {
				current.Status = "D"
			}

		case strings.HasPrefix(line, "rename from "):
			if current != nil {
				current.OldPath = strings.TrimPrefix(line, "rename from ")
				current.Status = "R"
			}

		case strings.HasPrefix(line, "rename to "):
			if current != nil {
				current.NewPath = strings.TrimPrefix(line, "rename to ")
			}

		case strings.HasPrefix(line, "@@ "):
			if current == nil {
				break
			}
			if currentHunk != nil {
				current.Hunks = append(current.Hunks, *currentHunk)
			}
			h := parseHunkHeader(line)
			currentHunk = &h

		default:
			if current == nil || currentHunk == nil {
				break
			}
			if len(line) == 0 {
				currentHunk.Lines = append(currentHunk.Lines, HunkLine{Type: ' ', Content: ""})
				break
			}
			switch line[0] {
			case '+', '-', ' ':
				currentHunk.Lines = append(currentHunk.Lines, HunkLine{Type: line[0], Content: line[1:]})
			}
		}
	}

	if current != nil {
		if currentHunk != nil {
			current.Hunks = append(current.Hunks, *currentHunk)
		}
		files = append(files, *current)
	}

	return files
}

func parseHunkHeader(line string) Hunk {
	h := Hunk{}
	trimmed := strings.TrimPrefix(line, "@@ ")
	at := strings.Index(trimmed, " @@")
	if at < 0 {
		return h
	}
	ranges := strings.Fields(trimmed[:at])
	for _, r := range ranges {
		if strings.HasPrefix(r, "-") {
			h.OldStart, h.OldCount = parseRange(r[1:])
		} else if strings.HasPrefix(r, "+") {
			h.NewStart, h.NewCount = parseRange(r[1:])
		}
	}
	return h
}

func parseRange(s string) (start, count int) {
	parts := strings.SplitN(s, ",", 2)
	start = atoi(parts[0])
	if len(parts) == 2 {
		count = atoi(parts[1])
	} else {
		count = 1
	}
	return
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// DisplayPath returns the canonical display path for a FileDiff.
func DisplayPath(f FileDiff) string {
	if f.Status == "D" {
		return f.OldPath
	}
	if f.Status == "R" && f.OldPath != "" && f.NewPath != "" {
		return f.OldPath + " → " + f.NewPath
	}
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// FirstChangedLine returns the first new-side line number of the first hunk.
func FirstChangedLine(f FileDiff) int {
	if len(f.Hunks) == 0 {
		return 0
	}
	return f.Hunks[0].NewStart
}

// HunkChangedLine returns the first new-side changed line in a hunk.
func HunkChangedLine(h Hunk) int {
	newLine := h.NewStart
	for _, l := range h.Lines {
		if l.Type == '+' {
			return newLine
		}
		if l.Type != '-' {
			newLine++
		}
	}
	return h.NewStart
}
