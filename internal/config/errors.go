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

// ErrRampStepIncomplete is returned (wrapped) by CompletePlanStep when the
// ramp step the completing execution belongs to has not been bought in full:
// another cloud account on that step is still outstanding, or nothing on the
// step bought at all. Counting it would report commitment the plan has not
// made (issue #1861). Transient by construction -- it clears when the
// outstanding account buys, when its row is canceled, or when the step is
// abandoned -- so callers must treat it as "not yet", not as a failure of the
// purchase that just completed.
//
// The sentinel text stays generic because both cases wrap it and each supplies
// its own specifics; naming one of them here would mis-describe the other.
var ErrRampStepIncomplete = errors.New("ramp step incomplete")

// ErrRampStepAlreadyCounted is returned (wrapped) by CompletePlanStep when the
// plan has already counted the completing step. Unlike ErrRampStepIncomplete
// this is permanent and worth an audit note on the execution row: the purchase
// bought commitment for a step the ramp will never count again, which is how a
// step_number stamped by pre-#1669 code (or during that deploy overlap)
// surfaces.
var ErrRampStepAlreadyCounted = errors.New("ramp step already counted")

// ErrAuditLoss is returned (wrapped) by executeAndFinalize when the purchase
// run itself completed but the subsequent SavePurchaseExecution call failed.
// The execution is already "running" (per the CAS in claimAndRedrive) but its
// final state was never persisted -- the row is stranded in "running" until
// the next recovery sweep. Callers that silence all drive errors (e.g.
// claimAndRedrive) must propagate this sentinel so the sweep surfaces the
// persistence failure rather than silently dropping the stranded row.
var ErrAuditLoss = errors.New("audit loss: execution persistence failed after purchase")
