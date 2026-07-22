package platform

import "context"

// Family identifies a Linux distribution family once discovery has enriched it.
type Family string

const (
	FamilyUnknown Family = ""
	FamilyDebian  Family = "debian"
	FamilyArch    Family = "arch"
)

// DockerState records the Docker capability discovered for a distribution.
type DockerState string

const DockerUnknown DockerState = ""

// Distribution contains the platform metadata used by discovery and setup.
type Distribution struct {
	Name         string
	Family       Family
	Version      string
	User         string
	Home         string
	Architecture string
	Docker       DockerState
	LegacyBase   bool
}

// CommandRequest describes one process invocation without requiring callers to
// interpolate dynamic data into shell source.
type CommandRequest struct {
	Name  string
	Args  []string
	Env   []string
	Stdin []byte
}

// CommandResult contains the observable result of a process invocation.
type CommandResult struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

// CommandRunner executes an injected process request.
type CommandRunner interface {
	Run(context.Context, CommandRequest) (CommandResult, error)
}
