package network

import "errors"

// MissingState authorizes repair only after successful inspection.
type MissingState struct {
	detail  string
	command string
	args    []string
}

func (e *MissingState) Error() string { return e.detail }
func IsMissingState(err error) bool   { var missing *MissingState; return errors.As(err, &missing) }
func missingState(detail, command string, args ...string) error {
	return &MissingState{detail: detail, command: command, args: args}
}
