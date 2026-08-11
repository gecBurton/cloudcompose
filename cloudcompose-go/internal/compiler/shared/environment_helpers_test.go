package shared

import "testing"

// TestTfName_SanitizesEnvironmentNames verifies TfName's sanitization.
func TestTfName_SanitizesEnvironmentNames(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"prod", "prod"},
		{"my-env", "my_env"},
		{"123env", "_123env"},
		{"my.env!", "my_env_"},
	}
	for _, tc := range cases {
		if got := TfName(tc.in); got != tc.want {
			t.Errorf("TfName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCidrsubnet_MatchesTerraformLogic pins cidrsubnet against known
// values.
func TestCidrsubnet_MatchesTerraformLogic(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base    string
		newbits int
		netnum  int
		want    string
	}{
		{"10.0.0.0/16", 4, 0, "10.0.0.0/20"},
		{"10.0.0.0/16", 4, 1, "10.0.16.0/20"},
		{"10.0.0.0/16", 4, 2, "10.0.32.0/20"},
		{"10.0.0.0/16", 5, 0, "10.0.0.0/21"},
		{"10.0.0.0/16", 5, 1, "10.0.8.0/21"},
	}
	for _, tc := range cases {
		got, err := Cidrsubnet(tc.base, tc.newbits, tc.netnum)
		if err != nil {
			t.Fatalf("Cidrsubnet(%s, %d, %d) failed: %v", tc.base, tc.newbits, tc.netnum, err)
		}
		if got != tc.want {
			t.Errorf("Cidrsubnet(%s, %d, %d) = %q, want %q", tc.base, tc.newbits, tc.netnum, got, tc.want)
		}
	}
}

// TestCidrsubnet_MatchesTerraformsOwnDocumentedExample pins against the
// exact example in Terraform's own cidrsubnet documentation
// (developer.hashicorp.com/terraform/language/functions/cidrsubnet),
// not just internally-consistent values.
func TestCidrsubnet_MatchesTerraformsOwnDocumentedExample(t *testing.T) {
	t.Parallel()
	got, err := Cidrsubnet("10.1.2.0/24", 4, 15)
	if err != nil {
		t.Fatalf("Cidrsubnet failed: %v", err)
	}
	if got != "10.1.2.240/28" {
		t.Errorf("Cidrsubnet(10.1.2.0/24, 4, 15) = %q, want 10.1.2.240/28 (Terraform's own documented example)", got)
	}
}

// TestCidrsubnet_RejectsNetnumBeyondNewbitsCapacity is a regression test
// for a real bug found while implementing docs/azure-app-isolation-design.md's
// --subnet-index: netnum=128 with newbits=7 used to silently compute
// 10.1.0.0/24 -- a full range past the intended 10.0.128.0/17 block,
// with no error at all. Confirmed against Terraform's own documentation
// ("netnum is a whole number that can be represented as a binary
// integer with no more than newbits binary digits") that this should be
// a hard error, matching what Terraform's real cidrsubnet does, not
// silent wraparound into whatever address space happens to be next.
func TestCidrsubnet_RejectsNetnumBeyondNewbitsCapacity(t *testing.T) {
	t.Parallel()
	if _, err := Cidrsubnet("10.0.128.0/17", 7, 128); err == nil {
		t.Fatalf("expected an error for netnum=128 with newbits=7 (128 needs 8 bits, not 7)")
	}
}

func TestCidrsubnet_AcceptsNetnumAtExactCapacityBoundary(t *testing.T) {
	t.Parallel()
	got, err := Cidrsubnet("10.0.128.0/17", 7, 127)
	if err != nil {
		t.Fatalf("Cidrsubnet(netnum=127, newbits=7) should succeed (127 fits in 7 bits): %v", err)
	}
	if got != "10.0.255.0/24" {
		t.Errorf("got %q, want 10.0.255.0/24", got)
	}
}

func TestCidrsubnet_RejectsNegativeNetnum(t *testing.T) {
	t.Parallel()
	if _, err := Cidrsubnet("10.0.0.0/16", 4, -1); err == nil {
		t.Fatalf("expected an error for a negative netnum")
	}
}
