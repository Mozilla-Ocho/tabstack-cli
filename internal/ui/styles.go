package ui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

// Styles holds every lipgloss style the CLI uses. We bundle them into a struct
// rather than scattering package level vars so that colour can be switched off
// cleanly: when colour is disabled we hand back a Styles whose fields are all
// no-op styles, and the rest of the code never has to branch on it again.
type Styles struct {
	Success  lipgloss.Style
	Agent    lipgloss.Style
	Browser  lipgloss.Style
	Muted    lipgloss.Style
	ErrorTag lipgloss.Style
	Label    lipgloss.Style
	Box      lipgloss.Style
	Key      lipgloss.Style
	Cite     lipgloss.Style
}

// palette: kept small and adaptive so it reads on both light and dark
// terminals. lipgloss.AdaptiveColor picks Light or Dark based on the detected
// background.
var (
	colorSuccess = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	colorAgent   = lipgloss.AdaptiveColor{Light: "#6639ba", Dark: "#a371f7"}
	colorBrowser = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"}
	colorError   = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	colorCite    = lipgloss.Color("#ff5fd7") // bright pink for citation markers
)

// NewStyles builds the active style set. When noColor is true (either via the
// --no-color flag or the NO_COLOR environment variable) every style collapses
// to plain text.
func NewStyles(noColor bool) Styles {
	if noColor || os.Getenv("NO_COLOR") != "" {
		plain := lipgloss.NewStyle()
		return Styles{
			Success:  plain,
			Agent:    plain,
			Browser:  plain,
			Muted:    plain,
			ErrorTag: plain,
			Label:    plain,
			Box:      plain,
			Key:      plain,
			Cite:     plain,
		}
	}

	return Styles{
		Success:  lipgloss.NewStyle().Foreground(colorSuccess).Bold(true),
		Agent:    lipgloss.NewStyle().Foreground(colorAgent).Bold(true),
		Browser:  lipgloss.NewStyle().Foreground(colorBrowser),
		Muted:    lipgloss.NewStyle().Foreground(colorMuted),
		ErrorTag: lipgloss.NewStyle().Foreground(colorError).Bold(true),
		Label:    lipgloss.NewStyle().Bold(true),
		Box: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorSuccess).
			Padding(0, 1),
		Key:  lipgloss.NewStyle().Foreground(colorMuted),
		Cite: lipgloss.NewStyle().Foreground(colorCite).Bold(true),
	}
}
