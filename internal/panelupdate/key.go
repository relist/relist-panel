package panelupdate

// releasePublicKey is the base64 ed25519 public key that Update manifests are
// verified against. It is empty until the first release key exists (ticket
// 02); a build with no key refuses every manifest. A build can set a key for
// end-to-end tests:
//
//	-ldflags "-X relist-panel/internal/panelupdate.releasePublicKey=<base64>"
//
// Unit tests inject keys and never read this variable.
var releasePublicKey = ""
