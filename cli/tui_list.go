package main

import (
	"fmt"
	"io"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type itemDelegate struct {
	selected map[string]bool
}

func (d itemDelegate) Height() int {
	return 1
}

func (d itemDelegate) Spacing() int {
	return 0
}

func (d itemDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd {
	return nil
}

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	if item == nil {
		return
	}
	file, ok := item.(FileItem)
	if !ok {
		return
	}

	isSelected := m.Index() == index

	checkbox := "[ ]"
	if d.selected[file.FileId] {
		checkbox = "[✓]"
	}

	statusStr := "ready"
	if file.Status == "ANNOUNCED" {
		statusStr = "sending..."
	} else if file.Status == "UPLOAD_FAILED" {
		statusStr = "failed"
	}

	// Apply styles
	var style lipgloss.Style
	if isSelected {
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Background(lipgloss.Color("236")).Bold(true) // Hot pink highlight
	} else {
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	}

	var checkboxStyle lipgloss.Style
	if d.selected[file.FileId] {
		checkboxStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")) // Emerald Green
	} else {
		checkboxStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // Muted Grey
	}

	var statusStyle lipgloss.Style
	switch file.Status {
	case "ANNOUNCED":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // Amber Orange
	case "UPLOAD_FAILED":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // Crimson Red
	default:
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")) // Sky Blue
	}

	filenameStr := file.Filename
	if len(filenameStr) > 28 {
		filenameStr = filenameStr[:25] + "..."
	}

	line := fmt.Sprintf("%s %s %s %s %s %s",
		style.Render(fmt.Sprintf("%2d.", index+1)),
		checkboxStyle.Render(checkbox),
		style.Render(fmt.Sprintf("%-28s", filenameStr)),
		style.Render(fmt.Sprintf("%-12s", file.Username)),
		style.Render(fmt.Sprintf("%8s", formatBytes(file.Size))),
		statusStyle.Render(fmt.Sprintf("%-10s", statusStr)),
	)

	fmt.Fprint(w, line)
}

// Implement list.Item interface
func (f FileItem) FilterValue() string { return f.Filename }
func (f FileItem) Title() string       { return f.Filename }
func (f FileItem) Description() string {
	return fmt.Sprintf("%s · %s · %s", f.Username, formatBytes(f.Size), f.Status)
}
