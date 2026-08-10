package shared

import "testing"

func TestCloudComposeError_ErrorFormatsWithoutDetails(t *testing.T) {
	t.Parallel()
	err := NewCloudComposeError("something went wrong")
	if err.Error() != "something went wrong" {
		t.Errorf("Error() = %q, want 'something went wrong'", err.Error())
	}
}

func TestCloudComposeError_ErrorFormatsWithDetails(t *testing.T) {
	t.Parallel()
	err := NewCloudComposeErrorWithDetails("something went wrong", "extra context here")
	want := "something went wrong\n\nDetails: extra context here"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}
