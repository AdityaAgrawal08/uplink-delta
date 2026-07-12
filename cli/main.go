package main

import (
	"bufio"
	"strconv"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"uplink-cli/pkg/crc64"
	"uplink-cli/pkg/tarball"
)

// ANSI stripping regex
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Chunk threshold for multipart (10 MB)
const ChunkSize = 10 * 1024 * 1024

type InitRequest struct {
	Filename          string `json:"filename"`
	Size              int64  `json:"size"`
	MimeType          string `json:"mimeType"`
	HashValue         string `json:"hashValue"`
	Password          string `json:"password,omitempty"`
	ExpiresInSeconds  int    `json:"expiresInSeconds,omitempty"`
	DownloadLimit     int    `json:"downloadLimit,omitempty"`
	PartsCount        int    `json:"partsCount,omitempty"`
	ChecksumCrc64nvme string `json:"checksumCrc64nvme,omitempty"`
}

type InitResponse struct {
	ShareId         string   `json:"shareId"`
	UploadId        string   `json:"uploadId"`
	UploadUrl       string   `json:"uploadUrl,omitempty"`
	UploadUrls      []string `json:"uploadUrls,omitempty"`
	ObjectKey       string   `json:"objectKey"`
	Filename        string   `json:"filename"`
	StorageFilename string   `json:"storageFilename"`
	ExpiresAt       string   `json:"expiresAt"`
	UploadExpiresAt string   `json:"uploadExpiresAt"`
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

type PartInfo struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"etag"`
	Checksum   string `json:"checksum,omitempty"`
}

type ConfirmRequest struct {
	Parts []PartInfo `json:"parts,omitempty"`
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
	fmt.Println("R2-Uplink CLI Client (v7.1)")
	fmt.Println("Usage:")
	fmt.Println("  uplink send <filepath-or-directory> [flags]")
	fmt.Println("  uplink receive <share-link-or-id> [destination-path] [flags]")
	fmt.Println("\nCommands:")
	fmt.Println("  send      Uploads a file or directory to the platform")
	fmt.Println("  receive   Downloads a file or directory from the platform")
}

func cleanPrintName(name string) string {
	return ansiRegex.ReplaceAllString(name, "")
}

func sanitizeFilename(name string) string {
	base := filepath.Base(name)
	base = strings.ReplaceAll(base, "/", "")
	base = strings.ReplaceAll(base, "\\", "")
	base = strings.ReplaceAll(base, "..", "")
	if base == "" || base == "." {
		return "file"
	}
	return base
}

func getServerDefault() string {
	if val := os.Getenv("UPLINK_SERVER"); val != "" {
		return strings.TrimRight(val, "/")
	}
	return "https://uplink-delta-xi.vercel.app/"
}

func parseDurationToSeconds(durationStr string) (int, error) {
	if len(durationStr) < 2 {
		return 0, fmt.Errorf("invalid duration format: must contain a number and a unit suffix (m, h, d)")
	}
	suffix := durationStr[len(durationStr)-1:]
	numStr := durationStr[:len(durationStr)-1]
	val, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid number format in duration: %s", numStr)
	}
	if val <= 0 {
		return 0, fmt.Errorf("duration must be greater than zero")
	}
	switch suffix {
	case "m":
		return val * 60, nil
	case "h":
		return val * 3600, nil
	case "d":
		return val * 86400, nil
	default:
		return 0, fmt.Errorf("unknown duration suffix: %s (supported units: m, h, d)", suffix)
	}
}

func handleSend(args []string) {
	sendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	passwordFlag := sendCmd.String("password", "", "Password to protect the share link")
	expireFlag := sendCmd.String("expire", "5m", "Expiration duration (e.g. 5m, 30m, 2h, 1d)")
	serverFlag := sendCmd.String("server", getServerDefault(), "Server base URL")

	err := sendCmd.Parse(args)
	if err != nil {
		fmt.Println("Error parsing flags:", err)
		os.Exit(1)
	}

	expirySeconds, err := parseDurationToSeconds(*expireFlag)
	if err != nil {
		fmt.Printf("Error parsing expire flag: %v\n", err)
		os.Exit(1)
	}

	if sendCmd.NArg() < 1 {
		fmt.Println("Error: File or directory path is required. Usage: uplink send <path>")
		os.Exit(1)
	}

	inputPath := sendCmd.Arg(0)
	var filePath string
	var isDirectory bool
	var originalName string

	// Verify path
	fileInfo, err := os.Stat(inputPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Error: Path '%s' does not exist.\n", inputPath)
		} else {
			fmt.Printf("Error accessing path: %v\n", err)
		}
		os.Exit(1)
	}

	originalName = fileInfo.Name()

	if fileInfo.IsDir() {
		isDirectory = true
		fmt.Printf("Detected directory. Packaging '%s' to tarball...\n", originalName)

		// Create temporary file
		tempFile, err := os.CreateTemp("", "uplink_tarball_*.tar.gz")
		if err != nil {
			fmt.Printf("Error creating temp tarball: %v\n", err)
			os.Exit(1)
		}
		tempFile.Close()
		filePath = tempFile.Name()
		defer os.Remove(filePath) // Ensure cleanup on exit

		// Open write descriptor
		outFd, err := os.OpenFile(filePath, os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Printf("Error opening temp tarball for writing: %v\n", err)
			os.Exit(1)
		}

		err = tarball.Pack(inputPath, outFd)
		outFd.Close()
		if err != nil {
			fmt.Printf("Failed to package directory: %v\n", err)
			os.Exit(1)
		}

		// Re-stat the temporary archive file
		fileInfo, err = os.Stat(filePath)
		if err != nil {
			fmt.Printf("Error stating archive: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Directory packaged successfully. Archive size: %d bytes\n", fileInfo.Size())
	} else {
		filePath = inputPath
	}

	// Enforce sizes
	maxAllowedSize := int64(200 * 1024 * 1024)
	if isDirectory {
		maxAllowedSize = int64(500 * 1024 * 1024) // 500 MB for directories
	}

	if fileInfo.Size() > maxAllowedSize {
		fmt.Printf("Error: Upload exceeds maximum size limit of %d MB.\n", maxAllowedSize/(1024*1024))
		os.Exit(1)
	}

	file, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	isMultipart := fileInfo.Size() > ChunkSize
	partsCount := 1
	if isMultipart {
		partsCount = int((fileInfo.Size() + ChunkSize - 1) / ChunkSize)
	}

	// Pass 1: Compute hashes (SHA-256 and CRC64NVME if multipart)
	fmt.Print("Analyzing file integrity (Pass 1/2)... ")
	shaHasher := sha256.New()
	var crcHasher hash.Hash64
	var multiWriter io.Writer

	if isMultipart {
		crcHasher = crc64.New()
		multiWriter = io.MultiWriter(shaHasher, crcHasher)
	} else {
		multiWriter = shaHasher
	}

	_, err = io.Copy(multiWriter, file)
	if err != nil {
		fmt.Printf("Error hashing file: %v\n", err)
		os.Exit(1)
	}

	hashBytes := shaHasher.Sum(nil)
	hashHex := hex.EncodeToString(hashBytes)

	var crcBase64 string
	if isMultipart {
		crcBytes := crcHasher.Sum(nil)
		crcBase64 = base64.StdEncoding.EncodeToString(crcBytes)
	}

	fmt.Println("Done.")

	// Reset file descriptor pointer
	_, err = file.Seek(0, 0)
	if err != nil {
		fmt.Printf("Error resetting file pointer: %v\n", err)
		os.Exit(1)
	}

	// 2. Call /api/v1/share/init
	fmt.Print("Initializing upload session... ")
	filenameToSend := originalName
	if isDirectory {
		filenameToSend = originalName + ".tar.gz"
	}

	initReq := InitRequest{
		Filename:         filenameToSend,
		Size:             fileInfo.Size(),
		MimeType:         "application/octet-stream",
		HashValue:        hashHex,
		Password:         *passwordFlag,
		ExpiresInSeconds: expirySeconds,
	}

	if isMultipart {
		initReq.PartsCount = partsCount
		initReq.ChecksumCrc64nvme = crcBase64
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
	req.Header.Set("Idempotency-Key", fmt.Sprintf("cli_%d_%s", time.Now().UnixNano(), hashHex[:8]))

	client := &http.Client{Timeout: 30 * time.Second}
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

	// 3. Streaming upload (Single-part vs Multipart)
	var confirmReq ConfirmRequest

	if isMultipart {
		fmt.Println("Streaming chunks in multipart mode to storage (Pass 2/2)...")
		confirmReq.Parts = make([]PartInfo, partsCount)
		buffer := make([]byte, ChunkSize)
		totalUploaded := int64(0)

		for i := 1; i <= partsCount; i++ {
			n, readErr := file.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]

				// Compute part checksum
				partHasher := crc64.New()
				partHasher.Write(chunk)
				partChecksumBytes := partHasher.Sum(nil)
				partChecksumBase64 := base64.StdEncoding.EncodeToString(partChecksumBytes)

				partUploadUrl := initResp.UploadUrls[i-1]
				putReq, err := http.NewRequest("PUT", partUploadUrl, bytes.NewReader(chunk))
				if err != nil {
					fmt.Printf("\nError creating chunk request: %v\n", err)
					os.Exit(1)
				}
				putReq.Header.Set("Content-Type", "application/octet-stream")
				putReq.Header.Set("x-amz-checksum-crc64nvme", partChecksumBase64)
				putReq.ContentLength = int64(n)

				uploadClient := &http.Client{Timeout: 10 * time.Minute}
				putResp, err := uploadClient.Do(putReq)
				if err != nil {
					fmt.Printf("\nChunk %d upload network error: %v\n", i, err)
					os.Exit(1)
				}
				defer putResp.Body.Close()

				if putResp.StatusCode != 200 && putResp.StatusCode != 204 {
					bodyBytes, _ := io.ReadAll(putResp.Body)
					fmt.Printf("\nChunk %d upload failed (status %d): %s\n", i, putResp.StatusCode, string(bodyBytes))
					os.Exit(1)
				}

				etag := putResp.Header.Get("ETag")
				if etag == "" {
					etag = fmt.Sprintf("\"%s-%d\"", initResp.UploadId, i) // Fallback ETag format
				}

				confirmReq.Parts[i-1] = PartInfo{
					PartNumber: i,
					ETag:       etag,
					Checksum:   partChecksumBase64,
				}

				totalUploaded += int64(n)
				percent := int((float64(totalUploaded) / float64(fileInfo.Size())) * 100)
				fmt.Printf("\rProgress: %d%% (%d/%d bytes)", percent, totalUploaded, fileInfo.Size())
			}
			if readErr != nil && readErr != io.EOF {
				fmt.Printf("\nError reading file: %v\n", readErr)
				os.Exit(1)
			}
		}
		fmt.Println()
	} else {
		progressReader := &ProgressReader{
			reader: file,
			total:  fileInfo.Size(),
		}

		putReq, err := http.NewRequest("PUT", initResp.UploadUrl, progressReader)
		if err != nil {
			fmt.Printf("Error creating PUT request: %v\n", err)
			os.Exit(1)
		}

		// SHA-256 header required for single-part
		putReq.Header.Set("Content-Type", "application/octet-stream")
		// putReq.Header.Set("x-amz-checksum-sha256", hashBase64)
		putReq.ContentLength = fileInfo.Size()

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
		fmt.Println()
	}

	// 4. Confirm upload
	fmt.Print("Confirming transfer integrity... ")
	confirmUrl := fmt.Sprintf("%s/api/v1/share/%s/confirm", serverUrl, initResp.ShareId)

	var confirmBody io.Reader = nil
	if isMultipart {
		cBytes, _ := json.Marshal(confirmReq)
		confirmBody = bytes.NewBuffer(cBytes)
	}

	confirmReqObj, err := http.NewRequest("POST", confirmUrl, confirmBody)
	if err != nil {
		fmt.Printf("Error creating confirm request: %v\n", err)
		os.Exit(1)
	}
	confirmReqObj.Header.Set("Content-Type", "application/json")

	confirmResp, err := client.Do(confirmReqObj)
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
	forceFlag := recvCmd.Bool("force", false, "Force overwrite if file/directory exists")
	forceShortFlag := recvCmd.Bool("f", false, "Force overwrite (shortcut)")
	renameFlag := recvCmd.Bool("rename", false, "Rename downloaded target if it exists")
	renameShortFlag := recvCmd.Bool("r", false, "Rename downloaded target (shortcut)")
	passwordFlag := recvCmd.String("password", "", "Decryption password if protected")
	mkdirFlag := recvCmd.Bool("mkdir", false, "Create destination directory if it doesn't exist")
	mkdirShortFlag := recvCmd.Bool("p", false, "Create destination directory (shortcut)")

	err := recvCmd.Parse(args)
	if err != nil {
		fmt.Println("Error parsing flags:", err)
		os.Exit(1)
	}

	if recvCmd.NArg() < 1 {
		fmt.Println("Error: Share link or ID is required. Usage: uplink receive <share-link> [dest]")
		os.Exit(1)
	}

	shareInput := recvCmd.Arg(0)
	destPath := ""
	if recvCmd.NArg() >= 2 {
		destPath = recvCmd.Arg(1)
	}

	shareId := shareInput
	serverUrl := getServerDefault()

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

	isArchive := strings.HasSuffix(meta.Filename, ".tar.gz")
	cleanFilename := cleanPrintName(meta.Filename)

	if isArchive {
		originalDirName := cleanFilename[:len(cleanFilename)-len(".tar.gz")]
		fmt.Printf("Directory details found:\n  Name: %s\n  Archive Size: %d bytes\n", originalDirName, meta.Size)
	} else {
		fmt.Printf("File details found:\n  Name: %s\n  Size: %d bytes\n", cleanFilename, meta.Size)
	}

	// 2. Password prompter
	passwordToUse := *passwordFlag
	if meta.PasswordRequired && passwordToUse == "" {
		fmt.Print("This share is password-protected. Enter password: ")
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
	var finalExtractDir string

	if isArchive {
		// Target folder name without .tar.gz
		originalDirName := sanitizedName[:len(sanitizedName)-len(".tar.gz")]
		outputFilepath = originalDirName
		finalExtractDir = originalDirName
	}

	if destPath != "" {
		destFileInfo, statErr := os.Stat(destPath)
		if statErr == nil && destFileInfo.IsDir() {
			if isArchive {
				originalDirName := sanitizedName[:len(sanitizedName)-len(".tar.gz")]
				finalExtractDir = filepath.Join(destPath, originalDirName)
				outputFilepath = finalExtractDir
			} else {
				outputFilepath = filepath.Join(destPath, sanitizedName)
			}
		} else {
			parentDir := filepath.Dir(destPath)
			if _, pErr := os.Stat(parentDir); os.IsNotExist(pErr) {
				if *mkdirFlag || *mkdirShortFlag {
					err = os.MkdirAll(parentDir, 0o755)
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
			finalExtractDir = destPath
		}
	}

	absOut, err := filepath.Abs(outputFilepath)
	if err != nil {
		fmt.Printf("Error resolving output path: %v\n", err)
		os.Exit(1)
	}

	// Overwrite/Rename checks
	if _, err = os.Stat(absOut); err == nil {
		forceOverwrite := *forceFlag || *forceShortFlag
		renameFile := *renameFlag || *renameShortFlag

		if !forceOverwrite && !renameFile {
			fmt.Printf("Error: Target '%s' already exists. Use --force (-f) to overwrite or --rename (-r) to save with suffix.\n", absOut)
			os.Exit(1)
		}

		if renameFile && !forceOverwrite {
			dir := filepath.Dir(absOut)
			base := filepath.Base(absOut)
			suffix := 1
			for {
				var newName string
				if isArchive {
					newName = fmt.Sprintf("%s (%d)", base, suffix)
				} else {
					ext := filepath.Ext(base)
					baseWithoutExt := base[:len(base)-len(ext)]
					newName = fmt.Sprintf("%s (%d)%s", baseWithoutExt, suffix, ext)
				}
				outputFilepath = filepath.Join(dir, newName)
				absOut, _ = filepath.Abs(outputFilepath)
				if _, err = os.Stat(absOut); os.IsNotExist(err) {
					break
				}
				suffix++
			}
			if isArchive {
				finalExtractDir = absOut
			}
		}
	}

	// 4. Download file
	tempTarFile := absOut
	if isArchive {
		// Download directory archive as a temp tarball file
		tempTarFile = absOut + ".download.tar.gz"
	}

	fmt.Printf("Downloading to '%s'...\n", tempTarFile)
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

	outFd, err := os.OpenFile(tempTarFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		if os.IsPermission(err) {
			fmt.Println("Error: Permission denied writing to destination.")
		} else {
			fmt.Printf("Error creating output file: %v\n", err)
		}
		os.Exit(1)
	}

	progDownload := &ProgressReader{
		reader: downloadResp.Body,
		total:  meta.Size,
	}

	downloadHasher := sha256.New()
	multiWriter := io.MultiWriter(outFd, downloadHasher)

	_, err = io.Copy(multiWriter, progDownload)
	outFd.Close() // Close immediately
	if err != nil {
		os.Remove(tempTarFile)
		if errors.Is(err, io.ErrShortWrite) {
			fmt.Println("\nError: Write failed. Disk space may be exhausted.")
		} else {
			fmt.Printf("\nError during download: %v\n", err)
		}
		os.Exit(1)
	}

	computedHex := hex.EncodeToString(downloadHasher.Sum(nil))
	fmt.Println("\nDownload completed.")

	if computedHex != meta.HashValue {
		fmt.Println("Error: File integrity check failed! Computed checksum does not match expected hash.")
		os.Remove(tempTarFile)
		os.Exit(1)
	}

	fmt.Println("File integrity verified successfully.")

	// Unpack directory if archive
	if isArchive {
		fmt.Printf("Extracting folder contents to '%s'...\n", finalExtractDir)

		tarReader, err := os.Open(tempTarFile)
		if err != nil {
			fmt.Printf("Error opening download archive: %v\n", err)
			os.Remove(tempTarFile)
			os.Exit(1)
		}

		err = tarball.Unpack(tarReader, finalExtractDir)
		tarReader.Close()
		os.Remove(tempTarFile) // Clean up temporary download file

		if err != nil {
			fmt.Printf("Extraction failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Extraction completed successfully.")
	}
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
