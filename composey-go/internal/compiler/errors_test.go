package compiler

import "testing"

func TestComposeyError_ErrorFormatsWithoutDetails(t *testing.T) {
	t.Parallel()
	err := NewComposeyError("something went wrong")
	if err.Error() != "something went wrong" {
		t.Errorf("Error() = %q, want 'something went wrong'", err.Error())
	}
}

func TestComposeyError_ErrorFormatsWithDetails(t *testing.T) {
	t.Parallel()
	err := NewComposeyErrorWithDetails("something went wrong", "extra context here")
	want := "something went wrong\n\nDetails: extra context here"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}
