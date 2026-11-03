# Release candidates

Panel update (ADR-0024) read a release version as `MAJOR.MINOR.PATCH` and nothing else (`parseVersion`), and every release moved `:latest` and became GitHub's latest release. A version could therefore only be tried on a real Install by releasing it to everyone.

**Decision:** a release can be a release candidate, `X.Y.Z-rc.N`, which only an Install that asks for it by version is moved to.

- **The release version grammar is `MAJOR.MINOR.PATCH` or `MAJOR.MINOR.PATCH-rc.N`**, with an optional leading `v`, every number decimal. Nothing else is a release version. The order is semver's: `0.1.0-rc.1 < 0.1.0-rc.2 < 0.1.0-rc.10 < 0.1.0 < 0.1.1-rc.1`. This amends ADR-0024, whose code read only `MAJOR.MINOR.PATCH`.
- **An rc tag is a GitHub pre-release and never moves `:latest`.** GitHub never resolves `releases/latest/...` to a pre-release, so the web banner, `install.sh --update` and `curl | sh` installs see only stable releases. The rc image, its signed manifest and its `install.sh` are published as for any release.
- **The only way onto a candidate is `install.sh --update --version X.Y.Z-rc.N`.** The script turns the version into that release's manifest URL for the update Container. Nothing in the panel stores a channel.
- **A Panel update never moves backwards.** `--update` alone from a candidate moves on only once a stable release is greater than it.
- **An Install accepts a candidate only if its running version knows the grammar.** The Preflight runs in the current image, and 0.0.1 rejects an rc manifest. A stable release with this change has to ship first, or the candidate is installed fresh with its own `install.sh`.

## Considered options

**A stable/beta channel setting in the web.** Not now. Only the owner tests candidates; a setting, its storage and its banner logic would serve nobody else.

**Finding the newest pre-release through the GitHub API.** Rejected. It adds the API's rate limits, and a test Install could move to a candidate nobody picked.

**Full semver pre-release identifiers (`-beta.1`, `-alpha`, build metadata).** Rejected. `-rc.N` is all the release process uses.
