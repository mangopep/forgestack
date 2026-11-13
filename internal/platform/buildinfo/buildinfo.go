// Package buildinfo holds the service metadata contract shared across
// ForgeStack's reference services (see docs/platform/CONTRACTS.md):
// a stable name and a version string, both overridable at build time.
package buildinfo

// Name identifies this service in health responses and logs.
var Name = "forgestack-go-api"

// Version is set via -ldflags "-X .../buildinfo.Version=..." in release
// builds; "dev" is the honest default for an unversioned local build,
// never a fabricated version number.
var Version = "dev"
