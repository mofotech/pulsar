// Package cloudinit generates NoCloud seed ISOs for cloud-init bootstrapping.
package cloudinit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Port describes a network interface to include in the cloud-init network-config.
type Port struct {
	MACAddr string
}

// Config holds everything needed to generate a cloud-init seed ISO.
type Config struct {
	InstanceID    string
	Hostname      string
	UserData      string
	SSHPublicKeys []string // OpenSSH authorized_keys lines
	Ports         []Port
}

// WriteISO writes a NoCloud seed ISO to destPath.
// It creates the meta-data, user-data, and network-config files in a temp
// directory, then calls genisoimage to build the ISO.
func WriteISO(cfg Config, destPath string) error {
	tmp, err := os.MkdirTemp("", "cloudinit-"+cfg.InstanceID+"-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "meta-data"), []byte(buildMetaData(cfg)), 0o644); err != nil {
		return fmt.Errorf("write meta-data: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "user-data"), []byte(buildUserData(cfg)), 0o644); err != nil {
		return fmt.Errorf("write user-data: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "network-config"), []byte(buildNetworkConfig(cfg)), 0o644); err != nil {
		return fmt.Errorf("write network-config: %w", err)
	}

	out, err := exec.Command(
		"genisoimage",
		"-output", destPath,
		"-volid", "cidata",
		"-joliet",
		"-rock",
		"-quiet",
		tmp,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("genisoimage: %w\n%s", err, out)
	}
	return nil
}

func buildMetaData(cfg Config) string {
	hostname := cfg.Hostname
	if hostname == "" {
		hostname = cfg.InstanceID
	}
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", cfg.InstanceID, hostname)
}

func buildUserData(cfg Config) string {
	ud := strings.TrimSpace(cfg.UserData)

	// If the caller provided a raw script (not cloud-config), we can't safely
	// inject SSH keys into it — pass it through unchanged.
	if ud != "" && !strings.HasPrefix(ud, "#cloud-config") {
		return ud + "\n"
	}

	// Start from a cloud-config document (provided or empty).
	if ud == "" {
		ud = "#cloud-config"
	}

	// Inject SSH authorized keys when any were supplied.
	// We append an ssh_authorized_keys block only if it isn't already present in
	// the caller-supplied user-data; if it is, we leave it untouched to avoid
	// duplicates.
	if len(cfg.SSHPublicKeys) > 0 && !strings.Contains(ud, "ssh_authorized_keys") {
		ud += "\nssh_authorized_keys:"
		for _, key := range cfg.SSHPublicKeys {
			key = strings.TrimSpace(key)
			if key != "" {
				ud += "\n  - " + key
			}
		}
	}

	return ud + "\n"
}

// buildNetworkConfig generates a cloud-init network-config v2 (Netplan format)
// with DHCP4 enabled on each port, matched by MAC address.
func buildNetworkConfig(cfg Config) string {
	if len(cfg.Ports) == 0 {
		return "version: 2\n"
	}

	var sb strings.Builder
	sb.WriteString("version: 2\nethernets:\n")
	for i, p := range cfg.Ports {
		sb.WriteString(fmt.Sprintf("  eth%d:\n", i))
		sb.WriteString(fmt.Sprintf("    match:\n      macaddress: \"%s\"\n", p.MACAddr))
		sb.WriteString("    dhcp4: true\n")
		sb.WriteString("    set-name: eth" + fmt.Sprintf("%d", i) + "\n")
	}
	return sb.String()
}
