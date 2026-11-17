package playground

import "context"

// fakeDockerResource is one entry in a FakeDockerClient's in-memory state.
type fakeDockerResource struct {
	kind   DockerResourceKind
	ref    string
	labels map[string]string
}

// FakeDockerClient is a deterministic, in-memory DockerClient test double.
// This package's own tests use it exclusively -- no test in this
// repository talks to a real Docker daemon, per "use deterministic test
// doubles" (see also TestDockerClientLiveSmokeTest, the one exception,
// which is skipped outright whenever a real daemon isn't reachable).
type FakeDockerClient struct {
	resources []fakeDockerResource
	// RemoveErr, if set, is returned by Remove for every call instead of
	// actually removing anything -- for simulating a failed/partial
	// cleanup deterministically.
	RemoveErr error
}

// NewFakeDockerClient returns an empty FakeDockerClient.
func NewFakeDockerClient() *FakeDockerClient { return &FakeDockerClient{} }

// Seed adds a resource to the fake's state, as if `docker run --label
// k=v ...` (or the volume/network equivalent) had already created it.
// Tests use this to set up both resources this package should find and
// remove, and resources belonging to a *different* label value that must
// never be touched.
func (f *FakeDockerClient) Seed(kind DockerResourceKind, ref string, labels map[string]string) {
	f.resources = append(f.resources, fakeDockerResource{kind: kind, ref: ref, labels: labels})
}

// Has reports whether ref is still present, for test assertions.
func (f *FakeDockerClient) Has(ref string) bool {
	for _, r := range f.resources {
		if r.ref == ref {
			return true
		}
	}
	return false
}

func (f *FakeDockerClient) ListByLabel(_ context.Context, kind DockerResourceKind, labelKey, labelValue string) ([]string, error) {
	var refs []string
	for _, r := range f.resources {
		if r.kind == kind && r.labels[labelKey] == labelValue {
			refs = append(refs, r.ref)
		}
	}
	return refs, nil
}

func (f *FakeDockerClient) Remove(_ context.Context, kind DockerResourceKind, ref string) error {
	if f.RemoveErr != nil {
		return f.RemoveErr
	}
	for i, r := range f.resources {
		if r.kind == kind && r.ref == ref {
			f.resources = append(f.resources[:i], f.resources[i+1:]...)
			return nil
		}
	}
	return nil // removing an already-absent resource is not an error, matching the real docker CLI's own idempotent-enough behavior for this package's purposes
}
