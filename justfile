default:
    @just --list

build:
    go build -o mastbench ./cmd/mastbench

test:
    go test -race -v ./... -covermode=atomic -coverprofile=coverage.out

lint:
    golangci-lint run -c .golangci.yml

tidy:
    go mod tidy

# A quick run against a broker on localhost.
smoke BROKER="tcp://127.0.0.1:1883":
    go run ./cmd/mastbench connect --broker {{ BROKER }} --clients 200 --hold 5s
    go run ./cmd/mastbench throughput --broker {{ BROKER }} --publishers 5 --rate 100 --duration 10s
