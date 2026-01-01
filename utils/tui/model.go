package tui

import (
	"fmt"
	"math"
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

	tasksHeaderHeight = 2
	tasksTotalHeight  = 0 // will be calculated dynamically

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
	ID        string
	Name      string
	Status    string
	Progress  float64
	Speed     float64
	State     string // pending, running, done, error
	Prog      progress.Model
	StartTime time.Time
	EndTime   time.Time
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

	// Time tracking
	StartTime time.Time
	EndTime   time.Time
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
		// StartTime will be set when first task starts
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
		m.updateTasksViewport()

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
			case "pgup":
				m.selectionCursor -= m.height - 4
				if m.selectionCursor < 0 {
					m.selectionCursor = 0
				}
				if m.selectionCursor < m.selectionOffset {
					m.selectionOffset = m.selectionCursor
				}
			case "pgdown":
				m.selectionCursor += m.height - 4
				if m.selectionCursor >= len(m.selectionItems) {
					m.selectionCursor = len(m.selectionItems) - 1
				}
				if m.selectionCursor >= m.selectionOffset+m.height-4 {
					m.selectionOffset = m.selectionCursor - (m.height - 4) + 1
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

		var cmd tea.Cmd
		m.tasksViewport, cmd = m.tasksViewport.Update(msg)
		cmds = append(cmds, cmd)

	case tea.MouseMsg:
		if m.selectionMode {
			if msg.Type == tea.MouseWheelUp {
				if m.selectionCursor > 0 {
					m.selectionCursor--
					if m.selectionCursor < m.selectionOffset {
						m.selectionOffset = m.selectionCursor
					}
				}
			} else if msg.Type == tea.MouseWheelDown {
				if m.selectionCursor < len(m.selectionItems)-1 {
					m.selectionCursor++
					if m.selectionCursor >= m.selectionOffset+m.height-4 {
						m.selectionOffset = m.selectionCursor - (m.height - 4) + 1
					}
				}
			}
			return m, nil
		}

		var cmd tea.Cmd
		m.tasksViewport, cmd = m.tasksViewport.Update(msg)
		cmds = append(cmds, cmd)

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
			ID:        msg.ID,
			Name:      msg.Name,
			Status:    "Starting...",
			State:     "pending",
			Prog:      prog,
			Progress:  0,
			Speed:     0,
			StartTime: time.Now(),
		}
		m.tasks[msg.ID] = task
		m.taskKeys = append(m.taskKeys, msg.ID)
		cmds = append(cmds, task.Prog.SetPercent(0))

		// Start total timer if not started
		if m.StartTime.IsZero() {
			m.StartTime = time.Now()
		}
		m.updateTasksViewport()

	case UpdateTaskMsg:
		if task, ok := m.tasks[msg.ID]; ok {
			task.Status = msg.Message
			task.Progress = msg.Progress
			task.Speed = msg.Speed
			if msg.State != "" {
				task.State = msg.State
				// Stop timer if task is done or error
				if (msg.State == "done" || msg.State == "error") && task.EndTime.IsZero() {
					task.EndTime = time.Now()
				}
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
		m.updateTasksViewport()

	case UpdateTotalProgressMsg:
		m.totalPercent = msg.Progress
		if msg.Progress >= 1.0 && m.EndTime.IsZero() {
			m.EndTime = time.Now()
		}
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
		m.updateTasksViewport()
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
	// We need to account for the header which is NOT inside the viewport now
	tasksTotalHeight := availableHeight - totalViewHeight - logsActualHeight - tasksHeaderHeight - 1 // -1 for extra spacing
	if tasksTotalHeight < 5 {
		tasksTotalHeight = 5
	}

	// Apply dimensions
	m.viewport.Width = m.width - logStyle.GetHorizontalFrameSize()
	m.viewport.Height = logsViewportHeight

	m.tasksViewport.Width = m.width - 4
	m.tasksViewport.Height = tasksTotalHeight
}

func (m *Model) updateTasksViewport() {
	content := m.renderTaskViews()
	atBottom := m.tasksViewport.AtBottom()
	m.tasksViewport.SetContent(content)
	if atBottom {
		m.tasksViewport.GotoBottom()
	}
}

func (m Model) renderTaskViews() string {
	var taskViews []string
	// listHeaderStyle("Downloads") is removed from here
	for _, id := range m.taskKeys {
		t := m.tasks[id]
		icon := " "
		if t.State == "done" {
			icon = checkMark.String()
		} else if t.State == "error" {
			icon = xMark.String()
		}

		name := lipgloss.NewStyle().Width(20).Render(truncate(t.Name, 20))

		taskElapsed := time.Duration(0)
		if !t.StartTime.IsZero() {
			if !t.EndTime.IsZero() {
				taskElapsed = t.EndTime.Sub(t.StartTime)
			} else {
				taskElapsed = time.Since(t.StartTime)
			}
		}

		taskEta := calculateETA(taskElapsed, t.Progress)
		if t.State == "done" || t.State == "error" || !t.EndTime.IsZero() {
			taskEta = 0
		} else if t.State == "pending" {
			taskElapsed = 0
			taskEta = 0
		}

		timeInfo := fmt.Sprintf("%s / %s", formatDuration(taskElapsed), formatDuration(taskEta))

		// Use lipgloss.JoinHorizontal to better control alignment and avoid format string issues
		view := lipgloss.JoinVertical(lipgloss.Left,
			fmt.Sprintf("%s %s %s (%s)", icon, name, t.Status, timeInfo),
			t.Prog.View(),
		)
		taskViews = append(taskViews, listItemStyle(view))
	}

	return lipgloss.JoinVertical(lipgloss.Left, taskViews...)
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
	elapsed := time.Duration(0)
	if !m.StartTime.IsZero() {
		if !m.EndTime.IsZero() {
			elapsed = m.EndTime.Sub(m.StartTime)
		} else {
			elapsed = time.Since(m.StartTime)
		}
	}

	eta := calculateETA(elapsed, m.totalPercent)
	if m.StartTime.IsZero() || m.totalPercent == 0 || !m.EndTime.IsZero() {
		eta = 0 // Unknown or Done
	}

	title := fmt.Sprintf("Total Progress - %s - Elapsed: %s - ETA: %s",
		formatSpeed(m.totalSpeed),
		formatDuration(elapsed),
		formatDuration(eta),
	)
	totalView := lipgloss.JoinVertical(lipgloss.Left,
		totalHeaderStyle.Render(title),
		lipgloss.NewStyle().MarginLeft(2).PaddingTop(1).Render(m.totalProg.View()),
	)

	// 2. Render Tasks
	tasksHeader := listHeaderStyle("Downloads")
	tasksView := m.tasksViewport.View()

	// Add scrollbar for tasks if needed
	if m.tasksViewport.TotalLineCount() > m.tasksViewport.Height {
		// Simple ASCII scrollbar
		scrollPercent := m.tasksViewport.ScrollPercent()
		if scrollPercent < 0 {
			scrollPercent = 0
		}
		if scrollPercent > 1 {
			scrollPercent = 1
		}

		barHeight := m.tasksViewport.Height
		thumbHeight := int(math.Max(1, float64(barHeight)*float64(barHeight)/float64(m.tasksViewport.TotalLineCount())))
		thumbPos := int(scrollPercent * float64(barHeight-thumbHeight))

		var bar strings.Builder
		for i := 0; i < barHeight; i++ {
			if i >= thumbPos && i < thumbPos+thumbHeight {
				bar.WriteString("█\n")
			} else {
				bar.WriteString("│\n")
			}
		}

		tasksView = lipgloss.JoinHorizontal(lipgloss.Top, tasksView, " ", bar.String())
	}

	// 3. Render Logs
	logsHeader := listHeaderStyle("Logs")
	logsView := logStyle.Render(m.viewport.View())

	// Force logsView to have correct width/height if empty?
	// logStyle handles borders.

	// 4. Combine strictly
	// Use Place to force layout if needed, but JoinVertical is simpler
	// Add an empty line between totalView and tasksHeader
	return lipgloss.JoinVertical(lipgloss.Left,
		totalView,
		"",
		tasksHeader,
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

func calculateETA(elapsed time.Duration, progress float64) time.Duration {
	if progress <= 0 {
		return 0
	}
	if progress >= 1 {
		return 0
	}

	// Estimated Total Time = Elapsed / Progress
	// ETA = Estimated Total Time - Elapsed
	// ETA = (Elapsed / Progress) - Elapsed = Elapsed * (1/Progress - 1)

	etaNs := float64(elapsed.Nanoseconds()) * (1/progress - 1)
	return time.Duration(etaNs)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "--:--"
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
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
