format:
	cd cloudcompose-go && gofmt -w .

vet:
	cd cloudcompose-go && go vet ./...

test:
	cd cloudcompose-go && go test ./...

build:
	cd cloudcompose-go && go build -o cloudcompose ./cmd/cloudcompose
