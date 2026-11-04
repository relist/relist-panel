package authz

// Routes classifies each ServeMux pattern by the Level it requires. Keys are
// the exact pattern strings the packages register; a missing key means
// LevelAdmin.
var Routes = map[string]Rule{
	"GET /api/auth/status":       {Level: LevelSignedIn},
	"POST /api/auth/logout":      {Level: LevelSignedIn},
	"GET /api/auth/account":      {Level: LevelSignedIn},
	"POST /api/auth/password":    {Level: LevelSignedIn},
	"POST /api/auth/2fa/setup":   {Level: LevelSignedIn},
	"POST /api/auth/2fa/enable":  {Level: LevelSignedIn},
	"POST /api/auth/2fa/disable": {Level: LevelSignedIn},
	// The lock page polls it for everyone (ADR-0018).
	"GET /api/migration/status": {Level: LevelSignedIn},
}
