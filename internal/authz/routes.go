package authz

const (
	project     = "/api/projects/{id}"
	environment = "/api/projects/{projectID}/environments/{environmentID}"
	compose     = environment + "/composes/{composeID}"
)

// Routes classifies each ServeMux pattern by the Level it requires
// (.scratch/multi-user/spec.md, Permissions). Keys are the exact pattern
// strings the packages register; a missing key means LevelAdmin. The test
// TestEveryRouteIsClassified fails on a registered pattern that is in
// neither this table nor its explicit admin-only list.
var Routes = map[string]Rule{
	// Reached without a session: auth lets them through, or the Migration
	// session key authenticates them (ADR-0019).
	"/":                                    {Level: LevelPublic},
	"GET /healthz":                         {Level: LevelPublic},
	"POST /api/auth/login":                 {Level: LevelPublic},
	"GET /api/invites/{token}":             {Level: LevelPublic},
	"POST /api/invites/{token}":            {Level: LevelPublic},
	"POST /api/migration/claim":            {Level: LevelPublic},
	"GET /api/migration/preflight":         {Level: LevelPublic},
	"POST /api/migration/start":            {Level: LevelPublic},
	"GET /api/migration/items/{id}":        {Level: LevelPublic},
	"GET /api/migration/items/{id}/digest": {Level: LevelPublic},
	"POST /api/migration/abort":            {Level: LevelPublic},
	"POST /api/migration/finish":           {Level: LevelPublic},

	// Any signed-in User. The lists filter by the User's Access.
	"GET /api/auth/status":       {Level: LevelSignedIn},
	"POST /api/auth/logout":      {Level: LevelSignedIn},
	"GET /api/auth/account":      {Level: LevelSignedIn},
	"POST /api/auth/password":    {Level: LevelSignedIn},
	"POST /api/auth/2fa/setup":   {Level: LevelSignedIn},
	"POST /api/auth/2fa/enable":  {Level: LevelSignedIn},
	"POST /api/auth/2fa/disable": {Level: LevelSignedIn},
	// The lock page polls it for everyone (ADR-0018).
	"GET /api/migration/status":  {Level: LevelSignedIn},
	"GET /api/projects":          {Level: LevelSignedIn},
	"GET /api/deployments":       {Level: LevelSignedIn},
	"GET /api/deployments/queue": {Level: LevelSignedIn},
	"GET /api/ssh-keys":          {Level: LevelSignedIn},
	"GET /api/builders":          {Level: LevelSignedIn},

	// Viewer: read.
	"GET " + project: {Level: LevelViewer},
	// The handler answers 403 unless the User edits one of its Environments.
	"GET " + project + "/audit":                                  {Level: LevelViewer},
	"GET /api/projects/{projectID}/environments":                 {Level: LevelViewer},
	"GET " + environment + "/variables":                          {Level: LevelViewer},
	"GET " + environment + "/composes":                           {Level: LevelViewer},
	"GET " + compose:                                             {Level: LevelViewer},
	"GET " + compose + "/builder":                                {Level: LevelViewer},
	"GET " + compose + "/logs":                                   {Level: LevelViewer},
	"GET " + compose + "/services":                               {Level: LevelViewer},
	"GET " + compose + "/services/{service}/effective-variables": {Level: LevelViewer},
	"GET " + compose + "/services/{service}/metrics":             {Level: LevelViewer},
	"GET " + compose + "/zero-downtime":                          {Level: LevelViewer},
	"GET " + compose + "/bindings":                               {Level: LevelViewer},
	"GET " + compose + "/deployments":                            {Level: LevelViewer},
	"GET " + compose + "/deployments/{number}":                   {Level: LevelViewer},
	"GET " + compose + "/deployments/{number}/log":               {Level: LevelViewer},
	"GET " + compose + "/domains":                                {Level: LevelViewer},
	"GET " + compose + "/source":                                 {Level: LevelViewer},
	"GET " + compose + "/source/dockerfiles":                     {Level: LevelViewer},
	"GET " + compose + "/variable-sets":                          {Level: LevelViewer},
	"GET " + compose + "/variables":                              {Level: LevelViewer},

	// Operator: run what is there.
	"POST " + compose + "/deployments":                 {Level: LevelOperator},
	"POST " + compose + "/deployments/{number}/cancel": {Level: LevelOperator},
	"POST " + compose + "/restart":                     {Level: LevelOperator},
	"POST " + compose + "/stop":                        {Level: LevelOperator},

	// Editor: change it, and everything that reads secrets or data.
	"POST " + environment + "/variables":                             {Level: LevelEditor},
	"PUT " + environment + "/variables/{variableID}":                 {Level: LevelEditor},
	"DELETE " + environment + "/variables/{variableID}":              {Level: LevelEditor},
	"DELETE " + compose:                                              {Level: LevelEditor},
	"GET " + compose + "/removal-plan":                               {Level: LevelEditor},
	"GET " + compose + "/policy":                                     {Level: LevelViewer},
	"PUT " + compose + "/builder":                                    {Level: LevelEditor},
	"PUT " + compose + "/zero-downtime/{service}":                    {Level: LevelEditor},
	"PUT " + compose + "/bindings/{service}":                         {Level: LevelEditor},
	"POST " + compose + "/domains":                                   {Level: LevelEditor},
	"PUT " + compose + "/domains/{domainID}":                         {Level: LevelEditor},
	"DELETE " + compose + "/domains/{domainID}":                      {Level: LevelEditor},
	"PUT " + compose + "/source":                                     {Level: LevelEditor},
	"DELETE " + compose + "/source":                                  {Level: LevelEditor},
	"POST " + compose + "/source/test":                               {Level: LevelEditor},
	"POST " + compose + "/source/known-hosts/reset":                  {Level: LevelEditor},
	"POST " + compose + "/source/upload":                             {Level: LevelEditor},
	"POST " + compose + "/variable-sets":                             {Level: LevelEditor},
	"PUT " + compose + "/variable-sets/{setID}":                      {Level: LevelEditor},
	"DELETE " + compose + "/variable-sets/{setID}":                   {Level: LevelEditor},
	"POST " + compose + "/variable-sets/{setID}/entries":             {Level: LevelEditor},
	"PUT " + compose + "/variable-sets/{setID}/entries/{entryID}":    {Level: LevelEditor},
	"DELETE " + compose + "/variable-sets/{setID}/entries/{entryID}": {Level: LevelEditor},
	"POST " + compose + "/variables":                                 {Level: LevelEditor},
	"PUT " + compose + "/variables/{variableID}":                     {Level: LevelEditor},
	"DELETE " + compose + "/variables/{variableID}":                  {Level: LevelEditor},
	"GET " + compose + "/terminal":                                   {Level: LevelEditor},
	"GET " + compose + "/terminal/services":                          {Level: LevelEditor},
	"GET " + compose + "/volumes":                                    {Level: LevelEditor},
	"GET " + compose + "/volumes/archive":                            {Level: LevelEditor},
	"DELETE " + compose + "/volumes/content":                         {Level: LevelEditor},
	"PUT " + compose + "/volumes/content":                            {Level: LevelEditor},
	"PUT " + compose + "/volumes/directory":                          {Level: LevelEditor},
	"PUT " + compose + "/volumes/file":                               {Level: LevelEditor},
	"GET " + compose + "/volumes/list":                               {Level: LevelEditor},
	"GET " + compose + "/volumes/orphans":                            {Level: LevelEditor},
	"DELETE " + compose + "/volumes/orphans/{name}":                  {Level: LevelEditor},
	"GET " + compose + "/volumes/read":                               {Level: LevelEditor},
	"PUT " + compose + "/volumes/restore":                            {Level: LevelEditor},
	"GET " + compose + "/volumes/stat":                               {Level: LevelEditor},

	// Project Editor: the Project's own details and its Environments.
	"PUT " + project: {Level: LevelProjectEditor},
	"POST /api/projects/{projectID}/environments":                  {Level: LevelProjectEditor},
	"DELETE /api/projects/{projectID}/environments/{id}":           {Level: LevelProjectEditor},
	"GET /api/projects/{projectID}/environments/{id}/removal-plan": {Level: LevelProjectEditor},
}
