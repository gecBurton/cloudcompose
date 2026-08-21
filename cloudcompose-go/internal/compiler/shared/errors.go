package shared

// CloudComposeError is a user-facing error the CLI can print cleanly (message,
// optional details) without a stack trace, as opposed to an unexpected
// system error shown with more diagnostic detail.
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
