package media

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the media downloader UI and returns its process exit code.
func Run() int {
	p := tea.NewProgram(initialModel())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	return programExitCode(final, err)
}
