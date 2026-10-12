package doctor

import (
	"context"
	"fmt"
	"strings"

	"ez-am/internal/platform"
	"ez-am/internal/platform/wsl"
)

// fullProbeScript collects every fact the doctor needs in a SINGLE round trip
// into the distribution. The doctor used to spend one wsl.exe invocation per
// fact; WSL serializes those, so the ten probes cost 16-21 seconds while this
// one costs about two.
//
// Dynamic values never enter the script: an explicit runtime base arrives as
// $1 and everything else is resolved inside the distribution.
const fullProbeScript = `# ez-am-probe-facts
set -u
if [ ! -r /etc/os-release ]; then
  exit 65
fi
. /etc/os-release
printf '\036uid=%s' "$(id -u)"
printf '\036user=%s' "$(id -un)"
printf '\036home=%s' "$HOME"
printf '\036architecture=%s' "$(uname -m)"
printf '\036os_id=%s' "${ID-}"
printf '\036os_id_like=%s' "${ID_LIKE-}"
printf '\036os_version_id=%s' "${VERSION_ID-}"

explicit="${1-}"
if [ -n "$explicit" ]; then
  base="$explicit"
  legacy=0
elif [ -f "$HOME/amd/login.sh" ]; then
  base="$HOME/amd"
  legacy=1
else
  base="${XDG_DATA_HOME:-$HOME/.local/share}/ez-am"
  legacy=0
fi
printf '\036base=%s' "$base"
printf '\036legacy=%s' "$legacy"

missing=""
for name in dl.sh login.sh compose.yaml; do
  if [ ! -f "$base/$name" ]; then
    missing="$missing$name,"
  fi
done
printf '\036missing=%s' "${missing%,}"

if [ -s "$base/.env" ] && [ -s "$base/config/config.yaml" ]; then
  printf '\036credentials=1'
else
  printf '\036credentials=0'
fi

server="$(docker info --format '{{.ServerVersion}}' 2>/dev/null || true)"
printf '\036docker=%s' "$server"
if [ -z "$server" ]; then
  printf '\036docker_error=%s' "$(docker info 2>&1 >/dev/null | head -n 1 || true)"
fi

printf '\036buildx=%s' "$(docker buildx version 2>/dev/null | head -n 1 || true)"

docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | while IFS= read -r image; do
  if [ -n "$image" ]; then
    printf '\036image=%s' "$image"
  fi
done

wrapper_output="$(bash "$base/login.sh" status 2>&1)"
wrapper_status=$?
if [ "$wrapper_status" -eq 0 ]; then
  printf '\036wrapper=1'
else
  printf '\036wrapper=0'
  printf '\036wrapper_detail=%s' "$(printf '%s\n' "$wrapper_output" | grep -m1 '^\[!\]' || true)"
fi

if [ -d "$base/downloads" ] && [ -w "$base/downloads" ]; then
  printf '\036downloads=1'
else
  printf '\036downloads=0'
fi
`

// facts is everything one probe round trip returns.
type facts struct {
	uid          int
	user         string
	home         string
	architecture string
	osID         string
	osIDLike     string
	osVersion    string

	base    string
	legacy  bool
	missing string
	hasCred bool

	docker      string
	dockerError string
	buildx      string
	images      []string

	wrapperOK     bool
	wrapperDetail string

	downloadsOK bool
}

func probeFacts(ctx context.Context, in Inputs, distro, explicitBase string) (facts, error) {
	args := []string{"-d", distro, "--exec", "bash", "-lc", fullProbeScript, probeArgv0}
	if explicit := strings.TrimSpace(explicitBase); explicit != "" {
		args = append(args, strings.TrimSuffix(explicit, "/"))
	}
	result, err := in.Runner.Run(ctx, platform.CommandRequest{Name: "wsl.exe", Args: args})
	if err != nil {
		return facts{}, err
	}
	if result.Code != 0 {
		return facts{}, fmt.Errorf("the distribution probe exited with code %d: %s", result.Code, firstLine(string(result.Stderr)))
	}
	return parseFacts(result.Stdout)
}

const probeArgv0 = "ez-am-probe"

func parseFacts(payload []byte) (facts, error) {
	records := strings.Split(string(payload), "\x1e")
	var parsed facts
	for _, record := range records {
		if record == "" {
			continue
		}
		key, value, found := strings.Cut(record, "=")
		if !found || key == "" {
			return facts{}, fmt.Errorf("probe output contains a malformed record")
		}
		switch key {
		case "image":
			parsed.images = append(parsed.images, value)
		case "uid":
			uid, convErr := parseUID(value)
			if convErr != nil {
				return facts{}, convErr
			}
			parsed.uid = uid
		case "user":
			parsed.user = value
		case "home":
			parsed.home = value
		case "architecture":
			parsed.architecture = value
		case "os_id":
			parsed.osID = value
		case "os_id_like":
			parsed.osIDLike = value
		case "os_version_id":
			parsed.osVersion = value
		case "base":
			parsed.base = value
		case "legacy":
			parsed.legacy = value == "1"
		case "missing":
			parsed.missing = value
		case "credentials":
			parsed.hasCred = value == "1"
		case "docker":
			parsed.docker = value
		case "docker_error":
			parsed.dockerError = value
		case "buildx":
			parsed.buildx = value
		case "wrapper":
			parsed.wrapperOK = value == "1"
		case "wrapper_detail":
			parsed.wrapperDetail = value
		case "downloads":
			parsed.downloadsOK = value == "1"
		default:
			return facts{}, fmt.Errorf("probe output contains unknown field %q", key)
		}
	}
	if parsed.user == "" || parsed.home == "" || parsed.osID == "" || parsed.base == "" {
		return facts{}, fmt.Errorf("probe output is missing required fields")
	}
	return parsed, nil
}

func parseUID(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("probe output field %q is empty", "uid")
	}
	uid := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("probe field %q is not a decimal uid", "uid")
		}
		uid = uid*10 + int(char-'0')
	}
	return uid, nil
}

// rows turns one probe result into the report rows, in canonical order. The
// wording and the offered actions are the same as when every fact had its own
// probe.
func rows(f facts) []Check {
	return []Check{
		distributionRow(f),
		dockerRow(f),
		buildxRow(f),
		runtimeRow(f),
		configRow(f),
		imagesRow(f),
		wrapperRow(f),
		downloadsRow(f),
	}
}

func distributionRow(f facts) Check {
	const id, title = "distro", "Distribution"
	if f.uid == 0 || f.user == "root" || wsl.ValidateLinuxHome(f.home, f.uid) != nil {
		return Check{
			ID: id, Title: title, Status: StatusFail,
			Detail: "the default user of this distribution is root; configure a normal user first",
		}
	}
	family := wsl.ClassifyFamily(f.osID, strings.Fields(f.osIDLike))
	if family == "" {
		return Check{
			ID: id, Title: title, Status: StatusFail,
			Detail: fmt.Sprintf("unsupported distribution family %q; supported: Debian/Ubuntu and Arch", sanitize(f.osID)),
		}
	}
	return Check{
		ID: id, Title: title, Status: StatusOK,
		Detail: fmt.Sprintf("%s %s (%s) · %s", sanitize(f.osID), sanitize(f.osVersion), family, sanitize(f.architecture)),
	}
}

func dockerRow(f facts) Check {
	const id, title = "docker", "Docker"
	if f.docker == "" {
		detail := firstLine(f.dockerError)
		if detail == "" {
			detail = "the Docker daemon does not answer inside this distribution"
		}
		return Check{ID: id, Title: title, Status: StatusFail, Action: ActionStartDocker, Detail: detail}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: "server " + sanitize(f.docker)}
}

func buildxRow(f facts) Check {
	const id, title = "buildx", "BuildKit"
	if f.buildx == "" {
		return Check{
			ID: id, Title: title, Status: StatusWarn, Action: ActionInstallBuildx,
			Detail: "docker buildx is missing; needed once to build the wrapper image",
		}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: firstLine(f.buildx)}
}

func runtimeRow(f facts) Check {
	const id, title = "runtime", "Runtime files"
	if f.missing != "" {
		return Check{
			ID: id, Title: title, Status: StatusFail, Action: ActionInstallRuntime,
			Detail: "missing under " + f.base + ": " + sanitize(f.missing),
		}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: "installed under " + f.base}
}

func configRow(f facts) Check {
	const id, title = "config", "Credentials"
	if !f.hasCred {
		return Check{
			ID: id, Title: title, Status: StatusFail, Action: ActionCollectCreds,
			Detail: "the Apple account and media token are not configured yet",
		}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: "account and media token are stored"}
}

func imagesRow(f facts) Check {
	const id, title = "images", "Images"
	var missing []string
	if !hasRepository(f.images, "ghcr.io/zhaarey/apple-music-downloader") {
		missing = append(missing, "downloader")
	}
	if !hasRepository(f.images, "local/wrapper") {
		missing = append(missing, "wrapper")
	}
	if len(missing) > 0 {
		return Check{
			ID: id, Title: title, Status: StatusWarn,
			Detail: "not pulled yet: " + strings.Join(missing, ", ") + " (setup.sh fetches them)",
		}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: "downloader and wrapper images are present"}
}

func hasRepository(images []string, repository string) bool {
	for _, image := range images {
		if strings.Contains(image, repository) {
			return true
		}
	}
	return false
}

func wrapperRow(f facts) Check {
	const id, title = "wrapper", "Wrapper session"
	if f.wrapperOK {
		return Check{ID: id, Title: title, Status: StatusOK, Detail: "listening on 10020/20020/30020"}
	}
	detail := firstLine(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(f.wrapperDetail), "[!]")))
	if strings.Contains(detail, "amd-wrapper-auth") || strings.Contains(detail, "Authentication started") {
		return Check{
			ID: id, Title: title, Status: StatusFail, Action: ActionLogin,
			Detail: "an authentication request is already waiting for a code",
		}
	}
	if detail == "" {
		detail = "the wrapper is not healthy"
	}
	return Check{ID: id, Title: title, Status: StatusFail, Action: ActionLogin, Detail: detail}
}

func downloadsRow(f facts) Check {
	const id, title = "downloads", "Output directory"
	if !f.downloadsOK {
		return Check{ID: id, Title: title, Status: StatusWarn, Detail: "not writable yet: " + f.base + "/downloads"}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: f.base + "/downloads"}
}
