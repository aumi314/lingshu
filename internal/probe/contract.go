package probe

// Probe modes.
const (
	ModeQuality     = "quality"
	ModeFingerprint = "fingerprint"
)

// RunCounts records requested, sent and completed calls for one probe run.
type RunCounts struct {
	Planned   int `json:"planned"`
	Sent      int `json:"sent"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

// NormalizeMode maps persisted presets to the new probe modes. quick/full
// remain accepted for old probe_runs rows.
func NormalizeMode(mode string) string {
	switch mode {
	case ModeQuality, ModeFingerprint:
		return mode
	case "full":
		return ModeFingerprint
	case "quick", "":
		return ModeQuality
	default:
		return mode
	}
}

func (s Spec) mode() string {
	return s.Mode
}

func qualityObservations(observations []Item) []Item {
	result := observations[:0]
	for _, observation := range observations {
		if observation.Key != "mixed" {
			result = append(result, observation)
		}
	}
	return result
}
