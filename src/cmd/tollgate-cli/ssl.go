package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
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

var (
	sslDir    = "/etc/tollgate/ssl"
	backupDir = sslDir + "/backup"
	certDest  = sslDir + "/server.crt"
	keyDest   = sslDir + "/server.key"
	// uhttpdCertDefault is the certificate a stock OpenWrt image ships and that
	// uhttpd.main points at until something replaces it. It is a PLACEHOLDER
	// (subject CN=OpenWrt, SAN DNS:OpenWrt) and covers no router's own hostname
	// or LAN address — see certCoverage.
	uhttpdCertDefault = "/etc/uhttpd.crt"
	// sslOptOutFile records an operator who REMOVED the TLS identity on purpose
	// (`tollgate ssl remove`). The unattended setup path honours it: a reinstall
	// or an upgrade must not silently re-key a router behind an operator who
	// asked for no HTTPS identity, the same way setup_hostname never touches a
	// custom hostname (#444). `tollgate ssl apply` clears it — asking for an
	// identity IS the way back in.
	sslOptOutFile = sslDir + "/tls-identity-removed"
)

var sslYesFlag bool

// The service init scripts `ssl apply`/`ssl remove` deliver a changed identity
// to. Absolute paths in package variables (the same seam as sslDir above) so the
// tests can point them at a stub instead of a live router — a test that cannot
// run the removal path is a test that does not cover it.
var (
	uhttpdInitPath      = "/etc/init.d/uhttpd"
	dnsmasqInitPath     = "/etc/init.d/dnsmasq"
	nodogsplashInitPath = "/etc/init.d/nodogsplash"
)

// sslNoRestartFlag makes `ssl apply` leave the services alone. The unattended
// setup path (packaging/files/etc/uci-defaults/99-tollgate-setup) needs this:
// it runs before procd has brought uhttpd and nodogsplash up, and it converges
// the services itself (reloading uhttpd only when it is already running).
var sslNoRestartFlag bool

// errCertDoesNotCoverRouter is the exit status of `ssl covers`: the certificate
// is real but cannot validate any name this router is reached by.
var errCertDoesNotCoverRouter = errors.New("certificate does not cover this router")

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

// sslCoversCmd answers the question a browser asks, and exits non-zero when the
// answer is no, so a shell can branch on it. The uci-defaults setup path derives
// uhttpd.main.redirect_https from exactly this verdict (see
// docs/architecture/uhttpd-redirect-https-ownership-decision.md).
var sslCoversCmd = &cobra.Command{
	Use:   "covers [<cert-file>]",
	Short: "Check whether a certificate covers this router",
	Long: `Check whether a certificate validates a name this router is actually reached by.

Without an argument the certificate uhttpd.main is configured to present is
checked. The certificate covers the router when its SANs validate the configured
hostname, the <hostname>.lan alias dnsmasq serves, or the LAN IP; the CommonName
alone is not enough, because a browser ignores it when the certificate carries no
SAN extension.

Exit status is 0 when the certificate covers the router and 1 when it does not,
with the reason printed either way. A stock OpenWrt image's placeholder
certificate (subject CN=OpenWrt, SAN DNS:OpenWrt) therefore reports "covers: no",
which is what keeps uhttpd.main.redirect_https off on a router that has no real
identity yet.`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		certPath := uhttpdCertPath()
		if len(args) == 1 {
			certPath = args[0]
		}
		covers, reason := certCoversRouter(certPath)
		if jsonOutput {
			// The verdict is machine-readable BY CONTRACT — the exit status is
			// what the setup path branches on — so under --json it is one object
			// on stdout AND the same exit status: a "no" that printed a
			// successful-looking object and exited 0 is exactly the
			// nothing-happened-but-you-cannot-tell hazard of #375.
			payload := sslCoversResult{
				Success:   covers,
				Command:   "ssl covers",
				Cert:      certPath,
				Covers:    covers,
				Reason:    reason,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			}
			if !covers {
				payload.Error = errCertDoesNotCoverRouter.Error()
			}
			if err := printJSON(payload); err != nil {
				return err
			}
			if !covers {
				return errCertDoesNotCoverRouter
			}
			return nil
		}
		if covers {
			fmt.Printf("covers: yes — %s\n", reason)
			return nil
		}
		fmt.Printf("covers: no — %s\n", reason)
		return errCertDoesNotCoverRouter
	},
}

func init() {
	sslApplyCmd.Flags().BoolVarP(&sslYesFlag, "yes", "y", false, "Skip confirmation prompt")
	sslApplyCmd.Flags().BoolVar(&sslNoRestartFlag, "no-restart", false,
		"Do not reload uhttpd/nodogsplash after applying (the caller converges them)")
	sslRemoveCmd.Flags().BoolVarP(&sslYesFlag, "yes", "y", false, "Skip confirmation prompt")
	sslCmd.AddCommand(sslApplyCmd, sslRemoveCmd, sslStatusCmd, sslCoversCmd)
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

	lanIP, err := lanIPFromUCI()
	if err != nil {
		return err
	}

	if _, err := os.Stat(backupDir); err == nil {
		fmt.Println("WARNING: SSL backup already exists (SSL may already be applied).")
		fmt.Println("  Run 'tollgate ssl remove' first to cleanly revert.")
		if !confirmOrYes("Overwrite backup and re-apply?") {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Asking for an identity ends an earlier `ssl remove` — but only once an
	// identity is actually in place, so this is done after the apply below and
	// not before it. The apply functions return nil when the operator declines
	// the confirmation prompt, and an opt-out ended by a declined prompt would
	// let the next install re-key a router whose owner never asked for an
	// identity.
	var applyErr error
	if len(args) == 0 {
		applyErr = sslApplySelfSigned(lanIP)
	} else {
		applyErr = sslApplyRealCert(args, lanIP)
	}
	if applyErr != nil {
		return applyErr
	}
	if !identityInstalled() {
		return nil
	}
	return clearSSLOptOut()
}

// identityInstalled reports whether a cert/key pair is on disk to serve. It is
// the precondition for ending the operator's opt-out: `ssl apply` cleared the
// marker unconditionally before, which would have ended the opt-out of a run
// that installed nothing (a declined prompt, an aborted real-cert install).
func identityInstalled() bool {
	cert, err := os.Stat(certDest)
	if err != nil || cert.IsDir() || cert.Size() == 0 {
		return false
	}
	key, err := os.Stat(keyDest)
	return err == nil && !key.IsDir() && key.Size() > 0
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

	tmpl := selfSignedTemplate(hostname, lanIP)

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

	if err := reloadAfterApply(false); err != nil {
		return err
	}
	// The :8080 -> https:// hop is only safe while the certificate uhttpd
	// presents validates the address the browser used; derive it here too so an
	// operator running this by hand gets the same rule the setup path computes.
	if err := applyRedirectHTTPS(); err != nil {
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
	fmt.Println("  [4] nodogsplash: allow tcp port 443 so clients can reach uhttpd HTTPS")
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

	if err := allowPort443(); err != nil {
		return err
	}
	fmt.Println("[4] nodogsplash firewall updated.")

	if err := reloadAfterApply(true); err != nil {
		return err
	}
	if err := applyRedirectHTTPS(); err != nil {
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

// reloadAfterApply delivers a changed TLS identity to the running services,
// unless the caller asked for that to be skipped (--no-restart, used by the
// unattended setup path which converges the services itself).
func reloadAfterApply(realCert bool) error {
	if sslNoRestartFlag {
		fmt.Println("Skipping service reload (--no-restart): the caller converges uhttpd and nodogsplash.")
		return nil
	}
	return reloadServices(realCert)
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
	// Removal restores whatever certificate uhttpd.main held before — on a stock
	// image the placeholder — so the derived redirect must be recomputed, not
	// left pointing a browser at an identity that no longer covers the router.
	if err := applyRedirectHTTPS(); err != nil {
		return err
	}
	if err := reloadServices(false); err != nil {
		return err
	}

	os.RemoveAll(backupDir)

	// The removal is complete, so record the operator's decision: the install
	// path honours this marker and will not re-provision the identity that was
	// just removed. A failure to write it is reported, never hidden — the
	// router is reverted either way.
	if err := markSSLOptOut(fmt.Sprintf("self-signed identity for %s", domain)); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: %v\n  The identity is removed, but a later install may provision a new one.\n", err)
	}

	portalName, err := uciGet("system.@system[0].hostname")
	if err != nil || portalName == "" {
		portalName = "tollgate"
	}
	fmt.Println()
	fmt.Println("Done. Self-signed HTTPS removed.")
	fmt.Printf("  Portal URL: http://%s.lan/\n", portalName)
	return nil
}

func sslRemoveRealCert(domain string) error {
	fmt.Printf("Reverting SSL configuration for: %s\n", domain)
	fmt.Println()
	fmt.Println("Changes to revert:")
	fmt.Printf("  [1] Remove cert+key from %s/\n", sslDir)
	fmt.Println("  [2] uhttpd: restore previous cert configuration")
	fmt.Printf("  [3] dnsmasq: remove DNS entry for %s\n", domain)
	fmt.Println("  [4] nodogsplash: remove port 443 allow")
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

	// gatewaydomainname is never set by this flow anymore; clear any value
	// left by an older install — nodogsplash 5.0.2 redirect-loops the
	// pre-auth splash when it is set (#428).
	if err := uciDeleteIfExists("nodogsplash.@nodogsplash[0].gatewaydomainname"); err != nil {
		return err
	}
	if err := removePort443Allow(); err != nil {
		return err
	}
	fmt.Println("[3] nodogsplash cleaned up (gatewaydomainname removed, port 443 allow removed)")

	if err := uciCommitChecked("uhttpd"); err != nil {
		return err
	}
	if err := uciCommitChecked("dhcp"); err != nil {
		return err
	}
	if err := uciCommitChecked("nodogsplash"); err != nil {
		return err
	}
	// Same rule as the self-signed removal above: the restored certificate (the
	// image's placeholder, typically) does not cover this router, so the
	// :8080 -> https:// hop goes back off with it.
	if err := applyRedirectHTTPS(); err != nil {
		return err
	}
	if err := reloadServices(true); err != nil {
		return err
	}

	os.RemoveAll(backupDir)

	// Same marker as the self-signed removal above, and for the same reason: the
	// install path provisions an identity, so a removal has to be visible to it.
	if err := markSSLOptOut(fmt.Sprintf("real certificate for %s", domain)); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: %v\n  The identity is removed, but a later install may provision a new one.\n", err)
	}

	fmt.Println()
	fmt.Println("Done. HTTPS removed. Portal now served over HTTP.")
	portalName, err := uciGet("system.@system[0].hostname")
	if err != nil || portalName == "" {
		portalName = "tollgate"
	}
	fmt.Printf("  Portal URL: http://%s.lan/\n", portalName)
	return nil
}

func sslStatus() error {
	mode := fileRead(backupDir + "/ssl.mode")
	domain := fileRead(backupDir + "/ssl.domain")

	if _, err := os.Stat(certDest); os.IsNotExist(err) {
		fmt.Println("SSL: not configured")
		fmt.Println("  Run 'tollgate ssl apply' to generate a self-signed certificate")
		fmt.Println("  Run 'tollgate ssl apply <cert> [key]' to install a real certificate")
		// The product's own identity may not be configured while uhttpd still
		// serves whatever the image shipped. Say what that is, because a
		// placeholder that covers neither the hostname nor the LAN IP is what a
		// browser shows a hard certificate error for.
		if path := uhttpdCertPath(); path != "" {
			_, reason := certCoversRouter(path)
			fmt.Printf("  uhttpd serves: %s\n", path)
			fmt.Printf("  Coverage      : %s\n", reason)
		}
		// "Not configured" has two very different causes, and the difference
		// matters to whoever is debugging HTTPS: nobody ever provisioned an
		// identity, or an operator removed it on purpose and the install path is
		// now holding that decision.
		if sslOptedOut() {
			fmt.Printf("  TLS identity  : removed by the operator (%s)\n", sslOptOutFile)
			fmt.Println("                  the setup path will not provision one while that marker exists")
			fmt.Println("                  run 'tollgate ssl apply' to end the opt-out")
		}
		return nil
	}

	fmt.Println("SSL: configured")
	fmt.Printf("  Mode   : %s\n", mode)
	fmt.Printf("  Domain : %s\n", domain)
	fmt.Printf("  Cert   : %s\n", certDest)
	fmt.Printf("  Key    : %s\n", keyDest)
	if covers, reason := certCoversRouter(certDest); covers {
		fmt.Printf("  Coverage: this router (%s)\n", reason)
	} else {
		fmt.Printf("  Coverage: NOT this router (%s)\n", reason)
	}

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

// -- TLS identity coverage ---------------------------------------------------
//
// A certificate is only a usable identity for THIS router when it validates a
// name the router is actually reached by. The check below is what keeps
// uhttpd.main.redirect_https off while the router still presents the image's own
// placeholder certificate (subject CN=OpenWrt, SAN DNS:OpenWrt): that file is
// readable and non-empty, so every existence/size guard there is accepts it, and
// the browser then shows a hard certificate error for the address it typed and
// the :8080 -> https:// hop lands the operator on an UI over a certificate that
// cannot validate the router.

// lanIPFromUCI reads network.lan.ipaddr and returns the bare address.
func lanIPFromUCI() (string, error) {
	raw, err := uciGet("network.lan.ipaddr")
	if err != nil {
		return "", fmt.Errorf("cannot determine LAN IP (network.lan.ipaddr)")
	}
	ip := parseLanIP(raw)
	if ip == "" {
		return "", fmt.Errorf("cannot determine LAN IP (network.lan.ipaddr=%q)", strings.TrimSpace(raw))
	}
	return ip, nil
}

// parseLanIP turns a uci `network.lan.ipaddr` value into an address.
//
// OpenWrt accepts a CIDR suffix there and a 25.12 install stores one —
// measured on the bench GL-MT3000: `network.lan.ipaddr='192.168.1.1/24'`.
// net.ParseIP refuses that spelling, so handing the option straight to it
// produces a nil address; a template carrying a nil address makes
// x509.CreateCertificate fail outright ("invalid IP address in certificate"),
// which is how a router ended up with no identity at all. Returns "" when
// nothing usable is left.
func parseLanIP(value string) string {
	addr := strings.TrimSpace(value)
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		addr = strings.TrimSpace(addr[:i])
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return ""
	}
	return ip.String()
}

// routerSANs is the identity this router must present: the configured hostname,
// its <hostname>.lan alias (dnsmasq serves the system hostname in the 'lan'
// zone) and the LAN address the admin listeners bind.
func routerSANs(hostname, lanIP string) (dnsNames []string, ipAddresses []net.IP) {
	if h := strings.TrimSpace(hostname); h != "" {
		dnsNames = append(dnsNames, h+".lan", h)
	}
	if ip := net.ParseIP(parseLanIP(lanIP)); ip != nil {
		ipAddresses = append(ipAddresses, ip)
	}
	return dnsNames, ipAddresses
}

// selfSignedTemplate is the ONE certificate template this product generates;
// `ssl apply` and the unattended setup path both go through it.
func selfSignedTemplate(hostname, lanIP string) *x509.Certificate {
	domain := strings.TrimSpace(hostname)
	if domain == "" {
		domain = "TollGate"
	}
	if !strings.HasSuffix(domain, ".lan") {
		domain += ".lan"
	}
	dnsNames, ipAddresses := routerSANs(hostname, lanIP)
	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	return &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(3650 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
	}
}

// routerTLSNames returns the names this router answers to, from the live config.
func routerTLSNames() (hosts, ips []string) {
	hostname := strings.TrimSpace(uciGetOrEmpty("system.@system[0].hostname"))
	if hostname == "" {
		hostname, _ = os.Hostname()
		hostname = strings.TrimSpace(hostname)
	}
	if hostname != "" {
		hosts = append(hosts, hostname, hostname+".lan")
	}
	if ip := parseLanIP(uciGetOrEmpty("network.lan.ipaddr")); ip != "" {
		ips = append(ips, ip)
	}
	return hosts, ips
}

// certCoverage decides whether a certificate may serve as this router's TLS
// identity, and says why. The SANs are the subject of the test, matched with
// x509.VerifyHostname so wildcards behave as a browser expects; a CommonName
// with no SAN extension is deliberately NOT coverage, because modern browsers
// ignore it.
func certCoverage(cert *x509.Certificate, hosts, ips []string) (bool, string) {
	if cert == nil {
		return false, "no certificate to check"
	}
	if time.Now().After(cert.NotAfter) {
		return false, fmt.Sprintf("certificate expired on %s", cert.NotAfter.Format("2006-01-02"))
	}
	var names []string
	for _, name := range append(append([]string{}, hosts...), ips...) {
		if strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	if len(names) == 0 {
		return false, "this router has no hostname or LAN IP to check the certificate against"
	}
	for _, name := range names {
		if err := cert.VerifyHostname(name); err == nil {
			return true, fmt.Sprintf("SANs cover %s", name)
		}
	}
	return false, fmt.Sprintf("SANs (%s) cover none of this router's names (%s)",
		sanSummary(cert), strings.Join(names, ", "))
}

// sanSummary renders what a certificate claims to be, for the refusal message.
func sanSummary(cert *x509.Certificate) string {
	var parts []string
	for _, name := range cert.DNSNames {
		parts = append(parts, "DNS:"+name)
	}
	for _, ip := range cert.IPAddresses {
		parts = append(parts, "IP:"+ip.String())
	}
	if len(parts) > 0 {
		return strings.Join(parts, ",")
	}
	if cn := strings.TrimSpace(cert.Subject.CommonName); cn != "" {
		return "CN:" + cn + " (no SAN extension)"
	}
	return "none"
}

// certCoversRouter answers the question for a certificate file: does it cover
// this router? Unreadable, empty and non-PEM files are all "no".
func certCoversRouter(certPath string) (bool, string) {
	if strings.TrimSpace(certPath) == "" {
		return false, "no certificate path configured"
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		return false, fmt.Sprintf("cannot read %s: %v", certPath, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return false, fmt.Sprintf("%s is empty", certPath)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return false, fmt.Sprintf("%s is not a PEM certificate", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Sprintf("cannot parse %s: %v", certPath, err)
	}
	hosts, ips := routerTLSNames()
	return certCoverage(cert, hosts, ips)
}

// uhttpdCertPath is the certificate uhttpd.main will actually present — the one
// a browser is offered, and therefore the one the derived redirect depends on.
func uhttpdCertPath() string {
	if path := strings.TrimSpace(uciGetOrEmpty("uhttpd.main.cert")); path != "" {
		return path
	}
	return uhttpdCertDefault
}

// applyRedirectHTTPS re-derives uhttpd.main.redirect_https from the certificate
// uhttpd.main serves: '1' only while that certificate covers this router. It is
// the same rule the uci-defaults setup path evaluates (see
// docs/architecture/uhttpd-redirect-https-ownership-decision.md), so every
// writer of the option computes the same value.
func applyRedirectHTTPS() error {
	value := "0"
	path := uhttpdCertPath()
	if covers, reason := certCoversRouter(path); covers {
		fmt.Printf("  redirect_https=1 (%s covers this router)\n", path)
		value = "1"
	} else {
		fmt.Printf("  redirect_https=0 (%s does not: %s)\n", path, reason)
	}
	if err := uciSetScalar("uhttpd.main.redirect_https", value); err != nil {
		return err
	}
	return uciCommitChecked("uhttpd")
}

// -- the operator's opt-out --------------------------------------------------
//
// `tollgate ssl remove` is the documented way to say "this router serves no TLS
// identity". It used to be enough on its own, because nothing but the CLI ever
// wrote a certificate. Now that the install path provisions one, a removal that
// left no trace would be undone by the next install or upgrade — the operator's
// decision, overwritten silently. The marker is that trace; the setup path
// reads it (packaging/files/etc/uci-defaults/99-tollgate-setup →
// provision_tls_identity) and skips provisioning while it is present.

// sslOptedOut reports whether the TLS identity was removed by the operator.
func sslOptedOut() bool {
	info, err := os.Stat(sslOptOutFile)
	return err == nil && !info.IsDir()
}

// markSSLOptOut records that the operator removed the TLS identity. It is
// written AFTER the removal succeeded, so a failed removal cannot leave a
// marker that suppresses provisioning on a router that still has a cert.
func markSSLOptOut(summary string) error {
	if err := os.MkdirAll(sslDir, 0755); err != nil {
		return fmt.Errorf("failed to create SSL dir: %w", err)
	}
	body := fmt.Sprintf("TLS identity removed by an operator: %s\n%s\n"+
		"Written by `tollgate ssl remove` on %s.\n"+
		"The setup path (uci-defaults 99-tollgate-setup) will not provision a TLS\n"+
		"identity while this file exists. Run `tollgate ssl apply` to end the opt-out.\n",
		summary, strings.Repeat("-", 70), time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(sslOptOutFile, []byte(body), 0644); err != nil {
		return fmt.Errorf("failed to record the TLS opt-out: %w", err)
	}
	return nil
}

// clearSSLOptOut ends the opt-out. Called by `ssl apply`: asking for an identity
// is the way back in, and leaving the marker behind would make the next install
// remove the identity the operator just installed.
func clearSSLOptOut() error {
	if !sslOptedOut() {
		return nil
	}
	if err := os.Remove(sslOptOutFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clear the TLS opt-out: %w", err)
	}
	fmt.Printf("Cleared the TLS opt-out marker (%s): this router will keep its identity across installs again.\n", sslOptOutFile)
	return nil
}

// sslCoversResult is the --json payload of `tollgate ssl covers`.
type sslCoversResult struct {
	Success   bool   `json:"success"`
	Command   string `json:"command"`
	Cert      string `json:"cert"`
	Covers    bool   `json:"covers"`
	Reason    string `json:"reason"`
	Error     string `json:"error,omitempty"`
	Timestamp string `json:"timestamp"`
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
	if err := runCommandChecked(uhttpdInitPath, "reload"); err != nil {
		return fmt.Errorf("failed to reload uhttpd: %w", err)
	}
	if realCert {
		if err := runCommandChecked(dnsmasqInitPath, "reload"); err != nil {
			return fmt.Errorf("failed to reload dnsmasq: %w", err)
		}
	}
	if err := runCommandChecked(nodogsplashInitPath, "restart"); err != nil {
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

func uciDeleteIfExists(key string) error {
	// `uci delete` errors when the option is absent; absence is the
	// common case here, so treat it as success.
	if err := runCommandChecked("uci", "delete", key); err != nil {
		if strings.Contains(err.Error(), "Entry not found") {
			return nil
		}
		return err
	}
	return nil
}

func uciCommitChecked(config string) error {
	return runCommandChecked("uci", "commit", config)
}
