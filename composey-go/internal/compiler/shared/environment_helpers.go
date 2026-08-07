package shared

import (
	"fmt"
	"net"
	"regexp"
)

// Shared helpers for the "platform" shared-infrastructure generators
// (VPC/network/cluster bootstrap used by `composey init`), used by all
// three clouds' environment generators.

var tfNameInvalidChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// TfName converts an environment name to a valid Terraform resource name:
// Terraform resource names must start with a letter or underscore and
// contain only letters, digits, and underscores.
func TfName(name string) string {
	result := tfNameInvalidChars.ReplaceAllString(name, "_")
	if result != "" && result[0] >= '0' && result[0] <= '9' {
		result = "_" + result
	}
	return result
}

// Cidrsubnet calculates a subnet CIDR using Terraform's cidrsubnet logic.
// Simplified implementation that works for the common cases this module
// actually uses (IPv4, single-digit newbits).
func Cidrsubnet(baseCIDR string, newbits, netnum int) (string, error) {
	_, network, err := net.ParseCIDR(baseCIDR)
	if err != nil {
		return "", fmt.Errorf("parse CIDR %q: %w", baseCIDR, err)
	}
	ones, _ := network.Mask.Size()
	newPrefix := ones + newbits

	networkInt := ipToUint32(network.IP)
	subnetSize := uint32(1) << (32 - newPrefix)
	subnetInt := networkInt + uint32(netnum)*subnetSize

	return fmt.Sprintf("%s/%d", uint32ToIP(subnetInt), newPrefix), nil
}

func ipToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	return uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
}

func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// MergedTags merges caller-supplied tags with the fixed Name/Environment
// (or just Environment) pair every resource in the platform generators
// tags itself with. A caller tag sharing a key with a fixed one is
// overwritten by the fixed value, matching Terraform's usual "explicit
// fixed tags win" tag merge convention.
func MergedTags(tags map[string]string, fixed map[string]string) map[string]string {
	result := make(map[string]string, len(tags)+len(fixed))
	for k, v := range tags {
		result[k] = v
	}
	for k, v := range fixed {
		result[k] = v
	}
	return result
}
