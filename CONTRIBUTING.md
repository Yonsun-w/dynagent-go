# Contributing

Thanks for considering contributions to DynAgent.

## Development Principles

- Keep the runtime topology-free.
- Do not introduce third-party agent orchestration frameworks.
- Do not let nodes mutate master state directly.
- Prefer explicit contracts over hidden conventions.
- Preserve production-grade error handling and observability.

## Local Workflow

```bash
go mod tidy
go test ./...
go run ./cmd/demo --config ./configs/config.yaml
```

## Pull Request Rules

- Keep changes scoped.
- Add or update tests for core-path behavior.
- Update docs when public behavior, APIs, or architecture changes.
- Avoid introducing magic values; wire new settings through config.

## Areas That Need Help

- richer Postgres/Redis integration testing
- vector-memory backend abstraction
- more builtin generic nodes
- benchmark suite and load testing
- API versioning and SDKs
