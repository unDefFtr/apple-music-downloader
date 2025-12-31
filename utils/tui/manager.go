package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

var p *tea.Program

func Init() {
	if p == nil {
		m := NewModel()
		p = tea.NewProgram(m, tea.WithAltScreen())
	}
}

func Start() error {
	if p == nil {
		Init()
	}
	_, err := p.Run()
	return err
}

func SendLog(level, msg string) {
	if p != nil {
		p.Send(LogMsg{Level: level, Message: msg})
	}
}

func AddTask(id, name string) {
	if p != nil {
		p.Send(AddTaskMsg{ID: id, Name: name})
	}
}

func UpdateTask(id string, progress float64, msg string, state string, speed float64) {
	if p != nil {
		p.Send(UpdateTaskMsg{
			ID:       id,
			Progress: progress,
			Message:  msg,
			State:    state,
			Speed:    speed,
		})
	}
}

func UpdateTotalProgress(progress float64) {
	if p != nil {
		p.Send(UpdateTotalProgressMsg{
			Progress: progress,
		})
	}
}

func RequestSelection(title string, items []string) []int {
	if p != nil {
		resultChan := make(chan []int)
		p.Send(RequestSelectionMsg{
			Title:      title,
			Items:      items,
			ResultChan: resultChan,
		})
		return <-resultChan
	}
	return []int{}
}
