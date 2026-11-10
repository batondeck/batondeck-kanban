//go:build !linux && !darwin

package core

import "errors"

var errNoProcWatch = errors.New("no process ownership or exit watch on this platform")

// ProcessUID has no implementation here, so no pid can register.
func ProcessUID(int) (int, error) { return 0, errNoProcWatch }

// ProcessParent has no implementation here.
func ProcessParent(int) (int, string, error) { return 0, "", errNoProcWatch }

func newExitImpl(chan<- int) (exitImpl, error) { return nil, errNoProcWatch }
