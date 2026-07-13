package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

func shouldSkip(name string) bool {
	base := filepath.Base(name)
	return strings.HasPrefix(base, ".") ||
		strings.HasSuffix(base, ".tmp") ||
		strings.HasSuffix(base, ".swp")
}

func WatchDirectory(dir string, flags SendFlags) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.Add(dir)
	if err != nil {
		return err
	}

	fmt.Printf("Watching %s for changes...\n", dir)

	debounce := make(chan string, 100)
	go func() {
		for filename := range debounce {
			if shouldSkip(filename) {
				continue
			}
			time.Sleep(500 * time.Millisecond) // Wait for write to finish
			
			if _, err := os.Stat(filename); os.IsNotExist(err) {
				continue
			}

			fmt.Printf("[%s] Detected change: %s. Uploading...\n", time.Now().Format("15:04:05"), filepath.Base(filename))
			
			shareCode, err := performWatchUpload(filename, flags)
			if err != nil {
				fmt.Printf("[%s] Error uploading %s: %v\n", time.Now().Format("15:04:05"), filename, err)
				notifyTransferFailed(filepath.Base(filename), err)
			} else {
				fmt.Printf("[%s] Uploaded: %s → share code: %s\n", time.Now().Format("15:04:05"), filepath.Base(filename), shareCode)
				notifyTransferComplete(filepath.Base(filename))
			}
		}
	}()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				debounce <- event.Name
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Printf("Watch error: %v\n", err)
		}
	}
}
