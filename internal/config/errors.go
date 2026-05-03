package config

import "errors"

// ErrNotFound is returned when a requested config-store row does not exist.
var ErrNotFound = errors.New("not found")
