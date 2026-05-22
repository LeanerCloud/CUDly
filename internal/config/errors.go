package config

import "errors"

// ErrNotFound is returned when a requested config-store row does not exist.
var ErrNotFound = errors.New("not found")

// ErrExecutionNotInExpectedStatus is returned by TransitionExecutionStatus
// when the target execution exists but its current status is not in the
// allowed `fromStatuses` set — i.e. the atomic CAS rejected because some
// other writer transitioned the row first (e.g. the real executor finished
// between the reaper's SELECT and CAS). Callers can use errors.Is to
// distinguish this legitimate race-loss from a hard DB error.
var ErrExecutionNotInExpectedStatus = errors.New("execution not in expected status")
