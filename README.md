# Kafka Benchmarking Tool

A high-performance Kafka producer benchmarking tool built with franz-go.

## Configuration

This tool can be configured using either command-line flags or environment variables. Environment variables are particularly useful when running in containers.

### Priority Order

1. Command-line flags (highest priority)
2. Environment variables
3. Default values

### Available Options

All command-line flags can be set via environment variables by converting the flag name to uppercase and replacing hyphens with underscores.

#### Broker Configuration
- **Flag**: `-brokers` | **Env**: `BROKERS` (default: `localhost:9092`)
  - Comma-delimited list of seed brokers

#### Topic Configuration
- **Flag**: `-topic-prefix` | **Env**: `TOPIC_PREFIX` (default: `test-`)
  - Topic prefix to produce to
- **Flag**: `-topic-count` | **Env**: `TOPIC_COUNT` (default: `1`)
  - Number of topics to produce to
- **Flag**: `-use-pod-name` | **Env**: `USE_POD_NAME` (default: `true`)
  - If true and `POD_NAME` environment variable is set, include pod name in topic names
  - Topic name format: `<prefix><podname>-<N>` (e.g., `test-kafka-producer-0-2`)
  - Useful for Kubernetes StatefulSets where each pod produces to its own topics
  - Set to `false` if you want the same topic names across all pods
- **Flag**: `-partitions` | **Env**: `PARTITIONS` (default: `1`)
  - Number of partitions per topic
- **Flag**: `-replication-factor` | **Env**: `REPLICATION_FACTOR` (default: `1`)
  - Replication factor

#### Record Configuration
- **Flag**: `-record-bytes` | **Env**: `RECORD_BYTES` (default: `1000`)
  - Bytes per record value
- **Flag**: `-static-record` | **Env**: `STATIC_RECORD` (default: `false`)
  - Use the same record value for every record (eliminates creating and formatting values; implies -pool)
- **Flag**: `-batch-recs` | **Env**: `BATCH_RECS` (default: `1`)
  - Number of records to create before produce calls

#### Producer Configuration
- **Flag**: `-compression` | **Env**: `COMPRESSION` (default: `none`)
  - Compression algorithm (none, gzip, snappy, lz4, zstd)
- **Flag**: `-pool` | **Env**: `POOL` (default: `false`)
  - Use sync.Pool to reuse record structs/slices
- **Flag**: `-disable-idempotency` | **Env**: `DISABLE_IDEMPOTENCY` (default: `false`)
  - Disable idempotency (force 1 produce rps)
- **Flag**: `-max-inflight-produce-per-broker` | **Env**: `MAX_INFLIGHT_PRODUCE_PER_BROKER` (default: `5`)
  - Number of produce requests to allow per broker when idempotency is disabled
- **Flag**: `-acks` | **Env**: `ACKS` (default: `-1`)
  - Acks required (0, -1, 1)
- **Flag**: `-linger` | **Env**: `LINGER` (default: `0`)
  - Linger duration when producing (e.g., `100ms`)
- **Flag**: `-batch-max-bytes` | **Env**: `BATCH_MAX_BYTES` (default: `1000000`)
  - Maximum batch size per partition (must be less than Kafka's max.message.bytes)
- **Flag**: `-psync` | **Env**: `PSYNC` (default: `false`)
  - Produce synchronously
- **Flag**: `-pgoros` | **Env**: `PGOROS` (default: `1`)
  - Number of goroutines to spawn for producing

#### Rate Limiting
- **Flag**: `-rate-limit` | **Env**: `RATE_LIMIT` (default: `0`)
  - Limit throughput to this many records per second (0 = unlimited)
- **Flag**: `-rate-limit-mib` | **Env**: `RATE_LIMIT_MIB` (default: `0`)
  - Limit throughput to this many MiB per second (0 = unlimited)

**Rate Limiting Behavior:**
- Uses a token bucket algorithm that works correctly with multiple producer goroutines (`-pgoros`)
- When both limits are specified, the more restrictive limit will be enforced
- Allows small bursts (up to 2x the configured rate) to handle natural variation in produce timing
- Rate limits are enforced across all producer goroutines combined, not per goroutine

#### TLS Configuration
- **Flag**: `-tls` | **Env**: `TLS` (default: `false`)
  - Use TLS for connecting (for well-known TLS certs)
- **Flag**: `-ca-cert` | **Env**: `CA_CERT`
  - Path to CA cert for TLS (implies -tls)
- **Flag**: `-client-cert` | **Env**: `CLIENT_CERT`
  - Path to client cert for TLS (requires -client-key, implies -tls)
- **Flag**: `-client-key` | **Env**: `CLIENT_KEY`
  - Path to client key for TLS (requires -client-cert, implies -tls)

#### SASL Configuration
- **Flag**: `-sasl-method` | **Env**: `SASL_METHOD`
  - SASL method (plain, scram-sha-256, scram-sha-512, aws_msk_iam)
- **Flag**: `-sasl-user` | **Env**: `SASL_USER`
  - Username for SASL
- **Flag**: `-sasl-pass` | **Env**: `SASL_PASS`
  - Password for SASL

#### Monitoring Configuration
- **Flag**: `-pprof` | **Env**: `PPROF` (default: `:9876`)
  - Port to bind for pprof (empty to disable)
- **Flag**: `-prometheus` | **Env**: `PROMETHEUS` (default: `true`)
  - Install /metrics path for Prometheus metrics (requires -pprof)
- **Flag**: `-log-level` | **Env**: `LOG_LEVEL`
  - Log level (debug, info, warn, error)

## Usage Examples

### Using Command-Line Flags
```bash
./minibench -brokers kafka1:9092,kafka2:9092 -topic-prefix load-test- -topic-count 3
```

### Using Environment Variables
```bash
export BROKERS="kafka1:9092,kafka2:9092"
export TOPIC_PREFIX="load-test-"
export TOPIC_COUNT=3
./minibench
```

### Docker Example
```bash
docker run -e BROKERS=kafka:9092 -e TOPIC_COUNT=5 -e PGOROS=10 minibench
```

### Mixing Flags and Environment Variables
```bash
export BROKERS="kafka1:9092,kafka2:9092"
export RECORD_BYTES=5000
./minibench -pgoros 10 -compression snappy
```
In this example, `BROKERS` and `RECORD_BYTES` come from environment variables, while `pgoros` and `compression` are set via flags (which take precedence).

### Rate Limiting Examples

#### Limit by Records Per Second
```bash
# Limit to 10,000 records per second
./minibench -brokers kafka:9092 -rate-limit 10000
```

#### Limit by MiB Per Second
```bash
# Limit to 50 MiB per second
./minibench -brokers kafka:9092 -rate-limit-mib 50
```

#### Using Environment Variables for Rate Limiting
```bash
export RATE_LIMIT=5000
export RATE_LIMIT_MIB=25
./minibench -brokers kafka:9092
```

#### Docker with Rate Limiting
```bash
docker run -e BROKERS=kafka:9092 -e RATE_LIMIT=10000 -e PGOROS=5 minibench
```
