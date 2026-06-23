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

// Tabstack brand palette (tabstack.ai), as raw hex. Defined once here so the
// fang help theme (cmd/tabstack, lipgloss v2) and these output styles
// (lipgloss v1) share a single source of truth; each consumer wraps these in
// its own color type and picks a context-appropriate light/dark variant.
const (
	HexPurple      = "#541bff" // primary accent
	HexPurpleLight = "#7c5cff" // lighter purple for dark terminals
	HexPink        = "#ff97ea" // secondary accent (reads on dark)
	HexPinkLight   = "#b3408f" // pink that reads on light terminals
	HexGray        = "#7c7985" // muted text
	HexInk         = "#10100f" // near-black body text
	HexPaper       = "#f4f4f5" // off-white body text on dark
	HexWhite       = "#ffffff"
)

// palette: the brand hexes above, kept adaptive so they read on both light and
// dark terminals. lipgloss.AdaptiveColor picks Light or Dark based on the
// detected background. Success stays green for its conventional meaning.
var (
	colorSuccess = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	colorAgent   = lipgloss.AdaptiveColor{Light: HexPurple, Dark: HexPurpleLight}
	colorBrowser = lipgloss.AdaptiveColor{Light: HexPinkLight, Dark: HexPink}
	colorMuted   = lipgloss.AdaptiveColor{Light: HexGray, Dark: HexGray}
	colorError   = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	colorBrand   = lipgloss.AdaptiveColor{Light: HexPurple, Dark: HexPurpleLight} // accents/borders
	colorCite    = lipgloss.AdaptiveColor{Light: HexPinkLight, Dark: HexPink}     // citation markers
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
			BorderForeground(colorBrand).
			Padding(0, 1),
		Key:  lipgloss.NewStyle().Foreground(colorMuted),
		Cite: lipgloss.NewStyle().Foreground(colorCite).Bold(true),
	}
}
