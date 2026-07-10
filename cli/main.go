package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ANSI stripping regex
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

type InitRequest struct {
	Filename         string `json:"filename"`
	Size             int64  `json:"size"`
	MimeType         string `json:"mimeType"`
	HashValue        string `json:"hashValue"`
	Password         string `json:"password,omitempty"`
	ExpiresInSeconds int    `json:"expiresInSeconds"`
	DownloadLimit    int    `json:"downloadLimit"`
}

type InitResponse struct {
	ShareId         string `json:"shareId"`
	UploadId        string `json:"uploadId"`
	UploadUrl       string `json:"uploadUrl"`
	ObjectKey       string `json:"objectKey"`
	Filename         string `json:"filename"`
	StorageFilename string `json:"storageFilename"`
	ExpiresAt       string `json:"expiresAt"`
	UploadExpiresAt string `json:"uploadExpiresAt"`
}

type ShareMeta struct {
	ShareId          string `json:"shareId"`
	Filename         string `json:"filename"`
	Size             int64  `json:"size"`
	MimeType         string `json:"mimeType"`
	HashValue        string `json:"hashValue"`
	ExpiresAt        string `json:"expiresAt"`
	PasswordRequired bool   `json:"passwordRequired"`
	DownloadsCount   int    `json:"downloadsCount"`
	DownloadLimit    int    `json:"downloadLimit"`
}

type AuthorizeRequest struct {
	Password string `json:"password,omitempty"`
	Preview  bool   `json:"preview"`
}

type AuthorizeResponse struct {
	DownloadUrl string `json:"downloadUrl"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	MimeType    string `json:"mimeType"`
	HashValue   string `json:"hashValue"`
	ExpiresAt   string `json:"expiresAt"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]

	switch subcommand {
	case "send":
		handleSend(os.Args[2:])
	case "receive":
		handleReceive(os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("R2-Uplink CLI Client")
	fmt.Println("Usage:")
	fmt.Println("  uplink send <filepath> [flags]")
	fmt.Println("  uplink receive <share-link-or-id> [destination-path] [flags]")
	fmt.Println("\nCommands:")
	fmt.Println("  send      Uploads a file to the platform")
	fmt.Println("  receive   Downloads a file from the platform")
}

// Clean filename printed to terminal
func cleanPrintName(name string) string {
	return ansiRegex.ReplaceAllString(name, "")
}

// Strip path traversal parts from file name
func sanitizeFilename(name string) string {
	base := filepath.Base(name)
	// Replace directory separators and double dots
	base = strings.ReplaceAll(base, "/", "")
	base = strings.ReplaceAll(base, "\\", "")
	base = strings.ReplaceAll(base, "..", "")
	if base == "" || base == "." {
		return "file"
	}
	return base
}

func handleSend(args []string) {
	sendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	passwordFlag := sendCmd.String("password", "", "Password to protect the share link")
	expiryFlag := sendCmd.Int("expiry", 86400, "Expiration in seconds (max 24h/86400)")
	limitFlag := sendCmd.Int("limit", 10, "Download limit count")
	serverFlag := sendCmd.String("server", "http://localhost:3000", "Server base URL")

	err := sendCmd.Parse(args)
	if err != nil {
		fmt.Println("Error parsing flags:", err)
		os.Exit(1)
	}

	if sendCmd.NArg() < 1 {
		fmt.Println("Error: File path is required. Usage: uplink send <filepath>")
		os.Exit(1)
	}

	filePath := sendCmd.Arg(0)

	// Verify file
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Error: File '%s' does not exist.\n", filePath)
		} else {
			fmt.Printf("Error accessing file: %v\n", err)
		}
		os.Exit(1)
	}

	if fileInfo.IsDir() {
		fmt.Println("Error: Directories are not supported in Milestone 1 (available in Milestone 2).")
		os.Exit(1)
	}

	if fileInfo.Size() > 200*1024*1024 {
		fmt.Println("Error: File size exceeds 200MB limit for guest sharing.")
		os.Exit(1)
	}

	file, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	// Pass 1: Compute SHA-256
	fmt.Print("Analyzing file integrity (Pass 1/2)... ")
	hasher := sha256.New()
	_, err = io.Copy(hasher, file)
	if err != nil {
		fmt.Printf("Error hashing file: %v\n", err)
		os.Exit(1)
	}
	hashBytes := hasher.Sum(nil)
	hashHex := hex.EncodeToString(hashBytes)
	hashBase64 := base64.StdEncoding.EncodeToString(hashBytes)
	fmt.Printf("Done.\nSHA-256: %s\n", hashHex)

	// Reset file pointer
	_, err = file.Seek(0, 0)
	if err != nil {
		fmt.Printf("Error resetting file pointer: %v\n", err)
		os.Exit(1)
	}

	// 2. Call /api/v1/share/init
	fmt.Print("Initializing upload session... ")
	initReq := InitRequest{
		Filename:         fileInfo.Name(),
		Size:             fileInfo.Size(),
		MimeType:         "application/octet-stream",
		HashValue:        hashHex,
		Password:         *passwordFlag,
		ExpiresInSeconds: *expiryFlag,
		DownloadLimit:    *limitFlag,
	}

	jsonBytes, err := json.Marshal(initReq)
	if err != nil {
		fmt.Printf("Error encoding request: %v\n", err)
		os.Exit(1)
	}

	serverUrl := strings.TrimRight(*serverFlag, "/")
	initUrl := fmt.Sprintf("%s/api/v1/share/init", serverUrl)

	req, err := http.NewRequest("POST", initUrl, bytes.NewBuffer(jsonBytes))
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	// Generate random Idempotency-Key
	req.Header.Set("Idempotency-Key", fmt.Sprintf("cli_%d_%s", time.Now().UnixNano(), hashHex[:8]))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Network error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		fmt.Printf("Failed (status %d): %s\n", resp.StatusCode, string(bodyBytes))
		os.Exit(1)
	}

	var initResp InitResponse
	err = json.NewDecoder(resp.Body).Decode(&initResp)
	if err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Done.")

	// 3. Upload File to Storage via PUT (Pass 2)
	fmt.Println("Streaming file to storage (Pass 2/2)...")

	// Custom reader to track upload progress
	progressReader := &ProgressReader{
		reader: file,
		total:  fileInfo.Size(),
	}

	putReq, err := http.NewRequest("PUT", initResp.UploadUrl, progressReader)
	if err != nil {
		fmt.Printf("Error creating PUT request: %v\n", err)
		os.Exit(1)
	}
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.Header.Set("x-amz-checksum-sha256", hashBase64)
	putReq.ContentLength = fileInfo.Size()

	// High timeout for upload
	uploadClient := &http.Client{Timeout: 30 * time.Minute}
	putResp, err := uploadClient.Do(putReq)
	if err != nil {
		fmt.Printf("\nUpload network error: %v\n", err)
		os.Exit(1)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode != 200 && putResp.StatusCode != 204 {
		bodyBytes, _ := io.ReadAll(putResp.Body)
		fmt.Printf("\nUpload failed (status %d): %s\n", putResp.StatusCode, string(bodyBytes))
		os.Exit(1)
	}
	fmt.Println("\nUpload to storage completed.")

	// 4. Call /confirm
	fmt.Print("Confirming transfer integrity... ")
	confirmUrl := fmt.Sprintf("%s/api/v1/share/%s/confirm", serverUrl, initResp.ShareId)
	confirmResp, err := client.Post(confirmUrl, "application/json", nil)
	if err != nil {
		fmt.Printf("Network error: %v\n", err)
		os.Exit(1)
	}
	defer confirmResp.Body.Close()

	if confirmResp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(confirmResp.Body)
		fmt.Printf("Failed (status %d): %s\n", confirmResp.StatusCode, string(bodyBytes))
		os.Exit(1)
	}
	fmt.Println("Confirmed.")

	shareLink := fmt.Sprintf("%s/share/%s", serverUrl, initResp.ShareId)
	fmt.Printf("\nShare link generated successfully:\n%s\n", cleanPrintName(shareLink))
}

func handleReceive(args []string) {
	recvCmd := flag.NewFlagSet("receive", flag.ExitOnError)
	forceFlag := recvCmd.Bool("force", false, "Force overwrite if file exists")
	forceShortFlag := recvCmd.Bool("f", false, "Force overwrite if file exists (shortcut)")
	renameFlag := recvCmd.Bool("rename", false, "Rename downloaded file with numerical suffix if it exists")
	renameShortFlag := recvCmd.Bool("r", false, "Rename downloaded file with numerical suffix if it exists (shortcut)")
	passwordFlag := recvCmd.String("password", "", "Decryption password if protected")
	mkdirFlag := recvCmd.Bool("mkdir", false, "Create destination directory if it doesn't exist")
	mkdirShortFlag := recvCmd.Bool("p", false, "Create destination directory if it doesn't exist (shortcut)")

	err := recvCmd.Parse(args)
	if err != nil {
		fmt.Println("Error parsing flags:", err)
		os.Exit(1)
	}

	if recvCmd.NArg() < 1 {
		fmt.Println("Error: Share link or share ID is required. Usage: uplink receive <share-link-or-id> [destination-path]")
		os.Exit(1)
	}

	shareInput := recvCmd.Arg(0)
	destPath := ""
	if recvCmd.NArg() >= 2 {
		destPath = recvCmd.Arg(1)
	}

	// Parse Share ID and Server URL from link
	shareId := shareInput
	serverUrl := "http://localhost:3000"

	if strings.Contains(shareInput, "/share/") {
		u, err := url.Parse(shareInput)
		if err == nil {
			serverUrl = fmt.Sprintf("%s://%s", u.Scheme, u.Host)
			pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(pathParts) > 0 {
				shareId = pathParts[len(pathParts)-1]
			}
		}
	} else if strings.Contains(shareInput, "/") {
		// e.g. uplink-delta.dev/4KJ8Pm9k2d8a_1hD9w0qzA
		parts := strings.Split(shareInput, "/")
		shareId = parts[len(parts)-1]
		host := strings.Join(parts[:len(parts)-1], "/")
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			host = "http://" + host
		}
		serverUrl = host
	}

	client := &http.Client{Timeout: 15 * time.Second}

	// 1. Fetch metadata
	metaUrl := fmt.Sprintf("%s/api/v1/share/%s", serverUrl, shareId)
	resp, err := client.Get(metaUrl)
	if err != nil {
		fmt.Printf("Error fetching metadata: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		fmt.Println("Error: Share link not found.")
		os.Exit(1)
	} else if resp.StatusCode == 410 {
		fmt.Println("Error: Share link has expired.")
		os.Exit(1)
	} else if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error (status %d): %s\n", resp.StatusCode, string(bodyBytes))
		os.Exit(1)
	}

	var meta ShareMeta
	err = json.NewDecoder(resp.Body).Decode(&meta)
	if err != nil {
		fmt.Printf("Error decoding metadata: %v\n", err)
		os.Exit(1)
	}

	cleanFilename := cleanPrintName(meta.Filename)
	fmt.Printf("File details found:\n  Name: %s\n  Size: %d bytes\n", cleanFilename, meta.Size)

	// 2. Password Prompter if active
	passwordToUse := *passwordFlag
	if meta.PasswordRequired && passwordToUse == "" {
		fmt.Print("File is password-protected. Enter password: ")
		// Fallback clean read (note: doesn't hide text in basic read,
		// but avoids complex dependencies for now)
		reader := bufio.NewReader(os.Stdin)
		pwd, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("Error reading password: %v\n", err)
			os.Exit(1)
		}
		passwordToUse = strings.TrimSpace(pwd)
	}

	// 3. Authorize Download
	fmt.Print("Authorizing download... ")
	authReq := AuthorizeRequest{
		Password: passwordToUse,
		Preview:  false,
	}
	jsonBytes, err := json.Marshal(authReq)
	if err != nil {
		fmt.Printf("Error encoding request: %v\n", err)
		os.Exit(1)
	}

	authUrl := fmt.Sprintf("%s/api/v1/share/%s/authorize-download", serverUrl, shareId)
	authResp, err := client.Post(authUrl, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		fmt.Printf("Network error: %v\n", err)
		os.Exit(1)
	}
	defer authResp.Body.Close()

	if authResp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(authResp.Body)
		var errData map[string]interface{}
		json.Unmarshal(bodyBytes, &errData)
		errMsg := "Authorization failed"
		if errData != nil && errData["error"] != nil {
			errMsg = errData["error"].(string)
		}
		fmt.Printf("Failed: %s\n", errMsg)
		os.Exit(1)
	}

	var authData AuthorizeResponse
	err = json.NewDecoder(authResp.Body).Decode(&authData)
	if err != nil {
		fmt.Printf("Error parsing auth details: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Authorized.")

	// Determine output path
	sanitizedName := sanitizeFilename(meta.Filename)
	outputFilepath := sanitizedName

	if destPath != "" {
		// Clean and resolve path
		destFileInfo, statErr := os.Stat(destPath)
		if statErr == nil && destFileInfo.IsDir() {
			outputFilepath = filepath.Join(destPath, sanitizedName)
		} else {
			// Check if parent directory exists
			parentDir := filepath.Dir(destPath)
			if _, pErr := os.Stat(parentDir); os.IsNotExist(pErr) {
				if *mkdirFlag || *mkdirShortFlag {
					err = os.MkdirAll(parentDir, 0755)
					if err != nil {
						fmt.Printf("Error creating directory %s: %v\n", parentDir, err)
						os.Exit(1)
					}
				} else {
					fmt.Printf("Error: Destination folder %s does not exist. Use --mkdir or -p flag to create it.\n", parentDir)
					os.Exit(1)
				}
			}
			outputFilepath = destPath
		}
	}

	// Traversal safety: double check path is resolved cleanly
	absOut, err := filepath.Abs(outputFilepath)
	if err != nil {
		fmt.Printf("Error resolving output path: %v\n", err)
		os.Exit(1)
	}

	// Check if target file exists
	if _, err = os.Stat(absOut); err == nil {
		forceOverwrite := *forceFlag || *forceShortFlag
		renameFile := *renameFlag || *renameShortFlag

		if !forceOverwrite && !renameFile {
			fmt.Printf("Error: Destination file '%s' already exists. Use --force (-f) to overwrite or --rename (-r) to save with suffix.\n", absOut)
			os.Exit(1)
		}

		if renameFile && !forceOverwrite {
			// Find unique name
			dir := filepath.Dir(absOut)
			ext := filepath.Ext(absOut)
			baseWithoutExt := sanitizedName[:len(sanitizedName)-len(ext)]
			suffix := 1
			for {
				newName := fmt.Sprintf("%s (%d)%s", baseWithoutExt, suffix, ext)
				outputFilepath = filepath.Join(dir, newName)
				absOut, _ = filepath.Abs(outputFilepath)
				if _, err = os.Stat(absOut); os.IsNotExist(err) {
					break
				}
				suffix++
			}
		}
	}

	// 4. Download file
	fmt.Printf("Downloading to '%s'...\n", absOut)
	downloadResp, err := http.Get(authData.DownloadUrl)
	if err != nil {
		fmt.Printf("Error downloading file: %v\n", err)
		os.Exit(1)
	}
	defer downloadResp.Body.Close()

	if downloadResp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(downloadResp.Body)
		fmt.Printf("Download failed (status %d): %s\n", downloadResp.StatusCode, string(bodyBytes))
		os.Exit(1)
	}

	// Ensure output file is writable
	outFd, err := os.OpenFile(absOut, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		if os.IsPermission(err) {
			fmt.Println("Error: Permission denied writing to destination.")
		} else {
			fmt.Printf("Error creating output file: %v\n", err)
		}
		os.Exit(1)
	}
	defer outFd.Close()

	// Download progress reader
	progDownload := &ProgressReader{
		reader: downloadResp.Body,
		total:  meta.Size,
	}

	// Compute checksum on the fly during download to verify integrity
	downloadHasher := sha256.New()
	multiWriter := io.MultiWriter(outFd, downloadHasher)

	_, err = io.Copy(multiWriter, progDownload)
	if err != nil {
		// Check for disk full
		if errors.Is(err, io.ErrShortWrite) {
			fmt.Println("\nError: Write failed. Disk space may be exhausted.")
		} else {
			fmt.Printf("\nError during download: %v\n", err)
		}
		os.Exit(1)
	}

	computedHex := hex.EncodeToString(downloadHasher.Sum(nil))
	fmt.Println("\nDownload completed.")

	// Verify SHA-256 matches
	if computedHex != meta.HashValue {
		fmt.Println("Error: File integrity check failed! Computed checksum does not match expected hash.")
		// Delete corrupted file
		os.Remove(absOut)
		os.Exit(1)
	}

	fmt.Println("File integrity verified successfully.")
}

// Custom ProgressReader to show progress in terminal
type ProgressReader struct {
	reader io.Reader
	total  int64
	read   int64
	lastP  int
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.read += int64(n)
		if pr.total > 0 {
			percent := int((float64(pr.read) / float64(pr.total)) * 100)
			if percent/5 > pr.lastP/5 || percent == 100 { // print every 5%
				pr.lastP = percent
				fmt.Printf("\rProgress: %d%% (%d/%d bytes)", percent, pr.read, pr.total)
			}
		} else {
			fmt.Printf("\rProgress: %d bytes uploaded", pr.read)
		}
	}
	return n, err
}
