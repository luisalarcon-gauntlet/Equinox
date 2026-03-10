.PHONY: dev build test test-coverage lint clean

dev:
	go run main.go

build:
	go build -o equinox main.go

test:
	go test ./... -v

test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

lint:
	go vet ./...

clean:
	rm -f equinox coverage.out
