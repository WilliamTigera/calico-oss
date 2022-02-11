package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/libcalico-go/lib/logutils"
)

const (
	ReportNameEnv  = "TIGERA_COMPLIANCE_REPORT_NAME"
	ReportStartEnv = "TIGERA_COMPLIANCE_REPORT_START_TIME"
	ReportEndEnv   = "TIGERA_COMPLIANCE_REPORT_END_TIME"
)

// Config contain environment based configuration for all compliance components. Although not all configuration is
// required for all components, it is useful having everything defined in one location.
type Config struct {
	// LogLevel
	LogLevel string `envconfig:"LOG_LEVEL"`

	// Health checks common to all components.
	HealthEnabled bool          `envconfig:"HEALTH_ENABLED" default:"true"`
	HealthPort    int           `envconfig:"HEALTH_PORT" default:"9099"`
	HealthHost    string        `envconfig:"HEALTH_HOST" default:"0.0.0.0"`
	HealthTimeout time.Duration `envconfig:"HEALTH_TIMEOUT" default:"30s"`
	// kube-bench might take longer than 30s (default) on some provisioners.
	HealthTimeoutBenchMarker time.Duration `envconfig:"HEALTH_TIMEOUT_BENCHMARKER" default:"300s"`

	// Snapshotter specific data.
	SnapshotHour int `envconfig:"TIGERA_COMPLIANCE_SNAPSHOT_HOUR" default:"0"`

	// Controller specific data.
	Namespace                  string        `envconfig:"TIGERA_COMPLIANCE_JOB_NAMESPACE" default:"calico-monitoring"`
	JobStartDelay              time.Duration `envconfig:"TIGERA_COMPLIANCE_JOB_START_DELAY" default:"30m"`
	MaxActiveJobs              int           `envconfig:"TIGERA_COMPLIANCE_MAX_ACTIVE_JOBS" default:"1"`
	MaxSuccessfulJobsHistory   int           `envconfig:"TIGERA_COMPLIANCE_MAX_SUCCESSFUL_JOBS_HISTORY" default:"2"`
	MaxFailedJobsHistory       int           `envconfig:"TIGERA_COMPLIANCE_MAX_FAILED_JOBS_HISTORY" default:"10"`
	IgnoreUnstartedReportAfter time.Duration `envconfig:"TIGERA_COMPLIANCE_IGNORE_UNSTARTED_REPORT_AFTER" default:"168h"`
	MaxJobRetries              int32         `envconfig:"TIGERA_COMPLIANCE_MAX_JOB_RETRIES" default:"10"`
	JobPollInterval            time.Duration `envconfig:"TIGERA_COMPLIANCE_JOB_POLL_INTERVAL" default:"10s"`
	JobNamePrefix              string        `envconfig:"TIGERA_COMPLIANCE_JOB_NAME_PREFIX" default:"compliance-reporter."`

	// Reporter specific data. Controller sets this through the environment names.
	ReportName  string `envconfig:"TIGERA_COMPLIANCE_REPORT_NAME"`
	ReportStart string `envconfig:"TIGERA_COMPLIANCE_REPORT_START_TIME"`
	ReportEnd   string `envconfig:"TIGERA_COMPLIANCE_REPORT_END_TIME"`

	// Pod annotation and init container and container regexes used to determine if Envoy is enabled inside the
	// pod. Used by the reporter and passed-thru from the controller.
	PodIstioSidecarAnnotation  string `envconfig:"TIGERA_COMPLIANCE_POD_ISTIO_SIDECAR_ANNOTATION" default:"sidecar.istio.io/status"`
	PodIstioInitContainerRegex string `envconfig:"TIGERA_COMPLIANCE_POD_ISTIO_INIT_CONTAINER_REGEX" default:".*/istio/proxy_init:.*"`
	PodIstioContainerRegex     string `envconfig:"TIGERA_COMPLIANCE_POD_ISTIO_CONTAINER_REGEX" default:".*/istio/proxy.*"`

	// Parsed values.
	ParsedReportStart time.Time
	ParsedReportEnd   time.Time
	ParsedLogLevel    log.Level

	// Nodename
	NodeName string `envconfig:"NODENAME"`

	// This setting is required for es proxy that performs the authentication and authorization for an user.
	EnableMultiClusterClient       bool   `envconfig:"ENABLE_MULTI_CLUSTER_CLIENT" default:"false"`
	MultiClusterForwardingCA       string `envconfig:"MULTI_CLUSTER_FORWARDING_CA" default:"/manager-tls/cert"`
	MultiClusterForwardingEndpoint string `envconfig:"MULTI_CLUSTER_FORWARDING_ENDPOINT" default:"https://tigera-manager.tigera-manager.svc:9443"`

	// Settings for controlling archiving behaviour for Compliance reports (through Fluentd tailed log file)
	// Note: By default the logging to file for archiving is turned on. User is expected to interact with config
	// on Fluentd side (by enabling S3 storage).
	ArchiveLogsEnabled       bool   `envconfig:"TIGERA_COMPLIANCE_ARCHIVE_LOGS_ENABLED" default:"true"`
	ArchiveLogsDirectory     string `envconfig:"TIGERA_COMPLIANCE_ARCHIVE_LOGS_DIR" default:"/var/log/calico/compliance"`
	ArchiveLogsMaxFiles      int    `envconfig:"TIGERA_COMPLIANCE_ARCHIVE_LOGS_MAX_FILES" default:"2"`
	ArchiveLogsMaxFileSizeMB int    `envconfig:"TIGERA_COMPLIANCE_ARCHIVE_LOGS_MAX_FILESIZE_MB" default:"50"`

	// Whether staged network policies should be included in the cache calculations.
	IncludeStagedNetworkPolicies bool `envconfig:"TIGERA_COMPLIANCE_INCLUDE_STAGED_NETWORK_POLICIES" default:"false"`

	// Dex settings for authentication.
	OIDCAuthEnabled        bool   `envconfig:"TIGERA_COMPLIANCE_OIDC_AUTH_ENABLED" default:"false"`
	OIDCAuthIssuer         string `envconfig:"TIGERA_COMPLIANCE_OIDC_AUTH_ISSUER" default:"https://127.0.0.1:5556/dex"`
	OIDCAuthClientID       string `envconfig:"TIGERA_COMPLIANCE_OIDC_AUTH_CLIENT_ID" default:"tigera-manager"`
	OIDCAuthJWKSURL        string `envconfig:"TIGERA_COMPLIANCE_OIDC_AUTH_JWKSURL" default:"https://tigera-dex.tigera-dex.svc.cluster.local:5556/dex/keys"`
	OIDCAuthUsernameClaim  string `envconfig:"TIGERA_COMPLIANCE_OIDC_AUTH_USERNAME_CLAIM" default:"email"`
	OIDCAuthGroupsClaim    string `envconfig:"TIGERA_COMPLIANCE_OIDC_AUTH_GROUPS_CLAIM"`
	OIDCAuthUsernamePrefix string `envconfig:"TIGERA_COMPLIANCE_OIDC_AUTH_USERNAME_PREFIX"`
	OIDCAuthGroupsPrefix   string `envconfig:"TIGERA_COMPLIANCE_OIDC_AUTH_GROUPS_PREFIX"`
}

func MustLoadConfig() *Config {
	c, err := LoadConfig()
	if err != nil {
		log.Panicf("Error loading configuration: %v", err)
	}
	return c
}

func LoadConfig() (*Config, error) {
	var err error
	config := &Config{}
	err = envconfig.Process("", config)
	if err != nil {
		return nil, err
	}

	// Default the start/end times to now.
	now := time.Now()
	config.ParsedReportStart = now
	config.ParsedReportEnd = now

	// If the start/end times are specified, parse them now.
	if config.ReportStart != "" {
		config.ParsedReportStart, err = time.Parse(time.RFC3339, config.ReportStart)
		if err != nil {
			return nil, fmt.Errorf("report start-time specified in environment variable TIGERA_COMPLIANCE_REPORT_START_TIME is not RFC3339 formatted: %s",
				config.ReportStart,
			)
		}
	}

	if config.ReportEnd != "" {
		config.ParsedReportEnd, err = time.Parse(time.RFC3339, config.ReportEnd)
		if err != nil {
			return nil, fmt.Errorf("report end-time specified in environment variable TIGERA_COMPLIANCE_REPORT_END_TIME is not RFC3339 formatted: %s",
				config.ReportEnd,
			)
		}
	}

	if config.ParsedReportEnd.Before(config.ParsedReportStart) {
		return nil, fmt.Errorf("report end-time specified in TIGERA_COMPLIANCE_REPORT_END_TIME cannot be before start-time specified in TIGERA_COMPLIANCE_REPORT_START_TIME: %s < %s",
			config.ParsedReportEnd.Format(time.RFC3339), config.ParsedReportStart.Format(time.RFC3339),
		)
	}

	// Parse log level.
	config.ParsedLogLevel = logutils.SafeParseLogLevel(config.LogLevel)

	// Check snapshot hour is within range.
	if config.SnapshotHour < 0 || config.SnapshotHour > 23 {
		return nil, fmt.Errorf("snapshot-hour defined in environment variable TIGERA_COMPLIANCE_SNAPSHOT_HOUR should be within range 0-23: value=%d",
			config.SnapshotHour,
		)
	}

	return config, nil
}

func (c *Config) InitializeLogging() {
	log.SetFormatter(&logutils.Formatter{})
	log.AddHook(&logutils.ContextHook{})
	log.SetLevel(c.ParsedLogLevel)
}
