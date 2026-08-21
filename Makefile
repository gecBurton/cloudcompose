# disable_grpc_modules opts cloud.google.com/go/storage out of its gRPC
# transport support (and the gRPC/xds/envoy/OpenTelemetry dependency
# chain that otherwise pulls in), which this codebase never uses -- its
# own NewObjectLister only ever calls storage.NewClient's default
# JSON/XML REST transport. Every go vet/test/build below must pass this
# tag so CI, local dev, and released binaries all agree on the same
# smaller build; see cloudcompose-go/internal/compiler/gcp/backend_listing.go.
GO_TAGS := disable_grpc_modules

format:
	cd cloudcompose-go && gofmt -w .

vet:
	cd cloudcompose-go && go vet -tags $(GO_TAGS) ./...

test:
	cd cloudcompose-go && go test -tags $(GO_TAGS) ./...

build:
	cd cloudcompose-go && go build -tags $(GO_TAGS) -o cloud-compose ./cmd/cloudcompose
