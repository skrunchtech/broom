package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/skrunchtech/broom/internal/actions"
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
	viewPicker   viewState = iota // folder selection
	viewScanning                  // scanning in progress
	viewResults                   // duplicate groups
	viewDone                      // action completed
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

type Model struct {
	// Config
	opts       scanner.Options
	strategy   actions.KeepStrategy
	archiveDir string

	// View
	state     viewState
	width     int
	height    int
	cursor    int // selected group in results view
	fileCursor int // selected file within expanded group

	// Folder picker
	folders     []string
	pickerInput string

	// Scanning
	spinner     spinner.Model
	progressBar progress.Model
	scanPass    int
	scanDone    int
	scanTotal   int

	// Results
	groups      []groupState
	totalDupes  int64 // bytes recoverable

	// Done
	doneResults []actions.Result
	doneErr     error
}

func NewModel(folders []string, opts scanner.Options, strategy actions.KeepStrategy, archiveDir string) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleBrand

	pb := progress.New(progress.WithDefaultGradient())

	m := Model{
		opts:        opts,
		strategy:    strategy,
		archiveDir:  archiveDir,
		folders:     folders,
		spinner:     sp,
		progressBar: pb,
	}

	if len(folders) == 0 {
		m.state = viewPicker
	} else {
		m.state = viewPicker
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick)
}

// -- Update ------------------------------------------------------------------

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if msg.total > 0 {
			cmd := m.progressBar.SetPercent(float64(msg.done) / float64(msg.total))
			return m, cmd
		}
		return m, nil

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

	case actionDoneMsg:
		m.doneResults = msg.results
		m.doneErr = msg.err
		m.state = viewDone
		return m, nil
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {

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
		case "D":
			return m, m.execDelete()
		case "R":
			return m, m.execArchive()
		case "P":
			return m, m.execDryRun()
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

func (m Model) startScan() tea.Cmd {
	return func() tea.Msg {
		groups, err := scanner.Scan(m.folders, m.opts, func(pass, done, total int) {
			// Note: in a real app, send progress via channel; simplified here.
			_ = pass
			_ = done
			_ = total
		})
		return scanDoneMsg{groups: groups, err: err}
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

// -- View --------------------------------------------------------------------

func (m Model) View() string {
	switch m.state {
	case viewPicker:
		return m.viewPicker()
	case viewScanning:
		return m.viewScanning()
	case viewResults:
		return m.viewResults()
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

func (m Model) viewScanning() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render(" 🧹 broom — scanning ") + "\n\n")

	passNames := []string{"", "Walking directories", "Quick hash", "Full SHA256"}
	for i := 1; i <= 3; i++ {
		name := passNames[i]
		if i < m.scanPass {
			b.WriteString(styleKept.Render("  ✓ Pass "+fmt.Sprint(i)+" "+name) + "\n")
		} else if i == m.scanPass {
			b.WriteString("  " + m.spinner.View() + " Pass " + fmt.Sprint(i) + " " + name + "\n")
			if m.scanTotal > 0 {
				b.WriteString("  " + m.progressBar.View() + "\n")
			}
		} else {
			b.WriteString(styleDim.Render("  ○ Pass "+fmt.Sprint(i)+" "+name) + "\n")
		}
	}
	b.WriteString("\n" + styleBar.Render("[q] cancel"))
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

	// Show up to ~20 groups fitting in terminal.
	visible := 20
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
				b.WriteString(fmt.Sprintf("    %s %s%s\n", check, style.Render(f.Path), keepMark))
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(styleBar.Render("[j/k] navigate  [e/space] expand  [K] auto-keep newest  [A] select all"))
	b.WriteString("\n")
	b.WriteString(styleBar.Render("[D] delete marked  [R] archive marked  [P] dry-run preview  [q] quit"))

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
