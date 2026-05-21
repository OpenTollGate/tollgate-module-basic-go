package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	sslDir    = "/etc/tollgate/ssl"
	backupDir = sslDir + "/backup"
	certDest  = sslDir + "/server.crt"
	keyDest   = sslDir + "/server.key"
)

var sslYesFlag bool

var sslCmd = &cobra.Command{
	Use:   "ssl",
	Short: "SSL/TLS certificate management",
	Long:  "Manage SSL certificates for the TollGate LuCI admin interface",
}

var sslApplyCmd = &cobra.Command{
	Use:   "apply [<cert-file> [key-file]]",
	Short: "Apply an SSL certificate",
	Long: `Apply an SSL certificate for HTTPS access.

Without arguments, generates a self-signed certificate for the router's hostname.
With a single PEM file, splits combined cert+key.
With two files, uses separate cert and key files.`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return sslApply(args)
	},
}

var sslRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove SSL configuration",
	Long:  "Revert SSL changes made by 'ssl apply', restoring previous state",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return sslRemove()
	},
}

var sslStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show SSL status",
	Long:  "Display current SSL certificate configuration and status",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return sslStatus()
	},
}

func init() {
	sslApplyCmd.Flags().BoolVarP(&sslYesFlag, "yes", "y", false, "Skip confirmation prompt")
	sslRemoveCmd.Flags().BoolVarP(&sslYesFlag, "yes", "y", false, "Skip confirmation prompt")
	sslCmd.AddCommand(sslApplyCmd, sslRemoveCmd, sslStatusCmd)
	rootCmd.AddCommand(sslCmd)
}

func confirmOrYes(msg string) bool {
	if sslYesFlag {
		return true
	}
	return askConfirmation(msg)
}

func sslApply(args []string) error {
	cleanupStaleTempDirs()

	lanIP, err := uciGet("network.lan.ipaddr")
	if err != nil || lanIP == "" {
		return fmt.Errorf("cannot determine LAN IP (network.lan.ipaddr)")
	}

	if _, err := os.Stat(backupDir); err == nil {
		fmt.Println("WARNING: SSL backup already exists (SSL may already be applied).")
		fmt.Println("  Run 'tollgate ssl remove' first to cleanly revert.")
		if !confirmOrYes("Overwrite backup and re-apply?") {
			fmt.Println("Aborted.")
			return nil
		}
	}

	if len(args) == 0 {
		return sslApplySelfSigned(lanIP)
	}
	return sslApplyRealCert(args, lanIP)
}

func sslApplySelfSigned(lanIP string) error {
	hostname, err := uciGet("system.@system[0].hostname")
	if err != nil || hostname == "" {
		hostname = "TollGate"
	}
	domain := hostname + ".lan"

	fmt.Printf("Generating self-signed certificate for %s...\n", domain)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate RSA key: %w", err)
	}

	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	tmpl := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(3650 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{domain, hostname},
		IPAddresses:  []net.IP{net.ParseIP(lanIP)},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	workDir, err := os.MkdirTemp("", "tollgate-ssl-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	certFile := filepath.Join(workDir, "cert.pem")
	keyFile := filepath.Join(workDir, "key.pem")

	if err := writePEM(certFile, "CERTIFICATE", certDER); err != nil {
		return err
	}
	if err := writePEM(keyFile, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key)); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Certificate details:")
	fmt.Printf("  Domain : %s (self-signed)\n", domain)
	fmt.Println("  Expires: 10 years")
	fmt.Printf("  LAN IP : %s\n", lanIP)
	fmt.Println()
	fmt.Println("  NOTE: Self-signed certs are NOT trusted by browsers or RFC 8908 clients.")
	fmt.Println("  The captive portal will continue using HTTP interception.")
	fmt.Println("  LuCI admin will be accessible via HTTPS with a browser warning.")
	fmt.Println()

	fmt.Println("Changes to apply:")
	fmt.Printf("  [1] Install self-signed cert+key to %s/\n", sslDir)
	fmt.Printf("  [2] uhttpd: set cert='%s' key='%s'\n", certDest, keyDest)
	fmt.Println("  [3] nodogsplash: allow tcp port 443 so clients can reach uhttpd HTTPS")
	fmt.Println()

	if !confirmOrYes("Apply all?") {
		fmt.Println("Aborted.")
		return nil
	}

	if err := sslBackup("self-signed", domain, lanIP); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	if err := sslInstallCerts(certFile, keyFile); err != nil {
		return err
	}
	fmt.Println("[1] Self-signed certificate installed.")

	if err := configureUhttpd(); err != nil {
		return err
	}
	fmt.Println("[2] uhttpd configured.")

	if err := allowPort443(); err != nil {
		return err
	}
	fmt.Println("[3] nodogsplash firewall updated.")

	if err := reloadServices(false); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("Done. Self-signed HTTPS enabled for %s\n", domain)
	fmt.Println()
	fmt.Printf("  Portal URL: http://%s/ (NoDogSplash, HTTP only)\n", domain)
	fmt.Printf("  LuCI URL:   https://%s/ (uhttpd, HTTPS, self-signed)\n", domain)
	fmt.Println()
	fmt.Println("To revert: tollgate ssl remove")
	return nil
}

func sslApplyRealCert(args []string, lanIP string) error {
	certFile := args[0]
	keyFile := ""
	if len(args) == 2 {
		keyFile = args[1]
	}

	if len(args) == 1 {
		cf, kf, err := splitCombinedPEM(certFile)
		if err != nil {
			return err
		}
		defer os.RemoveAll(filepath.Dir(cf))
		certFile = cf
		keyFile = kf
	}

	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		return fmt.Errorf("cert file not found: %s", certFile)
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		return fmt.Errorf("key file not found: %s", keyFile)
	}

	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return fmt.Errorf("cannot read cert file: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("not a valid PEM certificate: %s", certFile)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	if time.Now().After(cert.NotAfter) {
		fmt.Println("WARNING: certificate has expired!")
		fmt.Println("  Continuing anyway — the cert will be installed but browsers will reject it.")
	}

	domain := extractDomain(cert)
	if domain == "" {
		return fmt.Errorf("could not extract domain from certificate (no SAN or CN found)")
	}

	fmt.Println()
	fmt.Println("Certificate details:")
	fmt.Printf("  Domain : %s\n", domain)
	fmt.Printf("  Expires: %s\n", cert.NotAfter.Format("Jan 2 15:04:05 2006 MST"))
	fmt.Printf("  SAN    : %s\n", strings.Join(cert.DNSNames, ", "))
	fmt.Printf("  LAN IP : %s\n", lanIP)
	fmt.Println()

	fmt.Println("Changes to apply:")
	fmt.Printf("  [1] Install cert+key to %s/\n", sslDir)
	fmt.Printf("  [2] uhttpd: set cert='%s' key='%s'\n", certDest, keyDest)
	fmt.Printf("  [3] dnsmasq: resolve %s -> %s\n", domain, lanIP)
	fmt.Printf("  [4] nodogsplash: gatewaydomainname='%s' (portal stays on HTTP port 80)\n", domain)
	fmt.Println("  [5] nodogsplash: allow tcp port 443 so clients can reach uhttpd HTTPS")
	fmt.Println()

	if !confirmOrYes("Apply all?") {
		fmt.Println("Aborted.")
		return nil
	}

	if err := sslBackup("real-cert", domain, lanIP); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	if err := sslInstallCerts(certFile, keyFile); err != nil {
		return err
	}
	fmt.Println("[1] Certificate installed.")

	if err := configureUhttpd(); err != nil {
		return err
	}
	fmt.Println("[2] uhttpd configured.")

	if err := configureDnsmasq(domain, lanIP); err != nil {
		return err
	}
	fmt.Printf("[3] dnsmasq configured: %s -> %s\n", domain, lanIP)

	if err := configureNodogsplash(domain); err != nil {
		return err
	}
	fmt.Println("[4] nodogsplash configured.")

	if err := allowPort443(); err != nil {
		return err
	}
	fmt.Println("[5] nodogsplash firewall updated.")

	if err := reloadServices(true); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("Done. HTTPS enabled for %s\n", domain)
	fmt.Println()
	fmt.Printf("  Portal URL: http://%s/ (NoDogSplash, HTTP only)\n", domain)
	fmt.Printf("  LuCI URL:   https://%s/ (uhttpd, HTTPS)\n", domain)
	fmt.Println()
	fmt.Println("To revert: tollgate ssl remove")
	return nil
}

func sslRemove() error {
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		return fmt.Errorf("no SSL backup found at %s/\n  Either SSL was never applied, or the backup was deleted", backupDir)
	}

	domain := fileRead(backupDir + "/ssl.domain")
	mode := fileRead(backupDir + "/ssl.mode")

	if mode == "self-signed" {
		return sslRemoveSelfSigned(domain)
	}
	return sslRemoveRealCert(domain)
}

func sslRemoveSelfSigned(domain string) error {
	fmt.Printf("Reverting self-signed SSL configuration for: %s\n", domain)
	fmt.Println()
	fmt.Println("Changes to revert:")
	fmt.Printf("  [1] Remove self-signed cert+key from %s/\n", sslDir)
	fmt.Println("  [2] uhttpd: restore previous cert configuration")
	fmt.Println("  [3] nodogsplash: remove port 443 allow rule")
	fmt.Println()

	if !confirmOrYes("Revert all?") {
		fmt.Println("Aborted.")
		return nil
	}

	os.Remove(certDest)
	os.Remove(keyDest)

	if err := restoreUhttpd(); err != nil {
		return err
	}
	fmt.Println("[1] uhttpd cert reverted.")

	if err := removePort443Allow(); err != nil {
		return err
	}
	fmt.Println("[2] nodogsplash firewall updated.")

	if err := uciCommitChecked("uhttpd"); err != nil {
		return err
	}
	if err := uciCommitChecked("nodogsplash"); err != nil {
		return err
	}
	if err := reloadServices(false); err != nil {
		return err
	}

	os.RemoveAll(backupDir)

	fmt.Println()
	fmt.Println("Done. Self-signed HTTPS removed.")
	fmt.Println("  Portal URL: http://TollGate.lan/")
	return nil
}

func sslRemoveRealCert(domain string) error {
	fmt.Printf("Reverting SSL configuration for: %s\n", domain)
	fmt.Println()
	fmt.Println("Changes to revert:")
	fmt.Printf("  [1] Remove cert+key from %s/\n", sslDir)
	fmt.Println("  [2] uhttpd: restore previous cert configuration")
	fmt.Printf("  [3] dnsmasq: remove DNS entry for %s\n", domain)
	fmt.Println("  [4] nodogsplash: revert gatewaydomainname and remove port 443 allow")
	fmt.Println()

	if !confirmOrYes("Revert all?") {
		fmt.Println("Aborted.")
		return nil
	}

	os.Remove(certDest)
	os.Remove(keyDest)

	if err := restoreUhttpd(); err != nil {
		return err
	}
	fmt.Println("[1] uhttpd cert reverted.")

	if err := removeDnsmasqDomain(domain); err != nil {
		return err
	}
	fmt.Printf("[2] Removed dnsmasq entry for %s\n", domain)

	originalDomain := fileRead(backupDir + "/nds.gatewaydomainname")
	if originalDomain == "" {
		originalDomain = "TollGate.lan"
	}
	originalPort := fileRead(backupDir + "/nds.gatewayport")
	if originalPort == "" {
		originalPort = "80"
	}
	if err := uciSetScalar("nodogsplash.@nodogsplash[0].gatewaydomainname", originalDomain); err != nil {
		return err
	}
	if err := uciSetScalar("nodogsplash.@nodogsplash[0].gatewayport", originalPort); err != nil {
		return err
	}
	if err := removePort443Allow(); err != nil {
		return err
	}
	fmt.Printf("[3] nodogsplash reverted to %s:%s\n", originalDomain, originalPort)

	if err := uciCommitChecked("uhttpd"); err != nil {
		return err
	}
	if err := uciCommitChecked("dhcp"); err != nil {
		return err
	}
	if err := uciCommitChecked("nodogsplash"); err != nil {
		return err
	}
	if err := reloadServices(true); err != nil {
		return err
	}

	os.RemoveAll(backupDir)

	fmt.Println()
	fmt.Println("Done. HTTPS removed. Portal now served over HTTP.")
	fmt.Printf("  Portal URL: http://%s/\n", originalDomain)
	return nil
}

func sslStatus() error {
	mode := fileRead(backupDir + "/ssl.mode")
	domain := fileRead(backupDir + "/ssl.domain")

	if _, err := os.Stat(certDest); os.IsNotExist(err) {
		fmt.Println("SSL: not configured")
		fmt.Println("  Run 'tollgate ssl apply' to generate a self-signed certificate")
		fmt.Println("  Run 'tollgate ssl apply <cert> [key]' to install a real certificate")
		return nil
	}

	fmt.Println("SSL: configured")
	fmt.Printf("  Mode   : %s\n", mode)
	fmt.Printf("  Domain : %s\n", domain)
	fmt.Printf("  Cert   : %s\n", certDest)
	fmt.Printf("  Key    : %s\n", keyDest)

	certPEM, err := os.ReadFile(certDest)
	if err == nil {
		block, _ := pem.Decode(certPEM)
		if block != nil {
			cert, err := x509.ParseCertificate(block.Bytes)
			if err == nil {
				fmt.Printf("  Subject: %s\n", cert.Subject)
				fmt.Printf("  Issuer : %s\n", cert.Issuer)
				fmt.Printf("  NotBefore: %s\n", cert.NotBefore.Format("2006-01-02 15:04:05"))
				fmt.Printf("  NotAfter : %s\n", cert.NotAfter.Format("2006-01-02 15:04:05"))
				if time.Now().After(cert.NotAfter) {
					fmt.Println("  WARNING: certificate has EXPIRED")
				} else {
					daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
					fmt.Printf("  Days remaining: %d\n", daysLeft)
				}
				if len(cert.DNSNames) > 0 {
					fmt.Printf("  SAN    : %s\n", strings.Join(cert.DNSNames, ", "))
				}
			}
		}
	}

	return nil
}

func writePEM(path, pemType string, bytes []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: pemType, Bytes: bytes})
}

func splitCombinedPEM(inputPath string) (certFile, keyFile string, err error) {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return "", "", fmt.Errorf("cannot read file: %w", err)
	}

	var certBlocks, keyBlocks []byte
	remaining := data
	for {
		var block *pem.Block
		block, remaining = pem.Decode(remaining)
		if block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			buf := pem.EncodeToMemory(block)
			certBlocks = append(certBlocks, buf...)
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			buf := pem.EncodeToMemory(block)
			keyBlocks = append(keyBlocks, buf...)
		}
	}

	if len(certBlocks) == 0 && len(keyBlocks) == 0 {
		return "", "", fmt.Errorf("no PEM certificate or key blocks found in: %s", inputPath)
	}
	if len(certBlocks) == 0 {
		return "", "", fmt.Errorf("private key found but no certificate in: %s\n  Provide a cert file as the first argument", inputPath)
	}
	if len(keyBlocks) == 0 {
		return "", "", fmt.Errorf("certificate found but no private key in: %s\n  Provide a key file as the second argument", inputPath)
	}

	workDir, err := os.MkdirTemp("", "tollgate-ssl-split-*")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	cf := workDir + "/cert.pem"
	kf := workDir + "/key.pem"
	if err := os.WriteFile(cf, certBlocks, 0644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(kf, keyBlocks, 0600); err != nil {
		return "", "", err
	}

	return cf, kf, nil
}

func extractDomain(cert *x509.Certificate) string {
	for _, name := range cert.DNSNames {
		if name != "" {
			return name
		}
	}
	if cert.Subject.CommonName != "" {
		return cert.Subject.CommonName
	}
	return ""
}

func sslBackup(mode, domain, lanIP string) error {
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup dir: %w", err)
	}

	writeBackupFile(backupDir+"/uhttpd.cert", uciGetOrEmpty("uhttpd.main.cert"))
	writeBackupFile(backupDir+"/uhttpd.key", uciGetOrEmpty("uhttpd.main.key"))
	writeBackupFile(backupDir+"/nds.gatewaydomainname", uciGetOrEmpty("nodogsplash.@nodogsplash[0].gatewaydomainname"))
	writeBackupFile(backupDir+"/nds.gatewayport", uciGetOrEmpty("nodogsplash.@nodogsplash[0].gatewayport"))
	writeBackupFile(backupDir+"/ssl.domain", domain)
	writeBackupFile(backupDir+"/ssl.lan_ip", lanIP)
	writeBackupFile(backupDir+"/ssl.mode", mode)

	out, _ := runCommand("uci", "show", "dhcp")
	lines := filterLines(out, "=domain")
	writeBackupFile(backupDir+"/dnsmasq.domains", strings.Join(lines, "\n"))

	fmt.Printf("Backup saved to %s/\n", backupDir)
	return nil
}

func sslInstallCerts(certFile, keyFile string) error {
	if err := os.MkdirAll(sslDir, 0755); err != nil {
		return fmt.Errorf("failed to create SSL dir: %w", err)
	}

	if err := copyFile(certFile, certDest); err != nil {
		return fmt.Errorf("failed to install cert: %w", err)
	}
	if err := copyFile(keyFile, keyDest); err != nil {
		return fmt.Errorf("failed to install key: %w", err)
	}
	os.Chmod(keyDest, 0600)
	os.Chmod(certDest, 0644)
	return nil
}

func configureUhttpd() error {
	if err := uciSetScalar("uhttpd.main.cert", certDest); err != nil {
		return err
	}
	if err := uciSetScalar("uhttpd.main.key", keyDest); err != nil {
		return err
	}

	listenHTTPS := uciGetList("uhttpd.main.listen_https")
	if !listContains(listenHTTPS, "0.0.0.0:443") {
		if err := runCommandChecked("uci", "add_list", "uhttpd.main.listen_https=0.0.0.0:443"); err != nil {
			return err
		}
	}
	if !listContains(listenHTTPS, "[::]:443") {
		if err := runCommandChecked("uci", "add_list", "uhttpd.main.listen_https=[::]:443"); err != nil {
			return err
		}
	}
	return uciCommitChecked("uhttpd")
}

func configureDnsmasq(domain, lanIP string) error {
	if err := removeDnsmasqDomainIfExists(domain); err != nil {
		return err
	}

	if err := runCommandChecked("uci", "add", "dhcp", "domain"); err != nil {
		return err
	}
	if err := uciSetScalar("dhcp.@domain[-1].name", domain); err != nil {
		return err
	}
	if err := uciSetScalar("dhcp.@domain[-1].ip", lanIP); err != nil {
		return err
	}
	return uciCommitChecked("dhcp")
}

func configureNodogsplash(domain string) error {
	if err := uciSetScalar("nodogsplash.@nodogsplash[0].gatewaydomainname", domain); err != nil {
		return err
	}
	return uciCommitChecked("nodogsplash")
}

func allowPort443() error {
	ndsUsers := uciGetList("nodogsplash.@nodogsplash[0].users_to_router")
	if !listContains(ndsUsers, "allow tcp port 443") {
		if err := runCommandChecked("uci", "add_list", "nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 443"); err != nil {
			return err
		}
	}
	return uciCommitChecked("nodogsplash")
}

func removePort443Allow() error {
	return runCommandChecked("uci", "-q", "del_list", "nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 443")
}

func removeDnsmasqDomain(domain string) error {
	if err := removeDnsmasqDomainIfExists(domain); err != nil {
		return err
	}
	return uciCommitChecked("dhcp")
}

func removeDnsmasqDomainIfExists(domain string) error {
	out, _ := runCommand("uci", "show", "dhcp")
	lines := filterLines(out, "=domain")
	for _, line := range lines {
		dotParts := strings.SplitN(line, ".", 2)
		if len(dotParts) < 2 {
			continue
		}
		eqParts := strings.SplitN(dotParts[1], "=", 2)
		if len(eqParts) < 1 {
			continue
		}
		idx := eqParts[0]
		name := uciGetOrEmpty("dhcp." + idx + ".name")
		if name == domain {
			return runCommandChecked("uci", "-q", "delete", "dhcp."+idx)
		}
	}
	return nil
}

func restoreUhttpd() error {
	prevCert := fileRead(backupDir + "/uhttpd.cert")
	prevKey := fileRead(backupDir + "/uhttpd.key")
	restored := false

	if prevCert != "" {
		if _, err := os.Stat(prevCert); err == nil {
			if err := uciSetScalar("uhttpd.main.cert", prevCert); err != nil {
				return err
			}
			restored = true
		}
	}
	if prevKey != "" {
		if _, err := os.Stat(prevKey); err == nil {
			if err := uciSetScalar("uhttpd.main.key", prevKey); err != nil {
				return err
			}
			restored = true
		}
	}

	if !restored {
		if _, err := os.Stat("/etc/uhttpd.crt"); err == nil {
			if _, err := os.Stat("/etc/uhttpd.key"); err == nil {
				if err := uciSetScalar("uhttpd.main.cert", "/etc/uhttpd.crt"); err != nil {
					return err
				}
				if err := uciSetScalar("uhttpd.main.key", "/etc/uhttpd.key"); err != nil {
					return err
				}
				return nil
			}
		}
		if err := runCommandChecked("uci", "-q", "delete", "uhttpd.main.cert"); err != nil {
			return err
		}
		if err := runCommandChecked("uci", "-q", "delete", "uhttpd.main.key"); err != nil {
			return err
		}
		if err := runCommandChecked("uci", "-q", "delete", "uhttpd.main.listen_https"); err != nil {
			return err
		}
	}
	return nil
}

func reloadServices(realCert bool) error {
	if err := runCommandChecked("/etc/init.d/uhttpd", "reload"); err != nil {
		return fmt.Errorf("failed to reload uhttpd: %w", err)
	}
	if realCert {
		if err := runCommandChecked("/etc/init.d/dnsmasq", "reload"); err != nil {
			return fmt.Errorf("failed to reload dnsmasq: %w", err)
		}
	}
	if err := runCommandChecked("/etc/init.d/nodogsplash", "restart"); err != nil {
		return fmt.Errorf("failed to restart nodogsplash: %w", err)
	}
	return nil
}

func cleanupStaleTempDirs() {
	matches, _ := filepath.Glob(os.TempDir() + "/tollgate-ssl-*")
	for _, m := range matches {
		os.RemoveAll(m)
	}
	matches, _ = filepath.Glob(os.TempDir() + "/tollgate-ssl-split-*")
	for _, m := range matches {
		os.RemoveAll(m)
	}
}

func fileRead(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeBackupFile(path, content string) {
	if content != "" {
		os.WriteFile(path, []byte(content+"\n"), 0644)
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func filterLines(input, contains string) []string {
	var result []string
	for _, line := range strings.Split(input, "\n") {
		if strings.Contains(line, contains) {
			result = append(result, line)
		}
	}
	return result
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runCommandChecked(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}

func uciGet(key string) (string, error) {
	out, err := runCommand("uci", "-q", "get", key)
	return strings.TrimSpace(out), err
}

func uciGetOrEmpty(key string) string {
	out, _ := uciGet(key)
	return out
}

func uciGetList(key string) []string {
	out := uciGetOrEmpty(key)
	if out == "" {
		return nil
	}
	var result []string
	for _, field := range strings.Fields(out) {
		cleaned := strings.Trim(field, "'")
		if cleaned != "" {
			result = append(result, cleaned)
		}
	}
	return result
}

func listContains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func uciSetScalar(key, value string) error {
	return runCommandChecked("uci", "set", key+"="+value)
}

func uciCommitChecked(config string) error {
	return runCommandChecked("uci", "commit", config)
}
