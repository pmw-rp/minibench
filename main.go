package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/tlscfg"

	"github.com/twmb/franz-go/plugin/kprom"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/aws"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

var (
	seedBrokers       = flag.String("brokers", "localhost:9092", "comma delimited list of seed brokers")
	topicPrefix       = flag.String("topic-prefix", "test-", "topic prefix to produce to")
	topicCount        = flag.Int("topic-count", 1, "number of topics to produce to")
	partitions        = flag.Int("partitions", 1, "number of partitions per topic")
	replicationFactor = flag.Int("replication-factor", 1, "replication factor")
	pprofPort         = flag.String("pprof", ":7070", "port to bind to for pprof, if non-empty")
	prom              = flag.Bool("prometheus", true, "if true, install a /metrics path for prometheus metrics to the default handler (usage requires -pprof)")

	useStaticValue = flag.Bool("static-record", false, "if true, use the same record value for every record (eliminates creating and formatting values for records; implies -pool)")

	recordBytes         = flag.Int("record-bytes", 1000, "bytes per record value (producing)")
	compression         = flag.String("compression", "none", "compression algorithm to use (none,gzip,snappy,lz4,zstd, for producing)")
	poolProduce         = flag.Bool("pool", false, "if true, use a sync.Pool to reuse record structs/slices (producing)")
	noIdempotency       = flag.Bool("disable-idempotency", false, "if true, disable idempotency (force 1 produce rps)")
	noIdempotentMaxReqs = flag.Int("max-inflight-produce-per-broker", 5, "if idempotency is disabled, the number of produce requests to allow per broker")
	acks                = flag.Int("acks", -1, "acks required; 0, -1, 1")
	linger              = flag.Duration("linger", 0, "if non-zero, linger to use when producing")
	batchMaxBytes       = flag.Int("batch-max-bytes", 1000000, "the maximum batch size to allow per-partition (must be less than Kafka's max.message.bytes, producing)")

	batchRecs = flag.Int("batch-recs", 1, "number of records to create before produce calls")
	psync     = flag.Bool("psync", false, "produce synchronously")

	pgoros = flag.Int("pgoros", 1, "number of goroutines concurrently spawn to produce")

	rateLimit    = flag.Int64("rate-limit", 0, "if non-zero, limit throughput to this many records per second (0 = unlimited)")
	rateLimitMiB = flag.Float64("rate-limit-mib", 0, "if non-zero, limit throughput to this many MiB per second (0 = unlimited)")

	logLevel = flag.String("log-level", "", "if non-empty, use a basic logger with this log level (debug, info, warn, error)")

	//consume = flag.Bool("consume", false, "if true, consume rather than produce")
	//group   = flag.String("group", "", "if non-empty, group to use for consuming rather than direct partition consuming (consuming)")

	dialTLS  = flag.Bool("tls", false, "if true, use tls for connecting (if using well-known TLS certs)")
	caFile   = flag.String("ca-cert", "", "if non-empty, path to CA cert to use for TLS (implies -tls)")
	certFile = flag.String("client-cert", "", "if non-empty, path to client cert to use for TLS (requires -client-key, implies -tls)")
	keyFile  = flag.String("client-key", "", "if non-empty, path to client key to use for TLS (requires -client-cert, implies -tls)")

	saslMethod = flag.String("sasl-method", "", "if non-empty, sasl method to use (must specify all options; supports plain, scram-sha-256, scram-sha-512, aws_msk_iam)")
	saslUser   = flag.String("sasl-user", "", "if non-empty, username to use for sasl (must specify all options)")
	saslPass   = flag.String("sasl-pass", "", "if non-empty, password to use for sasl (must specify all options)")

	rateRecs  int64
	rateBytes int64
)

func printRate() {
	for range time.Tick(time.Second) {
		recs := atomic.SwapInt64(&rateRecs, 0)
		bytes := atomic.SwapInt64(&rateBytes, 0)
		fmt.Printf("%0.2f MiB/s; %0.2fk records/s\n", float64(bytes)/(1024*1024), float64(recs)/1000)
	}
}

func die(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, msg+"\n", args...)
	os.Exit(1)
}

func chk(err error, msg string, args ...any) {
	if err != nil {
		die(msg, args...)
	}
}

// setFlagsFromEnv sets flag values from environment variables if they haven't been set via command line
func setFlagsFromEnv() {
	// Track which flags were explicitly set on the command line
	setFlags := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = true
	})

	// For flags not set on command line, check environment variables
	flag.VisitAll(func(f *flag.Flag) {
		if setFlags[f.Name] {
			return // Skip flags that were explicitly set
		}

		// Convert flag name to environment variable name (e.g., "topic-prefix" -> "TOPIC_PREFIX")
		envName := strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		if envVal := os.Getenv(envName); envVal != "" {
			f.Value.Set(envVal)
		}
	})
}

// rateLimiter implements a simple token bucket rate limiter
type rateLimiter struct {
	recsPerSec  int64
	bytesPerSec int64
	lastTime    time.Time
	mu          sync.Mutex
}

// newRateLimiter creates a new rate limiter. Pass 0 for unlimited.
func newRateLimiter(recsPerSec int64, bytesPerSec int64) *rateLimiter {
	return &rateLimiter{
		recsPerSec:  recsPerSec,
		bytesPerSec: bytesPerSec,
		lastTime:    time.Now(),
	}
}

// wait blocks until the rate limiter allows the specified records and bytes to proceed
func (rl *rateLimiter) wait(numRecs int, numBytes int) {
	if rl.recsPerSec == 0 && rl.bytesPerSec == 0 {
		return // No rate limiting
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastTime)

	var sleepDuration time.Duration

	// Calculate required delay based on records/sec limit
	if rl.recsPerSec > 0 {
		nanosPerRec := time.Second.Nanoseconds() / rl.recsPerSec
		requiredDuration := time.Duration(int64(numRecs) * nanosPerRec)
		if requiredDuration > elapsed {
			recsSleep := requiredDuration - elapsed
			if recsSleep > sleepDuration {
				sleepDuration = recsSleep
			}
		}
	}

	// Calculate required delay based on bytes/sec limit
	if rl.bytesPerSec > 0 {
		nanosPerByte := time.Second.Nanoseconds() / rl.bytesPerSec
		requiredDuration := time.Duration(int64(numBytes) * nanosPerByte)
		if requiredDuration > elapsed {
			bytesSleep := requiredDuration - elapsed
			if bytesSleep > sleepDuration {
				sleepDuration = bytesSleep
			}
		}
	}

	if sleepDuration > 0 {
		time.Sleep(sleepDuration)
	}

	rl.lastTime = time.Now()
}

func main() {
	flag.Parse()
	setFlagsFromEnv()

	var customTLS bool
	if *caFile != "" || *certFile != "" || *keyFile != "" {
		*dialTLS = true
		customTLS = true
	}

	if *recordBytes <= 0 {
		die("record bytes must be larger than zero")
	}

	// Initialize rate limiter
	var bytesPerSec int64
	if *rateLimitMiB > 0 {
		bytesPerSec = int64(*rateLimitMiB * 1024 * 1024)
	}
	limiter := newRateLimiter(*rateLimit, bytesPerSec)

	if *useStaticValue {
		staticValue = make([]byte, *recordBytes)
		formatValue(0, staticValue)
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(strings.Split(*seedBrokers, ",")...),
		//kgo.DefaultProduceTopic(*topic),
		kgo.MaxBufferedRecords(250<<20 / *recordBytes + 1),
		kgo.MaxConcurrentFetches(3),
		// We have good compression, so we want to limit what we read
		// back because snappy deflation will balloon our memory usage.
		kgo.FetchMaxBytes(5 << 20),
		kgo.ProducerBatchMaxBytes(int32(*batchMaxBytes)),
	}
	if *noIdempotency {
		opts = append(opts, kgo.DisableIdempotentWrite())
		opts = append(opts, kgo.MaxProduceRequestsInflightPerBroker(*noIdempotentMaxReqs))
	}
	//if *consume {
	//	opts = append(opts, kgo.ConsumeTopics(*topic))
	//	if *group != "" {
	//		opts = append(opts, kgo.ConsumerGroup(*group))
	//	}
	//}
	switch *acks {
	case 0:
		opts = append(opts, kgo.RequiredAcks(kgo.NoAck()))
	case 1:
		opts = append(opts, kgo.RequiredAcks(kgo.LeaderAck()))
	default:
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()))
	}

	if *prom {
		metrics := kprom.NewMetrics("kgo")
		http.Handle("/metrics", metrics.Handler())
		opts = append(opts, kgo.WithHooks(metrics))
	}

	switch strings.ToLower(*logLevel) {
	case "":
	case "debug":
		opts = append(opts, kgo.WithLogger(kgo.BasicLogger(os.Stderr, kgo.LogLevelDebug, nil)))
	case "info":
		opts = append(opts, kgo.WithLogger(kgo.BasicLogger(os.Stderr, kgo.LogLevelInfo, nil)))
	case "warn":
		opts = append(opts, kgo.WithLogger(kgo.BasicLogger(os.Stderr, kgo.LogLevelWarn, nil)))
	case "error":
		opts = append(opts, kgo.WithLogger(kgo.BasicLogger(os.Stderr, kgo.LogLevelError, nil)))
	default:
		die("unrecognized log level %s", *logLevel)
	}

	if *linger != 0 {
		opts = append(opts, kgo.ProducerLinger(*linger))
	}
	switch strings.ToLower(*compression) {
	case "", "none":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.NoCompression()))
	case "gzip":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.GzipCompression()))
	case "snappy":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.SnappyCompression()))
	case "lz4":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.Lz4Compression()))
	case "zstd":
		opts = append(opts, kgo.ProducerBatchCompression(kgo.ZstdCompression()))
	default:
		die("unrecognized compression %s", *compression)
	}

	if *dialTLS {
		if customTLS {
			tc, err := tlscfg.New(
				tlscfg.MaybeWithDiskCA(*caFile, tlscfg.ForClient),
				tlscfg.MaybeWithDiskKeyPair(*certFile, *keyFile),
			)
			if err != nil {
				die("unable to create tls config: %v", err)
			}
			opts = append(opts, kgo.DialTLSConfig(tc))
		} else {
			opts = append(opts, kgo.DialTLSConfig(new(tls.Config)))
		}
	}

	if *saslMethod != "" || *saslUser != "" || *saslPass != "" {
		if *saslMethod == "" || *saslUser == "" || *saslPass == "" {
			die("all of -sasl-method, -sasl-user, -sasl-pass must be specified if any are")
		}
		method := strings.ToLower(*saslMethod)
		method = strings.ReplaceAll(method, "-", "")
		method = strings.ReplaceAll(method, "_", "")
		switch method {
		case "plain":
			opts = append(opts, kgo.SASL(plain.Auth{
				User: *saslUser,
				Pass: *saslPass,
			}.AsMechanism()))
		case "scramsha256":
			opts = append(opts, kgo.SASL(scram.Auth{
				User: *saslUser,
				Pass: *saslPass,
			}.AsSha256Mechanism()))
		case "scramsha512":
			opts = append(opts, kgo.SASL(scram.Auth{
				User: *saslUser,
				Pass: *saslPass,
			}.AsSha512Mechanism()))
		case "awsmskiam":
			opts = append(opts, kgo.SASL(aws.Auth{
				AccessKey: *saslUser,
				SecretKey: *saslPass,
			}.AsManagedStreamingIAMMechanism()))
		default:
			die("unrecognized sasl option %s", *saslMethod)
		}
	}

	cl, err := kgo.NewClient(opts...)
	chk(err, "unable to initialize client: %v", err)

	if *pprofPort != "" {
		go func() {
			err := http.ListenAndServe(*pprofPort, nil)
			chk(err, "unable to run pprof listener: %v", err)
		}()
	}

	go printRate()

	topics := make([]string, *topicCount)
	for i := range topics {
		topics[i] = *topicPrefix + fmt.Sprintf("%v", i)
	}

	adm := kadm.NewClient(cl)

	response, err := adm.CreateTopics(context.Background(), int32(*partitions), int16(*replicationFactor), nil, topics...)
	chk(err, "unable to create topics: %v", err)
	_ = response

	switch {
	//case *consume:
	//	for {
	//		fetches := cl.PollFetches(context.Background())
	//		fetches.EachError(func(t string, p int32, err error) {
	//			chk(err, "topic %s partition %d had error: %v", t, p, err)
	//		})
	//		var recs int64
	//		var bytes int64
	//		fetches.EachRecord(func(r *kgo.Record) {
	//			recs++
	//			bytes += int64(len(r.Value))
	//		})
	//		atomic.AddInt64(&rateRecs, recs)
	//		atomic.AddInt64(&rateBytes, bytes)
	//	}
	case !*psync:
		var num atomic.Int64
		for range *pgoros {
			go func() {
				var recs []*kgo.Record
				for {
					recs = recs[:0]
					for range *batchRecs {
						recs = append(recs, newRecord(num.Add(1), &topics))
					}
					// Apply rate limiting before producing
					limiter.wait(len(recs), len(recs)*(*recordBytes))
					for _, r := range recs {
						cl.Produce(context.Background(), r, func(r *kgo.Record, err error) {
							if *useStaticValue {
								staticPool.Put(r)
							} else if *poolProduce {
								p.Put(r)
							}
							chk(err, "produce error: %v", err)
							atomic.AddInt64(&rateRecs, 1)
							atomic.AddInt64(&rateBytes, int64(*recordBytes))
						})
					}
				}
			}()
		}
		select {}
	default:
		var num atomic.Int64
		for range *pgoros {
			go func() {
				var recs []*kgo.Record
				for {
					recs = recs[:0]
					for range *batchRecs {
						recs = append(recs, newRecord(num.Add(1), &topics))
					}

					// Apply rate limiting before producing
					limiter.wait(len(recs), len(recs)*(*recordBytes))
					ress := cl.ProduceSync(context.Background(), recs...)
					go func() {
						for _, res := range ress {
							r, err := res.Record, res.Err
							if *useStaticValue {
								staticPool.Put(r)
							} else if *poolProduce {
								p.Put(r)
							}
							chk(err, "produce error: %v", err)
							atomic.AddInt64(&rateRecs, 1)
							atomic.AddInt64(&rateBytes, int64(*recordBytes))
						}
					}()
				}
			}()
		}
		select {}
	}
}

var (
	staticValue []byte
	staticPool  = sync.Pool{New: func() any { return kgo.SliceRecord(staticValue) }}
	p           = sync.Pool{New: func() any { return kgo.SliceRecord(make([]byte, *recordBytes)) }}
)

func newRecord(num int64, topics *[]string) *kgo.Record {
	var r *kgo.Record
	if *useStaticValue {
		r := staticPool.Get().(*kgo.Record)
		r.Topic = (*topics)[rand.IntN(len(*topics))]
		return r
	} else if *poolProduce {
		r = p.Get().(*kgo.Record)
	} else {
		r = kgo.SliceRecord(make([]byte, *recordBytes))
	}
	formatValue(num, r.Value)
	r.Topic = (*topics)[rand.IntN(len(*topics))]
	return r
}

func formatValue(num int64, v []byte) {
	var buf [20]byte // max int64 takes 19 bytes, then we add a space
	b := strconv.AppendInt(buf[:0], num, 10)
	b = append(b, ' ')

	n := copy(v, b)
	for n != len(v) {
		n += copy(v[n:], b)
	}
}
