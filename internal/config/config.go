package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Controller ControllerConfig `mapstructure:"controller"`
	Agent      AgentConfig      `mapstructure:"agent"`
	Logging    LoggingConfig    `mapstructure:"logging"`
	Telemetry  TelemetryConfig  `mapstructure:"telemetry"`
}

type ControllerConfig struct {
	ListenHTTP     string     `mapstructure:"listen_http"`
	ListenGRPC     string     `mapstructure:"listen_grpc"`
	Etcd           EtcdConfig `mapstructure:"etcd"`
	Postgres       PGConfig   `mapstructure:"postgres"`
	JWT            JWTConfig  `mapstructure:"jwt"`
	ImageStoreDir  string     `mapstructure:"image_store_dir"`
	PublicURL      string     `mapstructure:"public_url"`
	ConsoleVNCHost string     `mapstructure:"console_vnc_host"`
	// OIDCBaseURL is the externally-reachable base URL of the controller,
	// used to construct the OAuth2 redirect_uri sent to IDPs.
	// Defaults to PublicURL if empty.
	OIDCBaseURL string `mapstructure:"oidc_base_url"`
	// OIDCAllowedNetworks lists CIDRs (or IPs) of internal networks the
	// controller may contact for IdP discovery / token exchange, e.g. an
	// on-prem Keycloak. Everything else must be a public IP over https.
	// Env: PULSAR_CONTROLLER_OIDC_ALLOWED_NETWORKS="10.0.0.0/8,192.168.1.5"
	OIDCAllowedNetworks []string `mapstructure:"oidc_allowed_networks"`
}

type EtcdConfig struct {
	Endpoints []string  `mapstructure:"endpoints"`
	TLS       TLSConfig `mapstructure:"tls"`
}

type PGConfig struct {
	DSN string `mapstructure:"dsn"`
}

type JWTConfig struct {
	Secret string        `mapstructure:"secret"`
	Expiry time.Duration `mapstructure:"expiry"`
}

type TLSConfig struct {
	CACert string `mapstructure:"ca_cert"`
	Cert   string `mapstructure:"cert"`
	Key    string `mapstructure:"key"`
}

type AgentConfig struct {
	Pillar         string       `mapstructure:"pillar"`
	ControllerGRPC string       `mapstructure:"controller_grpc"`
	NodeID         string       `mapstructure:"node_id"`
	TLS            TLSConfig    `mapstructure:"tls"`
	Compute        ComputeAgent `mapstructure:"compute"`
	Network        NetworkAgent `mapstructure:"network"`
	Storage        StorageAgent `mapstructure:"storage"`
}

type ComputeAgent struct {
	LibvirtSocket     string   `mapstructure:"libvirt_socket"`
	HypervisorDrivers []string `mapstructure:"hypervisor_drivers"`
	ImageCacheDir     string   `mapstructure:"image_cache_dir"`
	InstanceDir       string   `mapstructure:"instance_dir"`
	// DomainType is the libvirt domain type: "kvm" (default) or "qemu" (software emulation).
	DomainType string `mapstructure:"domain_type"`
	// Emulator is the full path to the QEMU binary.
	// Defaults to /usr/bin/kvm; set to /usr/bin/qemu-system-x86_64 on hosts where
	// /usr/bin/kvm is unavailable or KVM is not supported through that wrapper.
	Emulator string `mapstructure:"emulator"`
}

type NetworkAgent struct {
	ExternalInterface string `mapstructure:"external_interface"`
	OverlayInterface  string `mapstructure:"overlay_interface"`
	VXLANPort         int    `mapstructure:"vxlan_port"`
	OVNNBAddr         string `mapstructure:"ovn_nb_addr"`
	OVSBridge         string `mapstructure:"ovs_bridge"`
	PhysnetName       string `mapstructure:"physnet_name"`
	// OVNChassisName is the OVN chassis name (host hostname) to use when binding
	// ports. This must match the chassis registered with ovn-controller on the host.
	// If empty, the chassis_id passed in the bind task is used as-is.
	OVNChassisName string `mapstructure:"ovn_chassis_name"`
}

type StorageAgent struct {
	// NodeIP is the IP address advertised to compute agents for iSCSI connections.
	// Defaults to the host's primary interface IP if unset.
	NodeIP   string          `mapstructure:"node_ip"`
	Backends StorageBackends `mapstructure:"backends"`
}

type StorageBackends struct {
	LVM LVMConfig `mapstructure:"lvm"`
}

type LVMConfig struct {
	VolumeGroup string `mapstructure:"volume_group"`
	ThinPool    string `mapstructure:"thin_pool"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

type TelemetryConfig struct {
	PrometheusListen string `mapstructure:"prometheus_listen"`
	OTLPEndpoint     string `mapstructure:"otlp_endpoint"`
}

func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("controller.listen_http", ":8080")
	v.SetDefault("controller.listen_grpc", ":9090")
	v.SetDefault("controller.etcd.endpoints", []string{"localhost:2379"})
	v.SetDefault("controller.jwt.expiry", "24h")
	v.SetDefault("controller.image_store_dir", "/var/lib/pulsar/images-store")
	v.SetDefault("controller.public_url", "http://localhost:8080")
	v.SetDefault("controller.console_vnc_host", "host.docker.internal")
	v.SetDefault("agent.controller_grpc", "localhost:9090")
	v.SetDefault("agent.compute.image_cache_dir", "/var/lib/pulsar/images")
	v.SetDefault("agent.compute.instance_dir", "/var/lib/pulsar/instances")
	v.SetDefault("agent.compute.libvirt_socket", "/var/run/libvirt/libvirt-sock")
	v.SetDefault("agent.compute.domain_type", "kvm")
	v.SetDefault("agent.compute.emulator", "/usr/bin/kvm")
	v.SetDefault("agent.network.vxlan_port", 4789)
	v.SetDefault("agent.network.ovn_nb_addr", "unix:/var/run/ovn/ovnnb_db.sock")
	v.SetDefault("agent.network.ovs_bridge", "br-int")
	v.SetDefault("agent.network.physnet_name", "physnet1")
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("telemetry.prometheus_listen", ":9091")

	// Config file
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("pulsar")
		v.SetConfigType("yaml")
		v.AddConfigPath("/etc/pulsar")
		v.AddConfigPath("$HOME/.pulsar")
		v.AddConfigPath(".")
	}

	// Env vars: PULSAR_CONTROLLER_LISTEN_HTTP etc.
	v.SetEnvPrefix("PULSAR")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// AutomaticEnv only applies to keys viper already knows about (from the
	// config file or a default). Secrets are deliberately left out of config
	// files, so bind their env vars explicitly or Unmarshal never sees them.
	for _, key := range []string{
		"controller.jwt.secret",
		"controller.postgres.dsn",
		"controller.oidc_base_url",
		"controller.oidc_allowed_networks",
	} {
		if err := v.BindEnv(key); err != nil {
			return nil, err
		}
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// MinJWTSecretLen is the minimum accepted JWT signing secret length (bytes).
// 32 bytes matches the HS256 output size; `openssl rand -hex 32` yields 64.
const MinJWTSecretLen = 32

// knownDefaultSecrets are placeholder values that have shipped in this repo's
// sample configs. Anyone can read them, so tokens signed with them are forgeable.
var knownDefaultSecrets = map[string]bool{
	"pulsar-dev-change-in-production": true,
	"pulsar-change-in-production":     true,
	"change-me":                       true,
	"changeme":                        true,
	"my-secret":                       true,
	"secret":                          true,
}

// Validate checks controller settings that must be safe before serving traffic.
func (c *ControllerConfig) Validate() error {
	s := c.JWT.Secret
	switch {
	case s == "":
		return errors.New("controller.jwt.secret is not set (set PULSAR_CONTROLLER_JWT_SECRET, e.g. `openssl rand -hex 32`)")
	case knownDefaultSecrets[strings.ToLower(s)]:
		return errors.New("controller.jwt.secret is a published placeholder value; generate a real one with `openssl rand -hex 32`")
	case len(s) < MinJWTSecretLen:
		return fmt.Errorf("controller.jwt.secret must be at least %d bytes (got %d); generate one with `openssl rand -hex 32`", MinJWTSecretLen, len(s))
	}
	for _, n := range c.OIDCAllowedNetworks {
		if n == "" {
			continue
		}
		if _, err := netip.ParsePrefix(n); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(n); err != nil {
			return fmt.Errorf("controller.oidc_allowed_networks: %q is not a CIDR or IP", n)
		}
	}
	return nil
}
