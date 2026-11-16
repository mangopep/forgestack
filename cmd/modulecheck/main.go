// Command modulecheck resolves selected ForgeStack module manifests.
package main

import (
	"fmt"
	"os"

	"github.com/mangopep/forgestack/internal/modulecontract"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "resolve" {
		fmt.Fprintln(os.Stderr, "usage: modulecheck resolve <manifest.json> [manifest.json ...]")
		os.Exit(2)
	}
	manifests := make([]modulecontract.Manifest, 0, len(os.Args)-2)
	for _, path := range os.Args[2:] {
		m, err := modulecontract.Read(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "modulecheck:", err)
			os.Exit(1)
		}
		manifests = append(manifests, m)
	}
	composition, err := modulecontract.Resolve(manifests)
	if err != nil {
		fmt.Fprintln(os.Stderr, "modulecheck:", err)
		os.Exit(1)
	}
	if err := composition.Encode(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "modulecheck:", err)
		os.Exit(1)
	}
}
