package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AdityaAgrawal08/uplink-delta/cli/wan"
	"github.com/libp2p/go-libp2p/core/network"
)

type ActiveSession struct {
	SessionId string    `json:"sessionId"`
	Username  string    `json:"username"`
	Server    string    `json:"server"`
	JoinedAt  time.Time `json:"joinedAt"`
	Password  string    `json:"password,omitempty"`
}

type SessionCreateRequest struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Duration int    `json:"duration,omitempty"`
}

type SessionCreateResponse struct {
	SessionId string `json:"sessionId"`
}

type SessionJoinRequest struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

type SessionJoinResponse struct {
	SessionId    string   `json:"sessionId"`
	Participants []string `json:"participants"`
}

type FileItem struct {
	FileId        string    `json:"fileId"`
	ShareId       string    `json:"shareId"`
	Filename      string    `json:"filename"`
	Username      string    `json:"username"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	UploadedAt    time.Time `json:"uploadedAt"`
	Status        string    `json:"status"` // "ANNOUNCED", "UPLOADED", "UPLOAD_FAILED"
	EncryptionKey string    `json:"encryptionKey,omitempty"`
}

type ParticipantInfo struct {
	Username string   `json:"username"`
	PeerID   string   `json:"peerId"`
	Addrs    []string `json:"addrs"`
}

type SessionFilesResponse struct {
	Files        []FileItem        `json:"files"`
	Participants []ParticipantInfo `json:"participants"`
}

type SessionAnnounceRequest struct {
	Filename      string `json:"filename"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	EncryptionKey string `json:"encryptionKey,omitempty"`
}

type SessionAnnounceResponse struct {
	FileId  string `json:"fileId"`
	ShareId string `json:"shareId"`
}

type SessionUploadCompleteRequest struct {
	FileId  string `json:"fileId"`
	ShareId string `json:"shareId"`
}

type SessionDownloadResponse struct {
	DownloadUrl string `json:"downloadUrl"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	MimeType    string `json:"mimeType"`
	HashValue   string `json:"hashValue"`
}

func getSessionFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".uplink", "active_session.json")
}

func SaveActiveSession(sess *ActiveSession) error {
	path := getSessionFilePath()
	dir := filepath.Dir(path)
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer f.Close()

	err = json.NewEncoder(f).Encode(sess)
	if err != nil {
		return err
	}
	f.Close()
	return os.Rename(tmp, path)
}

func LoadActiveSession() (*ActiveSession, error) {
	path := getSessionFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sess ActiveSession
	err = json.Unmarshal(data, &sess)
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

func DeleteActiveSession() error {
	return os.Remove(getSessionFilePath())
}

// Session API HTTP Clients
func CreateSession(server, username, password string, duration int) (string, error) {
	url := fmt.Sprintf("%s/api/v1/session/create", server)
	reqObj := SessionCreateRequest{
		Username: username,
		Password: password,
		Duration: duration,
	}
	jsonBytes, err := json.Marshal(reqObj)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if msg, ok := errResp["error"]; ok {
			return "", fmt.Errorf("status %d: %s", resp.StatusCode, msg)
		}
		return "", fmt.Errorf("failed to create session (status %d)", resp.StatusCode)
	}

	var res SessionCreateResponse
	err = json.NewDecoder(resp.Body).Decode(&res)
	if err != nil {
		return "", err
	}
	return res.SessionId, nil
}

func JoinSession(server, sessionId, username, password string) ([]string, error) {
	url := fmt.Sprintf("%s/api/v1/session/%s/join", server, sessionId)
	reqObj := SessionJoinRequest{
		Username: username,
		Password: password,
	}
	jsonBytes, err := json.Marshal(reqObj)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if msg, ok := errResp["error"]; ok {
			return nil, fmt.Errorf("status %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("failed to join session (status %d)", resp.StatusCode)
	}

	var res SessionJoinResponse
	err = json.NewDecoder(resp.Body).Decode(&res)
	if err != nil {
		return nil, err
	}
	return res.Participants, nil
}

var (
	P2PPeer           *wan.WANPeer
	P2PPeerID         string
	P2PAddrs          []string
	LocalFilesMutex   sync.Mutex
	SessionLocalFiles = make(map[string]string)
)

func StartSessionP2PListener(ctx context.Context, sessionPassword string) error {
	peer, err := wan.StartWANPeer(ctx)
	if err != nil {
		return err
	}
	P2PPeer = peer
	P2PPeerID = peer.Host.ID().String()

	// Collect multiaddresses
	for _, addr := range peer.Host.Addrs() {
		P2PAddrs = append(P2PAddrs, fmt.Sprintf("%s/p2p/%s", addr.String(), P2PPeerID))
	}

	peer.Host.SetStreamHandler("/uplink-p2p/1.0.0", func(s network.Stream) {
		defer s.Close()

		// Read shareCode (which is the shareId in session context)
		buf := make([]byte, 256)
		n, err := s.Read(buf)
		if err != nil {
			return
		}
		shareId := string(buf[:n])

		// Read password
		n, err = s.Read(buf)
		if err != nil {
			return
		}
		password := string(buf[:n])

		// Verify password (constant time check)
		if subtle.ConstantTimeCompare([]byte(password), []byte(sessionPassword)) != 1 {
			return
		}

		// Look up local file path
		LocalFilesMutex.Lock()
		filePath, exists := SessionLocalFiles[shareId]
		LocalFilesMutex.Unlock()

		if !exists {
			return
		}

		f, err := os.Open(filePath)
		if err != nil {
			return
		}
		defer f.Close()

		_, _ = io.Copy(s, f)
	})

	return nil
}
