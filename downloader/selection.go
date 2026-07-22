package main

import "strings"

const (
	profileALAC48  = "alac-48"
	profileALAC96  = "alac-96"
	profileALAC192 = "alac-192"
	profileAtmos   = "atmos"
	profileAAC     = "aac"
)

var profileOrder = []string{profileALAC48, profileALAC96, profileALAC192, profileAtmos, profileAAC}

type trackChoice struct {
	track    TrackMetadata
	selected bool
	profile  string
}

type downloadJob struct {
	profile  string
	trackIDs []string
}

const (
	progressValidating = "validating"
	progressWrapper    = "wrapper"
	progressContainer  = "container"
	progressDownload   = "download"
	progressFinalize   = "decrypt/tag"
	progressComplete   = "complete"
)

func hasAudioTrait(track TrackMetadata, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, trait := range track.AudioTraits {
		if strings.EqualFold(strings.TrimSpace(trait), want) {
			return true
		}
	}
	return false
}

func availableProfiles(track TrackMetadata) []string {
	profiles := make([]string, 0, len(profileOrder))
	hiRes := hasAudioTrait(track, "hi-res-lossless")
	lossless := hiRes || hasAudioTrait(track, "lossless")
	for _, profile := range profileOrder {
		switch profile {
		case profileALAC48:
			if lossless {
				profiles = append(profiles, profile)
			}
		case profileALAC96, profileALAC192:
			if hiRes {
				profiles = append(profiles, profile)
			}
		case profileAtmos:
			if hasAudioTrait(track, "atmos") {
				profiles = append(profiles, profile)
			}
		case profileAAC:
			profiles = append(profiles, profile)
		}
	}
	return profiles
}

func defaultProfile(track TrackMetadata) string {
	switch {
	case hasAudioTrait(track, "hi-res-lossless"):
		return profileALAC192
	case hasAudioTrait(track, "lossless"):
		return profileALAC48
	default:
		return profileAAC
	}
}

func cycleProfile(choice trackChoice, direction int) trackChoice {
	available := availableProfiles(choice.track)
	if len(available) == 0 {
		choice.profile = profileAAC
		return choice
	}
	current := 0
	for i, profile := range available {
		if profile == choice.profile {
			current = i
			break
		}
	}
	if direction < 0 {
		current = (current + len(available) - 1) % len(available)
	} else {
		current = (current + 1) % len(available)
	}
	choice.profile = available[current]
	return choice
}

func buildDownloadJobs(choices []trackChoice) []downloadJob {
	groups := make(map[string][]string)
	for _, choice := range choices {
		if !choice.selected {
			continue
		}
		profile := choice.profile
		if profile == "" {
			profile = defaultProfile(choice.track)
		}
		groups[profile] = append(groups[profile], choice.track.ID)
	}
	jobs := make([]downloadJob, 0, len(groups))
	for _, profile := range profileOrder {
		if ids := groups[profile]; len(ids) > 0 {
			jobs = append(jobs, downloadJob{profile: profile, trackIDs: append([]string(nil), ids...)})
		}
	}
	return jobs
}

func stageFromLine(line string) string {
	lower := strings.ToLower(strings.TrimSpace(line))
	if strings.HasPrefix(lower, "[stage]") {
		switch strings.TrimSpace(strings.TrimPrefix(lower, "[stage]")) {
		case "validating":
			return progressValidating
		case "wrapper":
			return progressWrapper
		case "container":
			return progressContainer
		case "download":
			return progressDownload
		case "complete":
			return progressComplete
		}
	}
	switch {
	case strings.Contains(lower, "decrypt"), strings.Contains(lower, "embed"), strings.Contains(lower, "tag"), strings.Contains(lower, "mp4box"):
		return progressFinalize
	case strings.Contains(lower, "completed"), strings.Contains(lower, "download complete"), strings.Contains(lower, "[complete]"):
		return progressComplete
	case strings.Contains(lower, "downloading"), strings.Contains(lower, "downloaded"), strings.Contains(lower, "segment"):
		return progressDownload
	case strings.Contains(lower, "queue"), strings.Contains(lower, "album"), strings.Contains(lower, "track "):
		return progressContainer
	default:
		return ""
	}
}

func appendRecentLog(current, next string, maxLines, maxBytes int) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	lines := make([]string, 0, maxLines+1)
	if current = strings.TrimSpace(current); current != "" {
		lines = append(lines, strings.Split(current, "\n")...)
	}
	lines = append(lines, next)
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	result := strings.Join(lines, "\n")
	if maxBytes > 0 && len(result) > maxBytes {
		result = result[len(result)-maxBytes:]
		if idx := strings.IndexByte(result, '\n'); idx >= 0 {
			result = result[idx+1:]
		}
	}
	return result
}
