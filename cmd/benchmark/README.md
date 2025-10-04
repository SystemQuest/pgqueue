# PgQueue Benchmark Tool

A comprehensive benchmark tool for testing PgQueue performance, aligned with PgQueuer's benchmark.py.

## Features

- **Multi-worker Testing**: Support for multiple concurrent enqueue and dequeue workers
- **Configurable Batch Sizes**: Tune batch sizes for optimal performance
- **Real-time Monitoring**: Live queue size monitoring during benchmark
- **Detailed Statistics**: Per-worker and aggregate performance metrics
- **Database Cleanup**: Automatic cleanup before benchmarking

## Installation

```bash
# Build the benchmark tool
go build -o bin/benchmark ./cmd/benchmark

# Or use make
make build-benchmark
```

## Usage

### Basic Usage

Run a 15-second benchmark with default settings:

```bash
./bin/benchmark
```

### Custom Configuration

```bash
./bin/benchmark \
  -t 30 \
  -dq 4 \
  -dqbs 20 \
  -eq 2 \
  -eqbs 50 \
  -db "postgres://user:pass@localhost:5432/pgqueue?sslmode=disable"
```

## Command Line Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--timer` | `-t` | 15 | Benchmark duration in seconds |
| `--dequeue` | `-dq` | 2 | Number of concurrent dequeue workers |
| `--dequeue-batch-size` | `-dqbs` | 10 | Batch size for dequeue operations |
| `--enqueue` | `-eq` | 1 | Number of concurrent enqueue workers |
| `--enqueue-batch-size` | `-eqbs` | 20 | Batch size for enqueue operations |
| `--db` | - | env:DATABASE_URL | PostgreSQL connection URL |
| `--show-queue-size` | - | true | Display queue size periodically |

## Examples

### High Throughput Test

Test with high enqueue and dequeue rates:

```bash
./bin/benchmark -t 60 -dq 8 -dqbs 50 -eq 4 -eqbs 100
```

### Low Latency Test

Test with small batches for lower latency:

```bash
./bin/benchmark -t 30 -dq 4 -dqbs 5 -eq 2 -eqbs 10
```

### Single Worker Performance

Test single worker performance:

```bash
./bin/benchmark -t 20 -dq 1 -dqbs 10 -eq 1 -eqbs 20
```

## Output Example

```
Settings:
Timer:                  15.00 seconds
Dequeue:                2
Dequeue Batch Size:     10
Enqueue:                1
Enqueue Batch Size:     20

Clearing database...
Queue size: 145
Queue size: 289
Queue size: 312
Queue size: 198
Queue size: 76

=== Benchmark Results ===
Total Jobs Processed:   45678
Jobs per Second:        23.45k
Average per Worker:     11725.00 jobs/sec

Per-Worker Stats:
  Worker 0: 11823.45 jobs/sec (23012 jobs)
  Worker 1: 11626.55 jobs/sec (22666 jobs)
```

## Performance Tuning Tips

1. **Worker Count**: Increase `-dq` for better CPU utilization
2. **Batch Size**: Larger batches (`-dqbs`, `-eqbs`) improve throughput but increase latency
3. **Database Connections**: Ensure your database can handle the connection count
4. **Network**: Run on same network as database for best results

## Alignment with PgQueuer

This benchmark tool is functionally equivalent to PgQueuer's `benchmark.py`:

| Feature | PgQueuer (Python) | PgQueue (Go) |
|---------|-------------------|--------------|
| Multi-worker support | ✅ | ✅ |
| Configurable batch sizes | ✅ | ✅ |
| Real-time queue monitoring | ✅ | ✅ |
| Database cleanup | ✅ | ✅ |
| Jobs/second metrics | ✅ | ✅ |
| Timer-based execution | ✅ | ✅ |

## Architecture

The benchmark tool consists of:

1. **Producer Goroutines**: Continuously enqueue jobs with random entrypoints
2. **Consumer Goroutines**: Process jobs using QueueManager
3. **Monitor Goroutine**: Track and display queue size
4. **Timer**: Controls overall benchmark duration

```
┌─────────────┐
│  Producer 1 │──┐
└─────────────┘  │
┌─────────────┐  │    ┌──────────────┐
│  Producer 2 │──┼───►│  PostgreSQL  │
└─────────────┘  │    │    Queue     │
┌─────────────┐  │    └──────────────┘
│  Producer N │──┘           │
└─────────────┘              │
                             ▼
┌─────────────┐       ┌─────────────┐
│  Consumer 1 │◄──────┤  Consumer 2 │
└─────────────┘       └─────────────┘
       │                     │
       └──────────┬──────────┘
                  ▼
          ┌───────────────┐
          │  Metrics      │
          │  Aggregation  │
          └───────────────┘
```

## Troubleshooting

### Connection Errors

If you see connection errors:
- Verify DATABASE_URL is correct
- Ensure PostgreSQL is running
- Check connection limits in postgresql.conf

### Low Performance

If jobs/sec is lower than expected:
- Increase worker count (`-dq`, `-eq`)
- Increase batch sizes (`-dqbs`, `-eqbs`)
- Check database CPU/memory usage
- Verify network latency

### Queue Size Growing

If queue size keeps growing:
- Increase dequeue workers (`-dq`)
- Increase dequeue batch size (`-dqbs`)
- Check consumer processing time

## Development

### Building

```bash
go build -o bin/benchmark ./cmd/benchmark
```

### Testing

```bash
# Start test database
make test-up

# Run benchmark
DATABASE_URL="postgres://testuser:testpassword@localhost:5265/testdb?sslmode=disable" \
  ./bin/benchmark -t 10
```

## Contributing

Contributions are welcome! Please ensure:

1. Code follows Go conventions
2. Maintains alignment with PgQueuer benchmark.py
3. Includes appropriate error handling
4. Updates this README if adding features

## License

Same license as the PgQueue project.
