package panelupdate

// releasePublicKey is the base64 ed25519 public key that Update manifests are
// verified against; its private half is the repo secret RELIST_RELEASE_KEY
// (ticket 02). The release workflow reads this line and builds it into the
// image. A build can set another key for end-to-end tests:
//
//	-ldflags "-X relist-panel/internal/panelupdate.releasePublicKey=<base64>"
//
// Unit tests inject keys and never read this variable.
var releasePublicKey = "iuckFC80OnOjv7A4cqvnRxGCuB0tsYFEzx1NLMMsGvE="
