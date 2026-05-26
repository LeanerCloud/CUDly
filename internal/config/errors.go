package config

import "errors"

// ErrNotFound is returned when a requested config-store row does not exist.
var ErrNotFound = errors.New("not found")

// ErrExecutionNotInExpectedStatus is returned by TransitionExecutionStatus
// when the target execution exists but its current status is not in the
// allowed `fromStatuses` set -- i.e. the atomic CAS rejected because some
// other writer transitioned the row first (e.g. the real executor finished
// between the reaper's SELECT and CAS). Callers can use errors.Is to
// distinguish this legitimate race-loss from a hard DB error.
var ErrExecutionNotInExpectedStatus = errors.New("execution not in expected status")

// ErrAuditLoss is returned (wrapped) by executeAndFinalize when the purchase
// run itself completed but the subsequent SavePurchaseExecution call failed.
// The execution is already "running" (per the CAS in claimAndRedrive) but its
// final state was never persisted -- the row is stranded in "running" until
// the next recovery sweep. Callers that silence all drive errors (e.g.
// claimAndRedrive) must propagate this sentinel so the sweep surfaces the
// persistence failure rather than silently dropping the stranded row.
var ErrAuditLoss = errors.New("audit loss: execution persistence failed after purchase")
