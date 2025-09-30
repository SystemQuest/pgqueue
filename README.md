# PgQueue4Go

A high-performance PostgreSQL-based job queue system for Go, inspired by PgQueuer.

## Features

- ✅ **PostgreSQL Native**: Leverages PostgreSQL's LISTEN/NOTIFY and FOR UPDATE SKIP LOCKED
- ✅ **Type Safe**: Built with sqlc for compile-time SQL validation
- ✅ **High Performance**: Optimized for throughput with batch operations
- ✅ **Simple Design**: Minimal dependencies, maximum reliability
- ✅ **Production Ready**: Comprehensive error handling and monitoring

## Quick Start

```bash
# Install the CLI tool
go install github.com/systemquest/pgqueue4go/cmd/pgqueue@latest

# Setup database schema
pgqueue install --database-url postgres://user:pass@localhost/dbname

# Start processing jobs
pgqueue dashboard
```

## Usage

```go
package main

import (
    "context"
    "log"
    
    "github.com/systemquest/pgqueue4go/pkg/queue"
)

func main() {
    // Connect to PostgreSQL
    q, err := queue.New("postgres://user:pass@localhost/dbname")
    if err != nil {
        log.Fatal(err)
    }
    defer q.Close()
    
    // Enqueue a job
    err = q.Enqueue(context.Background(), &queue.Job{
        Entrypoint: "send_email",
        Payload:    []byte(`{"to": "user@example.com"}`),
        Priority:   10,
    })
    if err != nil {
        log.Fatal(err)
    }
    
    // Process jobs
    err = q.Work(context.Background(), "send_email", func(ctx context.Context, job *queue.Job) error {
        // Process the job
        return nil
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

## Documentation

- [Installation Guide](docs/installation.md)
- [API Reference](docs/api.md)
- [Migration from PgQueuer](docs/migration.md)
- [Performance Guide](docs/performance.md)

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.