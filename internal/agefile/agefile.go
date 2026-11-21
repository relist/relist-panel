// Package agefile encrypts and decrypts age files with an scrypt passphrase
// (ADR-0046). The work factor is pinned at 2^15 (32 MiB, about 0.1 s): age's
// 2^18 default would cost 256 MiB and have the panel container killed. One
// derivation runs at a time in the whole panel, and a file asking for more
// than 2^15 is refused before any derivation.
package agefile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
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

// ErrDamaged is an age file whose header or payload does not parse or
// authenticate: truncated, corrupted, or not an age file after all.
var ErrDamaged = errors.New("this age file is damaged")

// mu serializes every scrypt derivation in the panel.
var mu sync.Mutex

var magic = []byte("age-encryption.org/v1\n")

// IsAge says whether prefix (at least 22 bytes of a file) starts the binary
// age header. Armored (PEM) age files are not detected.
func IsAge(prefix []byte) bool {
	return bytes.HasPrefix(prefix, magic)
}

// Encrypt writes src to dst as an age file under passphrase. Only the
// derivation holds the lock; the copy does not.
func Encrypt(dst io.Writer, passphrase string, src io.Reader) error {
	rec, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return err
	}
	rec.SetWorkFactor(WorkFactor)
	w, err := age.Encrypt(dst, lockedRecipient{rec})
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
// A read that hits damage in the payload fails with ErrDamaged.
func Decrypt(passphrase string, src io.Reader) (io.Reader, error) {
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	id.SetMaxWorkFactor(WorkFactor)
	r, err := age.Decrypt(src, checkedIdentity{id})
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		switch {
		case errors.Is(err, ErrTooExpensive):
			return nil, ErrTooExpensive
		case errors.As(err, &noMatch):
			return nil, ErrWrongPassphrase
		}
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	return damageReader{r}, nil
}

// lockedRecipient runs the scrypt derivation of Wrap under mu.
type lockedRecipient struct{ r *age.ScryptRecipient }

func (l lockedRecipient) Wrap(fileKey []byte) ([]*age.Stanza, error) {
	mu.Lock()
	defer mu.Unlock()
	return l.r.Wrap(fileKey)
}

// WrapWithLabels keeps age's rule that an scrypt recipient is the only one.
func (l lockedRecipient) WrapWithLabels(fileKey []byte) ([]*age.Stanza, []string, error) {
	mu.Lock()
	defer mu.Unlock()
	return l.r.WrapWithLabels(fileKey)
}

// checkedIdentity refuses a well-formed scrypt stanza above WorkFactor with
// ErrTooExpensive before any derivation, and runs the derivation under mu.
// A malformed work factor is left to age, which reports it as damage.
type checkedIdentity struct{ id *age.ScryptIdentity }

var digits = regexp.MustCompile(`^[1-9][0-9]*$`)

func (c checkedIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	for _, s := range stanzas {
		if s.Type != "scrypt" || len(s.Args) != 2 || !digits.MatchString(s.Args[1]) {
			continue
		}
		if n, err := strconv.Atoi(s.Args[1]); err != nil || n > WorkFactor {
			return nil, ErrTooExpensive
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return c.id.Unwrap(stanzas)
}

// damageReader reports payload damage as ErrDamaged. io.EOF passes through
// as is: io.Copy compares it with ==.
type damageReader struct{ r io.Reader }

func (d damageReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err != nil && err != io.EOF {
		err = fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	return n, err
}
