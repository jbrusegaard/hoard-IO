// Package tui renders scan results as an interactive, ncdu-style tree
// browser built on bubbletea. Like the rest of hoardio it is strictly
// read-only: it shows where space goes and never touches files.
package tui

import (
	"fmt"
	"sort"
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
	unreadable  int // directories the scanner could not read
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
	unreadable  int // directories the scan skipped, shown in the status line

	root  *report.Node
	stack []entry

	visible []*report.Node        // filtered children of the current node
	yOff    int                   // scroll offset into visible
	sorted  map[*report.Node]bool // node children already ordered for the active sort

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

// ScanDone builds a doneMsg for the program event queue. unreadable is the
// number of directories the scanner could not read, so the browser can say the
// totals it shows are incomplete.
func ScanDone(tree *report.Tree, stats scanner.Stats, elapsed time.Duration, interrupted bool, unreadable int) tea.Msg {
	return doneMsg{tree: tree, stats: stats, elapsed: elapsed, interrupted: interrupted, unreadable: unreadable}
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
	m.unreadable = msg.unreadable
	m.stats, m.elapsed = msg.stats, msg.elapsed
	m.root = msg.tree.Root
	m.stack = []entry{{node: m.root, idx: 0}}
	m.refreshVisible()

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.scanning {
		return m, scanningKey(msg)
	}

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

// scanningKey handles keys while a scan is running. The tree does not exist
// yet, so only quit is meaningful; dropping the rest keeps a cursor from moving
// over a directory list that is not there.
func scanningKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	default:
		return nil
	}
}

// toggleSort flips the sort order. Nothing is re-sorted here: dropping the
// memo map means every directory is re-ordered the next time it is shown, so a
// keystroke costs the current directory instead of the whole tree.
func (m *Model) toggleSort() {
	m.sortByName = !m.sortByName
	m.sorted = make(map[*report.Node]bool)
	m.refreshVisible()
}

// sortChildren orders one directory's children. It mirrors report.SortBySize
// and report.SortByName, which walk the entire subtree: on a 2M-node home
// folder that is 150-200ms per keystroke to display thirty rows.
func sortChildren(children []*report.Node, byName bool) {
	if byName {
		sort.SliceStable(children, func(i, j int) bool {
			a, b := children[i], children[j]
			if a.Dir != b.Dir {
				return a.Dir
			}

			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})

		return
	}

	sort.SliceStable(children, func(i, j int) bool {
		return children[i].DiskUsage > children[j].DiskUsage
	})
}

// sortNode orders node's children unless they already carry the active order.
func (m *Model) sortNode(node *report.Node) {
	if m.sorted[node] {
		return
	}

	if m.sorted == nil {
		m.sorted = make(map[*report.Node]bool)
	}

	sortChildren(node.Children, m.sortByName)
	m.sorted[node] = true
}

// cur returns the top of the navigation stack, or nil when there is none:
// before a scan finishes, and after a scan that failed outright.
func (m *Model) cur() *entry {
	if len(m.stack) == 0 {
		return nil
	}

	return &m.stack[len(m.stack)-1]
}

// selected returns the node under the cursor, or nil when nothing is selected.
func (m *Model) selected() *report.Node {
	e := m.cur()
	if e == nil || e.idx >= len(m.visible) {
		return nil
	}

	return m.visible[e.idx]
}

func (m *Model) refreshVisible() {
	e := m.cur()
	if e == nil {
		m.visible = nil

		return
	}

	node := e.node
	m.sortNode(node)

	// Never let m.visible alias node.Children: the filtered branch reuses
	// m.visible's backing array, which would overwrite the node's children.
	m.visible = m.visible[:0]

	for _, c := range node.Children {
		if m.showFiles || c.Dir {
			m.visible = append(m.visible, c)
		}
	}

	e.idx = min(e.idx, max(len(m.visible)-1, 0))
	m.yOff = min(m.yOff, max(len(m.visible)-m.listHeight(), 0))
}

func (m *Model) move(delta int) {
	e := m.cur()
	if e == nil {
		return
	}

	e.idx = max(0, min(e.idx+delta, len(m.visible)-1))
	m.ensureVisible()
}

func (m *Model) goInto() {
	child := m.selected()
	if child == nil || !child.Dir {
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
	e := m.cur()
	if e == nil {
		return
	}

	if e.idx < m.yOff {
		m.yOff = e.idx
	}

	if e.idx >= m.yOff+m.listHeight() {
		m.yOff = e.idx - m.listHeight() + 1
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

func (m *Model) pctOfParent(n *report.Node) float64 {
	e := m.cur()
	if e == nil || e.node.DiskUsage == 0 {
		return 0
	}

	return 100 * float64(n.DiskUsage) / float64(e.node.DiskUsage)
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
	crumb := crumbStyle.MaxWidth(width).Render(m.renderBreadcrumb())

	curIdx := -1
	if e := m.cur(); e != nil {
		curIdx = e.idx
	}

	var b strings.Builder
	b.WriteString(crumb + "\n")

	maxSize := largestChild(m.visible)

	listH := m.listHeight()
	end := min(m.yOff+listH, len(m.visible))
	rows := 0

	for i := m.yOff; i < end; i++ {
		b.WriteString(m.renderRow(m.visible[i], maxSize, width, i == curIdx) + "\n")

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
	if m.unreadable > 0 {
		total += fmt.Sprintf(" · %d unreadable", m.unreadable)
	}

	if m.interrupted {
		total += " (partial)"
	}

	sel := m.selected()
	if sel == nil {
		return truncate(total, m.width)
	}

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

	return helpStyle.Render(strings.Join(lines, "\n"))
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

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "55", Dark: "141"})
	labelStyle    = lipgloss.NewStyle().Faint(true)
	valueStyle    = lipgloss.NewStyle().Bold(true)
	spinnerStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "202", Dark: "208"})
	railStyle     = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.AdaptiveColor{Light: "252", Dark: "238"})
	sweepStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "24", Dark: "39"})
	scanCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).
			Padding(1, 2)

	// crumbStyle and helpStyle carry no fixed rules of their own beyond what
	// they set here; crumbStyle takes the terminal width at render time, and
	// lipgloss setters copy, so the shared value is never mutated.
	crumbStyle = lipgloss.NewStyle()
	helpStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

	statusStyle = lipgloss.NewStyle().Reverse(true).Bold(true)
	footerStyle = lipgloss.NewStyle().Faint(true)
	keyStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "24", Dark: "39"}).Bold(true)
	sepStyle    = lipgloss.NewStyle().Faint(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	emptyStyle  = lipgloss.NewStyle().Faint(true).Italic(true)
)
