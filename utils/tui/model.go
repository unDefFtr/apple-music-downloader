package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}

	// Fixed height definitions
	totalHeaderHeight = 1
	totalBarHeight    = 2
	totalViewHeight   = totalHeaderHeight + totalBarHeight

	logsHeaderHeight = 2
	logsBorderHeight = 2

	// Styles
	listHeaderStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(subtle).
			MarginRight(2).
			Render

	totalHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF7DB")).
				Background(lipgloss.Color("#5A56E0")).
				MarginLeft(2).
				Padding(0, 1).
				Bold(true)

	listItemStyle = lipgloss.NewStyle().PaddingLeft(2).Render

	checkMark = lipgloss.NewStyle().Foreground(special).SetString("✓")
	xMark     = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).SetString("✗")

	// Logs styles
	logStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(highlight).
			Padding(0, 1) // Reduced padding
)

type DownloadTask struct {
	ID       string
	Name     string
	Status   string
	Progress float64
	Speed    float64
	State    string // pending, running, done, error
	Prog     progress.Model
}

type Model struct {
	tasks         map[string]*DownloadTask
	taskKeys      []string       // maintain order
	viewport      viewport.Model // logs viewport
	tasksViewport viewport.Model // tasks viewport
	logs          []string
	width         int
	height        int
	quitting      bool
	totalProg     progress.Model
	totalPercent  float64
	totalSpeed    float64

	// Selection Mode
	selectionMode   bool
	selectionTitle  string
	selectionItems  []string
	selectionCursor int
	selectedIndices map[int]struct{}
	selectionChan   chan []int
	selectionOffset int // for scrolling
}

type TickMsg time.Time

func NewModel() Model {
	totalProg := progress.New(progress.WithDefaultGradient(), progress.WithWidth(40))
	totalProg.ShowPercentage = true
	return Model{
		tasks:           make(map[string]*DownloadTask),
		taskKeys:        make([]string, 0),
		viewport:        viewport.New(80, 10),
		tasksViewport:   viewport.New(80, 10),
		logs:            make([]string, 0),
		totalProg:       totalProg,
		totalPercent:    0,
		totalSpeed:      0,
		selectedIndices: make(map[int]struct{}),
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateHeights()

		// Update existing tasks' progress bar width
		for _, task := range m.tasks {
			task.Prog.Width = msg.Width - 20
			if task.Prog.Width < 0 {
				task.Prog.Width = 0
			}
		}
		m.totalProg.Width = msg.Width - 20
		if m.totalProg.Width < 20 {
			m.totalProg.Width = 20
		}

	case tea.KeyMsg:
		if m.selectionMode {
			switch msg.String() {
			case "q", "esc":
				// Cancel selection? Or quit app?
				// "q" usually quits app. Let's make "esc" cancel selection (return empty)
				if msg.String() == "esc" {
					m.selectionMode = false
					if m.selectionChan != nil {
						m.selectionChan <- []int{}
						close(m.selectionChan)
						m.selectionChan = nil
					}
					return m, nil
				}
				m.quitting = true
				return m, tea.Quit
			case "up", "k":
				if m.selectionCursor > 0 {
					m.selectionCursor--
					if m.selectionCursor < m.selectionOffset {
						m.selectionOffset = m.selectionCursor
					}
				}
			case "down", "j":
				if m.selectionCursor < len(m.selectionItems)-1 {
					m.selectionCursor++
					if m.selectionCursor >= m.selectionOffset+m.height-4 { // Reserve some header space
						m.selectionOffset = m.selectionCursor - (m.height - 4) + 1
					}
				}
			case " ":
				_, ok := m.selectedIndices[m.selectionCursor]
				if ok {
					delete(m.selectedIndices, m.selectionCursor)
				} else {
					m.selectedIndices[m.selectionCursor] = struct{}{}
				}
			case "enter":
				m.selectionMode = false
				res := make([]int, 0, len(m.selectedIndices))
				for i := range m.selectedIndices {
					res = append(res, i+1) // Return 1-based index as expected by task/album.go
				}
				if m.selectionChan != nil {
					m.selectionChan <- res
					close(m.selectionChan)
					m.selectionChan = nil
				}
			case "a":
				if len(m.selectedIndices) == len(m.selectionItems) {
					m.selectedIndices = make(map[int]struct{})
				} else {
					for i := range m.selectionItems {
						m.selectedIndices[i] = struct{}{}
					}
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		}

	case RequestSelectionMsg:
		m.selectionMode = true
		m.selectionTitle = msg.Title
		m.selectionItems = msg.Items
		m.selectionChan = msg.ResultChan
		m.selectionCursor = 0
		m.selectionOffset = 0
		m.selectedIndices = make(map[int]struct{})
		return m, nil

	case AddTaskMsg:
		prog := progress.New(progress.WithDefaultGradient())
		width := m.width
		if width == 0 {
			width = 80
		}
		prog.Width = width - 20
		if prog.Width < 0 {
			prog.Width = 0
		}

		task := &DownloadTask{
			ID:       msg.ID,
			Name:     msg.Name,
			Status:   "Starting...",
			State:    "pending",
			Prog:     prog,
			Progress: 0,
			Speed:    0,
		}
		m.tasks[msg.ID] = task
		m.taskKeys = append(m.taskKeys, msg.ID)
		cmds = append(cmds, task.Prog.SetPercent(0))

	case UpdateTaskMsg:
		if task, ok := m.tasks[msg.ID]; ok {
			task.Status = msg.Message
			task.Progress = msg.Progress
			task.Speed = msg.Speed
			if msg.State != "" {
				task.State = msg.State
			}
			cmd := task.Prog.SetPercent(msg.Progress)
			cmds = append(cmds, cmd)

			// Recalculate total speed
			var totalSpeed float64
			for _, t := range m.tasks {
				// Only count speed for running tasks, though others should be 0 anyway
				totalSpeed += t.Speed
			}
			m.totalSpeed = totalSpeed
		}

	case UpdateTotalProgressMsg:
		m.totalPercent = msg.Progress
		cmd := m.totalProg.SetPercent(msg.Progress)
		cmds = append(cmds, cmd)

	case LogMsg:
		logLine := fmt.Sprintf("[%s] %s", msg.Level, msg.Message)
		// Wrap content
		width := m.viewport.Width
		if width > 0 {
			logLine = wordwrap.String(logLine, width)
		}
		m.logs = append(m.logs, logLine)
		// Keep only last 1000 logs
		if len(m.logs) > 1000 {
			m.logs = m.logs[len(m.logs)-1000:]
		}
		m.viewport.SetContent(strings.Join(m.logs, "\n"))
		m.viewport.GotoBottom()

	// Progress bar tick
	case progress.FrameMsg:
		for _, task := range m.tasks {
			progressModel, cmd := task.Prog.Update(msg)
			task.Prog = progressModel.(progress.Model)
			cmds = append(cmds, cmd)
		}
		progressModel, cmd := m.totalProg.Update(msg)
		m.totalProg = progressModel.(progress.Model)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) recalculateHeights() {
	availableHeight := m.height

	// 1. Logs Area (approx 1/3, max 10 lines to avoid taking too much space)
	logsTotalHeight := availableHeight / 3
	if logsTotalHeight > 15 {
		logsTotalHeight = 15
	}
	if logsTotalHeight < 7 {
		logsTotalHeight = 7
	}

	logsViewportHeight := logsTotalHeight - logsHeaderHeight - logsBorderHeight
	if logsViewportHeight < 1 {
		logsViewportHeight = 1
	}
	logsActualHeight := logsHeaderHeight + logsBorderHeight + logsViewportHeight

	// 2. Tasks Area
	tasksTotalHeight := availableHeight - totalViewHeight - logsActualHeight
	if tasksTotalHeight < 5 {
		tasksTotalHeight = 5
	}

	// Apply dimensions
	m.viewport.Width = m.width - logStyle.GetHorizontalFrameSize()
	m.viewport.Height = logsViewportHeight

	m.tasksViewport.Width = m.width - 4
	m.tasksViewport.Height = tasksTotalHeight
}

func (m Model) View() string {
	if m.quitting {
		return "Bye!\n"
	}

	if m.selectionMode {
		var s strings.Builder
		s.WriteString(totalHeaderStyle.Render(m.selectionTitle) + "\n\n")

		s.WriteString("  [Space] Select/Deselect  [a] Select All  [Enter] Confirm  [Esc] Cancel\n\n")

		// Calculate visible range
		start := m.selectionOffset
		end := start + m.height - 4 // Approximate visible items
		if end > len(m.selectionItems) {
			end = len(m.selectionItems)
		}

		for i := start; i < end; i++ {
			cursor := " "
			if m.selectionCursor == i {
				cursor = ">"
			}

			checked := "[ ]"
			if _, ok := m.selectedIndices[i]; ok {
				checked = "[x]"
			}

			line := fmt.Sprintf("%s %s %s", cursor, checked, m.selectionItems[i])
			if m.selectionCursor == i {
				line = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Render(line)
			}
			s.WriteString(line + "\n")
		}
		return s.String()
	}

	// 1. Render Total Progress
	title := fmt.Sprintf("Total Progress - %s", formatSpeed(m.totalSpeed))
	totalView := lipgloss.JoinVertical(lipgloss.Left,
		totalHeaderStyle.Render(title),
		lipgloss.NewStyle().MarginLeft(2).PaddingTop(1).Render(m.totalProg.View()),
	)

	// 2. Render Tasks
	var taskViews []string
	taskViews = append(taskViews, listHeaderStyle("Downloads"))
	for _, id := range m.taskKeys {
		t := m.tasks[id]
		icon := " "
		if t.State == "done" {
			icon = checkMark.String()
		} else if t.State == "error" {
			icon = xMark.String()
		}

		name := lipgloss.NewStyle().Width(20).Render(truncate(t.Name, 20))
		// Use lipgloss.JoinHorizontal to better control alignment and avoid format string issues
		view := lipgloss.JoinVertical(lipgloss.Left,
			fmt.Sprintf("%s %s %s", icon, name, t.Status),
			t.Prog.View(),
		)
		taskViews = append(taskViews, listItemStyle(view))
	}

	tasksContent := lipgloss.JoinVertical(lipgloss.Left, taskViews...)
	m.tasksViewport.SetContent(tasksContent)
	m.tasksViewport.GotoBottom()
	tasksView := m.tasksViewport.View()

	// 3. Render Logs
	logsHeader := listHeaderStyle("Logs")
	logsView := logStyle.Render(m.viewport.View())

	// Force logsView to have correct width/height if empty?
	// logStyle handles borders.

	// 4. Combine strictly
	// Use Place to force layout if needed, but JoinVertical is simpler
	return lipgloss.JoinVertical(lipgloss.Left,
		totalView,
		tasksView,
		logsHeader,
		logsView,
	)
}

func truncate(s string, max int) string {
	if lipgloss.Width(s) <= max {
		return s
	}
	var w int
	var res []rune
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > max-3 {
			return string(res) + "..."
		}
		w += rw
		res = append(res, r)
	}
	return string(res) + "..."
}

func formatSpeed(bps float64) string {
	if bps >= 1024*1024 {
		return fmt.Sprintf("%.2f MB/s", bps/(1024*1024))
	} else if bps >= 1024 {
		return fmt.Sprintf("%.2f KB/s", bps/1024)
	}
	return fmt.Sprintf("%.0f B/s", bps)
}

// Messages
type AddTaskMsg struct {
	ID   string
	Name string
}

type UpdateTaskMsg struct {
	ID       string
	Progress float64
	Message  string
	State    string
	Speed    float64
}

type UpdateTotalProgressMsg struct {
	Progress float64
}

type LogMsg struct {
	Level   string
	Message string
}

type RequestSelectionMsg struct {
	Title      string
	Items      []string
	ResultChan chan []int
}
