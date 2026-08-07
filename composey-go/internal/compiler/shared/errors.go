package shared

// ComposeyError mirrors composey/exceptions.py's ComposeyError: a
// user-facing error the CLI can print cleanly (message, optional details)
// without a stack trace, as opposed to an unexpected/system error that
// gets shown with more diagnostic detail (and, with COMPOSEY_DEBUG set, a
// full stack trace on the Python side -- Go's own runtime.Error panics
// serve the equivalent role here, since Go doesn't unwind with a
// traceback the way Python does for a plain exception).
//
// Not ported as a full parallel exception hierarchy (ValidationError,
// CompilationError, InferenceError, ParseError, EnvironmentError,
// NetworkError, StorageError, IngressError, ScheduleError all inheriting
// from ComposeyError in Python): none of those Python subclasses are
// distinguished anywhere by type in composey/cli.py's except clauses --
// only the base ComposeyError is ever caught and handled specially. A Go
// port that added nine subclasses no code path actually discriminates on
// would be replicating unused API surface, not behavior.
type ComposeyError struct {
	Message string
	Details string
}

func (e *ComposeyError) Error() string {
	if e.Details != "" {
		return e.Message + "\n\nDetails: " + e.Details
	}
	return e.Message
}

// NewComposeyError constructs a ComposeyError with no details, the common
// case.
func NewComposeyError(message string) *ComposeyError {
	return &ComposeyError{Message: message}
}

// NewComposeyErrorWithDetails constructs a ComposeyError carrying
// additional detail to show beneath the main message, mirroring
// ComposeyError(message, details=...) in Python.
func NewComposeyErrorWithDetails(message, details string) *ComposeyError {
	return &ComposeyError{Message: message, Details: details}
}
