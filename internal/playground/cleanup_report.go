package playground

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// VerificationMode records whether a domain's verification in a
// CleanupReport came from a real, credentialed, AWS-reaching client, or
// from a fake/test double. Its zero value (""), deliberately, is
// treated by SafetyField/the report builder exactly like
// VerificationSimulated -- an unset/unknown mode is never silently
// treated as live evidence. This is this package's existing fail-closed
// philosophy (see e.g. CloudReconciliation.Clean's "unavailable is
// never clean") applied to report labeling.
//
// VerificationMode is NEVER set by a caller flag anywhere in this
// package -- see cloudVerificationMode/dockerVerificationMode below.
type VerificationMode string

const (
	VerificationSimulated VerificationMode = "SIMULATED"
	VerificationLive      VerificationMode = "LIVE"
)

// LiveCloudClient, if implemented by a CloudClient, lets that specific
// implementation attest -- from its OWN construction, never from
// anything a caller merely asserts on a Controller -- that it is backed
// by a real, authenticated, AWS-reaching client. No implementation of
// this interface exists anywhere in this repository: S3Adapter (the one
// real adapter this repo has) does not implement it, deliberately,
// because nothing in this offline-only codebase can ever legitimately
// construct one with a genuinely authenticated client -- doing so would
// require real AWS access, which this repository's development process
// forbids. A future, separately-reviewed real adapter could implement
// VerifiedLive to return true only after it has itself independently
// confirmed (e.g. a successful lightweight identity call made once at
// construction, cached, never re-asserted by a caller) that it holds
// live credentials. Until then, cloudVerificationMode below always
// returns VerificationSimulated for every CloudClient this repository
// can construct.
type LiveCloudClient interface {
	CloudClient
	// VerifiedLive reports whether this specific client instance has
	// independently confirmed it is backed by real, live credentials --
	// never a hardcoded true, never settable from outside the type's
	// own construction.
	VerifiedLive() bool
}

// LiveDockerClient is LiveCloudClient's Docker equivalent. No
// implementation exists in this repository; execDockerClient (docker.go)
// shells out to a real local docker daemon, which is a real verification
// of LOCAL Docker state, but this package still does not claim
// VerificationLive for it today -- see cloudVerificationMode's doc
// comment for the same reasoning applied here. Promoting
// execDockerClient to implement this interface is a deliberate, future,
// separately-reviewed decision, not an accident of omission.
type LiveDockerClient interface {
	DockerClient
	VerifiedLive() bool
}

// cloudVerificationMode derives a CleanupReport's cloud verification
// label strictly from client's own concrete type -- never from a
// Controller field a caller could set independently of what client
// actually is. Returns VerificationSimulated for nil, for every fake in
// this package, and for any CloudClient that does not implement
// LiveCloudClient (which today is every CloudClient this package can
// construct).
func cloudVerificationMode(client CloudClient) VerificationMode {
	if lv, ok := client.(LiveCloudClient); ok && lv.VerifiedLive() {
		return VerificationLive
	}
	return VerificationSimulated
}

// dockerVerificationMode is cloudVerificationMode's Docker equivalent.
func dockerVerificationMode(client DockerClient) VerificationMode {
	if lv, ok := client.(LiveDockerClient); ok && lv.VerifiedLive() {
		return VerificationLive
	}
	return VerificationSimulated
}

// SafetyField is the one-line, user-facing safety verdict a CleanupReport
// carries. Never inferred from "the removal call didn't error" -- always
// derived from an independent, fresh reconciliation read, the same
// standard CloudReconciliation.Clean()/Reconciliation.Clean() already
// enforce for the underlying cleanup decision itself.
type SafetyField string

const (
	// SafetyVerifiedClean means every configured domain was
	// independently re-checked THIS report and found to have nothing
	// live under this environment's own identity.
	SafetyVerifiedClean SafetyField = "VERIFIED CLEAN"
	// SafetyActionRequired means at least one tracked or orphaned
	// resource was independently confirmed still present, or local
	// cleanup itself failed outright (cleanup_incomplete) -- a human
	// has something concrete to go look at.
	SafetyActionRequired SafetyField = "ACTION REQUIRED"
	// SafetyUnverified means at least one configured domain could not
	// even be checked this report (Docker/cloud API unreachable,
	// throttled, credentials rejected) -- the absence of evidence is
	// never presented as evidence of absence.
	SafetyUnverified SafetyField = "UNVERIFIED"
)

// ResourceReport is one resource's independently-verified state inside a
// CleanupReport.
type ResourceReport struct {
	Domain string `json:"domain"` // "local", "docker", or "cloud"
	Kind   string `json:"kind"`
	// Account/Region are populated only for cloud resources -- a
	// Docker/local resource has no AWS account or region.
	Account string `json:"account,omitempty"`
	Region  string `json:"region,omitempty"`
	Ref     string `json:"ref"`
	// Present is only meaningful when Verified is true: whether this
	// resource was independently found to still exist.
	Present bool `json:"present"`
	// Verified records whether this resource's Present value actually
	// came from a fresh, independent check this report -- false means
	// "could not check," and Present must then be ignored, never read
	// as "confirmed absent."
	Verified bool `json:"verified"`
	// Tracked records whether this package's own records named this
	// resource (true) or it was found present but untracked --
	// "orphaned" in Reconciliation/CloudReconciliation's vocabulary
	// (false) -- worth a human's attention either way, but for
	// different reasons.
	Tracked bool `json:"tracked"`
	// VerifiedAt is when this specific resource's state was checked --
	// in practice the same instant as the report's own GeneratedAt,
	// since one report performs one reconciliation pass per domain, but
	// recorded per-resource because the requirement asks for a
	// verification timestamp per resource, not only per report.
	VerifiedAt time.Time `json:"verified_at"`
	// ConsoleURL links to this resource's own AWS Console page when
	// Present, or to the owning service's regional inventory page when
	// independently verified absent (its own page no longer exists) --
	// see consoleURLForCloudResource. Empty for domains with no AWS
	// Console equivalent (local, docker) or an unsupported cloud kind.
	ConsoleURL string `json:"console_url,omitempty"`
	// Error records this one resource's own check failing, distinct
	// from a whole-domain unavailability (see CleanupReport.Errors).
	Error string `json:"error,omitempty"`
}

// CleanupReport is the structured, durable record of one cleanup
// attempt for one environment -- produced by GenerateCleanupReport,
// never hand-assembled, so every report's Safety field is always
// derived the same way from the same independent reconciliation reads
// this package's cleanup decision itself already uses.
type CleanupReport struct {
	EnvironmentID string    `json:"environment_id"`
	Owner         string    `json:"owner"`
	GeneratedAt   time.Time `json:"generated_at"`
	// Outcome mirrors the environment's own persisted Status
	// (lifecycle.go) at the moment this report was generated --
	// e.g. "verified_clean", "cleanup_attention_required".
	Outcome string `json:"outcome"`
	// Safety is the one-line, user-facing verdict -- see SafetyField's
	// doc comment. This is the field a UI should headline; Outcome is
	// this package's own internal status vocabulary, useful for
	// operators already familiar with it, not meant to be the
	// headline a non-operator reads.
	Safety SafetyField `json:"safety"`
	// CloudVerification/DockerVerification record which verification
	// mode produced this report's cloud/docker resource entries (see
	// VerificationMode). Zero value ("") when that domain was not
	// configured at all for this cleanup.
	CloudVerification  VerificationMode `json:"cloud_verification,omitempty"`
	DockerVerification VerificationMode `json:"docker_verification,omitempty"`
	// Simulated is true whenever ANY configured domain's verification
	// was not explicitly asserted VerificationLive -- the single field
	// a UI needs to check before ever presenting this report's Safety
	// field as real AWS-verified evidence. A report with no cloud/docker
	// domain configured at all (local-only) is never Simulated: the
	// local filesystem check is always a real, direct read, never a
	// fake.
	Simulated bool             `json:"simulated"`
	Resources []ResourceReport `json:"resources"`
	// Errors collects whole-domain failures (a Reconcile/CloudReconcile
	// call itself erroring, not merely reporting unavailable) and the
	// cleanup attempt's own error, if any -- formatted as strings so
	// this struct remains plain-JSON-serializable evidence, never a Go
	// error value.
	Errors []string `json:"errors,omitempty"`
}

// GenerateCleanupReport builds a CleanupReport for env's CURRENT
// persisted state by independently re-reading (read-only, NEVER
// mutating anything -- never touches environment.json,
// cloud-cleanup-state.json, docker-resources.json, or
// cloud-resources.json) each domain's reconciliation. It reuses
// Reconcile/CloudReconcile -- the exact same read-only functions
// Controller.verifyAdditionalDomains' own cleanup decision is built on
// -- rather than re-deriving "is this clean" from any new logic.
//
// SafetyVerifiedClean requires BOTH of the following, not either alone
// (this is the fix for the second independent audit's CRITICAL finding:
// a report could previously claim VERIFIED CLEAN for an environment
// that was never cleaned at all, or whose tracked resources were simply
// never examined because a client was nil):
//  1. env.Status (the PERSISTED decision, already guarded at write time
//     by guardVerifiedCleanStatus/verifyAdditionalDomains) is itself
//     StatusVerifiedClean -- an active, pending, expired, or any other
//     non-terminal-success status can never produce SafetyVerifiedClean,
//     no matter what this report's own fresh checks find.
//  2. THIS report's own fresh re-check, this instant, of every domain
//     that has ANYTHING tracked for this environment -- regardless of
//     whether this particular Controller happens to have a Docker/Cloud
//     client configured -- found it independently verified absent. A
//     tracked resource in a domain with no configured client is NEVER
//     silently skipped: addUnverifiableDockerResourceReports/
//     addUnverifiableCloudResourceReports (below) load the same
//     docker-resources.json/cloud-resources.json tracking files
//     lifecycle.go's own guard reads, and report every tracked entry as
//     unverified (reusing the existing tracking records, never a
//     separately invented "is anything tracked" check). A missing or
//     corrupt tracking file is treated exactly like "yes, something is
//     tracked and unverifiable" -- fail closed, never fail open.
//
// cleanupErr is the error (if any) the cleanup attempt that produced
// env's current state returned -- included verbatim in the report's
// Errors, never used to change Safety on its own.
func (c Controller) GenerateCleanupReport(ctx context.Context, root string, env Environment, cleanupErr error) (CleanupReport, error) {
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now()
	}
	report := CleanupReport{
		EnvironmentID: env.ID,
		Owner:         env.Owner,
		GeneratedAt:   now,
		Outcome:       env.Status,
	}
	if cleanupErr != nil {
		report.Errors = append(report.Errors, cleanupErr.Error())
	}

	localPresent, localErr := localResourcesPresent(root, env.ID)
	if localErr != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("local resources check: %s", localErr))
	}
	report.Resources = append(report.Resources, ResourceReport{
		Domain:     "local",
		Kind:       "local_files",
		Ref:        env.ID + "/resources",
		Present:    localPresent,
		Verified:   localErr == nil,
		Tracked:    true,
		VerifiedAt: now,
		Error:      errString(localErr),
	})

	anyPresent := localErr == nil && localPresent
	anyUnverified := localErr != nil

	dockerPresent, dockerUnverified := c.collectDockerDomain(ctx, root, env, now, &report)
	anyPresent = anyPresent || dockerPresent
	anyUnverified = anyUnverified || dockerUnverified

	cloudPresent, cloudUnverified := c.collectCloudDomain(ctx, root, env, now, &report)
	anyPresent = anyPresent || cloudPresent
	anyUnverified = anyUnverified || cloudUnverified

	switch {
	case anyPresent || env.Status == StatusCleanupIncomplete || env.Status == StatusCleanupAttentionRequired:
		report.Safety = SafetyActionRequired
	case anyUnverified || env.Status != StatusVerifiedClean:
		report.Safety = SafetyUnverified
	default:
		report.Safety = SafetyVerifiedClean
	}
	return report, nil
}

// collectDockerDomain dispatches to the configured-client or
// no-client-at-all path for the Docker domain -- split out of
// GenerateCleanupReport purely to keep that function's own branching
// readable.
func (c Controller) collectDockerDomain(ctx context.Context, root string, env Environment, now time.Time, report *CleanupReport) (present, unverified bool) {
	if c.Docker != nil {
		return c.addDockerResourceReports(ctx, root, env, now, report)
	}
	return false, addUnverifiableDockerResourceReports(root, env, now, report)
}

// collectCloudDomain is collectDockerDomain's cloud equivalent.
func (c Controller) collectCloudDomain(ctx context.Context, root string, env Environment, now time.Time, report *CleanupReport) (present, unverified bool) {
	if c.Cloud != nil {
		return c.addCloudResourceReports(ctx, root, env, now, report)
	}
	return false, addUnverifiableCloudResourceReports(root, env, now, report)
}

// addUnverifiableDockerResourceReports runs when this report's
// Controller has no Docker client configured at all. It loads
// docker-resources.json directly -- the SAME tracking file
// TrackDockerResource/Reconcile/RemoveTrackedDockerResources already
// read and write (tracked.go) -- reusing that existing record rather
// than inventing a parallel one, and reports every tracked entry it
// finds as Verified: false (this report had no client to check it
// with), so a tracked resource can never be silently invisible merely
// because a client wasn't configured on this particular call. Fails
// closed on an unreadable/corrupt tracking file exactly like
// guardVerifiedCleanStatus (lifecycle.go) already does for the actual
// cleanup decision: a file that cannot be parsed is treated as "cannot
// confirm nothing is tracked," never as "nothing is tracked."
func addUnverifiableDockerResourceReports(root string, env Environment, now time.Time, report *CleanupReport) (unverified bool) {
	dir, err := environmentDir(root, env.ID)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("docker tracking check: %s", err))
		return true
	}
	tracked, err := loadTrackedDockerResources(dir)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("docker tracking metadata unreadable: %s", err))
		return true
	}
	for _, res := range tracked {
		report.Resources = append(report.Resources, ResourceReport{
			Domain: "docker", Kind: string(res.Kind), Ref: res.Ref,
			Verified: false, Tracked: true, VerifiedAt: now,
			Error: "no Docker client configured on this Controller -- tracked resource was never independently checked",
		})
	}
	return len(tracked) > 0
}

// addUnverifiableCloudResourceReports is
// addUnverifiableDockerResourceReports' cloud equivalent, reading
// cloud-resources.json (cloud_tracked.go) directly.
func addUnverifiableCloudResourceReports(root string, env Environment, now time.Time, report *CleanupReport) (unverified bool) {
	dir, err := environmentDir(root, env.ID)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("cloud tracking check: %s", err))
		return true
	}
	tracked, err := loadTrackedCloudResources(dir)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("cloud tracking metadata unreadable: %s", err))
		return true
	}
	for _, res := range tracked {
		report.Resources = append(report.Resources, ResourceReport{
			Domain: "cloud", Kind: string(res.Kind), Account: res.Account, Region: res.Region, Ref: res.Ref,
			Verified: false, Tracked: true, VerifiedAt: now,
			Error: "no Cloud client configured on this Controller -- tracked resource was never independently checked",
		})
	}
	return len(tracked) > 0
}

// addDockerResourceReports appends every Docker resource entry this
// report should carry and reports whether anything was found present
// or could not be verified -- factored out of GenerateCleanupReport
// purely to keep that function's branching readable; behavior is
// unchanged from having it inline.
func (c Controller) addDockerResourceReports(ctx context.Context, root string, env Environment, now time.Time, report *CleanupReport) (present, unverified bool) {
	report.DockerVerification = dockerVerificationMode(c.Docker)
	if report.DockerVerification != VerificationLive {
		report.Simulated = true
	}
	rec, err := Reconcile(ctx, c.Docker, root, env.ID)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("docker reconcile: %s", err))
		return false, true
	}
	for _, res := range rec.Present {
		report.Resources = append(report.Resources, ResourceReport{
			Domain: "docker", Kind: string(res.Kind), Ref: res.Ref,
			Present: true, Verified: rec.DockerAvailable, Tracked: isTrackedDocker(rec.Tracked, res), VerifiedAt: now,
		})
	}
	for _, res := range rec.Missing {
		report.Resources = append(report.Resources, ResourceReport{
			Domain: "docker", Kind: string(res.Kind), Ref: res.Ref,
			Present: false, Verified: rec.DockerAvailable, Tracked: true, VerifiedAt: now,
		})
	}
	return rec.DockerAvailable && len(rec.Present) > 0, !rec.DockerAvailable
}

// addCloudResourceReports is addDockerResourceReports' cloud
// equivalent, additionally attaching an AWS Console link to every
// resource entry it appends (consoleURLForCloudResource) -- a link is
// manual-navigation convenience only, see that function's doc comment;
// it is never itself evidence and never feeds back into Present/
// Verified/Safety above.
func (c Controller) addCloudResourceReports(ctx context.Context, root string, env Environment, now time.Time, report *CleanupReport) (present, unverified bool) {
	report.CloudVerification = cloudVerificationMode(c.Cloud)
	if report.CloudVerification != VerificationLive {
		report.Simulated = true
	}
	rec, err := CloudReconcile(ctx, c.Cloud, root, env.ID)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("cloud reconcile: %s", err))
		return false, true
	}
	for _, res := range rec.Present {
		report.Resources = append(report.Resources, ResourceReport{
			Domain: "cloud", Kind: string(res.Kind), Account: res.Account, Region: res.Region, Ref: res.Ref,
			Present: true, Verified: rec.CloudAvailable, Tracked: isTrackedCloud(rec.Tracked, res), VerifiedAt: now,
			ConsoleURL: consoleURLForCloudResource(res, true),
		})
	}
	for _, res := range rec.Missing {
		report.Resources = append(report.Resources, ResourceReport{
			Domain: "cloud", Kind: string(res.Kind), Account: res.Account, Region: res.Region, Ref: res.Ref,
			Present: false, Verified: rec.CloudAvailable, Tracked: true, VerifiedAt: now,
			ConsoleURL: consoleURLForCloudResource(res, false),
		})
	}
	return rec.CloudAvailable && len(rec.Present) > 0, !rec.CloudAvailable
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func isTrackedDocker(tracked []TrackedDockerResource, res TrackedDockerResource) bool {
	for _, t := range tracked {
		if t == res {
			return true
		}
	}
	return false
}

func isTrackedCloud(tracked []CloudResourceRef, ref CloudResourceRef) bool {
	for _, t := range tracked {
		if t == ref {
			return true
		}
	}
	return false
}

// localResourcesPresent reports whether env's local resources/
// subdirectory still exists -- the same, read-only Lstat check
// Manager.removeOwnedResources already performs, repeated here rather
// than refactored into a shared helper so this package's existing,
// already-tested cleanup path is not touched by this additive reporting
// feature at all.
func localResourcesPresent(root, id string) (bool, error) {
	dir, err := environmentDir(root, id)
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(filepath.Join(dir, "resources"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func cleanupReportsPath(dir string) string { return filepath.Join(dir, "cleanup-reports.json") }

// PersistCleanupReport durably appends report to this environment's
// cleanup-reports.json -- a sibling of environment.json, never inside
// the disposable resources/ subdirectory, with the exact same
// durability properties as cloud-resources.json/docker-resources.json.
// Every report ever generated for this environment is retained (never
// overwritten), so a later reader can see this environment's full
// cleanup/failure history, not just its latest attempt. The whole
// read-modify-write runs under withEnvironmentLock, for the same
// lost-update reason TrackCloudResource/TrackDockerResource already
// document.
func PersistCleanupReport(root string, report CleanupReport) error {
	if !safeID.MatchString(report.EnvironmentID) {
		return errors.New("invalid environment ID")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dir := filepath.Join(rootPath, report.EnvironmentID)
	return withEnvironmentLock(dir, func() error {
		existing, err := loadCleanupReports(dir)
		if err != nil {
			return err
		}
		existing = append(existing, report)
		return writeCleanupReports(dir, existing)
	})
}

// LoadCleanupReports returns every CleanupReport ever persisted for id,
// oldest first -- this environment's full, durable cleanup/failure
// history. Read-only; never modifies anything.
func LoadCleanupReports(root, id string) ([]CleanupReport, error) {
	dir, err := environmentDir(root, id)
	if err != nil {
		return nil, err
	}
	return loadCleanupReports(dir)
}

func loadCleanupReports(dir string) ([]CleanupReport, error) {
	data, err := os.ReadFile(cleanupReportsPath(dir)) //nolint:gosec // G304: dir is built from a regex-validated environment ID under an operator-supplied root, not arbitrary input
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cleanup reports: %w", err)
	}
	var reports []CleanupReport
	if err := json.Unmarshal(data, &reports); err != nil {
		return nil, fmt.Errorf("parse cleanup reports: %w", err)
	}
	return reports, nil
}

func writeCleanupReports(dir string, reports []CleanupReport) error {
	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(cleanupReportsPath(dir), append(data, '\n'))
}
