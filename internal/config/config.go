// Package config loads and validates application configuration from environment
// variables (e.g. SERVER_PORT=8081), with a local .env file as a development
// convenience and production-safe defaults underneath.
//
// Variable names are unprefixed, sharing a namespace with everything else in
// the environment. "service" is named defensively so it does not collide with
// generic NAME/ENV; "otel" deliberately does the opposite and adopts the
// OpenTelemetry SDK's own spelling (see OTelConfig). Check any new key against
// what an orchestrator might already set (Kubernetes injects <SERVICE>_PORT
// for every Service in the namespace).
//
// .env.example is the single list of available settings, written exactly as
// a deployment sets them - there is deliberately one vocabulary, not a
// config.yaml shadowing the env vars.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"

	platformconfig "github.com/disillusioned-labs/platform/config"
)

// Config is the root of all application settings, one field per subsystem.
type Config struct {
	Service    platformconfig.ServiceConfig    `mapstructure:"service"`
	Server     platformconfig.ServerConfig     `mapstructure:"server"`
	Pprof      platformconfig.PprofConfig      `mapstructure:"pprof"`
	Postgres   platformconfig.PostgresConfig   `mapstructure:"postgres"`
	Redis      platformconfig.RedisConfig      `mapstructure:"redis"`
	Cache      platformconfig.CacheConfig      `mapstructure:"cache"`
	Kafka      platformconfig.KafkaConfig      `mapstructure:"kafka"`
	OTel       platformconfig.OTelConfig       `mapstructure:"otel"`
	Log        platformconfig.LogConfig        `mapstructure:"log"`
	RateLimit  platformconfig.RateLimitConfig  `mapstructure:"ratelimit"`
	Auth       AuthConfig                      `mapstructure:"auth"`
	Storage    StorageConfig                   `mapstructure:"storage"`
	GRPCClient platformconfig.GRPCClientConfig `mapstructure:"grpc_client"`
	Identity   IdentityClientConfig            `mapstructure:"identity"`
	// GRPC is the internal gRPC server surface (decision D2, ExpenseService).
	GRPC   platformconfig.GRPCConfig `mapstructure:"grpc"`
	Remind ReminderConfig            `mapstructure:"reminder"`
	Ocr    OcrConfig                 `mapstructure:"ocr"`
}

// IdentityClientConfig names where identity's gRPC surface lives. Targets are
// per-dependency keys, kept out of GRPCClientConfig (shared outbound knobs) so
// the EnvKey-based .env layering names each variable after who it dials.
type IdentityClientConfig struct {
	// GRPCTarget is identity's gRPC address (identity.v1). Must not be empty:
	// authz checks on every request dial identity.
	GRPCTarget string `mapstructure:"grpc_target"`
}

// OcrConfig selects how documents reach the OCR pipeline. An empty
// GatewayTarget keeps the noop submitter (documents stay pending OCR); a set
// target switches to the ocr-gateway's Kontrak A gRPC surface.
type OcrConfig struct {
	// GatewayTarget is the ocr-gateway gRPC address (ocr.gateway.v1). Empty
	// means OCR integration is off: submits log and return, no error - a
	// missing gateway must not fail an upload.
	GatewayTarget string `mapstructure:"gateway_target"`
}

// ReminderConfig schedules the approval-stall nag: an active approval step
// older than ThresholdDays gets one reminder email per Interval tick.
// Visibility only - it never changes who may decide.
type ReminderConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	Interval      time.Duration `mapstructure:"interval"`
	ThresholdDays int           `mapstructure:"threshold_days"`
	BatchSize     int           `mapstructure:"batch_size"`
}

// StorageConfig selects the document store engine and its settings. The
// default engine is the local dev filesystem; s3 points at any S3-compatible
// object storage via platform/s3.
type StorageConfig struct {
	S3 StorageS3Config `mapstructure:"s3"`
}

type StorageS3Config struct {
	// Endpoint is the S3-compatible API endpoint (MinIO for local dev).
	// Empty means real AWS.
	Endpoint string `mapstructure:"endpoint"`
	// Region is the signing region (a placeholder such as us-east-1 works
	// for MinIO). Always required when engine=s3.
	Region string `mapstructure:"region"`
	// Static credentials for S3-compatible servers. Leave AccessKeyID empty
	// to use the default AWS credential chain (env/IMDSv2).
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	SessionToken    string `mapstructure:"session_token"`
	// UsePathStyle addresses buckets as endpoint/bucket/key - required for MinIO.
	UsePathStyle bool `mapstructure:"use_path_style"`
	// Bucket is the single bucket expense stores documents in.
	Bucket string `mapstructure:"bucket"`
	// PresignTTL is the lifetime of file_url presigned GETs.
	PresignTTL string `mapstructure:"presign_ttl"`
	// PingOnBoot fails the boot when the bucket is unreachable - upload
	// cannot degrade around object storage.
	PingOnBoot bool `mapstructure:"ping_on_boot"`
}

// AuthConfig points at identity's JWKS: expense never trusts a token without
// verifying it itself (zero trust), and never calls identity on the hot path.
type AuthConfig struct {
	// Issuer is the "iss" claim stamped by identity.
	Issuer string `mapstructure:"issuer"`
	// JWKSURL is identity's public key set endpoint.
	JWKSURL string `mapstructure:"jwks_url"`
}

// DotEnvFile is the optional local overrides file, loaded from the working
// directory. It is git-ignored; .env.example documents every key.
const DotEnvFile = ".env"

// Load builds the configuration from environment variables (e.g. POSTGRES_DSN),
// falling back to the defaults in setDefaults.
//
// A .env file in the working directory is loaded into the environment first as
// a local development convenience; real environment variables always win, so
// the precedence is environment > .env > defaults. Deployments set variables
// directly and ship no file.
func Load() (*Config, error) {
	dotEnv, err := platformconfig.ParseDotEnv(DotEnvFile)
	if err != nil {
		return nil, err
	}

	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	// Layer .env in as defaults rather than by setting process environment
	// variables. Viper resolves AutomaticEnv before defaults, so a real
	// environment variable still wins for free - and Load leaves no global
	// state behind, which keeps it idempotent and safe to call from tests.
	for _, key := range v.AllKeys() {
		if value, ok := dotEnv[platformconfig.EnvKey(key)]; ok {
			v.SetDefault(key, value)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.Kafka.Brokers = platformconfig.NormalizeKafkaBrokers(cfg.Kafka.Brokers)
	cfg.Kafka.Consumer.Topics = platformconfig.NormalizeKafkaTopics(cfg.Kafka.Consumer.Topics)
	cfg.Service.InstanceID = platformconfig.InstanceID()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// validate rejects every value the app would otherwise silently misinterpret.
// A boilerplate is copied far more often than it is read, so an unset or
// fat-fingered override must fail at boot rather than degrade in production.
func (c *Config) validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if err := platformconfig.ValidateService(&c.Service); err != nil {
		errs = append(errs, err)
	}

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		fail("server.port must be in 1..65535, got %d", c.Server.Port)
	}
	if c.Pprof.Enabled {
		if c.Pprof.Port < 1 || c.Pprof.Port > 65535 {
			fail("pprof.port must be in 1..65535 when pprof.enabled, got %d", c.Pprof.Port)
		}
		if c.Pprof.Port == c.Server.Port {
			fail("pprof.port (%d) must differ from server.port", c.Pprof.Port)
		}
	}
	for _, d := range []struct {
		key string
		val time.Duration
	}{
		{"server.read_timeout", c.Server.ReadTimeout},
		{"server.write_timeout", c.Server.WriteTimeout},
		{"server.idle_timeout", c.Server.IdleTimeout},
		{"server.shutdown_timeout", c.Server.ShutdownTimeout},
		{"server.request_timeout", c.Server.RequestTimeout},
	} {
		if d.val <= 0 {
			fail("%s must be > 0, got %s", d.key, d.val)
		}
	}
	if c.Server.DrainDelay < 0 {
		fail("server.drain_delay must not be negative, got %s", c.Server.DrainDelay)
	}
	// The 504 is written by the handler after the request context expires, so
	// the write deadline has to outlast the request deadline or the client
	// gets a dropped connection instead of a status.
	if c.Server.RequestTimeout >= c.Server.WriteTimeout {
		fail("server.request_timeout (%s) must be < server.write_timeout (%s)",
			c.Server.RequestTimeout, c.Server.WriteTimeout)
	}
	// Shutdown must be able to outlast one in-flight request, or graceful
	// shutdown truncates responses that were still within their budget.
	if c.Server.ShutdownTimeout < c.Server.RequestTimeout {
		fail("server.shutdown_timeout (%s) must be >= server.request_timeout (%s)",
			c.Server.ShutdownTimeout, c.Server.RequestTimeout)
	}

	if err := platformconfig.ValidatePostgres(&c.Postgres); err != nil {
		errs = append(errs, err)
	}

	// Redis validation.
	switch c.Redis.Mode {
	case platformconfig.RedisModeDisabled:
	case platformconfig.RedisModeOptional, platformconfig.RedisModeRequired:
		if c.Redis.Addr == "" {
			fail("redis.addr must be set when redis.mode is %s", c.Redis.Mode)
		}
	default:
		fail("redis.mode must be one of disabled|optional|required, got %q", c.Redis.Mode)
	}
	if c.Redis.DB < 0 {
		fail("redis.db must not be negative, got %d", c.Redis.DB)
	}
	if c.Cache.DefaultTTL <= 0 {
		fail("cache.default_ttl must be > 0, got %s", c.Cache.DefaultTTL)
	}

	// Kafka validation (common fields via platform).
	if err := platformconfig.ValidateKafka(&c.Kafka); err != nil {
		errs = append(errs, err)
	}

	// Expense only uses producer fields today (outbox publisher, M4);
	// producer-specific validation.
	if c.Kafka.Producer.RecordRetries < 0 {
		fail(
			"kafka.producer.record_retries must be >= 0, got %d",
			c.Kafka.Producer.RecordRetries,
		)
	}
	if c.Kafka.Producer.RecordDeliveryTimeout <= 0 {
		fail(
			"kafka.producer.record_delivery_timeout must be > 0, got %s",
			c.Kafka.Producer.RecordDeliveryTimeout,
		)
	}

	if err := platformconfig.ValidateOTel(&c.OTel); err != nil {
		errs = append(errs, err)
	}
	if err := platformconfig.ValidateLog(&c.Log); err != nil {
		errs = append(errs, err)
	}

	if c.Auth.Issuer == "" {
		errs = append(errs, fmt.Errorf("auth.issuer must not be empty"))
	}
	if c.Auth.JWKSURL == "" {
		errs = append(errs, fmt.Errorf("auth.jwks_url must not be empty"))
	}

	// RateLimit validation.
	if c.RateLimit.Enabled {
		if c.RateLimit.Requests <= 0 {
			fail("ratelimit.requests must be > 0 when ratelimit.enabled, got %d", c.RateLimit.Requests)
		}
		if c.RateLimit.Window <= 0 {
			fail("ratelimit.window must be > 0 when ratelimit.enabled, got %s", c.RateLimit.Window)
		}
	}

	if c.Storage.S3.Region == "" {
		fail("storage.s3.region must not be empty when storage.engine=s3 (us-east-1 works for MinIO)")
	}
	if c.Storage.S3.Bucket == "" {
		fail("storage.s3.bucket must not be empty when storage.engine=s3")
	}
	if (c.Storage.S3.AccessKeyID == "") != (c.Storage.S3.SecretAccessKey == "") {
		fail("storage.s3.access_key_id and secret_access_key must be both set or both empty (empty = default AWS credential chain)")
	}

	// gRPC client validation.
	if err := platformconfig.ValidateGRPCClient(&c.GRPCClient); err != nil {
		errs = append(errs, err)
	}
	if strings.TrimSpace(c.Identity.GRPCTarget) == "" {
		fail("identity.grpc_target must not be empty")
	}

	// gRPC server validation (internal ExpenseService surface, D2).
	if err := platformconfig.ValidateGRPC(&c.GRPC); err != nil {
		errs = append(errs, err)
	}
	if c.GRPC.ServerPort == c.Server.Port {
		fail("grpc.server_port (%d) must differ from server.port", c.GRPC.ServerPort)
	}

	// Reminder worker validation.
	if c.Remind.Enabled {
		if c.Remind.Interval <= 0 {
			fail("reminder.interval must be > 0 when reminder.enabled, got %s", c.Remind.Interval)
		}
		if c.Remind.ThresholdDays < 1 {
			fail("reminder.threshold_days must be >= 1 when reminder.enabled, got %d", c.Remind.ThresholdDays)
		}
		if c.Remind.BatchSize < 1 {
			fail("reminder.batch_size must be >= 1 when reminder.enabled, got %d", c.Remind.BatchSize)
		}
	}

	return errors.Join(errs...)
}

// setDefaults registers every key with its production-safe value; Viper's
// AutomaticEnv only resolves keys it already knows, so an unregistered key
// would be invisible even when its variable is set.
func setDefaults(v *viper.Viper) {
	v.SetDefault("service.name", "expense")
	v.SetDefault("service.env", platformconfig.EnvDevelopment)

	v.SetDefault("server.port", 8081)
	v.SetDefault("server.read_timeout", "10s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("server.idle_timeout", "60s")
	v.SetDefault("server.shutdown_timeout", "20s")
	v.SetDefault("server.request_timeout", "20s")
	v.SetDefault("server.drain_delay", "5s")
	// Off by default; the port is pre-filled so enabling it needs one variable.
	v.SetDefault("pprof.enabled", false)
	v.SetDefault("pprof.port", 6061)

	v.SetDefault("postgres.dsn", "postgres://expense_app:devpassword@localhost:5432/expense?sslmode=disable")
	v.SetDefault("postgres.max_conns", 25)
	v.SetDefault("postgres.min_conns", 2)
	v.SetDefault("postgres.max_conn_lifetime", "1h")
	v.SetDefault("postgres.migrate", false)
	v.SetDefault("postgres.query_exec_mode", "cache_statement")

	v.SetDefault("redis.mode", string(platformconfig.RedisModeDisabled))
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)

	v.SetDefault("cache.default_ttl", "5m")

	v.SetDefault("kafka.brokers", []string{"localhost:9092"})
	v.SetDefault("kafka.client_id", "expense")
	v.SetDefault("kafka.ping_timeout", "5s")
	v.SetDefault("kafka.producer.record_retries", int64(5))
	v.SetDefault("kafka.producer.record_delivery_timeout", "30s")
	// Consumer defaults are producer-only: the group stays empty unless the
	// worker is explicitly pointed at a topic, and the API binary never
	// consumes regardless of what is set here.
	v.SetDefault("kafka.consumer.group", "")
	v.SetDefault("kafka.consumer.topics", "")
	v.SetDefault("kafka.consumer.dlq_topic", "")
	v.SetDefault("kafka.consumer.retry.max_attempts", 3)
	v.SetDefault("kafka.consumer.retry.initial_delay", "200ms")
	v.SetDefault("kafka.consumer.retry.max_delay", "5s")

	v.SetDefault("otel.sdk_disabled", false)
	v.SetDefault("otel.traces_exporter", platformconfig.OTelExporterOTLP)
	v.SetDefault("otel.metrics_exporter", platformconfig.OTelExporterOTLP)
	v.SetDefault("otel.exporter_otlp_endpoint", "http://localhost:4317")
	// Empty means "inherit the base endpoint"; registered anyway because
	// AutomaticEnv only resolves keys Viper already knows.
	v.SetDefault("otel.exporter_otlp_traces_endpoint", "")
	v.SetDefault("otel.exporter_otlp_metrics_endpoint", "")
	v.SetDefault("otel.traces_sampler", "parentbased_traceidratio")
	v.SetDefault("otel.traces_sampler_arg", 1.0)
	// Milliseconds, per spec - this is the OTel SDK's own default.
	v.SetDefault("otel.metric_export_interval", 60000)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")

	v.SetDefault("ratelimit.enabled", true)
	v.SetDefault("ratelimit.requests", 40)
	v.SetDefault("ratelimit.window", "1s")

	v.SetDefault("auth.issuer", "identity")
	v.SetDefault("auth.jwks_url", "http://localhost:8080/.well-known/jwks.json")

	v.SetDefault("storage.engine", "local")
	v.SetDefault("storage.s3.endpoint", "")
	v.SetDefault("storage.s3.region", "")
	v.SetDefault("storage.s3.access_key_id", "")
	v.SetDefault("storage.s3.secret_access_key", "")
	v.SetDefault("storage.s3.session_token", "")
	v.SetDefault("storage.s3.use_path_style", false)
	v.SetDefault("storage.s3.bucket", "")
	v.SetDefault("storage.s3.presign_ttl", "15m")
	// Fail-fast is the production-safe default: upload cannot degrade
	// around an unreachable object store.
	v.SetDefault("storage.s3.ping_on_boot", true)

	// gRPC client (expense → identity).
	v.SetDefault("identity.grpc_target", "localhost:9090")
	v.SetDefault("grpc_client.timeout", "50ms")
	v.SetDefault("grpc_client.max_recv_msg_size", 4194304)
	v.SetDefault("grpc_client.max_send_msg_size", 4194304)
	v.SetDefault("grpc_client.tls.enabled", false)
	v.SetDefault("grpc_client.tls.ca_file", "")
	v.SetDefault("grpc_client.tls.cert_file", "")
	v.SetDefault("grpc_client.tls.key_file", "")
	v.SetDefault("grpc_client.tls.server_name", "")
	v.SetDefault("grpc_client.tls.mutual_tls", false)

	// gRPC server (internal ExpenseService surface, D2). 9091 keeps it clear
	// of identity's gRPC on 9090 and the Kafka broker on 9092.
	v.SetDefault("grpc.server_port", 9091)
	v.SetDefault("grpc.max_recv_msg_size", 4*1024*1024)
	v.SetDefault("grpc.max_send_msg_size", 4*1024*1024)
	v.SetDefault("grpc.max_header_size", 8*1024)
	v.SetDefault("grpc.unary_timeout", "10s")
	v.SetDefault("grpc.tls.enabled", false)
	v.SetDefault("grpc.tls.ca_file", "")
	v.SetDefault("grpc.tls.cert_file", "")
	v.SetDefault("grpc.tls.key_file", "")
	v.SetDefault("grpc.tls.server_name", "")
	v.SetDefault("grpc.tls.mutual_tls", false)

	// Approval-stall reminders (visibility only). Off by default: enabling
	// them is a deployment's explicit choice.
	v.SetDefault("reminder.enabled", false)
	v.SetDefault("reminder.interval", "24h")
	v.SetDefault("reminder.threshold_days", 2)
	v.SetDefault("reminder.batch_size", 100)

	// OCR submitter. Empty target = noop (documents stay pending OCR), so a
	// deployment without the OCR pipeline never fails an upload.
	v.SetDefault("ocr.gateway_target", "")
}
