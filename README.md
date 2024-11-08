# Project gh-rollback-app

Simply rollback to the last successfull workflow run.

## Getting Started

### Build
```bash
go build -o main.exe cmd/api/main.go
```
### Run
```bash
go run cmd/api/main.go
```

### Watch
```bash
air
```

## MakeFile

Run build make command with tests
```bash
make all
```

Build the application
```bash
make build
```

Run the application
```bash
make run
```

Live reload the application:
```bash
make watch
```

Run the test suite:
```bash
make test
```

Clean up binary from the last build:
```bash
make clean
```
