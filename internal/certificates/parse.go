// Package certificates stores the TLS certificates the operator uploads and
// hands them to Traefik's file provider (ADR-0022).
package certificates

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

const (
	maxCertificateBytes = 64 * 1024
	maxPrivateKeyBytes  = 16 * 1024
)

// FieldError names the invalid field of a request, by its JSON name.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// Parsed is a validated certificate and key, normalised to their PEM blocks,
// with what the list shows read from the leaf.
type Parsed struct {
	CertificatePEM string // the CERTIFICATE blocks only, leaf first
	PrivateKeyPEM  string // the one key block
	SANs           []string
	Issuer         string
	NotAfter       time.Time
}

// Parse checks that certPEM (a leaf, then its chain) and keyPEM belong
// together and that Traefik could serve the leaf at now.
func Parse(certPEM, keyPEM string, now time.Time) (Parsed, *FieldError) {
	if len(certPEM) > maxCertificateBytes {
		return Parsed{}, &FieldError{"certificate", "certificate must be at most 64 KiB"}
	}
	if len(keyPEM) > maxPrivateKeyBytes {
		return Parsed{}, &FieldError{"privateKey", "private key must be at most 16 KiB"}
	}

	var certs []*x509.Certificate
	var certOut strings.Builder
	rest := trimLines(certPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return Parsed{}, &FieldError{"certificate", fmt.Sprintf("certificate %d does not parse: %v", len(certs)+1, err)}
		}
		certs = append(certs, cert)
		certOut.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes}))
	}
	if len(certs) == 0 {
		return Parsed{}, &FieldError{"certificate", "no CERTIFICATE block found; paste the PEM starting with -----BEGIN CERTIFICATE-----"}
	}

	keyBlock, ferr := findKey(keyPEM)
	if ferr != nil {
		return Parsed{}, ferr
	}
	keyOut := string(pem.EncodeToMemory(&pem.Block{Type: keyBlock.Type, Bytes: keyBlock.Bytes}))

	if _, err := tls.X509KeyPair([]byte(certOut.String()), []byte(keyOut)); err != nil {
		if matchesLater(certs, keyBlock) {
			return Parsed{}, &FieldError{"privateKey", "private key does not match the first certificate; put the server certificate first, then the chain"}
		}
		return Parsed{}, &FieldError{"privateKey", "private key does not match the certificate"}
	}

	leaf := certs[0]
	if !now.Before(leaf.NotAfter) {
		return Parsed{}, &FieldError{"certificate", fmt.Sprintf("certificate expired on %s", leaf.NotAfter.UTC().Format(time.DateOnly))}
	}
	if len(leaf.DNSNames) == 0 {
		return Parsed{}, &FieldError{"certificate", "certificate has no DNS name (subject alternative name), so Traefik would never serve it"}
	}

	issuer := leaf.Issuer.CommonName
	if issuer == "" {
		issuer = leaf.Issuer.String()
	}
	return Parsed{
		CertificatePEM: certOut.String(),
		PrivateKeyPEM:  keyOut,
		SANs:           append([]string(nil), leaf.DNSNames...),
		Issuer:         issuer,
		NotAfter:       leaf.NotAfter.UTC(),
	}, nil
}

// findKey returns the first private key block of keyPEM, refusing an
// encrypted one.
func findKey(keyPEM string) (*pem.Block, *FieldError) {
	rest := trimLines(keyPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, &FieldError{"privateKey", "no private key block found; paste the PEM starting with -----BEGIN PRIVATE KEY-----"}
		}
		if !strings.HasSuffix(block.Type, "PRIVATE KEY") {
			continue
		}
		if block.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
			return nil, &FieldError{"privateKey", "private key is encrypted; remove its passphrase first"}
		}
		return block, nil
	}
}

// trimLines strips each line's surrounding whitespace, CR included, so a PEM
// pasted indented or with Windows line endings still decodes.
func trimLines(s string) []byte {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return []byte(strings.Join(lines, "\n"))
}

// matchesLater reports whether the key belongs to a certificate other than
// the first, i.e. the chain was pasted in the wrong order.
func matchesLater(certs []*x509.Certificate, keyBlock *pem.Block) bool {
	public := publicKey(keyBlock)
	if public == nil {
		return false
	}
	for _, cert := range certs[1:] {
		if public.Equal(cert.PublicKey) {
			return true
		}
	}
	return false
}

func publicKey(block *pem.Block) interface{ Equal(crypto.PublicKey) bool } {
	var key any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	if err != nil {
		return nil
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil
	}
	public, _ := signer.Public().(interface{ Equal(crypto.PublicKey) bool })
	return public
}
