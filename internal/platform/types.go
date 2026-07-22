package platform

// Family identifies a Linux distribution family once discovery has enriched it.
type Family string

const FamilyUnknown Family = ""

// DockerState records the Docker capability discovered for a distribution.
type DockerState string

const DockerUnknown DockerState = ""

// Distribution contains the platform metadata used by discovery and setup.
type Distribution struct {
	Name       string
	Family     Family
	Version    string
	User       string
	Home       string
	Docker     DockerState
	LegacyBase bool
}
