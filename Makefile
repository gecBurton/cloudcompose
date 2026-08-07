format:
	cd composey-go && gofmt -w .

vet:
	cd composey-go && go vet ./...

test:
	cd composey-go && go test ./...

build:
	cd composey-go && go build -o composey ./cmd/composey
