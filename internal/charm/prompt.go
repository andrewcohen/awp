package charm

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	promptQuestion = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	promptLabel    = lipgloss.NewStyle().Bold(true)
	promptHintSty  = lipgloss.NewStyle().Foreground(colorMuted)
	promptCueSty   = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
)

func Confirm(in io.Reader, out io.Writer, question string, defaultYes bool) (bool, error) {
	if in == nil || out == nil {
		return false, errors.New("confirm: nil input or output")
	}
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	q := strings.TrimRight(question, " :")
	if IsInteractiveWriter(out) {
		fmt.Fprintf(out, "%s %s: ", promptQuestion.Render(q), promptHintSty.Render(suffix))
	} else {
		fmt.Fprintf(out, "%s %s: ", q, suffix)
	}
	line, err := readLine(in)
	if err != nil {
		return false, err
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	if answer == "" {
		return defaultYes, nil
	}
	return answer == "y" || answer == "yes", nil
}

func ReadLine(in io.Reader, out io.Writer, label string) (string, error) {
	if in == nil || out == nil {
		return "", errors.New("readline: nil input or output")
	}
	trimmed := strings.TrimRight(label, " :")
	if IsInteractiveWriter(out) {
		fmt.Fprintf(out, "%s: ", promptLabel.Render(trimmed))
	} else {
		fmt.Fprintf(out, "%s: ", trimmed)
	}
	line, err := readLine(in)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func PressAnyKey(in io.Reader, out io.Writer, cue string) error {
	if out != nil && cue != "" {
		if IsInteractiveWriter(out) {
			fmt.Fprint(out, promptCueSty.Render(cue))
		} else {
			fmt.Fprint(out, cue)
		}
	}
	if in == nil {
		return nil
	}
	reader := bufio.NewReader(in)
	_, err := reader.ReadByte()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if out != nil {
		fmt.Fprintln(out)
	}
	return nil
}

func readLine(in io.Reader) (string, error) {
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return line, nil
}

func IsInteractiveWriter(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}

func IsInteractiveReader(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}
