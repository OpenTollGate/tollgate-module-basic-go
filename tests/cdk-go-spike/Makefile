.PHONY: build test test-network tidy

build:
	go build -tags cgo .

test:
	go test -v -count=1 ./...

test-network:
	CDK_SPIKE_NETWORK=1 go test -v -count=1 -run TestCdkGoTestmint ./...

tidy:
	go mod tidy