// Command ez-am is the single entry point of the project: it runs a read-only
// doctor, repairs what it can with the user's consent, and only then starts the
// media downloader.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"ez-am/internal/control"
	"ez-am/internal/doctor"
	"ez-am/internal/media"
	"ez-am/internal/platform/execrunner"
)

// splitArgs separates the subcommand from the flags so that `ez-am doctor
// --json` behaves like `ez-am --json doctor`.
func splitArgs(args []string) (positional []string, flagArgs []string) {
	valueFlags := map[string]bool{
		"--distro": true, "-distro": true,
		"--base": true, "-base": true,
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") || !valueFlags[arg] {
			continue
		}
		if index+1 < len(args) {
			index++
			flagArgs = append(flagArgs, args[index])
		}
	}
	return positional, flagArgs
}

const usage = `ez-am — Apple Music lossless downloader for WSL2 + Docker

Usage:
  ez-am                 check the environment, fix what is missing, then download
  ez-am doctor [--json] run the checks and print the report (exit 2 when not ready)
  ez-am download        go straight to the downloader (skips the checks)

Flags:
  --distro <name>       WSL distribution to use (default: auto-detect)
  --base <path>         runtime directory inside the distribution
                        (default: an existing $HOME/amd, otherwise XDG data)
  -h, --help            show this help
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("ez-am", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	distro := flags.String("distro", "", "WSL distribution to use")
	base := flags.String("base", "", "runtime directory inside the distribution")
	asJSON := flags.Bool("json", false, "print the doctor report as JSON (doctor only)")

	positional, flagArgs := splitArgs(args)
	if err := flags.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	command := "start"
	if len(positional) > 0 {
		command = positional[0]
		positional = positional[1:]
	}
	if len(positional) > 0 {
		fmt.Fprintf(os.Stderr, "ez-am: unexpected argument %q\n\n%s", positional[0], usage)
		return 2
	}

	runner := execrunner.New()
	ctx := context.Background()

	switch command {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	case "doctor":
		report := doctor.Run(ctx, doctor.Inputs{Runner: runner, Distro: *distro, Base: *base})
		if *asJSON {
			encoded, err := report.JSON()
			if err != nil {
				fmt.Fprintln(os.Stderr, "ez-am: could not encode the report:", err)
				return 1
			}
			fmt.Println(string(encoded))
		} else {
			fmt.Print(report.Text())
		}
		if report.Ready() {
			return 0
		}
		return 2
	case "download":
		return media.RunWithTarget(*distro, *base)
	case "start":
		result, err := control.Run(ctx, control.Options{Runner: runner, Distro: *distro, Base: *base})
		if err != nil {
			fmt.Fprintln(os.Stderr, "ez-am:", err)
			return 1
		}
		if !result.LaunchMedia {
			return 0
		}
		return media.RunWithTarget(result.Distro, result.Base)
	default:
		fmt.Fprintf(os.Stderr, "ez-am: unknown command %q\n\n%s", command, strings.TrimSpace(usage))
		return 2
	}
}
