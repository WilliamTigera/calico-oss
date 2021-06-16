// Copyright (c) 2016-2021 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	"github.com/projectcalico/libcalico-go/lib/names"
	"github.com/projectcalico/libcalico-go/lib/numorstring"

	"github.com/projectcalico/felix/idalloc"
	"github.com/projectcalico/felix/proto"
	"github.com/projectcalico/typha/pkg/discovery"
)

var (
	// RegexpIfaceElemRegexp matches an individual element in the overall interface list;
	// assumes the value represents a regular expression and is marked by '/' at the start
	// and end and cannot have spaces
	RegexpIfaceElemRegexp = regexp.MustCompile(`^\/[^\s]+\/$`)
	// NonRegexpIfaceElemRegexp matches an individual element in the overall interface list;
	// assumes the value is between 1-15 chars long and only be alphanumeric or - or _
	NonRegexpIfaceElemRegexp = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,15}$`)
	IfaceListRegexp          = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,15}(,[a-zA-Z0-9_-]{1,15})*$`)
	AuthorityRegexp          = regexp.MustCompile(`^[^:/]+:\d+$`)
	HostnameRegexp           = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	StringRegexp             = regexp.MustCompile(`^.*$`)
	IfaceParamRegexp         = regexp.MustCompile(`^[a-zA-Z0-9:._+-]{1,15}$`)
	// Hostname  have to be valid ipv4, ipv6 or strings up to 64 characters.
	HostAddressRegexp = regexp.MustCompile(`^[a-zA-Z0-9:._+-]{1,64}$`)
)

const (
	maxUint = ^uint(0)
	maxInt  = int(maxUint >> 1)
	minInt  = -maxInt - 1
)

// Source of a config value.  Values from higher-numbered sources override
// those from lower-numbered sources.  Note: some parameters (such as those
// needed to connect to the datastore) can only be set from a local source.
type Source uint8

const (
	Default = iota
	DatastoreGlobal
	DatastorePerHost
	ConfigFile
	EnvironmentVariable
	InternalOverride
	DisabledByLicenseCheck
)

// Default stats collection const used globally
const (
	DefaultAgeTimeout               = time.Duration(10) * time.Second
	DefaultInitialReportingDelay    = time.Duration(5) * time.Second
	DefaultExportingInterval        = time.Duration(1) * time.Second
	DefaultConntrackPollingInterval = time.Duration(5) * time.Second
)

var SourcesInDescendingOrder = []Source{DisabledByLicenseCheck, InternalOverride, EnvironmentVariable, ConfigFile, DatastorePerHost, DatastoreGlobal}

func (source Source) String() string {
	switch source {
	case Default:
		return "<default>"
	case DatastoreGlobal:
		return "datastore (global)"
	case DatastorePerHost:
		return "datastore (per-host)"
	case ConfigFile:
		return "config file"
	case EnvironmentVariable:
		return "environment variable"
	case DisabledByLicenseCheck:
		return "license check"
	case InternalOverride:
		return "internal override"
	}
	return fmt.Sprintf("<unknown(%v)>", uint8(source))
}

func (source Source) Local() bool {
	switch source {
	case Default, ConfigFile, EnvironmentVariable, InternalOverride:
		return true
	default:
		return false
	}
}

// Provider represents a particular provider or flavor of Kubernetes.
type Provider uint8

const (
	ProviderNone Provider = iota
	ProviderEKS
	ProviderGKE
	ProviderAKS
	ProviderOpenShift
	ProviderDockerEE
)

func (p Provider) String() string {
	switch p {
	case ProviderNone:
		return ""
	case ProviderEKS:
		return "EKS"
	case ProviderGKE:
		return "GKE"
	case ProviderAKS:
		return "AKS"
	case ProviderOpenShift:
		return "OpenShift"
	case ProviderDockerEE:
		return "DockerEnterprise"
	default:
		return fmt.Sprintf("<unknown-provider(%v)>", uint8(p))
	}
}

func newProvider(s string) (Provider, error) {
	switch strings.ToLower(s) {
	case strings.ToLower(ProviderNone.String()):
		return ProviderNone, nil
	case strings.ToLower(ProviderEKS.String()):
		return ProviderEKS, nil
	case strings.ToLower(ProviderGKE.String()):
		return ProviderGKE, nil
	case strings.ToLower(ProviderAKS.String()):
		return ProviderAKS, nil
	case strings.ToLower(ProviderOpenShift.String()):
		return ProviderOpenShift, nil
	case strings.ToLower(ProviderDockerEE.String()):
		return ProviderDockerEE, nil
	default:
		return 0, fmt.Errorf("unknown provider %s", s)
	}
}

// Config contains the best, parsed config values loaded from the various sources.
// We use tags to control the parsing and validation.
type Config struct {
	// Configuration parameters.
	UseInternalDataplaneDriver bool   `config:"bool;true"`
	DataplaneDriver            string `config:"file(must-exist,executable);calico-iptables-plugin;non-zero,die-on-fail,skip-default-validation"`

	// Wireguard configuration
	WireguardEnabled             bool   `config:"bool;false"`
	WireguardListeningPort       int    `config:"int;51820"`
	WireguardRoutingRulePriority int    `config:"int;99"`
	WireguardInterfaceName       string `config:"iface-param;wireguard.cali;non-zero"`
	WireguardMTU                 int    `config:"int;0"`

	BPFEnabled                         bool           `config:"bool;false"`
	BPFDisableUnprivileged             bool           `config:"bool;true"`
	BPFLogLevel                        string         `config:"oneof(off,info,debug);off;non-zero"`
	BPFDataIfacePattern                *regexp.Regexp `config:"regexp;^((en|wl|ww|sl|ib)[opsx].*|(eth|wlan|wwan).*|tunl0$|vxlan.calico$|egress.calico$|wireguard.cali$)"`
	BPFConnectTimeLoadBalancingEnabled bool           `config:"bool;true"`
	BPFExternalServiceMode             string         `config:"oneof(tunnel,dsr);tunnel;non-zero"`
	BPFKubeProxyIptablesCleanupEnabled bool           `config:"bool;true"`
	BPFKubeProxyMinSyncPeriod          time.Duration  `config:"seconds;1"`
	BPFKubeProxyEndpointSlicesEnabled  bool           `config:"bool;false"`
	BPFExtToServiceConnmark            int            `config:"int;0"`
	FlowLogsCollectProcessInfo         bool           `config:"bool;false"`
	FlowLogsCollectTcpStats            bool           `config:"bool;false"`

	// DebugBPFCgroupV2 controls the cgroup v2 path that we apply the connect-time load balancer to.  Most distros
	// are configured for cgroup v1, which prevents all but hte root cgroup v2 from working so this is only useful
	// for development right now.
	DebugBPFCgroupV2 string `config:"string;;local"`
	// DebugBPFMapRepinEnabled can be used to prevent Felix from repinning its BPF maps at startup.  This is useful for
	// testing with multiple Felix instances running on one host.
	DebugBPFMapRepinEnabled bool `config:"bool;false;local"`

	DatastoreType string `config:"oneof(kubernetes,etcdv3);etcdv3;non-zero,die-on-fail,local"`

	FelixHostname string `config:"hostname;;local,non-zero"`
	NodeIP        net.IP `config:"ipv4;;"`

	EtcdAddr      string   `config:"authority;127.0.0.1:2379;local"`
	EtcdScheme    string   `config:"oneof(http,https);http;local"`
	EtcdKeyFile   string   `config:"file(must-exist);;local"`
	EtcdCertFile  string   `config:"file(must-exist);;local"`
	EtcdCaFile    string   `config:"file(must-exist);;local"`
	EtcdEndpoints []string `config:"endpoint-list;;local"`

	TyphaAddr           string        `config:"authority;;local"`
	TyphaK8sServiceName string        `config:"string;;local"`
	TyphaK8sNamespace   string        `config:"string;kube-system;non-zero,local"`
	TyphaReadTimeout    time.Duration `config:"seconds;30;local"`
	TyphaWriteTimeout   time.Duration `config:"seconds;10;local"`

	// Client-side TLS config for Felix's communication with Typha.  If any of these are
	// specified, they _all_ must be - except that either TyphaCN or TyphaURISAN may be left
	// unset.  Felix will then initiate a secure (TLS) connection to Typha.  Typha must present
	// a certificate signed by a CA in TyphaCAFile, and with CN matching TyphaCN or URI SAN
	// matching TyphaURISAN.
	TyphaKeyFile  string `config:"file(must-exist);;local"`
	TyphaCertFile string `config:"file(must-exist);;local"`
	TyphaCAFile   string `config:"file(must-exist);;local"`
	TyphaCN       string `config:"string;;local"`
	TyphaURISAN   string `config:"string;;local"`

	Ipv6Support bool `config:"bool;true"`

	IptablesBackend                    string            `config:"oneof(legacy,nft,auto);auto"`
	RouteRefreshInterval               time.Duration     `config:"seconds;90"`
	InterfaceRefreshInterval           time.Duration     `config:"seconds;90"`
	DeviceRouteSourceAddress           net.IP            `config:"ipv4;"`
	DeviceRouteProtocol                int               `config:"int;3"`
	RemoveExternalRoutes               bool              `config:"bool;true"`
	IptablesRefreshInterval            time.Duration     `config:"seconds;90"`
	IptablesPostWriteCheckIntervalSecs time.Duration     `config:"seconds;1"`
	IptablesLockFilePath               string            `config:"file;/run/xtables.lock"`
	IptablesLockTimeoutSecs            time.Duration     `config:"seconds;0"`
	IptablesLockProbeIntervalMillis    time.Duration     `config:"millis;50"`
	FeatureDetectOverride              map[string]string `config:"keyvaluelist;;"`
	IpsetsRefreshInterval              time.Duration     `config:"seconds;10"`
	MaxIpsetSize                       int               `config:"int;1048576;non-zero"`
	XDPRefreshInterval                 time.Duration     `config:"seconds;90"`

	WindowsNetworkName *regexp.Regexp `config:"regexp;(?i)calico.*"`

	PolicySyncPathPrefix string `config:"file;;"`

	NetlinkTimeoutSecs time.Duration `config:"seconds;10"`

	MetadataAddr string `config:"hostname;127.0.0.1;die-on-fail"`
	MetadataPort int    `config:"int(0:65535);8775;die-on-fail"`

	OpenstackRegion string `config:"region;;die-on-fail"`

	InterfacePrefix  string           `config:"iface-list;cali;non-zero,die-on-fail"`
	InterfaceExclude []*regexp.Regexp `config:"iface-list-regexp;kube-ipvs0"`

	ChainInsertMode             string `config:"oneof(insert,append);insert;non-zero,die-on-fail"`
	DefaultEndpointToHostAction string `config:"oneof(DROP,RETURN,ACCEPT);DROP;non-zero,die-on-fail"`
	DropActionOverride          string `config:"oneof(DROP,ACCEPT,LOGandDROP,LOGandACCEPT);DROP;non-zero,die-on-fail"`
	IptablesFilterAllowAction   string `config:"oneof(ACCEPT,RETURN);ACCEPT;non-zero,die-on-fail"`
	IptablesMangleAllowAction   string `config:"oneof(ACCEPT,RETURN);ACCEPT;non-zero,die-on-fail"`
	LogPrefix                   string `config:"string;calico-drop"`
	LogDropActionOverride       bool   `config:"bool;false"`

	LogFilePath string `config:"file;/var/log/calico/felix.log;die-on-fail"`

	LogSeverityFile   string `config:"oneof(DEBUG,INFO,WARNING,ERROR,FATAL);INFO"`
	LogSeverityScreen string `config:"oneof(DEBUG,INFO,WARNING,ERROR,FATAL);INFO"`
	LogSeveritySys    string `config:"oneof(DEBUG,INFO,WARNING,ERROR,FATAL);INFO"`

	VXLANEnabled        bool   `config:"bool;false"`
	VXLANPort           int    `config:"int;4789"`
	VXLANVNI            int    `config:"int;4096"`
	VXLANMTU            int    `config:"int;0"`
	IPv4VXLANTunnelAddr net.IP `config:"ipv4;"`
	VXLANTunnelMACAddr  string `config:"string;"`

	IpInIpEnabled    bool   `config:"bool;false"`
	IpInIpMtu        int    `config:"int;0"`
	IpInIpTunnelAddr net.IP `config:"ipv4;"`

	// Knobs provided to explicitly control whether we add rules to drop encap traffic
	// from workloads. We always add them unless explicitly requested not to add them.
	AllowVXLANPacketsFromWorkloads bool `config:"bool;false"`
	AllowIPIPPacketsFromWorkloads  bool `config:"bool;false"`

	AWSSrcDstCheck string `config:"oneof(DoNothing,Enable,Disable);DoNothing;non-zero"`

	ServiceLoopPrevention string `config:"oneof(Drop,Reject,Disabled);Drop"`

	ReportingIntervalSecs time.Duration `config:"seconds;30"`
	ReportingTTLSecs      time.Duration `config:"seconds;90"`

	EndpointReportingEnabled   bool          `config:"bool;false"`
	EndpointReportingDelaySecs time.Duration `config:"seconds;1"`

	IptablesMarkMask uint32 `config:"mark-bitmask;0xffff0000;non-zero,die-on-fail"`

	DisableConntrackInvalidCheck bool `config:"bool;false"`

	EnableNflogSize bool `config:"bool;false"`

	HealthEnabled                   bool   `config:"bool;false"`
	HealthPort                      int    `config:"int(0:65535);9099"`
	HealthHost                      string `config:"host-address;localhost"`
	PrometheusMetricsEnabled        bool   `config:"bool;false"`
	PrometheusMetricsHost           string `config:"host-address;"`
	PrometheusMetricsPort           int    `config:"int(0:65535);9091"`
	PrometheusGoMetricsEnabled      bool   `config:"bool;true"`
	PrometheusProcessMetricsEnabled bool   `config:"bool;true"`
	PrometheusMetricsCertFile       string `config:"file(must-exist);"`
	PrometheusMetricsKeyFile        string `config:"file(must-exist);"`
	PrometheusMetricsCAFile         string `config:"file(must-exist);"`

	FailsafeInboundHostPorts  []ProtoPort `config:"port-list;tcp:22,udp:68,tcp:179,tcp:2379,tcp:2380,tcp:5473,tcp:6443,tcp:6666,tcp:6667;die-on-fail"`
	FailsafeOutboundHostPorts []ProtoPort `config:"port-list;udp:53,udp:67,tcp:179,tcp:2379,tcp:2380,tcp:5473,tcp:6443,tcp:6666,tcp:6667;die-on-fail"`

	NfNetlinkBufSize int `config:"int;65536"`

	StatsDumpFilePath string `config:"file;/var/log/calico/stats/dump;die-on-fail"`

	PrometheusReporterEnabled   bool          `config:"bool;false"`
	PrometheusReporterPort      int           `config:"int(0:65535);9092"`
	PrometheusReporterCertFile  string        `config:"file(must-exist);"`
	PrometheusReporterKeyFile   string        `config:"file(must-exist);"`
	PrometheusReporterCAFile    string        `config:"file(must-exist);"`
	SyslogReporterNetwork       string        `config:"string;"`
	SyslogReporterAddress       string        `config:"string;"`
	DeletedMetricsRetentionSecs time.Duration `config:"seconds;30"`

	FlowLogsEnableHostEndpoint     bool          `config:"bool;false"`
	FlowLogsFlushInterval          time.Duration `config:"seconds;300"`
	FlowLogsEnableNetworkSets      bool          `config:"bool;false"`
	FlowLogsMaxOriginalIPsIncluded int           `config:"int;50"`

	FlowLogsFileEnabled                   bool   `config:"bool;false"`
	FlowLogsFileDirectory                 string `config:"string;/var/log/calico/flowlogs"`
	FlowLogsFileMaxFiles                  int    `config:"int;5"`
	FlowLogsFileMaxFileSizeMB             int    `config:"int;100"`
	FlowLogsFileAggregationKindForAllowed int    `config:"int(0:3);2"`
	FlowLogsFileAggregationKindForDenied  int    `config:"int(0:3);1"`
	FlowLogsFileIncludeService            bool   `config:"bool;false"`
	FlowLogsFileIncludeLabels             bool   `config:"bool;false"`
	FlowLogsFileIncludePolicies           bool   `config:"bool;false"`
	FlowLogsFileEnabledForAllowed         bool   `config:"bool;true"`
	FlowLogsFileEnabledForDenied          bool   `config:"bool;true"`
	FlowLogsDynamicAggregationEnabled     bool   `config:"bool;false"`
	FlowLogsPositionFilePath              string `config:"string;/var/log/calico/flows.log.pos"`
	FlowLogsAggregationThresholdBytes     int    `config:"int;8192"`
	FlowLogsFilePerFlowProcessLimit       int    `config:"int;2"`

	DNSLogsFlushInterval       time.Duration `config:"seconds;300"`
	DNSLogsFileEnabled         bool          `config:"bool;false"`
	DNSLogsFileDirectory       string        `config:"string;/var/log/calico/dnslogs"`
	DNSLogsFileMaxFiles        int           `config:"int;5"`
	DNSLogsFileMaxFileSizeMB   int           `config:"int;100"`
	DNSLogsFileAggregationKind int           `config:"int(0:1);1"`
	DNSLogsFileIncludeLabels   bool          `config:"bool;true"`
	DNSLogsFilePerNodeLimit    int           `config:"int;0"`
	DNSLogsLatency             bool          `config:"bool;true"`

	L7LogsFlushInterval                  time.Duration `config:"seconds;300"`
	L7LogsFileEnabled                    bool          `config:"bool;true"`
	L7LogsFileDirectory                  string        `config:"string;/var/log/calico/l7logs"`
	L7LogsFileMaxFiles                   int           `config:"int;5"`
	L7LogsFileMaxFileSizeMB              int           `config:"int;100"`
	L7LogsFileAggregationHTTPHeaderInfo  string        `config:"string;ExcludeL7HTTPHeaderInfo"`
	L7LogsFileAggregationHTTPMethod      string        `config:"string;IncludeL7HTTPMethod"`
	L7LogsFileAggregationServiceInfo     string        `config:"string;IncludeL7ServiceInfo"`
	L7LogsFileAggregationDestinationInfo string        `config:"string;IncludeL7DestinationInfo"`
	L7LogsFileAggregationSourceInfo      string        `config:"string;IncludeL7SourceInfo"`
	L7LogsFileAggregationResponseCode    string        `config:"string;IncludeL7ResponseCode"`
	L7LogsFileAggregationTrimURL         string        `config:"string;IncludeL7FullURL"`
	L7LogsFileAggregationNumURLPath      int           `config:"int;5"`
	L7LogsFileAggregationURLCharLimit    int           `config:"int;250"`
	L7LogsFilePerNodeLimit               int           `config:"int;1500"`

	WindowsFlowLogsFileDirectory    string        `config:"string;c:\\TigeraCalico\\flowlogs"`
	WindowsFlowLogsPositionFilePath string        `config:"string;c:\\TigeraCalico\\flowlogs\\flows.log.pos"`
	WindowsStatsDumpFilePath        string        `config:"file;c:\\TigeraCalico\\stats\\dump;die-on-fail"`
	WindowsDNSCacheFile             string        `config:"file;c:\\TigeraCalico\\felix-dns-cache.txt"`
	WindowsDNSExtraTTL              time.Duration `config:"seconds;120"`

	KubeNodePortRanges []numorstring.Port `config:"portrange-list;30000:32767"`
	NATPortRange       numorstring.Port   `config:"portrange;"`
	NATOutgoingAddress net.IP             `config:"ipv4;"`

	// TSEE no longer does any usage reporting, but we still support the config fields
	// so as not to break deployments that set them.  (In particular, not to break the
	// case where a deployment sets UsageReportingEnabled to false.)
	UsageReportingEnabled          bool          `config:"bool;true"`
	UsageReportingInitialDelaySecs time.Duration `config:"seconds;300"`
	UsageReportingIntervalSecs     time.Duration `config:"seconds;86400"`
	ClusterGUID                    string        `config:"string;baddecaf"`
	ClusterType                    string        `config:"string;"`
	CalicoVersion                  string        `config:"string;"`
	CNXVersion                     string        `config:"string;"`

	ExternalNodesCIDRList []string `config:"cidr-list;;die-on-fail"`

	DebugMemoryProfilePath          string        `config:"file;;"`
	DebugCPUProfilePath             string        `config:"file;/tmp/felix-cpu-<timestamp>.pprof;"`
	DebugDisableLogDropping         bool          `config:"bool;false"`
	DebugSimulateCalcGraphHangAfter time.Duration `config:"seconds;0"`
	DebugSimulateDataplaneHangAfter time.Duration `config:"seconds;0"`
	DebugUseShortPollIntervals      bool          `config:"bool;false"`
	DebugCloudWatchLogsFile         string        `config:"file;;"`
	DebugWindowsPktMonStartArgs     string        `config:"string;"`

	// IPSecMode controls which mode IPSec is operating on.
	// Default value means IPSec is not enabled.
	IPSecMode string `config:"string;"`
	// IPSecAllowUnsecuredTraffic, when set to true, IPsec will be used opportunistically on packet paths where it is
	// supported but non-IPsec traffic will still be allowed.  This setting is useful for transitioning to/from IPsec
	// since it allows traffic to keep flowing while the IPsec peerings are being set up or torn down.  However,
	// it allows an attacker who has penetrated the network to inject packets and, if they can disrupt the IPsec
	// peerings, they may be able to force user traffic to be sent unencrypted.
	IPSecAllowUnsecuredTraffic bool `config:"bool;false"`
	// File contains PSK.
	IPSecPSKFile string `config:"file(must-exist);;local"`
	// Defaults are the RFC 6379 Suite B recommendations:
	// https://wiki.strongswan.org/projects/strongswan/wiki/IKEv2CipherSuites#Suite-B-Cryptographic-Suites-for-IPsec-RFC-6379
	IPSecIKEAlgorithm string `config:"string;aes128gcm16-prfsha256-ecp256"`
	IPSecESPAlgorithm string `config:"string;aes128gcm16-ecp256"`
	// IPSecLogLevel controls log level for IPSec components. [Default: Info]
	IPSecLogLevel              string        `config:"oneof(NOTICE,INFO,DEBUG,VERBOSE);INFO"`
	IPSecPolicyRefreshInterval time.Duration `config:"seconds;600"`

	IPSecRekeyTime time.Duration `config:"seconds;3600"`

	DebugPanicAfter       time.Duration `config:"seconds;0"`
	DebugSimulateDataRace bool          `config:"bool;false"`

	// Configure where Felix gets its routing information.
	// - workloadIPs: use workload endpoints to construct routes.
	// - calicoIPAM: use IPAM data to contruct routes.
	RouteSource string `config:"oneof(WorkloadIPs,CalicoIPAM);CalicoIPAM"`

	RouteTableRange idalloc.IndexRange `config:"route-table-range;1-250;die-on-fail"`

	IptablesNATOutgoingInterfaceFilter string `config:"iface-param;"`

	SidecarAccelerationEnabled bool `config:"bool;false"`
	// XDP is not supported in Calico Enterprise. Disable it by default.
	XDPEnabled        bool `config:"bool;false"`
	GenericXDPEnabled bool `config:"bool;false"`

	// Config for egress gateways.
	EgressIPSupport             string `config:"oneof(Disabled,EnabledPerNamespace,EnabledPerNamespaceOrPerPod);Disabled"`
	EgressIPVXLANPort           int    `config:"int;4790"`
	EgressIPVXLANVNI            int    `config:"int;4097"`
	EgressIPRoutingRulePriority int    `config:"int;100"`

	// Config for DNS policy.
	DNSCacheFile         string        `config:"file;/var/run/calico/felix-dns-cache.txt"`
	DNSCacheSaveInterval time.Duration `config:"seconds;60"`
	DNSTrustedServers    []ServerPort  `config:"server-list;k8s-service:kube-dns"`
	DNSCacheEpoch        int           `config:"int;0"`
	DNSExtraTTL          time.Duration `config:"seconds;0"`

	// Config for packet capturing
	CaptureDir             string `config:"string;/var/log/calico/pcap"`
	CaptureMaxSizeBytes    int    `config:"int;10000000"`
	CaptureRotationSeconds int    `config:"int;3600"`
	CaptureMaxFiles        int    `config:"int;2"`

	Variant string `config:"string;CalicoEnterprise"`

	// Configures MTU auto-detection.
	MTUIfacePattern *regexp.Regexp `config:"regexp;^((en|wl|ww|sl|ib)[opsx].*|(eth|wlan|wwan).*)"`

	// Configures Transparent proxying modes
	TPROXYMode string `config:"oneof(Disabled,Enabled,EnabledAllServices);Disabled"`
	TPROXYPort int    `config:"int;16001"`

	// State tracking.

	// internalOverrides contains our highest priority config source, generated from internal constraints
	// such as kernel version support.
	internalOverrides map[string]string
	// sourceToRawConfig maps each source to the set of config that was give to us via UpdateFrom.
	sourceToRawConfig map[Source]map[string]string
	// rawValues maps keys to the current highest-priority raw value.
	rawValues map[string]string
	// Err holds the most recent error from a config update.
	Err error

	loadClientConfigFromEnvironment func() (*apiconfig.CalicoAPIConfig, error)

	useNodeResourceUpdates bool
}

// Copy makes a copy of the object.  Internal state is deep copied but config parameters are only shallow copied.
// This saves work since updates to the copy will trigger the config params to be recalculated.
func (config *Config) Copy() *Config {
	// Start by shallow-copying the object.
	cp := *config

	// Copy the internal state over as a deep copy.
	cp.internalOverrides = map[string]string{}
	for k, v := range config.internalOverrides {
		cp.internalOverrides[k] = v
	}

	cp.sourceToRawConfig = map[Source]map[string]string{}
	for k, v := range config.sourceToRawConfig {
		cp.sourceToRawConfig[k] = map[string]string{}
		for k2, v2 := range v {
			cp.sourceToRawConfig[k][k2] = v2
		}
	}

	cp.rawValues = map[string]string{}
	for k, v := range config.rawValues {
		cp.rawValues[k] = v
	}

	return &cp
}

type ProtoPort struct {
	Net      string
	Protocol string
	Port     uint16
}

type ServerPort struct {
	IP   string
	Port uint16
}

// Load parses and merges the rawData from one particular source into this config object.
// If there is a config value already loaded from a higher-priority source, then
// the new value will be ignored (after validation).
func (config *Config) UpdateFrom(rawData map[string]string, source Source) (changed bool, err error) {
	log.Infof("Merging in config from %v: %v", source, rawData)
	// Defensively take a copy of the raw data, in case we've been handed
	// a mutable map by mistake.
	rawDataCopy := make(map[string]string)
	for k, v := range rawData {
		if v == "" {
			log.WithFields(log.Fields{
				"name":   k,
				"source": source,
			}).Info("Ignoring empty configuration parameter. Use value 'none' if " +
				"your intention is to explicitly disable the default value.")
			continue
		}
		rawDataCopy[k] = v
	}
	config.sourceToRawConfig[source] = rawDataCopy

	changed, err = config.resolve()
	return
}

func (config *Config) IsLeader() bool {
	return config.Variant == "CalicoEnterprise"
}

func (config *Config) InterfacePrefixes() []string {
	return strings.Split(config.InterfacePrefix, ",")
}

func (config *Config) OpenstackActive() bool {
	if strings.Contains(strings.ToLower(config.ClusterType), "openstack") {
		// OpenStack is explicitly known to be present.  Newer versions of the OpenStack plugin
		// set this flag.
		log.Debug("Cluster type contains OpenStack")
		return true
	}
	// If we get here, either OpenStack isn't present or we're running against an old version
	// of the OpenStack plugin, which doesn't set the flag.  Use heuristics based on the
	// presence of the OpenStack-related parameters.
	if config.MetadataAddr != "" && config.MetadataAddr != "127.0.0.1" {
		log.Debug("OpenStack metadata IP set to non-default, assuming OpenStack active")
		return true
	}
	if config.MetadataPort != 0 && config.MetadataPort != 8775 {
		log.Debug("OpenStack metadata port set to non-default, assuming OpenStack active")
		return true
	}
	for _, prefix := range config.InterfacePrefixes() {
		if prefix == "tap" {
			log.Debug("Interface prefix list contains 'tap', assuming OpenStack")
			return true
		}
	}
	log.Debug("No evidence this is an OpenStack deployment; disabling OpenStack special-cases")
	return false
}

func (c *Config) EgressIPCheckEnabled() bool {
	return c.EgressIPSupport == "EnabledPerNamespace" || c.EgressIPSupport == "EnabledPerNamespaceOrPerPod"
}

func (c *Config) IPSecEnabled() bool {
	return c.IPSecMode != "" && c.IPSecIKEAlgorithm != "" && c.IPSecESPAlgorithm != ""
}

func (c *Config) GetPSKFromFile() string {
	// Extract pre-shared key from file.
	if c.IPSecMode != "PSK" {
		return ""
	}

	if _, err := os.Stat(c.IPSecPSKFile); os.IsNotExist(err) {
		log.Panicf("error file does not exist for PSK: %s", c.IPSecPSKFile)
	}

	data, err := ioutil.ReadFile(c.IPSecPSKFile)
	if err != nil {
		log.Panicf("error reading PSK from file: %s, err %v", c.IPSecPSKFile, err)
	}
	if len(data) == 0 {
		log.Panicf("error reading PSK from file: %s, read zero bytes", c.IPSecPSKFile)
	}

	return string(data)
}

// KubernetesProvider attempts to parse the kubernetes provider, e.g. AKS out of the ClusterType.
// The ClusterType is a string which contains a set of comma-separated values in no particular order.
func (config *Config) KubernetesProvider() Provider {
	settings := strings.Split(config.ClusterType, ",")
	for _, s := range settings {
		p, err := newProvider(s)
		if err == nil {
			log.WithFields(log.Fields{"clusterType": config.ClusterType, "provider": p}).Debug(
				"detected a known kubernetes provider")
			return p
		}
	}

	log.WithField("clusterType", config.ClusterType).Debug(
		"failed to detect a known kubernetes provider, defaulting to none")
	return ProviderNone
}

func (config *Config) resolve() (changed bool, err error) {
	newRawValues := make(map[string]string)
	// Map from lower-case version of name to the highest-priority source found so far.
	// We use the lower-case version of the name since we can calculate it both for
	// expected and "raw" parameters, which may be used by plugins.
	nameToSource := make(map[string]Source)
	for _, source := range SourcesInDescendingOrder {
	valueLoop:
		for rawName, rawValue := range config.sourceToRawConfig[source] {
			lowerCaseName := strings.ToLower(rawName)
			currentSource := nameToSource[lowerCaseName]
			param, ok := knownParams[lowerCaseName]
			if !ok {
				if source >= currentSource {
					// Stash the raw value in case it's useful for an external
					// dataplane driver.  Use the raw name since the driver may
					// want it.
					newRawValues[rawName] = rawValue
					nameToSource[lowerCaseName] = source
				}
				log.WithField("raw name", rawName).Info(
					"Ignoring unknown config param.")
				continue valueLoop
			}
			metadata := param.GetMetadata()
			name := metadata.Name
			if metadata.Local && !source.Local() {
				log.Warningf("Ignoring local-only configuration for %v from %v",
					name, source)
				continue valueLoop
			}

			log.Infof("Parsing value for %v: %v (from %v)",
				name, rawValue, source)
			var value interface{}
			if strings.ToLower(rawValue) == "none" {
				// Special case: we allow a value of "none" to force the value to
				// the zero value for a field.  The zero value often differs from
				// the default value.  Typically, the zero value means "turn off
				// the feature".
				if metadata.NonZero {
					err = errors.New("non-zero field cannot be set to none")
					log.Errorf(
						"Failed to parse value for %v: %v from source %v. %v",
						name, rawValue, source, err)
					config.Err = err
					return
				}
				value = metadata.ZeroValue
				log.Infof("Value set to 'none', replacing with zero-value: %#v.",
					value)
			} else {
				value, err = param.Parse(rawValue)
				if err != nil {
					logCxt := log.WithError(err).WithField("source", source)
					if metadata.DieOnParseFailure {
						logCxt.Error("Invalid (required) config value.")
						config.Err = err
						return
					} else {
						logCxt.WithField("default", metadata.Default).Warn(
							"Replacing invalid value with default")
						value = metadata.Default
						err = nil
					}
				}
			}

			log.Infof("Parsed value for %v: %v (from %v)",
				name, value, source)
			if source < currentSource {
				log.Infof("Skipping config value for %v from %v; "+
					"already have a value from %v", name,
					source, currentSource)
				continue
			}
			field := reflect.ValueOf(config).Elem().FieldByName(name)
			field.Set(reflect.ValueOf(value))
			newRawValues[name] = rawValue
			nameToSource[lowerCaseName] = source
		}
	}

	if config.IPSecMode != "" && config.IpInIpEnabled {
		log.Info("IPsec is enabled, ignoring IPIP configuration")
		config.IpInIpEnabled = false
		delete(newRawValues, "IpInIpEnabled")
		config.IpInIpTunnelAddr = nil
		delete(newRawValues, "IpInIpTunnelAddr")
	}

	changed = !reflect.DeepEqual(newRawValues, config.rawValues)
	config.rawValues = newRawValues
	return
}

func (config *Config) setBy(name string, source Source) bool {
	_, set := config.sourceToRawConfig[source][name]
	return set
}

func (config *Config) setByConfigFileOrEnvironment(name string) bool {
	return config.setBy(name, ConfigFile) || config.setBy(name, EnvironmentVariable)
}

func (config *Config) DatastoreConfig() apiconfig.CalicoAPIConfig {
	// We want Felix's datastore connection to be fully configurable using the same
	// CALICO_XXX_YYY (or just XXX_YYY) environment variables that work for any libcalico-go
	// client - for both the etcdv3 and KDD cases.  However, for the etcd case, Felix has for a
	// long time supported FELIX_XXXYYY environment variables, and we want those to keep working
	// too.

	// To achieve that, first build a CalicoAPIConfig using libcalico-go's
	// LoadClientConfigFromEnvironment - which means incorporating defaults and CALICO_XXX_YYY
	// and XXX_YYY variables.
	cfg, err := config.loadClientConfigFromEnvironment()
	if err != nil {
		log.WithError(err).Panic("Failed to create datastore config")
	}

	// Now allow FELIX_XXXYYY variables or XxxYyy config file settings to override that, in the
	// etcd case. Note that that etcd options are set even if the DatastoreType isn't etcdv3.
	// This allows the user to rely the default DatastoreType being etcdv3 and still being able
	// to configure the other etcdv3 options. As of the time of this code change, the etcd options
	// have no affect if the DatastoreType is not etcdv3.

	// Datastore type, either etcdv3 or kubernetes
	if config.setByConfigFileOrEnvironment("DatastoreType") {
		log.Infof("Overriding DatastoreType from felix config to %s", config.DatastoreType)
		if config.DatastoreType == string(apiconfig.EtcdV3) {
			cfg.Spec.DatastoreType = apiconfig.EtcdV3
		} else if config.DatastoreType == string(apiconfig.Kubernetes) {
			cfg.Spec.DatastoreType = apiconfig.Kubernetes
		}
	}

	// Endpoints.
	if config.setByConfigFileOrEnvironment("EtcdEndpoints") && len(config.EtcdEndpoints) > 0 {
		log.Infof("Overriding EtcdEndpoints from felix config to %s", config.EtcdEndpoints)
		cfg.Spec.EtcdEndpoints = strings.Join(config.EtcdEndpoints, ",")
		cfg.Spec.DatastoreType = apiconfig.EtcdV3
	} else if config.setByConfigFileOrEnvironment("EtcdAddr") {
		etcdEndpoints := config.EtcdScheme + "://" + config.EtcdAddr
		log.Infof("Overriding EtcdEndpoints from felix config to %s", etcdEndpoints)
		cfg.Spec.EtcdEndpoints = etcdEndpoints
		cfg.Spec.DatastoreType = apiconfig.EtcdV3
	}
	// TLS.
	if config.setByConfigFileOrEnvironment("EtcdKeyFile") {
		log.Infof("Overriding EtcdKeyFile from felix config to %s", config.EtcdKeyFile)
		cfg.Spec.EtcdKeyFile = config.EtcdKeyFile
	}
	if config.setByConfigFileOrEnvironment("EtcdCertFile") {
		log.Infof("Overriding EtcdCertFile from felix config to %s", config.EtcdCertFile)
		cfg.Spec.EtcdCertFile = config.EtcdCertFile
	}
	if config.setByConfigFileOrEnvironment("EtcdCaFile") {
		log.Infof("Overriding EtcdCaFile from felix config to %s", config.EtcdCaFile)
		cfg.Spec.EtcdCACertFile = config.EtcdCaFile
	}

	if !(config.IpInIpEnabled || config.VXLANEnabled || config.BPFEnabled || config.IPSecEnabled()) {
		// Polling k8s for node updates is expensive (because we get many superfluous
		// updates) so disable if we don't need it.
		log.Info("Encap disabled, disabling node poll (if KDD is in use).")
		cfg.Spec.K8sDisableNodePoll = true
	}
	return *cfg
}

// Validate() performs cross-field validation.
func (config *Config) Validate() (err error) {
	if config.FelixHostname == "" {
		err = errors.New("Failed to determine hostname")
	}

	if config.DatastoreType == "etcdv3" && len(config.EtcdEndpoints) == 0 {
		if config.EtcdScheme == "" {
			err = errors.New("EtcdEndpoints and EtcdScheme both missing")
		}
		if config.EtcdAddr == "" {
			err = errors.New("EtcdEndpoints and EtcdAddr both missing")
		}
	}

	// If any client-side TLS config parameters are specified, they _all_ must be - except that
	// either TyphaCN or TyphaURISAN may be left unset.
	if config.TyphaCAFile != "" ||
		config.TyphaCertFile != "" ||
		config.TyphaKeyFile != "" ||
		config.TyphaCN != "" ||
		config.TyphaURISAN != "" {
		// Some TLS config specified.
		if config.TyphaKeyFile == "" ||
			config.TyphaCertFile == "" ||
			config.TyphaCAFile == "" ||
			(config.TyphaCN == "" && config.TyphaURISAN == "") {
			err = errors.New("If any Felix-Typha TLS config parameters are specified," +
				" they _all_ must be" +
				" - except that either TyphaCN or TyphaURISAN may be left unset.")
		}
	}

	if config.IPSecMode != "" {
		var problems []string
		if config.IPSecPSKFile == "" {
			problems = append(problems, "IPSecPSKFile is not set")
		} else if _, err := os.Stat(config.IPSecPSKFile); os.IsNotExist(err) {
			problems = append(problems, "IPSecPSKFile not found")
		}
		if config.IPSecIKEAlgorithm == "" {
			problems = append(problems, "IPSecIKEAlgorithm is not set")
		}
		if config.IPSecESPAlgorithm == "" {
			problems = append(problems, "IPSecESPAlgorithm is not set")
		}
		if len(config.NodeIP) == 0 {
			problems = append(problems, "Node resource for this node is missing its IP")
		}
		if problems != nil {
			err = errors.New("IPsec is misconfigured: " + strings.Join(problems, "; "))
		}
	}

	if config.FlowLogsFileEnabled {
		if !config.FlowLogsFileEnabledForAllowed && !config.FlowLogsFileEnabledForDenied {
			err = errors.New("FlowLogsFileEnabled is set to true. " +
				"Enable at least one of FlowLogsFileEnabledForAllowed or " +
				"FlowLogsFileEnabledForDenied")
		}
	}

	if err != nil {
		config.Err = err
	}
	return
}

var knownParams map[string]param

func loadParams() {
	knownParams = make(map[string]param)
	config := Config{}
	kind := reflect.TypeOf(config)
	metaRegexp := regexp.MustCompile(`^([^;(]+)(?:\(([^)]*)\))?;` +
		`([^;]*)(?:;` +
		`([^;]*))?$`)
	for ii := 0; ii < kind.NumField(); ii++ {
		field := kind.Field(ii)
		tag := field.Tag.Get("config")
		if tag == "" {
			continue
		}
		captures := metaRegexp.FindStringSubmatch(tag)
		if len(captures) == 0 {
			log.Panicf("Failed to parse metadata for config param %v", field.Name)
		}
		log.Debugf("%v: metadata captures: %#v", field.Name, captures)
		kind := captures[1]       // Type: "int|oneof|bool|port-list|..."
		kindParams := captures[2] // Parameters for the type: e.g. for oneof "http,https"
		defaultStr := captures[3] // Default value e.g "1.0"
		flags := captures[4]
		var param param
		var err error
		switch kind {
		case "bool":
			param = &BoolParam{}
		case "int":
			intParam := &IntParam{}
			if kindParams != "" {
				var min, max int
				for _, r := range strings.Split(kindParams, ",") {
					minAndMax := strings.Split(r, ":")
					min, err = strconv.Atoi(minAndMax[0])
					if err != nil {
						log.Panicf("Failed to parse min value for %v", field.Name)
					}
					if len(minAndMax) == 2 {
						max, err = strconv.Atoi(minAndMax[1])
						if err != nil {
							log.Panicf("Failed to parse max value for %v", field.Name)
						}
					} else {
						max = min
					}
					intParam.Ranges = append(intParam.Ranges, MinMax{Min: min, Max: max})
				}
			} else {
				intParam.Ranges = []MinMax{{Min: minInt, Max: maxInt}}
			}
			param = intParam
		case "int32":
			param = &Int32Param{}
		case "mark-bitmask":
			param = &MarkBitmaskParam{}
		case "float":
			param = &FloatParam{}
		case "seconds":
			min := minInt
			max := maxInt
			if kindParams != "" {
				minAndMax := strings.Split(kindParams, ":")
				min, err = strconv.Atoi(minAndMax[0])
				if err != nil {
					log.Panicf("Failed to parse min value for %v", field.Name)
				}
				max, err = strconv.Atoi(minAndMax[1])
				if err != nil {
					log.Panicf("Failed to parse max value for %v", field.Name)
				}
			}
			param = &SecondsParam{Min: min, Max: max}
		case "millis":
			param = &MillisParam{}
		case "iface-list":
			param = &RegexpParam{Regexp: IfaceListRegexp,
				Msg: "invalid Linux interface name"}
		case "iface-list-regexp":
			param = &RegexpPatternListParam{
				NonRegexpElemRegexp: NonRegexpIfaceElemRegexp,
				RegexpElemRegexp:    RegexpIfaceElemRegexp,
				Delimiter:           ",",
				Msg:                 "list contains invalid Linux interface name or regex pattern",
			}
		case "regexp":
			param = &RegexpPatternParam{}
		case "iface-param":
			param = &RegexpParam{Regexp: IfaceParamRegexp,
				Msg: "invalid Linux interface parameter"}
		case "file":
			param = &FileParam{
				MustExist:  strings.Contains(kindParams, "must-exist"),
				Executable: strings.Contains(kindParams, "executable"),
			}
		case "authority":
			param = &RegexpParam{Regexp: AuthorityRegexp,
				Msg: "invalid URL authority"}
		case "ipv4":
			param = &Ipv4Param{}
		case "endpoint-list":
			param = &EndpointListParam{}
		case "port-list":
			param = &PortListParam{}
		case "portrange":
			param = &PortRangeParam{}
		case "portrange-list":
			param = &PortRangeListParam{}
		case "hostname":
			param = &RegexpParam{Regexp: HostnameRegexp,
				Msg: "invalid hostname"}
		case "host-address":
			param = &RegexpParam{Regexp: HostAddressRegexp,
				Msg: "invalid host address"}
		case "region":
			param = &RegionParam{}
		case "oneof":
			options := strings.Split(kindParams, ",")
			lowerCaseToCanon := make(map[string]string)
			for _, option := range options {
				lowerCaseToCanon[strings.ToLower(option)] = option
			}
			param = &OneofListParam{
				lowerCaseOptionsToCanonical: lowerCaseToCanon}
		case "string":
			param = &RegexpParam{Regexp: StringRegexp,
				Msg: "invalid string"}
		case "cidr-list":
			param = &CIDRListParam{}
		case "server-list":
			param = &ServerListParam{}
		case "route-table-range":
			param = &RouteTableRangeParam{}
		case "keyvaluelist":
			param = &KeyValueListParam{}
		default:
			log.Panicf("Unknown type of parameter: %v", kind)
		}

		metadata := param.GetMetadata()
		metadata.Name = field.Name
		metadata.ZeroValue = reflect.ValueOf(config).FieldByName(field.Name).Interface()
		if strings.Contains(flags, "non-zero") {
			metadata.NonZero = true
		}
		if strings.Contains(flags, "die-on-fail") {
			metadata.DieOnParseFailure = true
		}
		if strings.Contains(flags, "local") {
			metadata.Local = true
		}

		if defaultStr != "" {
			if strings.Contains(flags, "skip-default-validation") {
				metadata.Default = defaultStr
			} else {
				// Parse the default value and save it in the metadata. Doing
				// that here ensures that we syntax-check the defaults now.
				defaultVal, err := param.Parse(defaultStr)
				if err != nil {
					log.Panicf("Invalid default value: %v", err)
				}
				metadata.Default = defaultVal
			}
		} else {
			metadata.Default = metadata.ZeroValue
		}
		knownParams[strings.ToLower(field.Name)] = param
	}
}

func (config *Config) SetUseNodeResourceUpdates(b bool) {
	config.useNodeResourceUpdates = b
}

func (config *Config) UseNodeResourceUpdates() bool {
	return config.useNodeResourceUpdates
}

func (config *Config) RawValues() map[string]string {
	return config.rawValues
}

func (config *Config) SetLoadClientConfigFromEnvironmentFunction(fnc func() (*apiconfig.CalicoAPIConfig, error)) {
	config.loadClientConfigFromEnvironment = fnc
}

// OverrideParam installs a maximum priority parameter override for the given parameter.  This is useful for
// disabling features that are found to be unsupported, for example. By using an extra priority class, the
// override will persist even if the host/global config is updated.
func (config *Config) OverrideParam(name, value string) (bool, error) {
	config.internalOverrides[name] = value
	return config.UpdateFrom(config.internalOverrides, InternalOverride)
}

func (config *Config) TyphaDiscoveryOpts() []discovery.Option {
	return []discovery.Option{
		discovery.WithAddrOverride(config.TyphaAddr),
		discovery.WithKubeService(config.TyphaK8sNamespace, config.TyphaK8sServiceName),
	}
}

func New() *Config {
	if knownParams == nil {
		loadParams()
	}
	p := &Config{
		rawValues:         map[string]string{},
		sourceToRawConfig: map[Source]map[string]string{},
		internalOverrides: map[string]string{},
	}
	for _, param := range knownParams {
		param.setDefault(p)
	}
	hostname, err := names.Hostname()
	if err != nil {
		log.Warningf("Failed to get hostname from kernel, "+
			"trying HOSTNAME variable: %v", err)
		hostname = strings.ToLower(os.Getenv("HOSTNAME"))
	}
	p.FelixHostname = hostname
	p.EnableNflogSize = isNflogSizeAvailable()
	p.loadClientConfigFromEnvironment = apiconfig.LoadClientConfigFromEnvironment

	return p
}

type param interface {
	GetMetadata() *Metadata
	Parse(raw string) (result interface{}, err error)
	setDefault(*Config)
}

func FromConfigUpdate(msg *proto.ConfigUpdate) *Config {
	p := New()
	// It doesn't have very great meaning for this standalone
	// config object, but we use DatastorePerHost here, as the
	// source, because proto.ConfigUpdate is formed by merging
	// global and per-host datastore configuration fields.
	p.UpdateFrom(msg.Config, DatastorePerHost)
	return p
}
