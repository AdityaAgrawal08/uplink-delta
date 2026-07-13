package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type SendFlags struct {
	Password string `json:"password"`
	Expire   string `json:"expire"`
	Server   string `json:"server"`
	Lan      bool   `json:"lan"`
	Encrypt  bool   `json:"encrypt"`
}

type QueueItem struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	Filename      string    `json:"filename"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	Status        string    `json:"status"` // pending | uploading | paused | completed | failed
	Retries       int       `json:"retries"`
	MaxRetries    int       `json:"maxRetries"`
	CreatedAt     time.Time `json:"createdAt"`
	LastAttemptAt *time.Time `json:"lastAttemptAt"`
	Flags         SendFlags `json:"flags"`
}

type QueueWorker struct {
	mu      sync.Mutex
	running bool
	done    chan struct{}
}

var globalWorker = &QueueWorker{
	done: make(chan struct{}),
}

func getQueueDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".uplink", "queue")
}

func saveQueueItem(item *QueueItem) error {
	dir := getQueueDir()
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, item.ID+".json")
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer f.Close()

	err = json.NewEncoder(f).Encode(item)
	if err != nil {
		return err
	}
	f.Close()
	return os.Rename(tmp, path)
}

func loadQueueItems() ([]QueueItem, error) {
	dir := getQueueDir()
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []QueueItem{}, nil
		}
		return nil, err
	}

	var items []QueueItem
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			path := filepath.Join(dir, file.Name())
			data, err := os.ReadFile(path)
			if err == nil {
				var item QueueItem
				if json.Unmarshal(data, &item) == nil {
					items = append(items, item)
				}
			}
		}
	}
	return items, nil
}

func isNetworkReachable(serverUrl string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Head(serverUrl)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func (qw *QueueWorker) Start(interval time.Duration) {
	qw.mu.Lock()
	if qw.running {
		qw.mu.Unlock()
		return
	}
	qw.running = true
	qw.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				qw.processQueue()
			case <-qw.done:
				return
			}
		}
	}()
}

func (qw *QueueWorker) Stop() {
	qw.mu.Lock()
	defer qw.mu.Unlock()
	if qw.running {
		close(qw.done)
		qw.running = false
	}
}

func (qw *QueueWorker) processQueue() {
	qw.mu.Lock()
	defer qw.mu.Unlock()

	items, err := loadQueueItems()
	if err != nil {
		return
	}

	for _, item := range items {
		if item.Status != "pending" || item.Retries >= item.MaxRetries {
			continue
		}

		// Check if file still exists
		if _, err := os.Stat(item.Path); os.IsNotExist(err) {
			item.Status = "failed"
			_ = saveQueueItem(&item)
			continue
		}

		if !isNetworkReachable(item.Flags.Server) {
			continue
		}

		// Attempt upload
		item.Status = "uploading"
		now := time.Now()
		item.LastAttemptAt = &now
		_ = saveQueueItem(&item)

		fmt.Printf("\n[Queue Worker] Uploading %s...\n", item.Filename)
		
		// Setup file uploading arguments
		// We execute the upload file operation
		// Re-uses handleSendUpload logic
		err = performQueueUpload(&item)
		if err != nil {
			item.Retries++
			item.Status = "pending"
			fmt.Printf("[Queue Worker] Upload failed (attempt %d/%d): %v\n", item.Retries, item.MaxRetries, err)
		} else {
			item.Status = "completed"
			_ = os.Remove(filepath.Join(getQueueDir(), item.ID+".json"))
			fmt.Printf("[Queue Worker] Upload complete: %s\n", item.Filename)
		}
		
		if item.Status != "completed" {
			_ = saveQueueItem(&item)
		}
	}
}

func handleQueueSubcommand(args []string) {
	if len(args) == 0 {
		listQueue()
		return
	}

	sub := args[0]
	switch sub {
	case "pause":
		if len(args) < 2 {
			fmt.Println("Usage: uplink queue pause <id>")
			os.Exit(1)
		}
		updateQueueStatus(args[1], "paused")
	case "resume":
		if len(args) < 2 {
			fmt.Println("Usage: uplink queue resume <id>")
			os.Exit(1)
		}
		updateQueueStatus(args[1], "pending")
	case "cancel":
		if len(args) < 2 {
			fmt.Println("Usage: uplink queue cancel <id>")
			os.Exit(1)
		}
		deleteQueueItem(args[1])
	case "clear":
		clearCompletedQueueItems()
	default:
		fmt.Printf("Unknown queue command: %s\n", sub)
		fmt.Println("Usage:\n  uplink queue\n  uplink queue pause <id>\n  uplink queue resume <id>\n  uplink queue cancel <id>\n  uplink queue clear")
		os.Exit(1)
	}
}

func listQueue() {
	items, err := loadQueueItems()
	if err != nil {
		fmt.Printf("Error loading queue: %v\n", err)
		return
	}

	if len(items) == 0 {
		fmt.Println("Queue is empty.")
		return
	}

	fmt.Printf("%-36s %-20s %-10s %-10s %-8s\n", "ID", "Filename", "Size", "Status", "Retries")
	fmt.Println(strings.Repeat("-", 90))
	for _, item := range items {
		fmt.Printf("%-36s %-20s %-10s %-10s %-8d\n", item.ID, item.Filename, formatBytes(item.Size), item.Status, item.Retries)
	}
}

func updateQueueStatus(id string, status string) {
	path := filepath.Join(getQueueDir(), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Error: Queue item %s not found\n", id)
		return
	}

	var item QueueItem
	if json.Unmarshal(data, &item) != nil {
		fmt.Println("Error: Failed to decode queue item metadata")
		return
	}

	item.Status = status
	if err := saveQueueItem(&item); err != nil {
		fmt.Printf("Error saving queue item: %v\n", err)
		return
	}
	fmt.Printf("Item %s status updated to: %s\n", id, status)
}

func deleteQueueItem(id string) {
	path := filepath.Join(getQueueDir(), id+".json")
	if err := os.Remove(path); err != nil {
		fmt.Printf("Error: Failed to cancel queue item %s\n", id)
		return
	}
	fmt.Printf("Item %s cancelled.\n", id)
}

func clearCompletedQueueItems() {
	items, err := loadQueueItems()
	if err != nil {
		return
	}
	for _, item := range items {
		if item.Status == "completed" || item.Status == "failed" {
			_ = os.Remove(filepath.Join(getQueueDir(), item.ID+".json"))
		}
	}
	fmt.Println("Completed and failed queue items cleared.")
}
