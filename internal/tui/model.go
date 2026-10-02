// Package tui renders scan results as an interactive, ncdu-style tree
// browser built on bubbletea. Like the rest of hoardio it is strictly
// read-only: it shows where space goes and never touches files.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/jbrusegaard/hoardio/internal/report"
	"github.com/jbrusegaard/hoardio/internal/scanner"
)

type progressMsg struct {
	stats   scanner.Stats
	elapsed time.Duration
}

type doneMsg struct {
	tree        *report.Tree
	stats       scanner.Stats
	elapsed     time.Duration
	interrupted bool
}

type failMsg struct{ err error }

type entry struct {
	node *report.Node
	idx  int // cursor position within node's visible children
}

// Model is the bubbletea model: scan progress first, then tree browsing.
type Model struct {
	scanning    bool
	failed      bool
	scanErr     string
	interrupted bool

	root  *report.Node
	stack []entry

	visible []*report.Node // filtered children of the current node
	yOff    int            // scroll offset into visible

	stats   scanner.Stats
	elapsed time.Duration
	spinIdx int

	width, height int
	sortByName    bool
	showFiles     bool
	showHelp      bool
	scanRoot      string // path being scanned, shown during progress
}

var _ tea.Model = (*Model)(nil)

// New returns a Model in scanning state.
func New() *Model {
	return &Model{scanning: true, showFiles: true, width: 80, height: 24}
}

// SetScanRoot labels the path being scanned in the progress view.
func (m *Model) SetScanRoot(path string) { m.scanRoot = path }

// Progress builds a progressMsg for the program event queue.
func Progress(stats scanner.Stats, elapsed time.Duration) tea.Msg {
	return progressMsg{stats: stats, elapsed: elapsed}
}

// ScanDone builds a doneMsg for the program event queue.
func ScanDone(tree *report.Tree, stats scanner.Stats, elapsed time.Duration, interrupted bool) tea.Msg {
	return doneMsg{tree: tree, stats: stats, elapsed: elapsed, interrupted: interrupted}
}

// ScanFailed builds a failMsg for the program event queue.
func ScanFailed(err error) tea.Msg { return failMsg{err: err} }

const (
	barWidth   = 14
	spinnerSet = `|/-\`
)

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case progressMsg:
		m.stats, m.elapsed = msg.stats, msg.elapsed
		m.spinIdx = (m.spinIdx + 1) % len(spinnerSet)
	case doneMsg:
		return m.finishScan(msg)
	case failMsg:
		m.scanning, m.failed, m.scanErr = false, true, msg.err.Error()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m *Model) finishScan(msg doneMsg) (tea.Model, tea.Cmd) {
	m.scanning = false
	m.interrupted = msg.interrupted
	m.stats, m.elapsed = msg.stats, msg.elapsed
	m.root = msg.tree.Root
	report.SortBySize(m.root)
	m.stack = []entry{{node: m.root, idx: 0}}
	m.refreshVisible()

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "enter", "right", "l":
		m.goInto()
	case "left", "h", "backspace", "esc":
		m.goUp()
	case "n":
		m.toggleSort()
	case "f":
		m.showFiles = !m.showFiles
		m.refreshVisible()
	}

	return m, nil
}

func (m *Model) toggleSort() {
	m.sortByName = !m.sortByName

	if m.root != nil {
		if m.sortByName {
			report.SortByName(m.root)
		} else {
			report.SortBySize(m.root)
		}
	}

	m.refreshVisible()
}

func (m *Model) cur() *entry { return &m.stack[len(m.stack)-1] }

func (m *Model) refreshVisible() {
	node := m.cur().node

	children := node.Children
	if m.showFiles {
		m.visible = children
	} else {
		m.visible = m.visible[:0]

		for _, c := range children {
			if c.Dir {
				m.visible = append(m.visible, c)
			}
		}
	}

	m.cur().idx = min(m.cur().idx, max(len(m.visible)-1, 0))
	m.yOff = min(m.yOff, max(len(m.visible)-m.listHeight(), 0))
}

func (m *Model) move(delta int) {
	e := m.cur()
	e.idx = max(0, min(e.idx+delta, len(m.visible)-1))
	m.ensureVisible()
}

func (m *Model) goInto() {
	if len(m.visible) == 0 {
		return
	}

	child := m.visible[m.cur().idx]
	if !child.Dir {
		return
	}

	m.stack = append(m.stack, entry{node: child, idx: 0})
	m.yOff = 0
	m.refreshVisible()
}

func (m *Model) goUp() {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
		m.refreshVisible()
	}
}

func (m *Model) listHeight() int { return max(m.height-4, 1) }

func (m *Model) ensureVisible() {
	if m.cur().idx < m.yOff {
		m.yOff = m.cur().idx
	}

	if m.cur().idx >= m.yOff+m.listHeight() {
		m.yOff = m.cur().idx - m.listHeight() + 1
	}
}

// View implements tea.Model.
func (m *Model) View() string {
	switch {
	case m.scanning:
		return m.viewScanning()
	case m.failed:
		return "scan failed: " + m.scanErr + "\n\npress q to quit\n"
	default:
		v := m.viewBrowser()
		if m.showHelp {
			v += "\n" + m.helpBlock()
		}

		return v
	}
}

func (m *Model) viewScanning() string {
	root := "<scanning>"
	if m.scanRoot != "" {
		root = m.scanRoot
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n\n", titleStyle.Render("Scanning"), dirStyle.Render(truncate(root, m.width-24)))
	fmt.Fprintf(&b, "  %s %s\n", labelStyle.Render(fmt.Sprintf("%-5s", "dirs")), valueStyle.Render(fmt.Sprintf("%10d", m.stats.DirsScanned)))
	fmt.Fprintf(&b, "  %s %s\n", labelStyle.Render(fmt.Sprintf("%-5s", "files")), valueStyle.Render(fmt.Sprintf("%10d", m.stats.FilesSeen)))
	fmt.Fprintf(&b, "  %s %s\n", labelStyle.Render(fmt.Sprintf("%-5s", "size")), valueStyle.Render(fmt.Sprintf("%10s", scanner.HumanBytes(m.stats.BytesDisk))))
	fmt.Fprintf(&b, "  %s %s\n", labelStyle.Render(fmt.Sprintf("%-5s", "time")), valueStyle.Render(fmt.Sprintf("%10s", m.elapsed.Round(time.Second).String())))
	fmt.Fprintf(&b, "\n%s %s\n", footerStyle.Render("press ctrl+c to quit"), spinnerStyle.Render(string(rune(spinnerSet[m.spinIdx]))))

	return b.String()
}

func (m *Model) pctOfParent(n *report.Node) float64 {
	parent := m.cur().node
	if parent.DiskUsage == 0 {
		return 0
	}

	return 100 * float64(n.DiskUsage) / float64(parent.DiskUsage)
}

func largestChild(visible []*report.Node) int64 {
	var largest int64
	for _, c := range visible {
		if c.DiskUsage > largest {
			largest = c.DiskUsage
		}
	}

	return largest
}

// renderRow draws one list entry: cursor, heat bar, percent of parent, size, name.
func (m *Model) renderRow(child *report.Node, maxSize int64, width int, selected bool) string {
	frac := 0.0
	if maxSize > 0 {
		frac = float64(child.DiskUsage) / float64(maxSize)
	}

	bar := sizeBar(frac, barWidth)
	pct := fmt.Sprintf("%5.1f%%", m.pctOfParent(child))
	size := fmt.Sprintf("%9s", scanner.HumanBytes(child.DiskUsage))

	name := child.Name
	if child.Dir {
		name += "/"
	}

	nameW := width - 2 - barWidth - 2 - len(pct) - 1 - 9 - 1
	name = truncate(name, max(nameW, 8))

	cursor := "  "
	if selected {
		cursor = cursorStyle.Render("▸ ")
	}

	nameStyled := fileStyle.Render(name)
	if child.Dir {
		nameStyled = dirStyle.Render(name)
	}

	row := fmt.Sprintf("%s%s %s %s %s", cursor, heatStyle(frac).Render(bar), heatStyle(frac).Render(pct), size, nameStyled)
	if selected {
		row = selectedStyle.Render(row)
	}

	return row
}

func (m *Model) viewBrowser() string {
	width := m.width
	crumb := lipgloss.NewStyle().MaxWidth(width).Render(m.renderBreadcrumb())

	var b strings.Builder
	b.WriteString(crumb + "\n")

	maxSize := largestChild(m.visible)

	listH := m.listHeight()
	end := min(m.yOff+listH, len(m.visible))
	rows := 0

	for i := m.yOff; i < end; i++ {
		b.WriteString(m.renderRow(m.visible[i], maxSize, width, i == m.cur().idx) + "\n")

		rows++
	}

	for r := rows; r < listH; r++ {
		if rows == 0 && r == 0 {
			b.WriteString(emptyStyle.Render("(empty)") + "\n")
		} else {
			b.WriteString("\n")
		}
	}

	b.WriteString(statusStyle.Width(max(width-1, 1)).Render(m.statusLine()))
	b.WriteString("\n")
	b.WriteString(m.renderFooter())

	return b.String()
}

func (m *Model) renderBreadcrumb() string {
	parts := make([]string, 0, len(m.stack))
	for _, e := range m.stack {
		parts = append(parts, dirStyle.Render(e.node.Name)) // root node's Name is the scan path
	}

	return strings.Join(parts, sepStyle.Render(" / "))
}

func (m *Model) renderFooter() string {
	keys := []struct {
		key, desc string
	}{
		{"↑↓", "move"}, {"enter", "open"}, {"←", "up"}, {"n", "sort"}, {"f", "files"}, {"?", "help"}, {"q", "quit"},
	}

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, keyStyle.Render(k.key)+footerStyle.Render(" "+k.desc))
	}

	return strings.Join(pairs, sepStyle.Render(" · "))
}

func (m *Model) statusLine() string {
	total := fmt.Sprintf("%s in %d dirs · %d files · %s",
		scanner.HumanBytes(m.stats.BytesDisk), m.stats.DirsScanned, m.stats.FilesSeen,
		m.elapsed.Round(time.Second))
	if len(m.visible) == 0 {
		return total
	}

	sel := m.visible[m.cur().idx]

	detail := fmt.Sprintf("%s disk · %s apparent", scanner.HumanBytes(sel.DiskUsage), scanner.HumanBytes(sel.ApparentSize))
	if sel.Dir {
		detail += fmt.Sprintf(" · %d items", len(sel.Children))
	}

	return truncate(fmt.Sprintf("%s   %s: %s", total, sel.Name, detail), m.width)
}

func (m *Model) helpBlock() string {
	lines := []string{
		"up/down or k/j   move cursor",
		"enter/right/l  open directory",
		"left/h/bs/esc  go up a level",
		"n              toggle size/name sort",
		"f              toggle showing files",
		"q              quit",
	}

	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func sizeBar(frac float64, w int) string {
	filled := int(frac*float64(w) + 0.5)
	filled = max(0, min(w, filled))

	return strings.Repeat("█", filled) + dimStyle.Render(strings.Repeat("░", w-filled))
}

// heatStyle maps a size fraction (relative to the largest sibling) onto a
// green → amber → red heat ramp, like WinDirStat's treemap.
func heatStyle(frac float64) lipgloss.Style {
	switch {
	case frac > 0.66:
		return hotStyle
	case frac > 0.33:
		return warmStyle
	default:
		return coolStyle
	}
}

func truncate(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}

	if w < 2 {
		// "…" is one cell wide; slicing its UTF-8 bytes would corrupt it.
		if w >= runewidth.StringWidth("…") {
			return "…"
		}

		return ""
	}

	return runewidth.Truncate(s, w, "…")
}

var (
	dirStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "24", Dark: "39"})
	fileStyle = lipgloss.NewStyle()

	coolStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "42"})
	warmStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "214"})
	hotStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "160", Dark: "196"})

	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "24", Dark: "39"}).Bold(true)
	selectedStyle = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "188", Dark: "236"})

	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "55", Dark: "141"})
	labelStyle   = lipgloss.NewStyle().Faint(true)
	valueStyle   = lipgloss.NewStyle().Bold(true)
	spinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "202", Dark: "208"})

	statusStyle = lipgloss.NewStyle().Reverse(true).Bold(true)
	footerStyle = lipgloss.NewStyle().Faint(true)
	keyStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "24", Dark: "39"}).Bold(true)
	sepStyle    = lipgloss.NewStyle().Faint(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	emptyStyle  = lipgloss.NewStyle().Faint(true).Italic(true)
)
