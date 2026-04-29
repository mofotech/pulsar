package network

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
)

// GenerateMAC returns a locally-administered unicast MAC address with fa:16:3e prefix.
func GenerateMAC() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("fa:16:3e:%02x:%02x:%02x", b[0], b[1], b[2]), nil
}

// allocateIP returns the next unused IP in the subnet that isn't the gateway or reserved.
// allocated is the set of already-taken IPs (from etcd port records).
// If pool is non-nil, only IPs in [pool.Start, pool.End] are considered.
func allocateIP(cidr, gatewayIP string, allocated map[string]struct{}, pool *AllocationPool) (string, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("parse cidr %s: %w", cidr, err)
	}

	// Skip network address, broadcast, and gateway
	reserved := map[string]struct{}{
		network.IP.String():             {},
		broadcastAddr(network).String(): {},
	}
	if gatewayIP != "" {
		reserved[gatewayIP] = struct{}{}
	}

	// Determine iteration start/end from pool if provided
	var poolStart, poolEnd net.IP
	if pool != nil && pool.Start != "" && pool.End != "" {
		poolStart = net.ParseIP(pool.Start).To4()
		poolEnd = net.ParseIP(pool.End).To4()
		if poolStart == nil || poolEnd == nil {
			return "", fmt.Errorf("invalid allocation pool: %s - %s", pool.Start, pool.End)
		}
	}

	// Iterate from first host address (or pool start)
	ip := cloneIP(network.IP)
	inc(ip)
	if poolStart != nil && ipGT(poolStart, ip) {
		ip = cloneIP(poolStart)
	}

	for network.Contains(ip) {
		// Stop if we've passed the pool end
		if poolEnd != nil && ipGT(ip, poolEnd) {
			break
		}
		s := ip.String()
		if _, isReserved := reserved[s]; !isReserved {
			if _, isAllocated := allocated[s]; !isAllocated {
				return s, nil
			}
		}
		inc(ip)
	}
	if pool != nil {
		return "", fmt.Errorf("allocation pool %s-%s in subnet %s is exhausted", pool.Start, pool.End, cidr)
	}
	return "", fmt.Errorf("subnet %s is exhausted", cidr)
}

// ipGT returns true if a > b (both must be 4-byte IPv4).
func ipGT(a, b net.IP) bool {
	a4, b4 := a.To4(), b.To4()
	if a4 == nil || b4 == nil {
		return false
	}
	for i := 0; i < 4; i++ {
		if a4[i] > b4[i] {
			return true
		}
		if a4[i] < b4[i] {
			return false
		}
	}
	return false
}

func broadcastAddr(n *net.IPNet) net.IP {
	ip := cloneIP(n.IP.To4())
	if ip == nil {
		ip = cloneIP(n.IP.To16())
	}
	for i := range ip {
		ip[i] |= ^n.Mask[i]
	}
	return ip
}

func cloneIP(ip net.IP) net.IP {
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] != 0 {
			break
		}
	}
}

// subnetSize returns the number of usable host addresses in the CIDR.
func subnetSize(cidr string) (int64, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, err
	}
	ones, bits := network.Mask.Size()
	size := new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(bits-ones)), nil)
	// subtract network and broadcast
	size.Sub(size, big.NewInt(2))
	if size.IsInt64() {
		return size.Int64(), nil
	}
	return 0, fmt.Errorf("subnet too large")
}

// defaultGateway returns the first host address in a CIDR (e.g., 10.0.0.1 for 10.0.0.0/24).
func defaultGateway(cidr string) (string, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}
	ip := cloneIP(network.IP)
	inc(ip)
	return ip.String(), nil
}

// Silence unused functions
var _ = subnetSize
