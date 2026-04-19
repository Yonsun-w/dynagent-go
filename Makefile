APP_NAME := dynagent

.PHONY: tidy test coverage run demo node lint

tidy:
	go mod tidy

test:
	go test ./...

coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

run:
	go run ./cmd/server --config ./configs/config.yaml

demo:
	go run ./cmd/demo --config ./configs/config.yaml

node:
	go run ./cmd/node-runner --manifest ./configs/nodes.d/external_echo.yaml

lint:
	go test ./...
