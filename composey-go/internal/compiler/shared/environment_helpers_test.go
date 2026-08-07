package shared

import "testing"

// TestTfName_SanitizesEnvironmentNames mirrors _tf_name.
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
// values, mirroring _cidrsubnet.
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
