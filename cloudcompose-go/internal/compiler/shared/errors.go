package shared

// CloudComposeError is a user-facing error the CLI can print cleanly (message,
// optional details) without a stack trace, as opposed to an unexpected/
// system error that gets shown with more diagnostic detail (and, with
// CLOUDCOMPOSE_DEBUG set, a full stack trace -- Go's own runtime.Error panics
// serve the equivalent role here).
//
// Not implemented as a full parallel exception hierarchy (separate
// ValidationError, CompilationError, InferenceError, ParseError,
// EnvironmentError, NetworkError, StorageError, IngressError,
// ScheduleError types all embedding CloudComposeError): none of those would
// be distinguished anywhere by type in the CLI's error-handling paths --
// only the base CloudComposeError is ever caught and handled specially. A
// design that added nine subclasses no code path actually discriminates
// on would be adding unused API surface, not behavior.
type CloudComposeError struct {
	Message string
	Details string
}

func (e *CloudComposeError) Error() string {
	if e.Details != "" {
		return e.Message + "\n\nDetails: " + e.Details
	}
	return e.Message
}

// NewCloudComposeError constructs a CloudComposeError with no details, the common
// case.
func NewCloudComposeError(message string) *CloudComposeError {
	return &CloudComposeError{Message: message}
}

// NewCloudComposeErrorWithDetails constructs a CloudComposeError carrying
// additional detail to show beneath the main message.
func NewCloudComposeErrorWithDetails(message, details string) *CloudComposeError {
	return &CloudComposeError{Message: message, Details: details}
}
