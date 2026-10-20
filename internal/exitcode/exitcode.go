package exitcode

import "errors"

// Category describes a user-visible termination reason.
type Category string

const (
	Usage       Category = "usage"
	Declined    Category = "declined"
	NotFixable  Category = "not-fixable"
	SafetyStop  Category = "safety-stop"
	Timeout     Category = "timeout"
	Interrupted Category = "interrupted"
)

type Error struct {
	Category Category
	Err      error
	Reported bool
}

func (e *Error) Error() string {
	if e.Err == nil {
		return string(e.Category)
	}
	return e.Err.Error()
}
func (e *Error) Unwrap() error               { return e.Err }
func New(category Category, err error) error { return &Error{Category: category, Err: err} }
func NewReported(category Category, err error) error {
	return &Error{Category: category, Err: err, Reported: true}
}
func IsReported(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Reported
}
func Code(err error) int {
	if err == nil {
		return 0
	}
	var e *Error
	if !errors.As(err, &e) {
		return 5
	}
	switch e.Category {
	case Usage:
		return 2
	case Declined:
		return 3
	case NotFixable:
		return 4
	case SafetyStop:
		return 5
	case Timeout:
		return 6
	case Interrupted:
		return 130
	default:
		return 5
	}
}
