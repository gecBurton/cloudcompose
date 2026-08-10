package aws

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

func intPtr(i int) *int           { return &i }
func strPtrConn(s string) *string { return &s }

func testConnections() (map[string]models.Connection, []string) {
	db := models.Connection{Host: "db.eu-west-2.rds.amazonaws.com", Port: intPtr(5432), AddressedBy: "host"}
	cache := models.Connection{Host: "cache.euw2.cache.amazonaws.com", Port: intPtr(6379), AddressedBy: "host"}
	bucket := models.Connection{
		Host:        "prod-blobs.s3.amazonaws.com",
		Name:        strPtrConn("prod-blobs"),
		AddressedBy: "name",
	}
	return map[string]models.Connection{"db": db, "cache": cache, "blobs": bucket},
		[]string{"db", "cache", "blobs"}
}

func resolve(t *testing.T, value string) string {
	t.Helper()
	connections, order := testConnections()
	return ResolveValue(value, connections, order).Value
}

func TestResolveValue_BareReferenceToDatabaseResolvesToHost(t *testing.T) {
	t.Parallel()
	connections, _ := testConnections()
	if got := resolve(t, "db"); got != connections["db"].Host {
		t.Errorf("resolve(db) = %q, want %q", got, connections["db"].Host)
	}
}

func TestResolveValue_BareReferenceToBucketResolvesToName(t *testing.T) {
	t.Parallel()
	// A bucket is addressed by name, not by host: BUCKET_NAME: blobs wants
	// the bucket, not a domain.
	connections, _ := testConnections()
	if got := resolve(t, "blobs"); got != *connections["blobs"].Name {
		t.Errorf("resolve(blobs) = %q, want %q", got, *connections["blobs"].Name)
	}
}

func TestResolveValue_URLHostSwappedPortFromConnection(t *testing.T) {
	t.Parallel()
	connections, _ := testConnections()
	want := "redis://" + connections["cache"].Host + ":6379"
	if got := resolve(t, "redis://cache"); got != want {
		t.Errorf("resolve(redis://cache) = %q, want %q", got, want)
	}
	if got := resolve(t, "redis://cache:6379"); got != want {
		t.Errorf("resolve(redis://cache:6379) = %q, want %q", got, want)
	}
}

func TestResolveValue_SchemeAndPathPreserved(t *testing.T) {
	t.Parallel()
	connections, _ := testConnections()
	want := "postgres://" + connections["db"].Host + ":5432/app"
	if got := resolve(t, "postgres://db:5432/app"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	want2 := "rediss://" + connections["cache"].Host + ":6379/0"
	if got := resolve(t, "rediss://cache/0"); got != want2 {
		t.Errorf("got %q, want %q", got, want2)
	}
}

func TestResolveValue_PortDroppedWhenConnectionDeclaresNone(t *testing.T) {
	t.Parallel()
	connections, _ := testConnections()
	want := "http://" + connections["blobs"].Host
	if got := resolve(t, "http://blobs:9000"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveValue_UserinfoPreservedWhenNoCredentials(t *testing.T) {
	t.Parallel()
	connections, _ := testConnections()
	want := "postgres://user:pw@" + connections["db"].Host + ":5432/app"
	if got := resolve(t, "postgres://user:pw@db:5432/app"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveValue_ValuesReferencingNothingAreUntouched(t *testing.T) {
	t.Parallel()
	values := []string{
		"",
		"localhost",
		"/run/secrets/db-password",
		"database", // not the service `db`
		"redis://other-cache:6379",
		"https://example.com/db", // `db` only in the path
	}
	for _, v := range values {
		if got := resolve(t, v); got != v {
			t.Errorf("resolve(%q) = %q, want unchanged", v, got)
		}
	}
}

func TestResolveValue_ServiceNamedLikeSubstringNotMatched(t *testing.T) {
	t.Parallel()
	connections := map[string]models.Connection{"db": {Host: "db.eu-west-2.rds.amazonaws.com", Port: intPtr(5432)}}
	order := []string{"db"}

	if got := ResolveValue("dbadmin", connections, order).Value; got != "dbadmin" {
		t.Errorf("got %q, want dbadmin", got)
	}
	if got := ResolveValue("http://dbadmin:80", connections, order).Value; got != "http://dbadmin:80" {
		t.Errorf("got %q, want http://dbadmin:80", got)
	}
}

func credentialedDB() (map[string]models.Connection, []string) {
	connections := map[string]models.Connection{
		"db": {
			Host:     "db.eu-west-2.rds.amazonaws.com",
			Port:     intPtr(5432),
			Username: strPtrConn("cloudcompose"),
			Password: strPtrConn("s3cret"),
			Database: strPtrConn("orders"),
		},
	}
	return connections, []string{"db"}
}

func TestResolveValue_ManagedCredentialsReplaceComposeFileOnes(t *testing.T) {
	t.Parallel()
	connections, order := credentialedDB()
	got := ResolveValue("postgres://user:pw@db:5432/app", connections, order).Value
	want := "postgres://cloudcompose:s3cret@db.eu-west-2.rds.amazonaws.com:5432/orders"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveValue_CredentialsSuppliedEvenWhenURLStatedNone(t *testing.T) {
	t.Parallel()
	connections, order := credentialedDB()
	got := ResolveValue("postgres://db/app", connections, order).Value
	want := "postgres://cloudcompose:s3cret@db.eu-west-2.rds.amazonaws.com:5432/orders"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveValue_DeployedDatabaseNameReplacesLocalOne(t *testing.T) {
	t.Parallel()
	connections, order := credentialedDB()
	got := ResolveValue("postgres://db:5432/whatever", connections, order).Value
	if got[len(got)-7:] != "/orders" {
		t.Errorf("got %q, want it to end with /orders", got)
	}
}

func TestResolveValue_QueryParametersSurviveSubstitution(t *testing.T) {
	t.Parallel()
	connections, order := credentialedDB()
	got := ResolveValue("postgres://db/app?sslmode=require", connections, order).Value
	want := "postgres://cloudcompose:s3cret@db.eu-west-2.rds.amazonaws.com:5432/orders?sslmode=require"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveValue_ValueCarryingPasswordIsConfidential(t *testing.T) {
	t.Parallel()
	connections, order := credentialedDB()
	if !ResolveValue("postgres://db/app", connections, order).Confidential {
		t.Errorf("expected confidential = true")
	}
}

func TestResolveValue_BareReferenceNeverConfidential(t *testing.T) {
	t.Parallel()
	connections, order := credentialedDB()
	resolution := ResolveValue("db", connections, order)
	if resolution.Confidential {
		t.Errorf("expected confidential = false for a bare reference")
	}
	if resolution.Value != connections["db"].Host {
		t.Errorf("got %q, want %q", resolution.Value, connections["db"].Host)
	}
}

func TestResolveValue_URLWithoutCredentialsNotConfidential(t *testing.T) {
	t.Parallel()
	connections, order := testConnections()
	if ResolveValue("redis://cache:6379", connections, order).Confidential {
		t.Errorf("expected confidential = false")
	}
}

func TestResolveValue_ServiceReferencedIsReported(t *testing.T) {
	t.Parallel()
	connections, order := testConnections()
	if got := ResolveValue("http://blobs:9000", connections, order).Service; got == nil || *got != "blobs" {
		t.Errorf("got %v, want blobs", got)
	}
	if got := ResolveValue("blobs", connections, order).Service; got == nil || *got != "blobs" {
		t.Errorf("got %v, want blobs", got)
	}
	if got := ResolveValue("nothing-to-see", connections, order).Service; got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestDefaultPort_PrefersTheConnection(t *testing.T) {
	t.Parallel()
	db := models.Connection{Host: "h", Port: intPtr(5432)}
	bucket := models.Connection{Host: "h2"} // declares no port

	if got := DefaultPort(&db, 80); got != 5432 {
		t.Errorf("got %d, want 5432", got)
	}
	if got := DefaultPort(&bucket, 443); got != 443 {
		t.Errorf("got %d, want 443", got)
	}
	if got := DefaultPort(nil, 8080); got != 8080 {
		t.Errorf("got %d, want 8080", got)
	}
}
