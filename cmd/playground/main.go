// Command playground creates and cleans up isolated local ForgeStack environments.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

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
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "playground:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: playground create|cleanup [flags]")
	fmt.Fprintln(os.Stderr, "create flags: --root .forgestack/environments --owner <name> --mode playground|product --ttl 4h --modules go-api,docker-compose")
	fmt.Fprintln(os.Stderr, "cleanup flags: --root .forgestack/environments --owner <name> --id env-<16 hex>")
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
