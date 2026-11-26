// Command playground creates and cleans up isolated local ForgeStack environments.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mangopep/forgestack/internal/platform/telemetry"
	"github.com/mangopep/forgestack/internal/playground"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "create":
		err = create(os.Args[2:])
	case "cleanup":
		err = cleanup(os.Args[2:])
	case "track-docker":
		err = trackDocker(os.Args[2:])
	case "track-cloud":
		err = trackCloud(os.Args[2:])
	case "reconcile":
		err = reconcile(os.Args[2:])
	case "expire-due":
		err = expireDue(os.Args[2:])
	case "cleanup-due":
		err = cleanupDue(os.Args[2:])
	case "worker":
		err = worker(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "playground:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: playground create|cleanup|track-docker|track-cloud|reconcile|expire-due|cleanup-due|worker [flags]")
	fmt.Fprintln(os.Stderr, "create flags: --root .forgestack/environments --owner <name> --mode playground|product --ttl 4h --modules go-api,docker-compose")
	fmt.Fprintln(os.Stderr, "cleanup flags: --root .forgestack/environments --owner <name> --id env-<16 hex>")
	fmt.Fprintln(os.Stderr, "track-docker flags: --root .forgestack/environments --owner <name> --id env-<16 hex> --kind container|volume|network --ref <id-or-name>")
	fmt.Fprintln(os.Stderr, "track-cloud flags: --root .forgestack/environments --owner <name> --id env-<16 hex> --cloud-provider s3 --cloud-account <id> --cloud-region <region> --ref <bucket-name>")
	fmt.Fprintln(os.Stderr, "reconcile flags: --root .forgestack/environments --id env-<16 hex> (read-only; reports drift, never removes anything)")
	fmt.Fprintln(os.Stderr, "expire-due flags: --root .forgestack/environments (transitions due playgrounds to EXPIRED; never removes anything)")
	fmt.Fprintln(os.Stderr, "cleanup-due flags: --root .forgestack/environments [--docker] [--cloud-provider s3 --cloud-mode sim --cloud-account <id> --cloud-region <region>] "+
		"(expires + cleans up every due environment; local filesystem, and -- with --docker/--cloud-provider -- tracked Docker/cloud resources. "+
		"Cloud is opt-in and simulated-only in this build: --cloud-mode defaults to sim and fails closed if set to live, before any AWS credential or network access.)")
	fmt.Fprintln(os.Stderr, "worker flags: --root .forgestack/environments --interval 5m [--docker] [--cloud-provider s3 --cloud-mode sim --cloud-account <id> --cloud-region <region>] [--metrics-addr :9469] "+
		"(runs cleanup-due on a timer until SIGINT/SIGTERM; opt-in, not started by any other command; metrics-addr opt-in, disabled by default)")
	fmt.Fprintf(os.Stderr, "known cloud providers: %s\n", strings.Join(playground.KnownCloudProviders(), ", "))
	os.Exit(2)
}

func create(args []string) error {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	root := flags.String("root", ".forgestack/environments", "local environment directory")
	owner := flags.String("owner", currentOwner(), "environment owner")
	mode := flags.String("mode", "playground", "playground or persistent product")
	ttl := flags.Duration("ttl", 0, "maximum lifetime for playground mode; defaults to 4h; product requires 0")
	modules := flags.String("modules", "go-api", "comma-separated selected module names")
	if err := flags.Parse(args); err != nil {
		return err
	}
	parsedModules := split(*modules)
	if *mode == "playground" && *ttl == 0 {
		*ttl = 4 * time.Hour
	}
	env, err := playground.NewManager().Create(*root, *owner, *mode, *ttl, parsedModules)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(env)
}

func cleanup(args []string) error {
	flags := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	root := flags.String("root", ".forgestack/environments", "local environment directory")
	owner := flags.String("owner", currentOwner(), "must match the environment owner")
	id := flags.String("id", "", "environment ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	result, err := playground.NewManager().Cleanup(*root, *id, *owner)
	if result.Cleanup != "" {
		_ = json.NewEncoder(os.Stdout).Encode(result)
	}
	return err
}

func trackDocker(args []string) error {
	flags := flag.NewFlagSet("track-docker", flag.ContinueOnError)
	root := flags.String("root", ".forgestack/environments", "local environment directory")
	owner := flags.String("owner", currentOwner(), "must match the environment owner")
	id := flags.String("id", "", "environment ID")
	kind := flags.String("kind", "", "container|volume|network")
	ref := flags.String("ref", "", "the Docker container ID, volume name, or network name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	k, err := parseDockerKind(*kind)
	if err != nil {
		return err
	}
	return playground.NewManager().TrackDockerResource(*root, *id, *owner, playground.TrackedDockerResource{Kind: k, Ref: *ref})
}

func trackCloud(args []string) error {
	flags := flag.NewFlagSet("track-cloud", flag.ContinueOnError)
	root := flags.String("root", ".forgestack/environments", "local environment directory")
	owner := flags.String("owner", currentOwner(), "must match the environment owner")
	id := flags.String("id", "", "environment ID")
	provider := flags.String("cloud-provider", "s3", "cloud provider this resource belongs to (see known providers in usage)")
	account := flags.String("cloud-account", "", "the resource's cloud account ID")
	region := flags.String("cloud-region", "", "the resource's cloud region")
	ref := flags.String("ref", "", "the resource's own identifier (for s3, the bucket name)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	kind, err := parseCloudProviderKind(*provider)
	if err != nil {
		return err
	}
	return playground.NewManager().TrackCloudResource(*root, *id, *owner, playground.CloudResourceRef{
		Kind:    kind,
		Account: *account,
		Region:  *region,
		Ref:     *ref,
	})
}

func reconcile(args []string) error {
	flags := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	root := flags.String("root", ".forgestack/environments", "local environment directory")
	id := flags.String("id", "", "environment ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	rec, err := playground.Reconcile(context.Background(), playground.NewDockerClient(), *root, *id)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(rec)
}

func expireDue(args []string) error {
	flags := flag.NewFlagSet("expire-due", flag.ContinueOnError)
	root := flags.String("root", ".forgestack/environments", "local environment directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	expired, err := playground.NewController().ExpireDue(*root)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(expired)
}

func cleanupDue(args []string) error {
	flags := flag.NewFlagSet("cleanup-due", flag.ContinueOnError)
	root := flags.String("root", ".forgestack/environments", "local environment directory")
	useDocker := flags.Bool("docker", false, "also remove and independently verify each environment's tracked Docker resources")
	cloudFlags := registerCloudFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	controller := playground.NewController()
	if *useDocker {
		controller = controller.WithDocker(playground.NewDockerClient())
	}
	controller, err := cloudFlags.apply(*root, controller, func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) })
	if err != nil {
		return err
	}
	if _, err := controller.ExpireDue(*root); err != nil {
		return err
	}
	results, err := controller.CleanupDue(context.Background(), *root)
	if err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(results)
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(results)
}

// cloudFlags holds one subcommand's --cloud-provider/--cloud-mode/
// --cloud-account/--cloud-region/--cloud-retry-backoff flag values.
// Registered identically by cleanupDue and worker (registerCloudFlags)
// so the two subcommands can never drift apart on flag names/defaults/
// help text.
type cloudFlags struct {
	provider     *string
	mode         *string
	account      *string
	region       *string
	retryBackoff *time.Duration
}

func registerCloudFlags(flags *flag.FlagSet) cloudFlags {
	return cloudFlags{
		provider:     flags.String("cloud-provider", "", "if set (e.g. s3), also remove and independently verify each environment's tracked cloud resources for this provider; omitted by default -- no cloud component is constructed and behavior is identical to before this flag existed"),
		mode:         flags.String("cloud-mode", string(playground.CloudModeSimulated), "cloud mode: sim (in-memory fake, zero AWS calls) or live (not available in this build -- fails closed before any credential or network access)"),
		account:      flags.String("cloud-account", "", "required with --cloud-provider: the cloud account this client is scoped to"),
		region:       flags.String("cloud-region", "", "required with --cloud-provider: the cloud region this client is scoped to"),
		retryBackoff: flags.Duration("cloud-retry-backoff", time.Minute, "minimum time between two real cloud removal attempts for the same environment (safety backoff against hammering a rate-limited API); only consulted when --cloud-provider is set; 0 disables the gate entirely"),
	}
}

// apply constructs and wires a CloudClient onto controller if
// --cloud-provider was set, logging its (always explicitly labeled)
// description via logf first. Returns controller unchanged, with no
// CloudClient and no provider/registry code ever invoked, when
// --cloud-provider was left at its default "" -- the no-cloud-options
// case this whole mechanism must never disturb. The returned client is
// already fully set up (seeded, for a simulated provider) by
// playground.NewCloudClient itself; this function never touches a
// provider-specific type.
func (f cloudFlags) apply(root string, controller playground.Controller, logf func(string, ...any)) (playground.Controller, error) {
	if *f.provider == "" {
		return controller, nil
	}
	client, label, err := playground.NewCloudClient(*f.provider, playground.CloudProviderMode(*f.mode), *f.account, *f.region, root)
	if err != nil {
		return controller, fmt.Errorf("cloud provider: %w", err)
	}
	logf("playground: cloud provider -- %s", label)
	controller = controller.WithCloud(client)
	controller.CloudRetryBackoff = *f.retryBackoff
	return controller, nil
}

func worker(args []string) error {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	root := flags.String("root", ".forgestack/environments", "local environment directory")
	interval := flags.Duration("interval", 5*time.Minute, "how often to run expire-due + cleanup-due")
	useDocker := flags.Bool("docker", false, "also remove and independently verify tracked Docker resources each pass")
	cloudFlagSet := registerCloudFlags(flags)
	metricsAddr := flags.String("metrics-addr", "", "if set (e.g. :9469), serve Prometheus metrics for cleanup observability on this address; also enables OTel tracing via OTEL_EXPORTER_OTLP_ENDPOINT if set. Disabled by default -- no behavior change, no listener, no telemetry overhead unless explicitly opted into.")
	if err := flags.Parse(args); err != nil {
		return err
	}

	w := playground.NewWorker(*root, *interval)
	if *useDocker {
		w = w.WithDocker(playground.NewDockerClient())
	}
	w.Logf = func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	controller, err := cloudFlagSet.apply(*root, w.Controller, w.Logf)
	if err != nil {
		return err
	}
	w.Controller = controller

	if *metricsAddr != "" {
		// Reuses internal/platform/telemetry exactly like cmd/api does --
		// no new monitoring stack, no new dependency. Setup with no
		// OTEL_EXPORTER_OTLP_ENDPOINT set (the common case) still
		// succeeds and simply never exports a span anywhere; it never
		// blocks or errors cleanup itself.
		setupCtx, cancelSetup := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownTelemetry, err := telemetry.SetupFromEnv(setupCtx, "forgestack-playground-worker", "dev")
		cancelSetup()
		if err != nil {
			return fmt.Errorf("telemetry setup: %w", err)
		}
		defer func() {
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelShutdown()
			if err := shutdownTelemetry(shutdownCtx); err != nil {
				slog.Warn("telemetry shutdown", "error", err)
			}
		}()

		reg := prometheus.NewRegistry()
		w.Controller.Metrics = playground.NewMetrics(reg)
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
		server := &http.Server{Addr: *metricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics server", "error", err)
			}
		}()
		defer func() {
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelShutdown()
			_ = server.Shutdown(shutdownCtx)
		}()
		fmt.Fprintf(os.Stderr, "playground worker: serving Prometheus metrics on %s/metrics\n", *metricsAddr)
	}

	// SIGINT (Ctrl-C) and SIGTERM (what `kill` sends by default, and what
	// a service manager sends to ask a process to stop) both cancel ctx;
	// Worker.Run treats that as graceful shutdown, finishing its current
	// pass and returning nil rather than being killed mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "playground worker: starting against %s, interval=%s, docker=%v (stop with Ctrl-C or SIGTERM)\n", *root, *interval, *useDocker)
	return w.Run(ctx)
}

func parseDockerKind(s string) (playground.DockerResourceKind, error) {
	switch s {
	case "container":
		return playground.DockerContainer, nil
	case "volume":
		return playground.DockerVolume, nil
	case "network":
		return playground.DockerNetwork, nil
	default:
		return "", fmt.Errorf("unknown --kind %q (want container, volume, or network)", s)
	}
}

// parseCloudProviderKind maps a --cloud-provider name to the
// CloudResourceKind it tracks. This is a separate choke point from
// cloudProviderRegistry (internal/playground/cloud_provider.go), which
// maps the same name to a CloudClient *implementation* -- deliberately:
// a provider name can exist in the registry with no CloudResourceKind
// yet defined, or vice versa, without implying the other is ready. Add
// one case here, matching ValidateCloudResourceKind's own single choke
// point (cloud.go), the day a second cloud resource kind ships.
func parseCloudProviderKind(provider string) (playground.CloudResourceKind, error) {
	switch provider {
	case "s3":
		return playground.CloudS3Bucket, nil
	default:
		return "", fmt.Errorf("unknown --cloud-provider %q (known providers: %s)", provider, strings.Join(playground.KnownCloudProviders(), ", "))
	}
}

func currentOwner() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}
func split(value string) []string {
	var out []string
	for _, s := range strings.Split(value, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
