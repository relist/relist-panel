// Package agefile encrypts and decrypts age files with an scrypt passphrase
// (ADR-0046). The work factor is pinned at 2^15 (32 MiB, about 0.1 s): age's
// 2^18 default would cost 256 MiB and have the panel container killed. One
// derivation runs at a time in the whole panel, and a file asking for more
// than 2^15 is refused before any derivation.
package agefile

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"

	"filippo.io/age"
)

// WorkFactor is the scrypt work factor (log2 N) on encrypt, and the hard
// maximum on decrypt.
const WorkFactor = 15

// ErrWrongPassphrase is a file none of whose stanzas opened with the given
// passphrase: a wrong passphrase, or a file keyed to recipients instead.
var ErrWrongPassphrase = errors.New("wrong passphrase, or the file is not encrypted with a passphrase")

// ErrTooExpensive is a file whose header asks for more memory than the panel
// can afford.
var ErrTooExpensive = errors.New("this file's scrypt work factor is above 2^15, which the panel cannot afford: decrypt it with `age -d` first and upload the result")

// mu serializes every scrypt derivation in the panel.
var mu sync.Mutex

var magic = []byte("age-encryption.org/v1\n")

// IsAge says whether prefix (at least 22 bytes of a file) starts the binary
// age header. Armored (PEM) age files are not detected.
func IsAge(prefix []byte) bool {
	return bytes.HasPrefix(prefix, magic)
}

// Encrypt writes src to dst as an age file under passphrase.
func Encrypt(dst io.Writer, passphrase string, src io.Reader) error {
	rec, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return err
	}
	rec.SetWorkFactor(WorkFactor)
	mu.Lock()
	defer mu.Unlock()
	w, err := age.Encrypt(dst, rec)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, src); err != nil {
		return err
	}
	return w.Close()
}

// Decrypt opens an age file under passphrase and returns its plain stream.
// The derivation is done when it returns; reading the stream derives nothing.
func Decrypt(passphrase string, src io.Reader) (io.Reader, error) {
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	id.SetMaxWorkFactor(WorkFactor)
	mu.Lock()
	defer mu.Unlock()
	r, err := age.Decrypt(src, id)
	if err != nil {
		if tooExpensive(err) {
			return nil, ErrTooExpensive
		}
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, ErrWrongPassphrase
		}
		return nil, err
	}
	return r, nil
}

// tooExpensive recognizes the identity's work-factor refusal, which
// age.Decrypt reports before any derivation.
func tooExpensive(err error) bool {
	if strings.Contains(err.Error(), "work factor") {
		return true
	}
	var noMatch *age.NoIdentityMatchError
	if errors.As(err, &noMatch) {
		for _, e := range noMatch.Errors {
			if strings.Contains(e.Error(), "work factor") {
				return true
			}
		}
	}
	return false
}
