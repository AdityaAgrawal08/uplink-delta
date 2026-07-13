package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash"
	"io"
	"math/rand"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/AdityaAgrawal08/uplink-delta/cli/pkg/crc64"
	"github.com/AdityaAgrawal08/uplink-delta/cli/pkg/tarball"
	"golang.org/x/term"
	"github.com/AdityaAgrawal08/uplink-delta/cli/lan"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

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
	case "config":
		handleConfigSubcommand(os.Args[2:])
	case "clean":
		CleanOldResumeStates()
		fmt.Println("Cleared old resume states.")
	case "help", "--help", "-h":
		printUsage()
	default:
		if strings.HasPrefix(subcommand, "-") {
			fmt.Printf("✗ Error: Unknown option \"%s\"\n\n", subcommand)
		} else {
			fmt.Printf("✗ Error: Unknown command \"%s\"\n\n", subcommand)
		}
		fmt.Println("Run:\n  uplink --help\n\nto see all available commands.")
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("R2-Uplink CLI Client (v7.2)")
	fmt.Println("Usage: uplink <command> [arguments] [flags]\n")
	fmt.Println("Commands:")
	
	fmt.Println("  send        Upload a file or directory")
	fmt.Println("              uplink send report.pdf\n")
	
	fmt.Println("  receive     Download a file or directory")
	fmt.Println("              uplink receive 4827165038\n")

	fmt.Println("  config      Manage client configuration options")
	fmt.Println("              uplink config\n")

	fmt.Println("  clean       Clean expired upload resume states")
	fmt.Println("              uplink clean\n")
	
	fmt.Println("  help        Show available commands")
	fmt.Println("              uplink --help")
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
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

func sanitizeServerUrl(serverUrl string) string {
	serverUrl = strings.TrimRight(serverUrl, "/")
	if !strings.HasPrefix(serverUrl, "http://") && !strings.HasPrefix(serverUrl, "https://") {
		serverUrl = "https://" + serverUrl
	} else if strings.HasPrefix(serverUrl, "http://") {
		if !strings.Contains(serverUrl, "localhost") && !strings.Contains(serverUrl, "127.0.0.1") {
			fmt.Println("Warning: Upgrading insecure HTTP to HTTPS")
			serverUrl = "https://" + strings.TrimPrefix(serverUrl, "http://")
		}
	}
	return serverUrl
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

func generateShareCode() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[r.Intn(len(charset))]
	}
	return string(b)
}

func handleSend(args []string) {
	cfg := LoadConfig()

	sendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	passwordFlag := sendCmd.String("password", "", "Password to protect the share link")
	expireFlag := sendCmd.String("expire", cfg.Expiry, "Expiration duration (e.g. 5m, 30m, 2h, 1d)")
	serverFlag := sendCmd.String("server", cfg.Server, "Server base URL")
	lanFlag := sendCmd.Bool("lan", false, "Enable direct LAN P2P transfer")
	qrFlag := sendCmd.Bool("qr", false, "Force display QR code")
	noQrFlag := sendCmd.Bool("no-qr", false, "Suppress QR code display")

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
		fmt.Println("✗ Error: File or directory path is required.\n")
		fmt.Println("Usage:\n  uplink send <path>")
		os.Exit(1)
	}

	inputPath := strings.Trim(sendCmd.Arg(0), "\"'")
	var filePath string
	var isDirectory bool
	var originalName string

	// Verify path
	fileInfo, err := os.Stat(inputPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("✗ Error: File not found.\n")
			fmt.Println("Check that the path exists and that you have permission to access it.")
		} else {
			fmt.Printf("✗ Error: Accessing path failed: %v\n", err)
		}
		os.Exit(1)
	}

	originalName = fileInfo.Name()

	if fileInfo.IsDir() {
		isDirectory = true
		fmt.Printf("Detected directory. Packaging '%s' to tarball...\n", originalName)

		tempFile, err := os.CreateTemp("", "uplink_tarball_*.tar.gz")
		if err != nil {
			fmt.Printf("Error creating temp tarball: %v\n", err)
			os.Exit(1)
		}
		tempFile.Close()
		filePath = tempFile.Name()
		defer os.Remove(filePath) // Ensure cleanup on exit

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

	// 1. Analyze file integrity (Pass 1/2)
	fmt.Print("Analyzing file integrity (Pass 1/2)... ")
	shaHasher := sha256.New()
	var crcHasher hash.Hash64
	var multiWriter io.Writer

	// S3 5MB minimum limit is used for ChunkSize
	const MinS3PartSize = 5 * 1024 * 1024
	var chunkSize int64 = 10 * 1024 * 1024
	serverUrl := sanitizeServerUrl(*serverFlag)

	if cfg.AdaptiveChunks {
		chunker := &AdaptiveChunker{}
		_, err = chunker.Measure(serverUrl)
		if err == nil {
			chunkSize = chunker.ChunkSize()
		}
	}

	isMultipart := fileInfo.Size() > chunkSize
	partsCount := 1
	if isMultipart {
		partsCount = int((fileInfo.Size() + chunkSize - 1) / chunkSize)
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
	hashBase64 := base64.StdEncoding.EncodeToString(hashBytes)

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

	// 2. LAN P2P Transfer mode
	if *lanFlag {
		cert, fingerprint, err := lan.GenerateEphemeralCert()
		if err != nil {
			fmt.Printf("✗ Error generating ephemeral certificate: %v\n", err)
			os.Exit(1)
		}

		port := cfg.LanPort
		var listener net.Listener
		var listenErr error
		for attempt := 0; attempt < 3; attempt++ {
			addr := fmt.Sprintf(":%d", port+attempt)
			listener, listenErr = net.Listen("tcp", addr)
			if listenErr == nil {
				port = port + attempt
				listener.Close()
				break
			}
		}
		if listenErr != nil {
			fmt.Printf("✗ Error starting LAN server: could not bind ports %d-%d\n", cfg.LanPort, cfg.LanPort+2)
			os.Exit(1)
		}

		shareCode := generateShareCode()

		fmt.Printf("\n✓ Direct LAN P2P Transfer Initialized!\n")
		fmt.Printf("Share Code: %s\n", shareCode)
		fmt.Printf("Fingerprint: %s\n", fingerprint)
		
		shareUrl := fmt.Sprintf("uplink receive %s --lan", shareCode)
		showQR := false
		if *qrFlag {
			showQR = true
		} else if !*noQrFlag {
			showQR = ShouldShowQR(cfg.ShowQR)
		}
		if showQR {
			PrintQRCode(shareUrl)
		}

		fmt.Printf("Serving file on port %d... Waiting 1s for local peer...\n", port)

		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "uplink-peer"
		}
		serviceInfo := lan.ServiceInfo{
			Hostname:    hostname,
			Port:        port,
			ShareCode:   shareCode,
			FileName:    originalName,
			Size:        fileInfo.Size(),
			Fingerprint: fingerprint,
		}
		shutdownMDNS, err := lan.RegisterService(serviceInfo)
		if err != nil {
			fmt.Printf("Error registering mDNS service: %v\n", err)
			os.Exit(1)
		}

		ctx, cancel := context.WithCancel(context.Background())
		serverDone := make(chan struct{})
		go func() {
			err := lan.ServeFileLAN(ctx, filePath, port, cert, func() {
				fmt.Println("\n✓ LAN Transfer completed successfully!")
				cancel()
				os.Exit(0)
			})
			if err != nil && err != context.Canceled {
				fmt.Printf("\n✗ LAN Server Error: %v\n", err)
			}
			close(serverDone)
		}()

		time.Sleep(1 * time.Second)

		if lan.GetActiveConnections() > 0 {
			fmt.Println("Local peer connected! Performing LAN transfer...")
			<-ctx.Done()
			shutdownMDNS()
			os.Exit(0)
		} else {
			fmt.Println("No peer connected on LAN yet. Proceeding with fallback upload to cloud...")
			defer func() {
				cancel()
				shutdownMDNS()
			}()
		}
	}

	// 3. Resumable Upload check
	var resumeState *ResumeState
	var isResume bool
	stateFilename := sanitizeFilename(originalName) + ".json"

	if isMultipart {
		state, err := LoadResumeState(stateFilename)
		if err == nil && state.Valid() && state.SHA256 == hashHex && state.FileSize == fileInfo.Size() {
			partsUrl := fmt.Sprintf("%s/api/v1/share/%s/parts", serverUrl, state.ShareId)
			partsResp, err := http.Get(partsUrl)
			if err == nil && partsResp.StatusCode == 200 {
				var partsData struct {
					UploadId string     `json:"uploadId"`
					Parts    []PartInfo `json:"parts"`
				}
				if json.NewDecoder(partsResp.Body).Decode(&partsData) == nil {
					serverPartsMap := make(map[int]bool)
					for _, p := range partsData.Parts {
						serverPartsMap[p.PartNumber] = true
					}
					for _, p := range state.Done {
						serverPartsMap[p] = true
					}
					
					var mergedParts []int
					for p := range serverPartsMap {
						mergedParts = append(mergedParts, p)
					}
					
					resumeState = state
					resumeState.Done = mergedParts
					isResume = true
					fmt.Printf("Resuming upload session %s (%d/%d parts completed)...\n", state.ShareId, len(mergedParts), state.TotalParts)
				}
			}
			if partsResp != nil {
				partsResp.Body.Close()
			}
		}
	}

	// 4. Initialize upload in Cloud
	var initResp InitResponse
	mimeType := mime.TypeByExtension(filepath.Ext(originalName))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	if isResume {
		initResp.ShareId = resumeState.ShareId
		initResp.UploadId = resumeState.UploadId
		initResp.UploadUrls = resumeState.UploadUrls
	} else {
		fmt.Print("Initializing upload (Pass 2/2)... ")
		initReq := InitRequest{
			Filename:         originalName,
			Size:             fileInfo.Size(),
			MimeType:         mimeType,
			HashValue:        hashHex,
			Password:         *passwordFlag,
			ExpiresInSeconds: expirySeconds,
			PartsCount:       partsCount,
		}

		if isMultipart {
			initReq.ChecksumCrc64nvme = crcBase64
		}

		jsonBytes, err := json.Marshal(initReq)
		if err != nil {
			fmt.Printf("Error encoding request: %v\n", err)
			os.Exit(1)
		}

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

		err = json.NewDecoder(resp.Body).Decode(&initResp)
		if err != nil {
			fmt.Printf("Error parsing response: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Done.")

		if isMultipart {
			resumeState = &ResumeState{
				ShareId:    initResp.ShareId,
				UploadId:   initResp.UploadId,
				UploadUrls: initResp.UploadUrls,
				FileSize:   fileInfo.Size(),
				SHA256:     hashHex,
				Done:       []int{},
				TotalParts: partsCount,
				Timestamp:  time.Now().Format(time.RFC3339),
			}
			_ = resumeState.Save(stateFilename)
		}
	}

	// 5. Streaming upload (Single-part vs Multipart)
	var confirmReq ConfirmRequest

	if isMultipart {
		confirmReq.Parts = make([]PartInfo, partsCount)
		buffer := make([]byte, chunkSize)
		totalUploaded := int64(0)
		printer := &ProgressPrinter{
			title:      "Uploading...",
			total:      fileInfo.Size(),
			startTime:  time.Now(),
			firstPrint: true,
		}

		for i := 1; i <= partsCount; i++ {
			// Read chunk size
			n, readErr := file.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]

				// Check if already completed (from resumeState)
				alreadyDone := false
				if resumeState != nil {
					for _, d := range resumeState.Done {
						if d == i {
							alreadyDone = true
							break
						}
					}
				}

				partHasher := crc64.New()
				partHasher.Write(chunk)
				partChecksumBytes := partHasher.Sum(nil)
				partChecksumBase64 := base64.StdEncoding.EncodeToString(partChecksumBytes)
				etag := fmt.Sprintf("\"%s-%d\"", initResp.UploadId, i)

				if alreadyDone {
					totalUploaded += int64(n)
					printer.Print(totalUploaded)
					confirmReq.Parts[i-1] = PartInfo{
						PartNumber: i,
						ETag:       etag,
						Checksum:   partChecksumBase64,
					}
					continue
				}

				partUploadUrl := initResp.UploadUrls[i-1]
				putReq, err := http.NewRequest("PUT", partUploadUrl, bytes.NewReader(chunk))
				if err != nil {
					fmt.Printf("\nError creating chunk request: %v\n", err)
					os.Exit(1)
				}
				putReq.Header.Set("Content-Type", mimeType)
				putReq.Header.Set("x-amz-checksum-crc64nvme", partChecksumBase64)
				putReq.ContentLength = int64(n)

				uploadClient := &http.Client{Timeout: 10 * time.Minute}
				putResp, err := uploadClient.Do(putReq)
				if err != nil {
					fmt.Printf("\nChunk %d upload network error: %v\n", i, err)
					os.Exit(1)
				}

				if putResp.StatusCode != 200 && putResp.StatusCode != 204 {
					bodyBytes, _ := io.ReadAll(putResp.Body)
					putResp.Body.Close()
					fmt.Printf("\nChunk %d upload failed (status %d): %s\n", i, putResp.StatusCode, string(bodyBytes))
					os.Exit(1)
				}
				putResp.Body.Close()

				retEtag := putResp.Header.Get("ETag")
				if retEtag != "" {
					etag = retEtag
				}

				confirmReq.Parts[i-1] = PartInfo{
					PartNumber: i,
					ETag:       etag,
					Checksum:   partChecksumBase64,
				}

				totalUploaded += int64(n)
				printer.Print(totalUploaded)

				// Save progress to state file
				if resumeState != nil {
					resumeState.Done = append(resumeState.Done, i)
					_ = resumeState.Save(stateFilename)
				}
			}
			if readErr != nil && readErr != io.EOF {
				fmt.Printf("\nError reading file: %v\n", readErr)
				os.Exit(1)
			}
		}
	} else {
		printer := &ProgressPrinter{
			title:      "Uploading...",
			total:      fileInfo.Size(),
			startTime:  time.Now(),
			firstPrint: true,
		}
		progressReader := &ProgressReader{
			reader:  file,
			printer: printer,
		}

		putReq, err := http.NewRequest("PUT", initResp.UploadUrl, progressReader)
		if err != nil {
			fmt.Printf("Error creating PUT request: %v\n", err)
			os.Exit(1)
		}

		putReq.Header.Set("Content-Type", mimeType)
		putReq.Header.Set("x-amz-checksum-sha256", hashBase64)
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

	// 6. Confirm upload
	confirmUrl := fmt.Sprintf("%s/api/v1/share/%s/confirm", serverUrl, initResp.ShareId)

	var confirmBody io.Reader = nil
	if isMultipart {
		cBytes, _ := json.Marshal(confirmReq)
		confirmBody = bytes.NewBuffer(cBytes)
	}

	client := &http.Client{Timeout: 30 * time.Second}
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

	var confirmData struct {
		DownloadCode string `json:"downloadCode"`
		ShareId      string `json:"shareId"`
	}
	_ = json.NewDecoder(confirmResp.Body).Decode(&confirmData)

	if confirmData.DownloadCode != "" {
		_ = clipboard.WriteAll(confirmData.DownloadCode)
	}

	// Cleanup upload resume state file
	if isMultipart {
		_ = DeleteResumeState(stateFilename)
	}

	shareLink := fmt.Sprintf("%s/share/%s", serverUrl, confirmData.ShareId)
	
	fmt.Printf("\n✓ Upload completed\n\n")
	fmt.Printf("File:\n%s\n\n", originalName)
	if confirmData.DownloadCode != "" {
		fmt.Printf("Code:\n%s\n\n", confirmData.DownloadCode)
	}
	fmt.Printf("Link:\n%s\n\n", cleanPrintName(shareLink))
	fmt.Printf("Expires:\n%s\n\n", *expireFlag)
	fmt.Printf("Size:\n%s\n", formatBytes(fileInfo.Size()))

	// Display QR code on completion
	showQR := false
	if *qrFlag {
		showQR = true
	} else if !*noQrFlag {
		showQR = ShouldShowQR(cfg.ShowQR)
	}
	if showQR {
		PrintQRCode(shareLink)
	}
}

func handleReceive(args []string) {
	cfg := LoadConfig()

	recvCmd := flag.NewFlagSet("receive", flag.ExitOnError)
	forceFlag := recvCmd.Bool("force", false, "Force overwrite if file/directory exists")
	forceShortFlag := recvCmd.Bool("f", false, "Force overwrite (shortcut)")
	renameFlag := recvCmd.Bool("rename", false, "Rename downloaded target if it exists")
	renameShortFlag := recvCmd.Bool("r", false, "Rename downloaded target (shortcut)")
	passwordFlag := recvCmd.String("password", "", "Decryption password if protected")
	mkdirFlag := recvCmd.Bool("mkdir", false, "Create destination directory if it doesn't exist")
	mkdirShortFlag := recvCmd.Bool("p", false, "Create destination directory (shortcut)")
	lanFlag := recvCmd.Bool("lan", false, "Enable direct LAN P2P transfer")

	err := recvCmd.Parse(args)
	if err != nil {
		fmt.Println("Error parsing flags:", err)
		os.Exit(1)
	}

	if recvCmd.NArg() < 1 {
		fmt.Println("✗ Error: Share link, ID, or download code is required.\n")
		fmt.Println("Usage:\n  uplink receive <share-link-or-code> [dest]")
		os.Exit(1)
	}

	shareInput := recvCmd.Arg(0)
	destPath := ""
	if recvCmd.NArg() >= 2 {
		destPath = recvCmd.Arg(1)
	}

	shareId := shareInput
	serverUrl := cfg.Server

	isShortCode, _ := regexp.MatchString(`^\d{10}$`, shareInput)
	if isShortCode {
		shareId = shareInput
		serverUrl = cfg.Server
	} else if strings.Contains(shareInput, "/share/") {
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
	} else {
		shareId = shareInput
		serverUrl = cfg.Server
	}

	serverUrl = sanitizeServerUrl(serverUrl)

	// LAN Peer discovery check
	if *lanFlag {
		fmt.Printf("Scanning LAN for mDNS service with share code %s...\n", shareId)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		addr, filename, size, fingerprint, mdnsErr := lan.DiscoverService(ctx, shareId)
		cancel()

		if mdnsErr == nil {
			fmt.Printf("Peer found on LAN at %s!\n", addr)
			fmt.Printf("File: %s (%s)\n", filename, formatBytes(size))
			fmt.Printf("Fingerprint: %s\n", fingerprint)

			// Resolve local output destination path
			sanitizedName := sanitizeFilename(filename)
			outputFilepath := sanitizedName
			var finalExtractDir string
			isArchive := strings.HasSuffix(filename, ".tar.gz")

			if isArchive {
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

			tempTarFile := outputFilepath
			if isArchive {
				tempTarFile = outputFilepath + ".download.tar.gz"
			}

			fmt.Printf("Downloading directly from local peer over HTTPS...\n")
			printer := &ProgressPrinter{
				title:      "Downloading (LAN)...",
				total:      size,
				startTime:  time.Now(),
				firstPrint: true,
			}

			lanUrl := fmt.Sprintf("https://%s", addr)
			err = lan.DownloadFileLAN(lanUrl, tempTarFile, 0, fingerprint, func(written int64) {
				printer.Print(written)
			})

			if err == nil {
				if isArchive {
					tarReader, err := os.Open(tempTarFile)
					if err != nil {
						fmt.Printf("✗ Error: Opening download archive failed: %v\n", err)
						os.Remove(tempTarFile)
						os.Exit(1)
					}
					err = tarball.Unpack(tarReader, finalExtractDir)
					tarReader.Close()
					os.Remove(tempTarFile)
					if err != nil {
						fmt.Printf("✗ Error: Extraction failed: %v\n", err)
						os.Exit(1)
					}
					fmt.Printf("\n✓ LAN Download completed & extracted to %s!\n", finalExtractDir)
				} else {
					fmt.Printf("\n✓ LAN Download completed successfully!\nDestination: %s\n", outputFilepath)
				}
				os.Exit(0)
			} else {
				fmt.Printf("\n✗ LAN Transfer failed: %v\n", err)
			}
		}

		fmt.Println("No peer found on LAN. Falling back to cloud download...")
	}

	client := &http.Client{Timeout: 15 * time.Second}

	// Fetch metadata from Cloud
	metaUrl := fmt.Sprintf("%s/api/v1/share/%s", serverUrl, shareId)
	resp, err := client.Get(metaUrl)
	if err != nil {
		fmt.Printf("✗ Error: Fetching metadata failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		fmt.Println("✗ Error: Download code not found.\n")
		fmt.Println("The file may have expired or the code is incorrect.")
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

	// Password prompter
	passwordToUse := *passwordFlag
	if meta.PasswordRequired && passwordToUse == "" {
		fmt.Print("This share is password-protected. Enter password: ")
		pwdBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			fmt.Printf("\nError reading password: %v\n", err)
			os.Exit(1)
		}
		fmt.Println()
		passwordToUse = strings.TrimSpace(string(pwdBytes))
	}

	// Authorize Download
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

	// Determine output path
	sanitizedName := sanitizeFilename(meta.Filename)
	outputFilepath := sanitizedName
	var finalExtractDir string

	if isArchive {
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

	tempTarFile := absOut
	if isArchive {
		tempTarFile = absOut + ".download.tar.gz"
	}

	// Probe range support
	rangeSupported := false
	probeReq, err := http.NewRequest("HEAD", authData.DownloadUrl, nil)
	if err == nil {
		probeResp, err := client.Do(probeReq)
		if err == nil {
			defer probeResp.Body.Close()
			if probeResp.Header.Get("Accept-Ranges") == "bytes" {
				rangeSupported = true
			}
		}
	}

	printer := &ProgressPrinter{
		title:      "Downloading...",
		total:      meta.Size,
		startTime:  time.Now(),
		firstPrint: true,
	}

	if rangeSupported {
		err = DownloadResumable(authData.DownloadUrl, tempTarFile, meta.HashValue, func(written int64) {
			printer.Print(written)
		})
	} else {
		downloadResp, err := http.Get(authData.DownloadUrl)
		if err != nil {
			fmt.Printf("✗ Error: Downloading file failed: %v\n", err)
			os.Exit(1)
		}
		defer downloadResp.Body.Close()

		if downloadResp.StatusCode != 200 {
			bodyBytes, _ := io.ReadAll(downloadResp.Body)
			fmt.Printf("✗ Error: Download failed (status %d): %s\n", downloadResp.StatusCode, string(bodyBytes))
			os.Exit(1)
		}

		outFd, err := os.OpenFile(tempTarFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			fmt.Printf("✗ Error: Creating output file failed: %v\n", err)
			os.Exit(1)
		}

		progDownload := &ProgressReader{
			reader:  downloadResp.Body,
			printer: printer,
		}

		downloadHasher := sha256.New()
		multiWriter := io.MultiWriter(outFd, downloadHasher)
		_, err = io.Copy(multiWriter, progDownload)
		outFd.Close()

		if err == nil {
			computedHex := hex.EncodeToString(downloadHasher.Sum(nil))
			if computedHex != meta.HashValue {
				fmt.Println("\n✗ Error: File integrity check failed!")
				os.Remove(tempTarFile)
				os.Exit(1)
			}
		}
	}

	if err != nil {
		os.Remove(tempTarFile)
		fmt.Printf("\n✗ Error: Download interrupted: %v\n", err)
		os.Exit(1)
	}

	// Unpack directory if archive
	if isArchive {
		tarReader, err := os.Open(tempTarFile)
		if err != nil {
			fmt.Printf("✗ Error: Opening download archive failed: %v\n", err)
			os.Remove(tempTarFile)
			os.Exit(1)
		}

		err = tarball.Unpack(tarReader, finalExtractDir)
		tarReader.Close()
		os.Remove(tempTarFile)

		if err != nil {
			fmt.Printf("✗ Error: Extraction failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n✓ Download completed\n\nFile:\n%s\n\nDestination:\n%s\n\nSize:\n%s\n", cleanFilename, finalExtractDir, formatBytes(meta.Size))
	} else {
		fmt.Printf("\n✓ Download completed\n\nFile:\n%s\n\nDestination:\n%s\n\nSize:\n%s\n", cleanFilename, absOut, formatBytes(meta.Size))
	}
}

type ProgressPrinter struct {
	title      string
	total      int64
	startTime  time.Time
	lastPrint  time.Time
	firstPrint bool
	isFinished bool
}

func (pp *ProgressPrinter) Print(read int64) {
	now := time.Now()
	if !pp.isFinished && read < pp.total && now.Sub(pp.lastPrint) < 100*time.Millisecond {
		return
	}
	pp.lastPrint = now

	percent := float64(0)
	if pp.total > 0 {
		percent = float64(read) / float64(pp.total)
	}
	percentInt := int(percent * 100)

	elapsed := time.Since(pp.startTime).Seconds()
	speed := 0.0
	if elapsed > 0 {
		speed = float64(read) / elapsed
	}

	etaStr := "Calculating..."
	if speed > 0 && pp.total > 0 {
		remainingBytes := pp.total - read
		etaSeconds := float64(remainingBytes) / speed
		if etaSeconds <= 0 {
			etaStr = "0 seconds"
		} else if etaSeconds < 60 {
			etaStr = fmt.Sprintf("%d seconds", int(etaSeconds))
		} else if etaSeconds < 3600 {
			etaStr = fmt.Sprintf("%d minutes", int(etaSeconds/60))
		} else {
			etaStr = fmt.Sprintf("%d hours", int(etaSeconds/3600))
		}
	}
	if read >= pp.total {
		etaStr = "0 seconds"
		pp.isFinished = true
	}

	barWidth := 20
	completed := int(percent * float64(barWidth))
	if completed > barWidth {
		completed = barWidth
	}
	barStr := strings.Repeat("█", completed) + strings.Repeat("░", barWidth-completed)

	speedStr := fmt.Sprintf("%s/s", formatBytes(int64(speed)))
	transferredStr := fmt.Sprintf("%s / %s", formatBytes(read), formatBytes(pp.total))

	if !pp.firstPrint {
		// print formatting is handled via carriage return
	} else {
		pp.firstPrint = false
	}

	fmt.Printf("\r\033[K%s [%s] %d%% (%s) | %s | ETA: %s", pp.title, barStr, percentInt, transferredStr, speedStr, etaStr)
	if read >= pp.total {
		fmt.Println()
	}
}

type ProgressReader struct {
	reader  io.Reader
	printer *ProgressPrinter
	read    int64
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.read += int64(n)
		pr.printer.Print(pr.read)
	}
	return n, err
}
