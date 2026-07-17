package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/AdityaAgrawal08/uplink-delta/cli/wan"
)

type tuiState int

const (
	stateLoading tuiState = iota
	stateList
	stateDownloading
	stateError
)

type tickMsg struct{}
type clearStatusMsg struct{}

type updateListMsg struct {
	files        []FileItem
	participants []ParticipantInfo
	err          error
}

type downloadProgressMsg struct {
	fileId   string
	filename string
	written  int64
	total    int64
}

type downloadDoneMsg struct {
	fileId   string
	filename string
	err      error
}

type heartbeatFailMsg struct {
	failed bool
}

type model struct {
	state          tuiState
	list           list.Model
	spinner        spinner.Model
	files          []FileItem
	participants   []ParticipantInfo
	selected       map[string]bool
	session        *ActiveSession
	err            error
	width          int
	height         int
	downloadStatus string
	downloadsDone  int
	downloadsTotal int
	lastUpdated    time.Time
	statusMsg      string
	statusTime     time.Time
	heartbeatFail  bool
	
	// download queue
	downloadQueue []FileItem
	isDownloading bool
	
	// cancel functions
	ctxCancel context.CancelFunc
}

func initialModel(sess *ActiveSession, cancel context.CancelFunc) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	selectedMap := make(map[string]bool)
	delegate := itemDelegate{
		selected: selectedMap,
	}

	fileList := list.New([]list.Item{}, delegate, 0, 0)
	fileList.Title = "Session Files"
	fileList.SetShowHelp(false)

	return model{
		state:        stateLoading,
		list:         fileList,
		spinner:      s,
		selected:     selectedMap,
		session:      sess,
		lastUpdated:  time.Now(),
		ctxCancel:    cancel,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		fetchFilesCmd(m.session.Server, m.session.SessionId),
		pollFilesCmd(),
	)
}

func fetchFilesCmd(server, sessionId string) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("%s/api/v1/session/%s/files", server, sessionId)
		resp, err := http.Get(url)
		if err != nil {
			return updateListMsg{err: err}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return updateListMsg{err: fmt.Errorf("HTTP status %d", resp.StatusCode)}
		}

		var res SessionFilesResponse
		err = json.NewDecoder(resp.Body).Decode(&res)
		if err != nil {
			return updateListMsg{err: err}
		}
		return updateListMsg{files: res.Files, participants: res.Participants}
	}
}

func pollFilesCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-8)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		cmds = append(cmds, fetchFilesCmd(m.session.Server, m.session.SessionId))
		cmds = append(cmds, pollFilesCmd())

	case updateListMsg:
		if msg.err != nil {
			if m.state == stateLoading {
				m.state = stateError
				m.err = msg.err
			} else {
				m.statusMsg = fmt.Sprintf("Refresh Error: %v", msg.err)
				m.statusTime = time.Now()
				cmds = append(cmds, clearStatusCmd())
			}
		} else {
			m.files = msg.files
			m.participants = msg.participants
			m.lastUpdated = time.Now()

			if m.state == stateLoading {
				m.state = stateList
			}

			// Map files to list.Item
			items := make([]list.Item, len(m.files))
			for i, f := range m.files {
				items[i] = f
			}
			m.list.SetItems(items)
		}

	case heartbeatFailMsg:
		m.heartbeatFail = msg.failed
		if msg.failed {
			m.statusMsg = "Warning: Heartbeat disconnected. Reconnecting..."
			m.statusTime = time.Now()
		}

	case downloadProgressMsg:
		m.downloadStatus = fmt.Sprintf("Downloading %s... (%s/%s)",
			msg.filename,
			formatBytes(msg.written),
			formatBytes(msg.total),
		)

	case downloadDoneMsg:
		m.downloadsDone++
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Download failed: %s: %v", msg.filename, msg.err)
			m.statusTime = time.Now()
		} else {
			m.statusMsg = fmt.Sprintf("Successfully downloaded %s", msg.filename)
			m.statusTime = time.Now()
			// Uncheck it
			m.selected[msg.fileId] = false
		}
		cmds = append(cmds, clearStatusCmd())
		
		// Process next file in queue
		m.isDownloading = false
		if len(m.downloadQueue) > 0 {
			nextFile := m.downloadQueue[0]
			m.downloadQueue = m.downloadQueue[1:]
			m.isDownloading = true
			cmds = append(cmds, m.downloadFileCmd(nextFile))
		} else {
			m.state = stateList
			m.downloadStatus = ""
		}

	case clearStatusMsg:
		if time.Since(m.statusTime) >= 4*time.Second {
			m.statusMsg = ""
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.ctxCancel() // cancel heartbeat loop
			return m, tea.Quit

		case "Q":
			m.ctxCancel()
			_ = DeleteActiveSession()
			return m, tea.Quit

		case "space":
			if m.state == stateList && len(m.files) > 0 {
				if item := m.list.SelectedItem(); item != nil {
					file := item.(FileItem)
					m.selected[file.FileId] = !m.selected[file.FileId]
				}
			}

		case "r":
			m.statusMsg = "Refreshing file list..."
			m.statusTime = time.Now()
			cmds = append(cmds, fetchFilesCmd(m.session.Server, m.session.SessionId))
			cmds = append(cmds, clearStatusCmd())

		case "enter":
			if m.state == stateList {
				// Queue all selected files
				var queue []FileItem
				for _, f := range m.files {
					if m.selected[f.FileId] && f.Status == "UPLOADED" {
						queue = append(queue, f)
					}
				}

				if len(queue) > 0 {
					m.state = stateDownloading
					m.downloadsDone = 0
					m.downloadsTotal = len(queue)
					m.downloadQueue = queue
					m.isDownloading = true
					
					nextFile := m.downloadQueue[0]
					m.downloadQueue = m.downloadQueue[1:]
					cmds = append(cmds, m.downloadFileCmd(nextFile))
				}
			}
		}
	}

	if m.state == stateList {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func clearStatusCmd() tea.Cmd {
	return tea.Tick(4*time.Second, func(t time.Time) tea.Msg {
		return clearStatusMsg{}
	})
}

// Download Command - Tries P2P first, falls back to R2 Server
func (m model) downloadFileCmd(file FileItem) tea.Cmd {
	return func() tea.Msg {
		cfg := LoadConfig()
		destDir := cfg.DownloadDir
		if destDir == "" {
			var err error
			destDir, err = os.Getwd()
			if err != nil {
				return downloadDoneMsg{fileId: file.FileId, filename: file.Filename, err: err}
			}
		}
		destPath := filepath.Join(destDir, file.Filename)

		// 1. Resolve peer P2P address if uploader is active
		var uploaderPeerID string
		var uploaderAddrs []string
		for _, p := range m.participants {
			if p.Username == file.Username && p.PeerID != "" && len(p.Addrs) > 0 {
				uploaderPeerID = p.PeerID
				uploaderAddrs = p.Addrs
				break
			}
		}

		// 2. Try P2P download first
		if uploaderPeerID != "" && len(uploaderAddrs) > 0 {
			// Announce connecting to P2P
			globalProgram.Send(downloadProgressMsg{fileId: file.FileId, filename: file.Filename, written: 0, total: file.Size})
			
			// Setup background libp2p host temporarily for client or reuse m.session
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			err := wan.DownloadFileWAN(ctx, file.ShareId, destPath, m.session.Password, file.SHA256, func(written int64) {
				globalProgram.Send(downloadProgressMsg{fileId: file.FileId, filename: file.Filename, written: written, total: file.Size})
			})
			if err == nil {
				return downloadDoneMsg{fileId: file.FileId, filename: file.Filename, err: nil}
			}
			// If P2P failed, fall through to HTTP R2 download
			os.Remove(destPath)
		}

		// 3. Fallback to Server HTTP download
		url := fmt.Sprintf("%s/api/v1/session/%s/download/%s", m.session.Server, m.session.SessionId, file.FileId)
		req, err := http.NewRequest("POST", url, nil)
		if err != nil {
			return downloadDoneMsg{fileId: file.FileId, filename: file.Filename, err: err}
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return downloadDoneMsg{fileId: file.FileId, filename: file.Filename, err: err}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return downloadDoneMsg{fileId: file.FileId, filename: file.Filename, err: fmt.Errorf("auth status %d", resp.StatusCode)}
		}

		var dlRes SessionDownloadResponse
		err = json.NewDecoder(resp.Body).Decode(&dlRes)
		if err != nil {
			return downloadDoneMsg{fileId: file.FileId, filename: file.Filename, err: err}
		}

		err = DownloadResumable(dlRes.DownloadUrl, destPath, dlRes.HashValue, func(written int64, resumeOffset int64) {
			globalProgram.Send(downloadProgressMsg{fileId: file.FileId, filename: file.Filename, written: written, total: file.Size})
		})

		return downloadDoneMsg{fileId: file.FileId, filename: file.Filename, err: err}
	}
}

func (m model) View() tea.View {
	var s string

	// Title style
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("255")).
		Background(lipgloss.Color("93")). // Vibrant purple title
		Padding(0, 1)

	headerText := fmt.Sprintf("SESSION: %s  ·  USER: %s  ·  %d PARTICIPANTS",
		m.session.SessionId,
		m.session.Username,
		len(m.participants),
	)
	s += titleStyle.Render(headerText) + "\n\n"

	switch m.state {
	case stateLoading:
		s += fmt.Sprintf(" %s Loading collaborative files...\n", m.spinner.View())

	case stateError:
		s += lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(fmt.Sprintf(" ✗ Error: %v\n", m.err))
		s += "\n Press q or ctrl+c to quit\n"

	case stateList, stateDownloading:
		s += m.list.View() + "\n"

		// Background Uploads progress reporting
		UploadsMutex.Lock()
		activeUploadsCount := 0
		var uploadsText []string
		for _, up := range ActiveUploads {
			if up.Status == "UPLOADING" || up.Status == "ANNOUNCED" {
				activeUploadsCount++
				progress := 0.0
				if up.Size > 0 {
					progress = float64(up.Uploaded) / float64(up.Size) * 100
				}
				uploadsText = append(uploadsText, fmt.Sprintf(" ↑ %s: %.1f%%", up.Filename, progress))
			}
		}
		UploadsMutex.Unlock()

		if activeUploadsCount > 0 {
			s += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render(strings.Join(uploadsText, " | ")) + "\n"
		}

		if m.state == stateDownloading {
			prog := fmt.Sprintf(" [%d/%d] %s", m.downloadsDone+1, m.downloadsTotal, m.downloadStatus)
			s += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Render(prog) + "\n"
		}

		// Status / Notification Bar
		if m.statusMsg != "" {
			s += "\n " + lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(m.statusMsg) + "\n"
		} else {
			s += "\n Last updated: " + m.lastUpdated.Format("15:04:05") + "\n"
		}

		// Heartbeat status warning
		if m.heartbeatFail {
			s += " " + lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("⚠️ Heartbeat disconnected! Server offline?") + "\n"
		}

		// Footer Help
		s += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Render(
			" space: select  ·  enter: download  ·  r: refresh  ·  q: close TUI  ·  Q: leave session",
		)
	}

	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

var globalProgram *tea.Program

func StartTUI(sess *ActiveSession) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Start session P2P listener if possible
	_ = StartSessionP2PListener(ctx, sess.Password)

	// 2. Start heartbeat loop
	var p2pId string
	var p2pAddrs []string
	if P2PPeer != nil {
		p2pId = P2PPeerID
		p2pAddrs = P2PAddrs
	}
	
	go StartHeartbeat(ctx, sess.SessionId, sess.Username, sess.Server, p2pId, p2pAddrs, func(err error) {
		if globalProgram != nil {
			globalProgram.Send(heartbeatFailMsg{failed: true})
		}
	})

	m := initialModel(sess, cancel)
	globalProgram = tea.NewProgram(m)

	if _, err := globalProgram.Run(); err != nil {
		fmt.Printf("✗ TUI Error: %v\n", err)
		os.Exit(1)
	}
}
