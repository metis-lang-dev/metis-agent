// FleetTLSConfig: load the shared fleet CA and serve a leaf signed by it,
// minted once and persisted (leaf.crt/leaf.key). Chrome needs a secure
// context for getUserMedia off localhost (HARNESS.md §6).
package transport

// tls — fold TLS into the harness (lifted from nina_splat): load the shared FLEET CA (one root the
// operator installs once, trusting every robot) and serve a leaf signed by
// it. The leaf is minted ONCE and persisted (<leafDir>/leaf.crt + leaf.key)
// and reused on every boot. It used to be re-minted at each start, which
// changed its key and, worse, its names: the SANs were whatever interfaces
// happened to be up, so after a reboot the WiFi IP was missing and browsers
// warned again (2026-09-15). Delete the two files to mint a fresh leaf, e.g.
// after adding a -tls-san name.
import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// leafRenewBefore: a persisted leaf this close to expiry is re-minted.
const leafRenewBefore = 30 * 24 * time.Hour

func FleetTLSConfig(caCertPath, caKeyPath, leafDir string, extraSANs []string) (*tls.Config, error) {
	caCert, caKey, err := loadFleetCA(caCertPath, caKeyPath)
	if err != nil {
		return nil, err
	}
	crtPath := filepath.Join(leafDir, "leaf.crt")
	keyPath := filepath.Join(leafDir, "leaf.key")
	host, _ := os.Hostname()
	stableDNS, stableIPs := stableNames(host, extraSANs)

	cert, leaf, err := loadLeaf(crtPath, keyPath)
	switch {
	case err == nil:
		if problem := leafProblem(leaf, caCert); problem != "" {
			log.Printf("tls: persisted leaf %s unusable (%s) — minting a new one", crtPath, problem)
			break
		}
		if missing := missingNames(leaf, stableDNS, stableIPs); len(missing) > 0 {
			log.Printf("tls: reusing %s, but it does NOT cover %s — delete %s and %s "+
				"and restart to mint a leaf with the current names",
				crtPath, strings.Join(missing, ", "), crtPath, keyPath)
		} else {
			log.Printf("tls: reusing persisted leaf %s (names %s; expires %s)",
				crtPath, describeNames(leaf), leaf.NotAfter.Format("2006-01-02"))
		}
		return serverTLS(cert), nil
	case errors.Is(err, fs.ErrNotExist):
		log.Printf("tls: no persisted leaf in %s — minting one", leafDir)
	default:
		log.Printf("tls: persisted leaf unreadable (%v) — minting a new one", err)
	}

	cert, leaf, err = mintLeaf(caCert, caKey, host, stableDNS, stableIPs, crtPath, keyPath)
	if err != nil {
		return nil, err
	}
	log.Printf("tls: minted leaf %s (names %s; expires %s)",
		crtPath, describeNames(leaf), leaf.NotAfter.Format("2006-01-02"))
	return serverTLS(cert), nil
}

func serverTLS(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}

func loadFleetCA(caCertPath, caKeyPath string) (*x509.Certificate, any, error) {
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet CA cert: %w", err)
	}
	caKeyPEM, err := os.ReadFile(caKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet CA key: %w", err)
	}
	cb, _ := pem.Decode(caCertPEM)
	if cb == nil {
		return nil, nil, fmt.Errorf("fleet CA cert: bad PEM")
	}
	caCert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	kb, _ := pem.Decode(caKeyPEM)
	if kb == nil {
		return nil, nil, fmt.Errorf("fleet CA key: bad PEM")
	}
	caKey, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet CA key parse: %w", err)
	}
	return caCert, caKey, nil
}

// stableNames: the names a leaf must always cover — localhost, the hostname
// and its mDNS form, loopback, and the configured extras. Interface IPs are
// added at mint time too, but are not required on reuse (they depend on which
// links were up at boot).
func stableNames(host string, extraSANs []string) ([]string, []net.IP) {
	dns := []string{"localhost"}
	if host != "" {
		dns = append(dns, host)
		if !strings.Contains(host, ".") {
			dns = append(dns, host+".local")
		}
	}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	for _, s := range extraSANs {
		if ip := net.ParseIP(s); ip != nil {
			ips = appendIP(ips, ip)
		} else {
			dns = appendName(dns, s)
		}
	}
	return dns, ips
}

func loadLeaf(crtPath, keyPath string) (tls.Certificate, *x509.Certificate, error) {
	if _, err := os.Stat(crtPath); err != nil {
		return tls.Certificate{}, nil, err
	}
	if _, err := os.Stat(keyPath); err != nil {
		return tls.Certificate{}, nil, err
	}
	cert, err := tls.LoadX509KeyPair(crtPath, keyPath)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return cert, leaf, nil
}

func leafProblem(leaf, caCert *x509.Certificate) string {
	if err := leaf.CheckSignatureFrom(caCert); err != nil {
		return "not signed by the fleet CA"
	}
	if time.Now().After(leaf.NotAfter.Add(-leafRenewBefore)) {
		return "expires " + leaf.NotAfter.Format("2006-01-02")
	}
	return ""
}

func missingNames(leaf *x509.Certificate, dns []string, ips []net.IP) []string {
	var missing []string
	for _, n := range dns {
		if leaf.VerifyHostname(n) != nil {
			missing = append(missing, n)
		}
	}
	for _, ip := range ips {
		if leaf.VerifyHostname(ip.String()) != nil {
			missing = append(missing, ip.String())
		}
	}
	return missing
}

func mintLeaf(caCert *x509.Certificate, caKey any, host string, dns []string,
	ips []net.IP, crtPath, keyPath string) (tls.Certificate, *x509.Certificate, error) {
	if addrs, e := net.InterfaceAddrs(); e == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
				if v4 := ipn.IP.To4(); v4 != nil {
					ips = appendIP(ips, v4)
				}
			}
		}
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"metis-agent"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
		DNSNames:     dns,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leafCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	kd, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})
	cert, err := tls.X509KeyPair(leafCertPEM, leafKeyPEM)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	// persisting is best-effort: a read-only disk still serves this boot
	if err := os.MkdirAll(filepath.Dir(crtPath), 0o700); err != nil {
		log.Printf("tls: cannot create %s (%v) — leaf NOT persisted, re-minted next boot",
			filepath.Dir(crtPath), err)
		return cert, leaf, nil
	}
	if err := writeFileAtomic(keyPath, leafKeyPEM, 0o600); err != nil {
		log.Printf("tls: cannot write %s (%v) — leaf NOT persisted", keyPath, err)
		return cert, leaf, nil
	}
	if err := writeFileAtomic(crtPath, leafCertPEM, 0o644); err != nil {
		log.Printf("tls: cannot write %s (%v) — leaf NOT persisted", crtPath, err)
	}
	return cert, leaf, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path))
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func describeNames(leaf *x509.Certificate) string {
	names := append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	return strings.Join(names, ",")
}

func appendName(names []string, n string) []string {
	for _, have := range names {
		if strings.EqualFold(have, n) {
			return names
		}
	}
	return append(names, n)
}

func appendIP(ips []net.IP, ip net.IP) []net.IP {
	for _, have := range ips {
		if have.Equal(ip) {
			return ips
		}
	}
	return append(ips, ip)
}
