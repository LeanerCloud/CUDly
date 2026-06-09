package execution

// Collect aggregates the Value fields from all successful results into a flat slice.
func Collect[T any](results []Result[T]) []T {
	out := make([]T, 0, len(results))
	for _, r := range results {
		if r.Err == nil {
			out = append(out, r.Value)
		}
	}
	return out
}

// CollectErrors returns a slice of errors from failed results.
func CollectErrors[T any](results []Result[T]) []error {
	var errs []error
	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, r.Err)
		}
	}
	return errs
}

// FirstError returns the first non-nil error from results, or nil if all succeeded.
func FirstError[T any](results []Result[T]) error {
	for _, r := range results {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}
