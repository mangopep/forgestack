package playground

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// CloudProviderMode selects what kind of CloudClient a provider factory
// builds: CloudModeSimulated is the only mode that actually returns a
// client in this increment; CloudModeLive is accepted as a value so the
// CLI surface and this registry already have a place for it, but every
// registered factory must reject it (see newS3CloudProvider) before
// doing anything else -- no credential resolution, no network call, no
// AWS SDK config. Building a real adapter remains a separately approved
// increment (see docs/architecture/CLOUD-CLEANUP-S3.md's "Live AWS
// validation plan").
type CloudProviderMode string

const (
	CloudModeSimulated CloudProviderMode = "sim"
	CloudModeLive      CloudProviderMode = "live"
)

// cloudProviderFactory builds a CloudClient for one named provider (e.g.
// "s3") and mode, scoped to account+region, fully ready to use --
// including, for a simulated provider, already seeded from whatever is
// tracked under root -- and returns a human-readable label describing
// exactly what was built, for callers (the CLI) to print, never to
// assert LIVE/SIMULATED itself. That assertion is made independently by
// cloudVerificationMode (cleanup_report.go) from the returned
// CloudClient's own concrete type, never from this label or from the
// mode string the caller requested; a factory that someday wants a
// CleanupReport to ever read VerificationLive must return a client
// implementing LiveCloudClient truthfully, not just say "live" in its
// label. root is passed through purely so a simulated provider can seed
// itself from what is already tracked there (see
// seedSimulatedS3Fixtures) -- a live provider has no use for it, exactly
// like it has no use for account/region before it fails closed.
type cloudProviderFactory func(mode CloudProviderMode, account, region, root string) (CloudClient, string, error)

// cloudProviderRegistry is the one place a new AWS (or other cloud)
// service's CLI support gets added. Registering "ec2" or any other
// service later means writing one more newXxxCloudProvider function with
// this same signature and adding one more entry here -- nothing in
// cmd/playground's flag parsing, dispatch, or help text needs to change,
// and nothing outside this file needs to know a new provider exists.
var cloudProviderRegistry = map[string]cloudProviderFactory{
	"s3": newS3CloudProvider,
}

// KnownCloudProviders returns the registered provider names, sorted,
// for CLI help/usage text -- so that text never drifts out of sync with
// cloudProviderRegistry's actual contents.
func KnownCloudProviders() []string {
	names := make([]string, 0, len(cloudProviderRegistry))
	for name := range cloudProviderRegistry {
		names = append(names, name)
	}
	// Small, fixed set; a simple insertion sort keeps this dependency-free.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	return names
}

// NewCloudClient resolves provider+mode+account+region+root to a
// ready-to-use CloudClient via cloudProviderRegistry, the single choke
// point cmd/playground's --cloud-provider/--cloud-mode flags go through.
// An unrecognized provider or mode is rejected here, before any
// provider-specific code runs at all. The returned client needs no
// further setup from the caller -- cmd/playground never touches a
// provider-specific type (e.g. *FakeCloudClient) or seeding function
// directly, which is what lets a second provider be added later without
// any change to cmd/playground.
func NewCloudClient(provider string, mode CloudProviderMode, account, region, root string) (CloudClient, string, error) {
	factory, ok := cloudProviderRegistry[provider]
	if !ok {
		return nil, "", fmt.Errorf("unknown cloud provider %q (known providers: %s)", provider, strings.Join(KnownCloudProviders(), ", "))
	}
	return factory(mode, account, region, root)
}

// errLiveCloudModeNotAvailable is returned by every registered provider
// factory for CloudModeLive, in this increment, without exception. It is
// returned as the very first thing newS3CloudProvider's live branch
// does -- before even checking account/region -- so that choosing live
// mode can never reach, let alone depend on, any credential-resolution
// or network code path. This file does not import
// "github.com/aws/aws-sdk-go-v2/config" or
// ".../aws-sdk-go-v2/credentials", and never will while this error is
// the live branch's only statement: there is nothing here to call.
var errLiveCloudModeNotAvailable = errors.New(
	"live AWS cloud mode is not available in this build: no credentials are resolved and no network access is attempted; " +
		"use --cloud-mode=sim, or build and wire a separately approved live adapter per docs/architecture/CLOUD-CLEANUP-S3.md",
)

// newS3CloudProvider is cloudProviderRegistry's "s3" entry.
func newS3CloudProvider(mode CloudProviderMode, account, region, root string) (CloudClient, string, error) {
	if mode == CloudModeLive {
		return nil, "", errLiveCloudModeNotAvailable
	}
	if mode != CloudModeSimulated {
		return nil, "", fmt.Errorf("unknown cloud mode %q for provider %q (want %q or %q)", mode, "s3", CloudModeSimulated, CloudModeLive)
	}
	if strings.TrimSpace(account) == "" || strings.TrimSpace(region) == "" {
		return nil, "", errors.New("cloud provider \"s3\" requires both --cloud-account and --cloud-region")
	}
	fc := NewFakeCloudClient()
	if err := seedSimulatedS3Fixtures(fc, root, account, region); err != nil {
		return nil, "", fmt.Errorf("seeding simulated s3 fixtures: %w", err)
	}
	client := &scopedSimulatedCloudClient{inner: fc, account: account, region: region}
	return client, fmt.Sprintf(
		"SIMULATED s3 cloud client (account=%s region=%s) -- in-memory fake only: zero AWS API calls, zero credentials resolved, zero network access",
		account, region,
	), nil
}

// errSimulatedScopeMismatch is scopedSimulatedCloudClient's equivalent
// of the real S3Adapter's errS3ScopeMismatch (cloud_s3_adapter.go): a
// ref whose Account/Region does not match this simulated client's own
// configured scope must be refused with an error, exactly like a real
// cross-account/region call would be (AWS returns AccessDenied, or this
// adapter's own checkScope refuses it before ever calling AWS) -- never
// silently treated as "confirmed absent," which is what plain
// FakeCloudClient would otherwise report for a ref it was never seeded
// with (see seedSimulatedS3Fixtures, which deliberately never seeds an
// out-of-scope ref).
var errSimulatedScopeMismatch = errors.New("simulated cloud client: ref is outside this client's configured account/region scope")

// scopedSimulatedCloudClient wraps a *FakeCloudClient with the one piece
// of real-adapter behavior FakeCloudClient does not model on its own:
// refusing a ref outside this client's own account+region, the same
// contract CloudClient's own doc comment describes ("the client itself
// is scoped to exactly one account+region; it never spans multiple") and
// the real S3Adapter.checkScope enforces. ListByTag needs no override --
// it only ever returns refs seedSimulatedS3Fixtures already scoped to
// this client -- but VerifyOwnership/Exists/Remove can be called
// directly with any ref a caller (ultimately, whatever is tracked in
// cloud-resources.json) supplies, including one belonging to a different
// account/region by operator error, and must reject it precisely as a
// real credentials-scoped client would.
type scopedSimulatedCloudClient struct {
	inner           *FakeCloudClient
	account, region string
}

func (c *scopedSimulatedCloudClient) checkScope(ref CloudResourceRef) error {
	if ref.Account != c.account || ref.Region != c.region {
		return fmt.Errorf("%w (ref account=%q region=%q, client account=%q region=%q)",
			errSimulatedScopeMismatch, ref.Account, ref.Region, c.account, c.region)
	}
	return nil
}

func (c *scopedSimulatedCloudClient) ListByTag(ctx context.Context, kind CloudResourceKind, tagKey, tagValue string) ([]CloudResourceRef, error) {
	return c.inner.ListByTag(ctx, kind, tagKey, tagValue)
}

func (c *scopedSimulatedCloudClient) VerifyOwnership(ctx context.Context, ref CloudResourceRef, requiredTags map[string]string) (bool, error) {
	if err := c.checkScope(ref); err != nil {
		return false, err
	}
	return c.inner.VerifyOwnership(ctx, ref, requiredTags)
}

func (c *scopedSimulatedCloudClient) Exists(ctx context.Context, ref CloudResourceRef) (bool, error) {
	if err := c.checkScope(ref); err != nil {
		return false, err
	}
	return c.inner.Exists(ctx, ref)
}

func (c *scopedSimulatedCloudClient) Remove(ctx context.Context, ref CloudResourceRef) error {
	if err := c.checkScope(ref); err != nil {
		return err
	}
	return c.inner.Remove(ctx, ref)
}

// cloudFixtureMissingTagSuffix and cloudFixtureFlakyPrefix are the two
// reserved bucket-name (CloudResourceRef.Ref) conventions
// seedSimulatedS3Fixtures recognizes. They are deliberately encoded in
// the bucket name, not a separate flag: every fixture scenario a CLI
// integration test needs must be selectable purely by what is tracked
// (via `playground track-cloud`), with no additional simulation-only
// flag surface for a real deployment to ever stumble into by accident.
const (
	cloudFixtureMissingTagSuffix = "-missing-tag"
	cloudFixtureFlakyPrefix      = "-flaky-"
)

// seedSimulatedS3Fixtures seeds fc with one deterministic fixture bucket
// for every environment under root whose tracked cloud resources
// (cloud-resources.json, written by `track-cloud`) include an S3 bucket
// ref scoped to exactly (account, region) -- the same account+region the
// simulated client itself was built for (see newS3CloudProvider). A
// tracked ref scoped to a DIFFERENT account or region is deliberately
// left unseeded here too: combined with scopedSimulatedCloudClient's own
// checkScope (which actively refuses such a ref, rather than relying on
// it simply being absent from fc's map), a wrong-account/region tracked
// resource is rejected with an error -- never silently confused with
// "confirmed absent," and never with one actually in scope.
//
// Fixture behavior is selected purely from the tracked ref's own bucket
// name (Ref) plus, for the "-flaky-" case, the already-persisted
// cloud-cleanup-state.json attempt count -- never randomness, never the
// wall clock -- so seeding the same root twice (e.g. across two separate
// one-shot `cleanup-due` process invocations) reproduces the same
// fixture, letting a flaky bucket converge to success across restarts
// exactly as a real transient-then-recovered failure would:
//   - a Ref with no recognized suffix: a clean, correctly-tagged, empty
//     bucket -- cleans up successfully on the very first attempt.
//   - a Ref ending in "-missing-tag": seeded WITHOUT
//     CloudTagManagedBy, so VerifyOwnership never matches -- simulating
//     an out-of-band tag change/drift this package must never clean up
//     through.
//   - a Ref matching "-flaky-N" (e.g. "my-bucket-flaky-2"): Remove fails
//     with ErrCloudThrottled for as many of THIS call's attempts as are
//     still owed, computed as N minus the attempts cloud-cleanup-
//     state.json already recorded for this environment before this
//     call -- so it fails on the first N invocations/passes and
//     succeeds from the (N+1)th onward, whether those N+1 attempts
//     happen within one long-running `worker` process or across N+1
//     separate `cleanup-due` process restarts.
//
// RemoveFailuresRemaining is one counter shared by the whole
// FakeCloudClient (see cloud_fake.go), not scoped per bucket -- an
// existing property of FakeCloudClient this function does not change.
// Seeding more than one "-flaky-" bucket into the same fc in the same
// call will cross-contaminate their failure counts; callers that need
// independent flaky behavior for multiple environments should use
// separate roots (or separate FakeCloudClients), not rely on this
// function to isolate them.
func seedSimulatedS3Fixtures(fc *FakeCloudClient, root, account, region string) error {
	envs, err := ListEnvironments(root)
	if err != nil {
		return err
	}
	for _, env := range envs {
		dir, err := environmentDir(root, env.ID)
		if err != nil {
			return err
		}
		refs, err := loadTrackedCloudResources(dir)
		if err != nil {
			return err
		}
		state, err := loadCloudCleanupAttemptState(dir)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref.Kind != CloudS3Bucket || ref.Account != account || ref.Region != region {
				continue
			}
			seedOneSimulatedS3Fixture(fc, env, ref, state.Attempts)
		}
	}
	return nil
}

func seedOneSimulatedS3Fixture(fc *FakeCloudClient, env Environment, ref CloudResourceRef, priorAttempts int) {
	tags := map[string]string{
		CloudTagEnvironmentID: env.ID,
		CloudTagOwner:         env.Owner,
		CloudTagManagedBy:     CloudManagedByValue,
	}
	if strings.HasSuffix(ref.Ref, cloudFixtureMissingTagSuffix) {
		delete(tags, CloudTagManagedBy)
	}
	fc.SeedBucket(ref, tags, 0, 0, 0)

	if idx := strings.LastIndex(ref.Ref, cloudFixtureFlakyPrefix); idx >= 0 {
		var wantFailures int
		if _, err := fmt.Sscanf(ref.Ref[idx+len(cloudFixtureFlakyPrefix):], "%d", &wantFailures); err == nil {
			if remaining := wantFailures - priorAttempts; remaining > 0 {
				fc.RemoveFailuresRemaining += remaining
			}
		}
	}
}
