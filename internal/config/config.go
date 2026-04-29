package config

import (
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
