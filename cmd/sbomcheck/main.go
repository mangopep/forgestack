// Command sbomcheck validates a CycloneDX SBOM against the NTIA/CISA
// minimum-elements baseline, and can augment one with the producer and
// commit/repository metadata syft doesn't set on its own. See
// internal/sbomcheck for what's actually checked and why.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mangopep/forgestack/internal/sbomcheck"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "validate":
		runValidate(os.Args[2:])
	case "augment":
		runAugment(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  sbomcheck validate <sbom.json>")
	fmt.Fprintln(os.Stderr, "  sbomcheck augment --producer <name> --repository <repo> --commit <sha> <in.json> <out.json>")
}

func runValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	jsonOut := fs.String("json-out", "", "optional path to also write the validation report as JSON")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	path := fs.Arg(0)

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is this CLI's own operator-supplied argument
	if err != nil {
		fmt.Fprintln(os.Stderr, "read SBOM:", err)
		os.Exit(1)
	}
	doc, err := sbomcheck.Parse(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse SBOM:", err)
		os.Exit(1)
	}
	report := sbomcheck.Validate(path, doc)

	fmt.Printf("%s: %d components\n", path, report.ComponentCount)
	if len(report.MissingRequired) > 0 {
		fmt.Println("MISSING REQUIRED ELEMENTS:")
		for _, e := range report.MissingRequired {
			fmt.Println("  -", e)
		}
	}
	for el, cov := range report.CoverageGaps {
		fmt.Printf("coverage: %s = %d/%d\n", el, cov.Present, cov.Total)
	}

	if *jsonOut != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "marshal report:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*jsonOut, data, 0o600); err != nil { //nolint:gosec // G703: path is this CLI's own operator-supplied flag, not untrusted input
			fmt.Fprintln(os.Stderr, "write report:", err)
			os.Exit(1)
		}
	}

	if !report.Pass {
		fmt.Println("FAIL: one or more required minimum elements missing")
		os.Exit(1)
	}
	fmt.Println("PASS: all required minimum elements present (see coverage gaps above for elements that are present-where-available, not 100%)")
}

func runAugment(args []string) {
	fs := flag.NewFlagSet("augment", flag.ExitOnError)
	producer := fs.String("producer", "ForgeStack CI", "producer/author name to record if the SBOM has none")
	repository := fs.String("repository", "", "repository to record as a metadata property")
	commit := fs.String("commit", "", "commit SHA to record as a metadata property")
	_ = fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
		os.Exit(2)
	}
	inPath, outPath := fs.Arg(0), fs.Arg(1)

	data, err := os.ReadFile(inPath) //nolint:gosec // G304: path is this CLI's own operator-supplied argument
	if err != nil {
		fmt.Fprintln(os.Stderr, "read SBOM:", err)
		os.Exit(1)
	}
	augmented, err := sbomcheck.Augment(data, *producer, *repository, *commit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "augment SBOM:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, augmented, 0o600); err != nil { //nolint:gosec // G703: path is this CLI's own operator-supplied argument, not untrusted input
		fmt.Fprintln(os.Stderr, "write SBOM:", err)
		os.Exit(1)
	}
	fmt.Printf("Augmented SBOM written to %s\n", outPath)
}
