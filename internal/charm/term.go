package charm

import (
	"os"
	"strings"
)

func IsDumbTerminal() bool {
	return strings.TrimSpace(os.Getenv("TERM")) == "dumb"
}
