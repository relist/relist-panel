package certificates

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)

type keyKind int

const (
	keyPKCS8 keyKind = iota
	keyPKCS1
	keySEC1
)

type pairOptions struct {
	sans     []string
	notAfter time.Time
	kind     keyKind
	signed   bool // signed by a generated CA, whose PEM is returned as chain
}

type testPair struct {
	cert, key, chain string // chain is the CA's PEM, empty when self-signed
	notAfter         time.Time
}

func newPair(t *testing.T, opts pairOptions) testPair {
	t.Helper()
	if opts.notAfter.IsZero() {
		opts.notAfter = testNow.Add(90 * 24 * time.Hour)
	}
	var signer crypto.Signer
	var keyBlock *pem.Block
	switch opts.kind {
	case keyPKCS1:
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		signer = k
		keyBlock = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}
	case keySEC1:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		signer = k
		keyBlock = &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}
	default:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		signer = k
		keyBlock = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	}

	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "leaf"},
		DNSNames:     opts.sans,
		NotBefore:    testNow.Add(-24 * time.Hour),
		NotAfter:     opts.notAfter,
	}
	parent, parentKey := leaf, signer
	var chain string
	if opts.signed {
		caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		ca := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "Test CA"},
			NotBefore:             testNow.Add(-48 * time.Hour),
			NotAfter:              testNow.Add(3650 * 24 * time.Hour),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign,
		}
		caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caKey.Public(), caKey)
		if err != nil {
			t.Fatal(err)
		}
		chain = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
		parent, parentKey = ca, caKey
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, parent, signer.Public(), parentKey)
	if err != nil {
		t.Fatal(err)
	}
	return testPair{
		cert:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		key:      string(pem.EncodeToMemory(keyBlock)),
		chain:    chain,
		notAfter: opts.notAfter,
	}
}

func TestParseValidLeafAndChain(t *testing.T) {
	p := newPair(t, pairOptions{sans: []string{"example.com", "*.example.com"}, signed: true})
	got, ferr := Parse(p.cert+p.chain, p.key, testNow)
	if ferr != nil {
		t.Fatalf("Parse: %v", ferr)
	}
	if strings.Join(got.SANs, ",") != "example.com,*.example.com" {
		t.Errorf("SANs = %v", got.SANs)
	}
	if !got.NotAfter.Equal(p.notAfter.Truncate(time.Second)) {
		t.Errorf("NotAfter = %v, want %v", got.NotAfter, p.notAfter)
	}
	if got.Issuer != "Test CA" {
		t.Errorf("Issuer = %q", got.Issuer)
	}
	if n := strings.Count(got.CertificatePEM, "BEGIN CERTIFICATE"); n != 2 {
		t.Errorf("CertificatePEM has %d blocks, want 2", n)
	}
	if !strings.Contains(got.PrivateKeyPEM, "BEGIN PRIVATE KEY") {
		t.Errorf("PrivateKeyPEM = %q", got.PrivateKeyPEM)
	}
}

func TestParseAcceptsCRLFAndWhitespace(t *testing.T) {
	p := newPair(t, pairOptions{sans: []string{"example.com"}, signed: true})
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	cert := "\r\n\r\n  Subject: leaf\r\n" + crlf(p.cert) + "\r\n" + crlf(p.chain) + "\r\n  "
	key := "\r\n  " + crlf(p.key) + "\r\n\r\n"
	if _, ferr := Parse(cert, key, testNow); ferr != nil {
		t.Fatalf("Parse: %v", ferr)
	}
}

func TestParseKeyFormats(t *testing.T) {
	for name, kind := range map[string]keyKind{"pkcs8": keyPKCS8, "pkcs1": keyPKCS1, "sec1": keySEC1} {
		t.Run(name, func(t *testing.T) {
			p := newPair(t, pairOptions{sans: []string{"example.com"}, kind: kind})
			if _, ferr := Parse(p.cert, p.key, testNow); ferr != nil {
				t.Fatalf("Parse: %v", ferr)
			}
		})
	}
}

func TestParseRefuses(t *testing.T) {
	good := newPair(t, pairOptions{sans: []string{"example.com"}, signed: true})
	other := newPair(t, pairOptions{sans: []string{"example.com"}})
	expired := newPair(t, pairOptions{sans: []string{"example.com"}, notAfter: testNow.Add(-time.Hour)})
	noSAN := newPair(t, pairOptions{})
	garbage := "-----BEGIN CERTIFICATE-----\nbm90IGEgY2VydGlmaWNhdGU=\n-----END CERTIFICATE-----\n"
	encrypted := "-----BEGIN ENCRYPTED PRIVATE KEY-----\nAAAA\n-----END ENCRYPTED PRIVATE KEY-----\n"
	procType := "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00000000000000000000000000000000\n\nAAAA\n-----END RSA PRIVATE KEY-----\n"

	cases := []struct {
		name, cert, key, field, message string
	}{
		{"empty certificate", "", good.key, "certificate", "no CERTIFICATE block"},
		{"garbage certificate", garbage, good.key, "certificate", "does not parse"},
		{"expired", expired.cert, expired.key, "certificate", "expired"},
		{"no DNS SAN", noSAN.cert, noSAN.key, "certificate", "no DNS name"},
		{"certificate too large", good.cert + strings.Repeat("x", 64*1024), good.key, "certificate", "64 KiB"},
		{"empty key", good.cert, "", "privateKey", "no private key block"},
		{"encrypted pkcs8", good.cert, encrypted, "privateKey", "encrypted"},
		{"encrypted proc-type", good.cert, procType, "privateKey", "encrypted"},
		{"key from another pair", good.cert, other.key, "privateKey", "does not match"},
		{"chain CA first", good.chain + good.cert, good.key, "privateKey", "server certificate first"},
		{"chain block with bad base64", good.cert + "-----BEGIN CERTIFICATE-----\n!!!not base64!!!\n-----END CERTIFICATE-----\n", good.key, "certificate", "does not parse"},
		{"truncated chain block", good.cert + good.chain[:len(good.chain)/2], good.key, "certificate", "does not parse"},
		{"key too large", good.cert, good.key + strings.Repeat("x", 16*1024), "privateKey", "16 KiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ferr := Parse(tc.cert, tc.key, testNow)
			if ferr == nil {
				t.Fatal("Parse accepted it")
			}
			if ferr.Field != tc.field || !strings.Contains(ferr.Message, tc.message) {
				t.Errorf("got %s: %q, want %s containing %q", ferr.Field, ferr.Message, tc.field, tc.message)
			}
		})
	}
}
