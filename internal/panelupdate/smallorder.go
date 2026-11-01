package panelupdate

import (
	"crypto/ed25519"
	"errors"
	"math/big"
)

// Go's ed25519.Verify accepts small-order public keys, for which signatures
// can be forged. checkKey refuses them: a key must be a point on the curve
// whose multiple by 8 is not the identity.

var (
	edP = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	edD = func() *big.Int {
		inv := new(big.Int).ModInverse(big.NewInt(121666), edP)
		d := new(big.Int).Mul(big.NewInt(-121665), inv)
		return d.Mod(d, edP)
	}()
	edI = new(big.Int).Exp(big.NewInt(2), new(big.Int).Rsh(new(big.Int).Sub(edP, big.NewInt(1)), 2), edP)
)

func checkKey(key []byte) error {
	if len(key) != ed25519.PublicKeySize {
		return errors.New("the release public key is not valid")
	}
	if smallOrder(key) {
		return errors.New("the release public key is not valid")
	}
	return nil
}

func modInv(a *big.Int) *big.Int { return new(big.Int).ModInverse(a, edP) }

// smallOrder reports whether enc is not a point, or a point of order 8 or less.
func smallOrder(enc []byte) bool {
	le := make([]byte, 32)
	for i := range le {
		le[i] = enc[31-i]
	}
	le[0] &^= 0x80 // the sign bit of x; the order does not depend on it
	y := new(big.Int).SetBytes(le)
	y.Mod(y, edP)
	y2 := new(big.Int).Mul(y, y)
	u := new(big.Int).Sub(y2, big.NewInt(1))
	v := new(big.Int).Mul(edD, y2)
	v.Add(v, big.NewInt(1)).Mod(v, edP)
	r := new(big.Int).Mul(u, modInv(v))
	r.Mod(r, edP)
	x := new(big.Int).Exp(r, new(big.Int).Rsh(new(big.Int).Add(edP, big.NewInt(3)), 3), edP)
	if new(big.Int).Mod(new(big.Int).Mul(x, x), edP).Cmp(r) != 0 {
		x.Mul(x, edI).Mod(x, edP)
		if new(big.Int).Mod(new(big.Int).Mul(x, x), edP).Cmp(r) != 0 {
			return true // not on the curve
		}
	}
	for i := 0; i < 3; i++ {
		x, y = edAdd(x, y, x, y)
	}
	return x.Sign() == 0 && y.Cmp(big.NewInt(1)) == 0
}

// edAdd is the complete affine addition on edwards25519.
func edAdd(x1, y1, x2, y2 *big.Int) (*big.Int, *big.Int) {
	xx := new(big.Int).Mul(x1, x2)
	yy := new(big.Int).Mul(y1, y2)
	t := new(big.Int).Mul(edD, xx)
	t.Mul(t, yy).Mod(t, edP)
	x3 := new(big.Int).Mul(x1, y2)
	x3.Add(x3, new(big.Int).Mul(x2, y1))
	x3.Mul(x3, modInv(new(big.Int).Add(big.NewInt(1), t))).Mod(x3, edP)
	y3 := new(big.Int).Add(yy, xx)
	y3.Mul(y3, modInv(new(big.Int).Sub(big.NewInt(1), t).Mod(new(big.Int).Sub(big.NewInt(1), t), edP))).Mod(y3, edP)
	return x3, y3
}
