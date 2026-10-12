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
	"ez-am/internal/i18n"
	"ez-am/internal/media"
	"ez-am/internal/platform/execrunner"
)

// splitArgs separates the subcommand from the flags so that `ez-am doctor
// --json` behaves like `ez-am --json doctor`.
func splitArgs(args []string) (positional []string, flagArgs []string) {
	valueFlags := map[string]bool{
		"--distro": true, "-distro": true,
		"--base": true, "-base": true,
		"--lang": true, "-lang": true,
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

// prescanLang reads --lang before flag parsing so that even the usage text and
// the flag errors come out in the requested language.
func prescanLang(args []string) string {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if name, value, found := strings.Cut(arg, "="); found && (name == "--lang" || name == "-lang") {
			return value
		}
		if arg == "--lang" || arg == "-lang" {
			if index+1 < len(args) {
				return args[index+1]
			}
		}
	}
	return ""
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// Flag > EZ_AM_LANG > saved choice > system locale.
	i18n.Set(i18n.Resolve(prescanLang(args)))

	flags := flag.NewFlagSet("ez-am", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { fmt.Fprint(os.Stderr, i18n.T("cli.usage")) }
	distro := flags.String("distro", "", "WSL distribution to use")
	base := flags.String("base", "", "runtime directory inside the distribution")
	lang := flags.String("lang", "", "interface language: en or zh")
	asJSON := flags.Bool("json", false, "print the doctor report as JSON (doctor only)")

	positional, flagArgs := splitArgs(args)
	if err := flags.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if requested := strings.TrimSpace(*lang); requested != "" {
		i18n.Set(i18n.Normalize(requested))
		// Remember an explicit choice for the next run; a read-only config
		// directory must not break the command.
		_ = i18n.Save(i18n.Current())
	}

	command := "start"
	if len(positional) > 0 {
		command = positional[0]
		positional = positional[1:]
	}
	if len(positional) > 0 {
		fmt.Fprintf(os.Stderr, "ez-am: %s\n\n%s", i18n.Tf("cli.err.unexpected", positional[0]), i18n.T("cli.usage"))
		return 2
	}

	runner := execrunner.New()
	ctx := context.Background()

	switch command {
	case "help", "-h", "--help":
		fmt.Print(i18n.T("cli.usage"))
		return 0
	case "doctor":
		report := doctor.Run(ctx, doctor.Inputs{Runner: runner, Distro: *distro, Base: *base})
		if *asJSON {
			encoded, err := report.JSON()
			if err != nil {
				fmt.Fprintln(os.Stderr, "ez-am:", i18n.Tf("cli.err.report", err.Error()))
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
		fmt.Fprintf(os.Stderr, "ez-am: %s\n\n%s", i18n.Tf("cli.err.unknown", command), strings.TrimSpace(i18n.T("cli.usage")))
		return 2
	}
}
