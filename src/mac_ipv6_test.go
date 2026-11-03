package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGetMacAddressIPv6FromNeighborTable proves a payer on an IPv6 address
// (the FIPS-mesh deployment carries IPv6 ULA gateway and client addresses)
// can resolve a MAC. /proc/net/arp is IPv4-only; before the neighbor-table
// source existed, every IPv6 payer was refused with device-unresolved —
// "Payment refused: the client has no resolvable identity".
func TestGetMacAddressIPv6FromNeighborTable(t *testing.T) {
	dir := t.TempDir()
	// No DHCP leases, empty v4 ARP table, real v6 neighbor fixture.
	dhcpLeasePath = filepath.Join(dir, "dhcp.leases")
	os.WriteFile(dhcpLeasePath, []byte(""), 0644)
	arpTablePath = filepath.Join(dir, "arp")
	os.WriteFile(arpTablePath, []byte("IP address  HW type  Flags  HW address  Mask  Device\n"), 0644)
	// `ip -6 neigh show` output format:
	// fd36:3c0f:d6f:8029:ea50:958b:672e:bb0 dev fips0 lladdr 32:6b:e7:68:7d:c3 REACHABLE
	neighborSource = func() ([]byte, error) {
		return []byte(
			"fd36:3c0f:d6f:8029:ea50:958b:672e:bb0 dev fips0 lladdr 32:6b:e7:68:7d:c3 REACHABLE\n" +
				"fe80::1 dev enp1s0 lladdr 00:11:22:33:44:55 STALE\n"), nil
	}
	defer func() { neighborSource = nil }()

	mac, err := getMacAddress("fd36:3c0f:d6f:8029:ea50:958b:672e:bb0")
	if err != nil {
		t.Fatalf("IPv6 payer must resolve via neighbor table: %v", err)
	}
	if mac != "32:6b:e7:68:7d:c3" {
		t.Fatalf("wrong MAC: %s", mac)
	}
}

func TestGetMacAddressIPv6UnresolvedStillFails(t *testing.T) {
	dir := t.TempDir()
	dhcpLeasePath = filepath.Join(dir, "dhcp.leases")
	os.WriteFile(dhcpLeasePath, []byte(""), 0644)
	arpTablePath = filepath.Join(dir, "arp")
	os.WriteFile(arpTablePath, []byte(""), 0644)
	neighborSource = func() ([]byte, error) { return nil, os.ErrNotExist }
	defer func() { neighborSource = nil }()

	_, err := getMacAddress("fd36:3c0f:d6f:8029:ea50:958b:672e:bb0")
	if err == nil {
		t.Fatal("unknown IPv6 payer must still be refused")
	}
}
