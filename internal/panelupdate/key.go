package panelupdate

// releasePublicKey is the base64 ed25519 public key that Update manifests are
// verified against. The value below is a placeholder until the first release
// key exists (ticket 02). A build can set another one for end-to-end tests:
//
//	-ldflags "-X relist-panel/internal/panelupdate.releasePublicKey=<base64>"
//
// Unit tests inject keys and never read this variable.
var releasePublicKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
