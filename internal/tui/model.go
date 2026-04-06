package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/skrunchtech/broom/internal/actions"
	"github.com/skrunchtech/broom/internal/folderscanner"
	"github.com/skrunchtech/broom/internal/scanner"
)

// -- Styles ------------------------------------------------------------------

var (
	styleBrand = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F4A460"))

	styleSubtle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	styleSelected = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4A460"))

	styleDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#444444"))

	styleKept = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#50FA7B"))

	styleMarked = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5555"))

	styleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#F4A460")).
			Padding(0, 1)

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#F4A460")).
			Padding(0, 1)

	styleBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))
)

// -- View states -------------------------------------------------------------

type viewState int

const (
	viewPicker        viewState = iota // folder selection
	viewScanning                       // scanning in progress
	viewResults                        // file duplicate groups
	viewFolderResults                  // folder duplicate groups
	viewDone                           // action completed
)

// -- Messages ----------------------------------------------------------------

type scanProgressMsg struct {
	pass  int
	done  int
	total int
}

type scanDoneMsg struct {
	groups []scanner.DuplicateGroup
	err    error
}

type folderScanDoneMsg struct {
	matches []folderscanner.FolderMatch
	err     error
}

type actionDoneMsg struct {
	results []actions.Result
	err     error
}

// -- Model -------------------------------------------------------------------

type groupState struct {
	group    scanner.DuplicateGroup
	expanded bool
	marked   []bool // which files are marked for removal
	keepIdx  int
}

type folderMatchState struct {
	match  folderscanner.FolderMatch
	marked []bool // which folders are marked for removal
}

type Model struct {
	// Config
	opts        scanner.Options
	strategy    actions.KeepStrategy
	archiveDir  string
	folderMode  bool

	// View
	state      viewState
	width      int
	height     int
	cursor     int
	fileCursor int

	// Folder picker
	folders     []string
	pickerInput string

	// Scanning
	spinner     spinner.Model
	progressBar progress.Model
	scanPass    int
	scanDone    int
	scanTotal   int
	cancelScan  context.CancelFunc
	progressCh  chan scanProgressMsg

	// File results
	groups     []groupState
	totalDupes int64

	// Folder results
	folderMatches    []folderMatchState
	totalFolderDupes int64

	// Done
	doneResults []actions.Result
	doneErr     error
}

func NewModel(folders []string, opts scanner.Options, strategy actions.KeepStrategy, archiveDir string, folderMode bool) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleBrand

	pb := progress.New(progress.WithDefaultGradient())

	m := Model{
		opts:        opts,
		strategy:    strategy,
		archiveDir:  archiveDir,
		folderMode:  folderMode,
		folders:     folders,
		spinner:     sp,
		progressBar: pb,
	}

	if len(folders) > 0 {
		m.state = viewScanning
	} else {
		m.state = viewPicker
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	if m.state == viewScanning {
		return tea.Batch(m.spinner.Tick, m.startScan())
	}
	return m.spinner.Tick
}

// -- Update ------------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.progressBar.Width = msg.Width - 8
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		var cmd tea.Cmd
		pm, c := m.progressBar.Update(msg)
		m.progressBar = pm.(progress.Model)
		cmd = c
		return m, cmd

	case scanProgressMsg:
		m.scanPass = msg.pass
		m.scanDone = msg.done
		m.scanTotal = msg.total
		var cmds []tea.Cmd
		if msg.total > 0 {
			cmds = append(cmds, m.progressBar.SetPercent(float64(msg.done)/float64(msg.total)))
		}
		// Re-subscribe for next progress event.
		if m.progressCh != nil {
			cmds = append(cmds, waitForProgress(m.progressCh))
		}
		return m, tea.Batch(cmds...)

	case scanDoneMsg:
		if msg.err != nil {
			m.doneErr = msg.err
			m.state = viewDone
			return m, nil
		}
		m.groups = make([]groupState, len(msg.groups))
		m.totalDupes = 0
		for i, g := range msg.groups {
			keepIdx := actions.SelectKeeper(g, m.strategy)
			marked := make([]bool, len(g.Files))
			for j := range g.Files {
				marked[j] = j != keepIdx
			}
			m.groups[i] = groupState{
				group:   g,
				keepIdx: keepIdx,
				marked:  marked,
			}
			// Count recoverable bytes (all marked files).
			for j, f := range g.Files {
				if marked[j] {
					m.totalDupes += f.Size
				}
			}
		}
		m.state = viewResults
		return m, nil

	case folderScanDoneMsg:
		if msg.err != nil {
			m.doneErr = msg.err
			m.state = viewDone
			return m, nil
		}
		m.folderMatches = make([]folderMatchState, len(msg.matches))
		m.totalFolderDupes = 0
		for i, match := range msg.matches {
			marked := make([]bool, len(match.Folders))
			// Auto-mark all but the first (largest/root) folder.
			for j := range marked {
				marked[j] = j != 0
			}
			m.folderMatches[i] = folderMatchState{match: match, marked: marked}
			m.totalFolderDupes += match.Size * int64(len(match.Folders)-1)
		}
		m.state = viewFolderResults
		return m, nil

	case actionDoneMsg:
		m.doneResults = msg.results
		m.doneErr = msg.err
		m.state = viewDone
		return m, nil
	}

	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {

	case viewScanning:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.cancelScan != nil {
				m.cancelScan()
			}
			return m, tea.Quit
		}

	case viewPicker:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if len(m.folders) > 0 {
				m.state = viewScanning
				return m, m.startScan()
			}
		case "d":
			if m.cursor < len(m.folders) {
				m.folders = append(m.folders[:m.cursor], m.folders[m.cursor+1:]...)
				if m.cursor > 0 {
					m.cursor--
				}
			}
		case "j", "down":
			if m.cursor < len(m.folders)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		}

	case viewResults:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.cursor < len(m.groups)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "e", " ":
			m.groups[m.cursor].expanded = !m.groups[m.cursor].expanded
		case "K":
			// Auto-mark all groups using keep strategy.
			for i, g := range m.groups {
				keepIdx := actions.SelectKeeper(g.group, m.strategy)
				for j := range g.marked {
					m.groups[i].marked[j] = j != keepIdx
					m.groups[i].keepIdx = keepIdx
				}
			}
		case "A":
			// Select all duplicates across all groups.
			for i, g := range m.groups {
				for j := range g.marked {
					m.groups[i].marked[j] = j != g.keepIdx
				}
			}
		case "a":
			// Mark current group (all files except keeper).
			for j := range m.groups[m.cursor].marked {
				m.groups[m.cursor].marked[j] = j != m.groups[m.cursor].keepIdx
			}
		case "u":
			// Unmark all files in current group (skip this group).
			for j := range m.groups[m.cursor].marked {
				m.groups[m.cursor].marked[j] = false
			}
		case "U":
			// Unmark everything across all groups.
			for i := range m.groups {
				for j := range m.groups[i].marked {
					m.groups[i].marked[j] = false
				}
			}
		case "D":
			return m, m.execDelete()
		case "R":
			return m, m.execArchive()
		case "P":
			return m, m.execDryRun()
		}

	case viewFolderResults:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.cursor < len(m.folderMatches)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "a":
			// Mark current match (all folders except the kept one).
			for j := range m.folderMatches[m.cursor].marked {
				m.folderMatches[m.cursor].marked[j] = j != 0
			}
		case "u":
			// Unmark all folders in current match (skip it).
			for j := range m.folderMatches[m.cursor].marked {
				m.folderMatches[m.cursor].marked[j] = false
			}
		case "U":
			// Unmark everything across all matches.
			for i := range m.folderMatches {
				for j := range m.folderMatches[i].marked {
					m.folderMatches[i].marked[j] = false
				}
			}
		case "A":
			// Mark all duplicates across all matches.
			for i, fm := range m.folderMatches {
				for j := range fm.marked {
					m.folderMatches[i].marked[j] = j != 0
				}
			}
		case "D":
			return m, m.execFolderDelete()
		case "R":
			return m, m.execFolderArchive()
		}

	case viewDone:
		switch msg.String() {
		case "q", "ctrl+c", "enter":
			return m, tea.Quit
		}
	}

	return m, nil
}

// -- Commands ----------------------------------------------------------------

func (m *Model) startScan() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelScan = cancel
	folders := m.folders
	opts := m.opts

	// Buffered so progress callbacks never block the scan.
	progCh := make(chan scanProgressMsg, 64)
	m.progressCh = progCh

	progress := func(pass, done, total int) {
		select {
		case progCh <- scanProgressMsg{pass: pass, done: done, total: total}:
		default:
		}
	}

	var scanCmd tea.Cmd
	if m.folderMode {
		scanCmd = func() tea.Msg {
			matches, err := folderscanner.Scan(ctx, folders, opts, progress)
			close(progCh)
			return folderScanDoneMsg{matches: matches, err: err}
		}
	} else {
		scanCmd = func() tea.Msg {
			groups, err := scanner.Scan(ctx, folders, opts, progress)
			close(progCh)
			return scanDoneMsg{groups: groups, err: err}
		}
	}

	return tea.Batch(scanCmd, waitForProgress(progCh))
}

// waitForProgress blocks until the next progress event then re-subscribes.
func waitForProgress(ch chan scanProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}


func (m Model) markedGroups() []scanner.DuplicateGroup {
	var result []scanner.DuplicateGroup
	for _, gs := range m.groups {
		var files []scanner.FileInfo
		// Keep file is always index 0 in the synthetic group.
		files = append(files, gs.group.Files[gs.keepIdx])
		for j, f := range gs.group.Files {
			if j != gs.keepIdx && gs.marked[j] {
				files = append(files, f)
			}
		}
		if len(files) > 1 {
			result = append(result, scanner.DuplicateGroup{
				Hash:  gs.group.Hash,
				Size:  gs.group.Size,
				Files: files,
			})
		}
	}
	return result
}

func (m Model) execDryRun() tea.Cmd {
	groups := m.markedGroups()
	return func() tea.Msg {
		results := actions.DryRun(groups, m.strategy)
		return actionDoneMsg{results: results}
	}
}

func (m Model) execDelete() tea.Cmd {
	groups := m.markedGroups()
	return func() tea.Msg {
		results, err := actions.Delete(groups, m.strategy)
		return actionDoneMsg{results: results, err: err}
	}
}

func (m Model) execArchive() tea.Cmd {
	groups := m.markedGroups()
	dir := m.archiveDir
	if dir == "" {
		dir = os.TempDir() + "/broom-archive"
	}
	return func() tea.Msg {
		results, err := actions.Archive(groups, m.strategy, dir)
		return actionDoneMsg{results: results, err: err}
	}
}

func (m Model) execFolderDelete() tea.Cmd {
	var paths []string
	for _, fm := range m.folderMatches {
		for j, f := range fm.match.Folders {
			if fm.marked[j] {
				paths = append(paths, f.Path)
			}
		}
	}
	return func() tea.Msg {
		var results []actions.Result
		for _, p := range paths {
			if err := os.RemoveAll(p); err != nil {
				return actionDoneMsg{results: results, err: err}
			}
			results = append(results, actions.Result{Action: "deleted", Path: p})
		}
		return actionDoneMsg{results: results}
	}
}

func (m Model) execFolderArchive() tea.Cmd {
	type entry struct{ path string }
	var entries []entry
	for _, fm := range m.folderMatches {
		for j, f := range fm.match.Folders {
			if fm.marked[j] {
				entries = append(entries, entry{f.Path})
			}
		}
	}
	dir := m.archiveDir
	if dir == "" {
		dir = os.TempDir() + "/broom-archive"
	}
	return func() tea.Msg {
		var results []actions.Result
		for _, e := range entries {
			dest := filepath.Join(dir, filepath.Base(e.path))
			if err := os.MkdirAll(dir, 0755); err != nil {
				return actionDoneMsg{results: results, err: err}
			}
			if err := os.Rename(e.path, dest); err != nil {
				return actionDoneMsg{results: results, err: err}
			}
			results = append(results, actions.Result{Action: "archived", Path: dest, Original: e.path})
		}
		return actionDoneMsg{results: results}
	}
}

// -- View --------------------------------------------------------------------

func (m *Model) View() string {
	switch m.state {
	case viewPicker:
		return m.viewPicker()
	case viewScanning:
		return m.viewScanning()
	case viewResults:
		return m.viewResults()
	case viewFolderResults:
		return m.viewFolderResults()
	case viewDone:
		return m.viewDone()
	}
	return ""
}

func (m Model) viewPicker() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render(" 🧹 broom ") + "\n\n")
	b.WriteString(styleSubtle.Render("Folders to scan:") + "\n\n")

	if len(m.folders) == 0 {
		b.WriteString(styleDim.Render("  (no folders added — pass paths as arguments)") + "\n")
	}
	for i, f := range m.folders {
		prefix := "  "
		if i == m.cursor {
			prefix = styleSelected.Render("▸ ")
		}
		b.WriteString(prefix + f + "\n")
	}
	b.WriteString("\n")
	b.WriteString(styleBar.Render("[enter] scan  [d] remove folder  [j/k] navigate  [q] quit"))
	return styleBox.Render(b.String())
}

func (m *Model) viewScanning() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render(" 🧹 broom — scanning ") + "\n\n")

	passNames := []string{"", "Walking directories", "Quick hash (4KB)", "Full SHA256"}
	for i := 1; i <= 3; i++ {
		name := passNames[i]
		if i < m.scanPass {
			b.WriteString(styleKept.Render("  ✓ Pass "+fmt.Sprint(i)+" — "+name) + "\n\n")
		} else if i == m.scanPass {
			b.WriteString("  " + m.spinner.View() + " Pass " + fmt.Sprint(i) + " — " + name + "\n")
			if m.scanTotal > 0 {
				pct := int(float64(m.scanDone) / float64(m.scanTotal) * 100)
				b.WriteString("  " + m.progressBar.View() + "\n")
				if m.scanDone >= m.scanTotal {
					b.WriteString(styleSubtle.Render("  Finalizing results...") + "\n")
				} else {
					b.WriteString(styleSubtle.Render(fmt.Sprintf("  %d / %d files (%d%%)", m.scanDone, m.scanTotal, pct)) + "\n")
				}
			} else if m.scanDone > 0 {
				b.WriteString(styleSubtle.Render(fmt.Sprintf("  %d files found...", m.scanDone)) + "\n")
			} else {
				b.WriteString(styleSubtle.Render("  Starting...") + "\n")
			}
			b.WriteString("\n")
		} else {
			b.WriteString(styleDim.Render("  ○ Pass "+fmt.Sprint(i)+" — "+name) + "\n\n")
		}
	}
	b.WriteString(styleBar.Render("[q] cancel"))
	return styleBox.Render(b.String())
}

func (m Model) viewResults() string {
	if len(m.groups) == 0 {
		return styleBox.Render(styleKept.Render("\n  ✓ No duplicates found!\n\n") +
			styleBar.Render("  [q] quit"))
	}

	var b strings.Builder
	header := fmt.Sprintf(" 🧹 broom — %d groups · %s recoverable ", len(m.groups), humanBytes(m.totalDupes))
	b.WriteString(styleHeader.Render(header) + "\n\n")

	// Scroll based on terminal height so cursor is always visible.
	visible := m.height - 8
	if visible < 4 {
		visible = 4
	}
	start := 0
	if m.cursor > visible-3 {
		start = m.cursor - visible + 3
	}

	for i := start; i < len(m.groups) && i < start+visible; i++ {
		gs := m.groups[i]
		cursor := "  "
		if i == m.cursor {
			cursor = styleSelected.Render("▸ ")
		}
		expand := "▶"
		if gs.expanded {
			expand = "▼"
		}
		b.WriteString(fmt.Sprintf("%s%s group %d · %s · %d files\n",
			cursor, expand, i+1, humanBytes(gs.group.Size), len(gs.group.Files)))

		if gs.expanded {
			for j, f := range gs.group.Files {
				check := "[ ]"
				style := styleSubtle
				if gs.marked[j] {
					check = styleMarked.Render("[x]")
					style = styleMarked
				}
				keepMark := ""
				if j == gs.keepIdx {
					keepMark = styleKept.Render(" ★")
					style = styleKept
				}
				b.WriteString(fmt.Sprintf("    %s %s%s\n", check, style.Render(truncatePath(f.Path, m.width-12)), keepMark))
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(styleBar.Render("[j/k] navigate  [e/space] expand  [K] auto-keep newest  [A] select all  [U] unselect all  [a] select group  [u] skip group"))
	b.WriteString("\n")
	b.WriteString(styleBar.Render("[D] delete marked  [R] archive marked  [P] dry-run preview  [q] quit"))

	return styleBox.Render(b.String())
}

func (m *Model) viewFolderResults() string {
	if len(m.folderMatches) == 0 {
		return styleBox.Render(styleKept.Render("\n  ✓ No duplicate folders found!\n\n") +
			styleBar.Render("  [q] quit"))
	}

	var b strings.Builder
	header := fmt.Sprintf(" 🧹 broom — %d duplicate folders · %s recoverable ",
		len(m.folderMatches), humanBytes(m.totalFolderDupes))
	b.WriteString(styleHeader.Render(header) + "\n\n")

	// Each match renders 1 header line + N folder lines + 1 blank line.
	// Scroll so the cursor is always visible within the terminal height.
	viewport := m.height - 8 // reserve header + footer lines
	if viewport < 4 {
		viewport = 4
	}

	// Find the start index that keeps cursor in view.
	// Track cumulative line count to determine scroll position.
	linesBeforeCursor := 0
	for i := 0; i < m.cursor; i++ {
		linesBeforeCursor += len(m.folderMatches[i].match.Folders) + 2
	}
	start := 0
	lineCount := 0
	for i := 0; i < len(m.folderMatches); i++ {
		linesForMatch := len(m.folderMatches[i].match.Folders) + 2
		if lineCount+linesForMatch > viewport && i <= m.cursor {
			start = i
			lineCount = 0
		}
		if i == m.cursor {
			break
		}
		lineCount += linesForMatch
	}
	_ = linesBeforeCursor

	rendered := 0
	for i := start; i < len(m.folderMatches); i++ {
		fm := m.folderMatches[i]
		linesForMatch := len(fm.match.Folders) + 2
		if rendered+linesForMatch > viewport {
			break
		}
		rendered += linesForMatch

		cursor := "  "
		if i == m.cursor {
			cursor = styleSelected.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s⊟ match %d · %s · %d folders · %d files\n",
			cursor, i+1, humanBytes(fm.match.Size), len(fm.match.Folders), fm.match.FileCount))

		for j, f := range fm.match.Folders {
			check := "[ ]"
			style := styleKept
			marker := styleKept.Render(" ★ keep")
			if fm.marked[j] {
				check = styleMarked.Render("[x]")
				style = styleMarked
				marker = ""
			}
			b.WriteString(fmt.Sprintf("    %s %s%s\n",
				check, style.Render(truncatePath(f.Path, m.width-14)), marker))
		}
		b.WriteString("\n")
	}

	b.WriteString(styleBar.Render("[j/k] navigate  [A] select all  [U] unselect all  [a] select match  [u] skip match"))
	b.WriteString("\n")
	b.WriteString(styleBar.Render("[D] delete marked folders  [R] archive marked folders  [q] quit"))
	return styleBox.Render(b.String())
}

func (m Model) viewDone() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render(" 🧹 broom — done ") + "\n\n")

	if m.doneErr != nil {
		b.WriteString(styleMarked.Render("  Error: "+m.doneErr.Error()) + "\n")
	} else {
		kept, removed, archived, wouldDelete := 0, 0, 0, 0
		for _, r := range m.doneResults {
			switch r.Action {
			case "kept":
				kept++
			case "deleted":
				removed++
			case "archived":
				archived++
			case "would-delete":
				wouldDelete++
			}
		}
		if wouldDelete > 0 {
			b.WriteString(fmt.Sprintf("  %s  %d files would be removed\n", styleSubtle.Render("dry run:"), wouldDelete))
		}
		if removed > 0 {
			b.WriteString(fmt.Sprintf("  %s  %d files deleted\n", styleMarked.Render("deleted:"), removed))
		}
		if archived > 0 {
			b.WriteString(fmt.Sprintf("  %s  %d files archived\n", styleKept.Render("archived:"), archived))
		}
		b.WriteString(fmt.Sprintf("  %s  %d files kept\n", styleKept.Render("kept:"), kept))
	}

	b.WriteString("\n" + styleBar.Render("[enter/q] quit"))
	return styleBox.Render(b.String())
}

// -- Helpers -----------------------------------------------------------------

// truncatePath shortens a path to maxLen by keeping the end (most informative part).
// e.g. "/very/long/path/to/file.txt" → "…/path/to/file.txt"
func truncatePath(path string, maxLen int) string {
	if maxLen <= 0 || len(path) <= maxLen {
		return path
	}
	return "…" + path[len(path)-maxLen+1:]
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
