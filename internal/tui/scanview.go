package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jbrusegaard/hoardio/internal/scanner"
)

const (
	sweepTrack = 36
	sweepLen   = 4
	cardWidth  = 46
)

// sweepPos returns the left offset of the moving segment, ping-ponging so the
// motion never jumps when it wraps.
func sweepPos(frame, track, bar int) int {
	span := track - bar
	if span <= 0 {
		return 0
	}

	p := frame % (2 * span)
	if p > span {
		p = 2*span - p
	}

	return p
}

// sweepBar draws an indeterminate activity rail: a faint track with a short
// accent segment gliding across it. It conveys "work in progress" without
// implying a completion percentage the scanner cannot know.
func sweepBar(frame, track int) string {
	if track < sweepLen+2 {
		return railStyle.Render(strings.Repeat("─", max(track, 0)))
	}

	pos := sweepPos(frame, track, sweepLen)

	return railStyle.Render(strings.Repeat("─", pos)) +
		sweepStyle.Render(strings.Repeat("─", sweepLen)) +
		railStyle.Render(strings.Repeat("─", track-pos-sweepLen))
}

// viewScanning renders the in-progress card: title, root path, live counters
// and an activity rail. Falls back to an unbordered block on narrow terminals.
func (m *Model) viewScanning() string {
	root := "<scanning>"
	if m.scanRoot != "" {
		root = m.scanRoot
	}

	var content strings.Builder

	fmt.Fprintf(&content, "%s\n", titleStyle.Render("Scanning"))
	fmt.Fprintf(&content, "%s\n\n", dirStyle.Render(truncate(root, cardWidth-4)))
	fmt.Fprintf(&content, "  %s %s\n", labelStyle.Render(fmt.Sprintf("%-5s", "dirs")), valueStyle.Render(fmt.Sprintf("%10d", m.stats.DirsScanned)))
	fmt.Fprintf(&content, "  %s %s\n", labelStyle.Render(fmt.Sprintf("%-5s", "files")), valueStyle.Render(fmt.Sprintf("%10d", m.stats.FilesSeen)))
	fmt.Fprintf(&content, "  %s %s\n", labelStyle.Render(fmt.Sprintf("%-5s", "size")), valueStyle.Render(fmt.Sprintf("%10s", scanner.HumanBytes(m.stats.BytesDisk))))
	fmt.Fprintf(&content, "  %s %s\n\n", labelStyle.Render(fmt.Sprintf("%-5s", "time")), valueStyle.Render(fmt.Sprintf("%10s", m.elapsed.Round(time.Second).String())))
	fmt.Fprint(&content, sweepBar(m.spinIdx, sweepTrack))

	if m.width < cardWidth+8 {
		return content.String() + fmt.Sprintf("\n\n%s %s\n", footerStyle.Render("press ctrl+c to quit"), spinnerStyle.Render(string(rune(spinnerSet[m.spinIdx]))))
	}

	card := scanCardStyle.Width(cardWidth).Render(content.String())

	return lipgloss.PlaceVertical(m.height, lipgloss.Top,
		lipgloss.JoinVertical(lipgloss.Center, card,
			footerStyle.Render("press ctrl+c to quit")))
}
