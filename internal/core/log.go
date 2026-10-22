package core

import (
	"io"
	"log"
)

// NewLogger returns the process logger: one plain line per event, prefixed with the UTC date and
// time, written to w (stderr in every command). There is no structured format; each lane adds its
// own lines where the plan names them.
func NewLogger(w io.Writer) *log.Logger {
	return log.New(w, "", log.LstdFlags|log.LUTC)
}
