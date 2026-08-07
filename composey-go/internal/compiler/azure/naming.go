package azure

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Azure resource naming, mirroring compiler/inference/azure/naming.py.
//
// Azure constrains names per resource type, and the constraints disagree
// with each other: a container registry takes alphanumerics only, a
// storage account takes lowercase alphanumerics, a key vault takes dashes
// too. All three are globally unique across Azure and capped well below
// the length of a readable "{environment}-{application}-{resource}" name.
//
// When a name is too long it is truncated and given a short digest of the
// full name. Plain truncation would silently collide:
// "nginx-flask-mysql" and "nginx-flask-mysqlx" share a prefix and would
// otherwise land on the same key vault, with the second deployment failing
// against the first's resource. Names that already fit are left alone, so
// the common case stays readable.

const azureDigestLen = 6

// azureDigest is a short, stable discriminator for names that have to be
// truncated. hashlib.sha256(value.encode()).hexdigest()[:6] in Python;
// crypto/sha256 + hex.EncodeToString, sliced to 6 chars, matches
// byte-for-byte since both operate on the UTF-8 encoding of the string.
func azureDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:azureDigestLen]
}

// azureFit forces candidate within Azure's length bounds without losing
// uniqueness.
func azureFit(candidate, full string, minLen, maxLen int, sep string) string {
	if len(candidate) > maxLen {
		keep := maxLen - azureDigestLen - len(sep)
		candidate = strings.TrimRight(candidate[:keep], "-") + sep + azureDigest(full)
	}
	if len(candidate) < minLen {
		candidate = (candidate + azureDigest(full))
		if len(candidate) > maxLen {
			candidate = candidate[:maxLen]
		}
	}
	return candidate
}

var (
	nonAlphanumeric       = regexp.MustCompile(`[^a-zA-Z0-9]`)
	nonLowerAlphanumeric  = regexp.MustCompile(`[^a-z0-9]`)
	nonAlphanumericOrDash = regexp.MustCompile(`[^a-zA-Z0-9-]`)
	repeatedDashes        = regexp.MustCompile(`-+`)
)

// ContainerRegistryName names an azurerm_container_registry: 5-50
// alphanumeric characters. Dashes and underscores are not merely
// discouraged here, they are rejected: "alpha numeric characters only are
// allowed in name".
func ContainerRegistryName(envName, appName string) string {
	full := envName + "-" + appName + "-acr"
	candidate := nonAlphanumeric.ReplaceAllString(full, "")
	return azureFit(candidate, full, 5, 50, "")
}

// StorageAccountName names an azurerm_storage_account: 3-24 lowercase
// alphanumeric characters.
func StorageAccountName(envName, appName, serviceName string) string {
	full := envName + "-" + appName + "-" + serviceName
	candidate := nonLowerAlphanumeric.ReplaceAllString(strings.ToLower(full), "")
	return azureFit(candidate, full, 3, 24, "")
}

// KeyVaultName names an azurerm_key_vault: 3-24 characters, alphanumerics
// and dashes, starting with a letter and not ending with one.
func KeyVaultName(envName, appName string) string {
	full := envName + "-" + appName + "-kv"
	candidate := nonAlphanumericOrDash.ReplaceAllString(full, "-")
	candidate = strings.Trim(repeatedDashes.ReplaceAllString(candidate, "-"), "-")
	if candidate == "" || !isAlpha(candidate[0]) {
		candidate = "kv-" + candidate
	}
	return strings.Trim(azureFit(candidate, full, 3, 24, "-"), "-")
}

// FrontDoorProfileName names an azurerm_cdn_frontdoor_profile: no
// documented length cap below 260 characters, but kept consistent with
// this file's dash-joined, digest-on-collision convention rather than
// relying on that.
func FrontDoorProfileName(envName, appName string) string {
	full := envName + "-" + appName + "-fd"
	candidate := nonAlphanumericOrDash.ReplaceAllString(full, "-")
	candidate = strings.Trim(repeatedDashes.ReplaceAllString(candidate, "-"), "-")
	return strings.Trim(azureFit(candidate, full, 1, 64, "-"), "-")
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
