package agent

import (
	"fmt"
	"io"
)

// Logger keeps output to the journal deliberately narrow: variable names and counts
// are useful for debugging, values never are.
type Logger struct {
	Out io.Writer
	Err io.Writer
}

func (l Logger) Infof(format string, args ...any) {
	_, _ = fmt.Fprintf(l.Out, "secrets-agent: "+format+"\n", args...)
}

func (l Logger) Warnf(format string, args ...any) {
	_, _ = fmt.Fprintf(l.Err, "secrets-agent: "+format+"\n", args...)
}
