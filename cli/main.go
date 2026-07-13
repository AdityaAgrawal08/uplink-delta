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
	IsEncrypted       bool   `json:"isEncrypted,omitempty"`
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
	IsEncrypted      bool   `json:"isEncrypted"`
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
	case "queue":
		handleQueueSubcommand(os.Args[2:])
	case "watch":
		handleWatch(os.Args[2:])
	case "completion":
		if len(os.Args) < 3 {
			fmt.Println("Usage: uplink completion <bash|zsh|fish>")
			os.Exit(1)
		}
		handleCompletion(os.Args[2])
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
	fmt.Println("Uplink-Delta CLI Client (v3.1.0)")
	fmt.Println("Usage: uplink <command> [arguments] [flags]")
	fmt.Println()
	fmt.Println("Commands:")
	
	fmt.Println("  send        Upload a file or directory")
	fmt.Println("              uplink send report.pdf")
	fmt.Println()
	
	fmt.Println("  receive     Download a file or directory")
	fmt.Println("              uplink receive 4827165038")
	fmt.Println()

	fmt.Println("  config      Manage client configuration options")
	fmt.Println("              uplink config")
	fmt.Println()

	fmt.Println("  clean       Clean expired upload resume states")
	fmt.Println("              uplink clean")
	fmt.Println()

	fmt.Println("  queue       Manage offline upload queue")
	fmt.Println("              uplink queue")
	fmt.Println()

	fmt.Println("  watch       Watch a directory and auto-upload changes")
	fmt.Println("              uplink watch /path/to/dir")
	fmt.Println()

	fmt.Println("  completion  Generate shell autocompletion script")
	fmt.Println("              uplink completion bash")
	fmt.Println()
	
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
	encryptFlag := sendCmd.Bool("encrypt", false, "Enable Client-Side End-to-End Encryption")
	queueFlag := sendCmd.Bool("queue", false, "Queue upload locally and process in background")

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
		fmt.Println("✗ Error: File or directory path is required.")
		fmt.Println("Usage:\n  uplink send <path>")
		os.Exit(1)
	}

	inputPath := strings.Trim(sendCmd.Arg(0), "\"'")
	
	// Check queueing request
	if *queueFlag {
		fi, err := os.Stat(inputPath)
		if err != nil {
			fmt.Printf("✗ Error stating file for queue: %v\n", err)
			os.Exit(1)
		}
		item := &QueueItem{
			ID:        fmt.Sprintf("q_%d", time.Now().UnixNano()),
			Path:      inputPath,
			Filename:  fi.Name(),
			Size:      fi.Size(),
			Status:    "pending",
			MaxRetries: 5,
			CreatedAt: time.Now(),
			Flags: SendFlags{
				Password: *passwordFlag,
				Expire:   *expireFlag,
				Server:   *serverFlag,
				Lan:      *lanFlag,
				Encrypt:  *encryptFlag,
			},
		}
		err = saveQueueItem(item)
		if err != nil {
			fmt.Printf("✗ Error saving queue item: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ File enqueued successfully (ID: %s)\n", item.ID)
		os.Exit(0)
	}

	serverUrl := sanitizeServerUrl(*serverFlag)

	// Perform actual upload with fallback to queue if offline
	code, shareLink, filename, size, err := performCloudUploadWrapper(inputPath, *passwordFlag, expirySeconds, serverUrl, *encryptFlag, *lanFlag, *qrFlag, *noQrFlag)
	if err != nil {
		// Offline-first grace fallback: queue upload
		fmt.Printf("\n[Offline Fallback] Server is unreachable or upload failed: %v\n", err)
		fi, statErr := os.Stat(inputPath)
		if statErr == nil {
			item := &QueueItem{
				ID:        fmt.Sprintf("q_%d", time.Now().UnixNano()),
				Path:      inputPath,
				Filename:  fi.Name(),
				Size:      fi.Size(),
				Status:    "pending",
				MaxRetries: 5,
				CreatedAt: time.Now(),
				Flags: SendFlags{
					Password: *passwordFlag,
					Expire:   *expireFlag,
					Server:   *serverFlag,
					Lan:      *lanFlag,
					Encrypt:  *encryptFlag,
				},
			}
			_ = saveQueueItem(item)
			fmt.Printf("✓ Queued upload for automatic retry when network becomes available (ID: %s)\n", item.ID)
			os.Exit(0)
		}
		os.Exit(1)
	}

	fmt.Printf("\n✓ Upload completed\n\n")
	fmt.Printf("File:\n%s\n\n", filename)
	if code != "" {
		fmt.Printf("Code:\n%s\n\n", code)
	}
	fmt.Printf("Link:\n%s\n\n", cleanPrintName(shareLink))
	fmt.Printf("Expires:\n%s\n\n", *expireFlag)
	fmt.Printf("Size:\n%s\n", formatBytes(size))

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
	
	notifyTransferComplete(filename)
}

func performWatchUpload(filename string, flags SendFlags) (string, error) {
	expSec, _ := parseDurationToSeconds(flags.Expire)
	code, _, _, _, err := performCloudUploadWrapper(filename, flags.Password, expSec, flags.Server, flags.Encrypt, flags.Lan, false, true)
	return code, err
}

func performQueueUpload(item *QueueItem) error {
	expSec, _ := parseDurationToSeconds(item.Flags.Expire)
	_, _, _, _, err := performCloudUploadWrapper(item.Path, item.Flags.Password, expSec, item.Flags.Server, item.Flags.Encrypt, item.Flags.Lan, false, true)
	return err
}

func performCloudUploadWrapper(inputPath string, password string, expirySeconds int, serverUrl string, isEncrypted bool, enableLan bool, qrFlag bool, noQrFlag bool) (string, string, string, int64, error) {
	cfg := LoadConfig()
	fileInfo, err := os.Stat(inputPath)
	if err != nil {
		return "", "", "", 0, err
	}

	originalName := fileInfo.Name()
	var filePath string
	var isDirectory bool

	if fileInfo.IsDir() {
		isDirectory = true
		tempFile, err := os.CreateTemp("", "uplink_tarball_*.tar.gz")
		if err != nil {
			return "", "", "", 0, err
		}
		tempFile.Close()
		filePath = tempFile.Name()
		defer os.Remove(filePath)

		outFd, err := os.OpenFile(filePath, os.O_WRONLY, 0600)
		if err != nil {
			return "", "", "", 0, err
		}
		err = tarball.Pack(inputPath, outFd)
		outFd.Close()
		if err != nil {
			return "", "", "", 0, err
		}
		fileInfo, _ = os.Stat(filePath)
	} else {
		filePath = inputPath
	}

	maxAllowedSize := int64(200 * 1024 * 1024)
	if isDirectory {
		maxAllowedSize = int64(500 * 1024 * 1024)
	}
	if fileInfo.Size() > maxAllowedSize {
		return "", "", "", 0, fmt.Errorf("upload exceeds size limit of %d MB", maxAllowedSize/(1024*1024))
	}

	// Client-side End-to-End Encryption
	var keyHex string
	if isEncrypted {
		tempEncFile, err := os.CreateTemp("", "uplink_encrypted_*.enc")
		if err != nil {
			return "", "", "", 0, fmt.Errorf("E2EE temp file creation: %w", err)
		}
		tempEncFile.Close()
		defer os.Remove(tempEncFile.Name())

		keyHex, err = EncryptFileStream(filePath, tempEncFile.Name())
		if err != nil {
			return "", "", "", 0, fmt.Errorf("E2EE encryption stream: %w", err)
		}
		filePath = tempEncFile.Name()
		fileInfo, _ = os.Stat(filePath)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", "", "", 0, err
	}
	defer file.Close()

	// Analyze file integrity
	shaHasher := sha256.New()
	var crcHasher hash.Hash64
	var multiWriter io.Writer

	chunkSize := int64(10 * 1024 * 1024)
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
		return "", "", "", 0, err
	}

	hashBytes := shaHasher.Sum(nil)
	hashHex := hex.EncodeToString(hashBytes)
	hashBase64 := base64.StdEncoding.EncodeToString(hashBytes)

	var crcBase64 string
	if isMultipart {
		crcBytes := crcHasher.Sum(nil)
		crcBase64 = base64.StdEncoding.EncodeToString(crcBytes)
	}

	_, _ = file.Seek(0, 0)

	// LAN Peer mode (if enabled)
	if enableLan {
		cert, fingerprint, err := lan.GenerateEphemeralCert()
		if err == nil {
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
			if listenErr == nil {
				shareCode := generateShareCode()
				shareUrl := fmt.Sprintf("uplink receive %s --lan", shareCode)
				
				fmt.Printf("\n✓ Direct LAN P2P Transfer Initialized!\n")
				fmt.Printf("Share Code: %s\n", shareCode)
				fmt.Printf("Fingerprint: %s\n", fingerprint)

				showQR := false
				if qrFlag {
					showQR = true
				} else if !noQrFlag {
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
					Hostname:         hostname,
					Port:             port,
					ShareCode:        shareCode,
					FileName:         originalName,
					Size:             fileInfo.Size(),
					Fingerprint:      fingerprint,
					FileSHA256:       hashHex,
					PasswordRequired: password != "",
				}
				
				shutdownMDNS, mdnsErr := lan.RegisterService(serviceInfo)
				if mdnsErr == nil {
					ctx, cancel := context.WithCancel(context.Background())
					serverDone := make(chan struct{})
					go func() {
						err := lan.ServeFileLAN(ctx, filePath, port, cert, shareCode, password, 1, func() {
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
						cancel()
						shutdownMDNS()
					}
				}
			}
		}
	}

	// Cloud Init
	var resumeState *ResumeState
	var isResume bool
	stateFilename := sanitizeFilename(originalName) + ".json"

	var serverParts map[int]PartInfo

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
					serverParts = make(map[int]PartInfo)
					for _, p := range partsData.Parts {
						serverParts[p.PartNumber] = p
					}
					for _, p := range state.Parts {
						serverParts[p.PartNumber] = p
					}

					var mergedParts []int
					for pNum := range serverParts {
						mergedParts = append(mergedParts, pNum)
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
			Password:         password,
			ExpiresInSeconds: expirySeconds,
			PartsCount:       partsCount,
			IsEncrypted:      isEncrypted,
		}
		if isMultipart {
			initReq.ChecksumCrc64nvme = crcBase64
		}

		jsonBytes, err := json.Marshal(initReq)
		if err != nil {
			return "", "", "", 0, err
		}

		initUrl := fmt.Sprintf("%s/api/v1/share/init", serverUrl)
		req, err := http.NewRequest("POST", initUrl, bytes.NewBuffer(jsonBytes))
		if err != nil {
			return "", "", "", 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", fmt.Sprintf("cli_%d_%s", time.Now().UnixNano(), hashHex[:8]))

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", "", "", 0, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 201 {
			bodyBytes, _ := io.ReadAll(resp.Body)
			return "", "", "", 0, fmt.Errorf("status %d: %s", resp.StatusCode, string(bodyBytes))
		}

		err = json.NewDecoder(resp.Body).Decode(&initResp)
		if err != nil {
			return "", "", "", 0, err
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
				Parts:      []PartInfo{},
				TotalParts: partsCount,
				Timestamp:  time.Now().Format(time.RFC3339),
			}
			_ = resumeState.Save(stateFilename)
		}
	}

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
			n, readErr := file.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]
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
					if serverPart, ok := serverParts[i]; ok {
						confirmReq.Parts[i-1] = serverPart
					} else {
						confirmReq.Parts[i-1] = PartInfo{
							PartNumber: i,
							ETag:       etag,
							Checksum:   partChecksumBase64,
						}
					}
					continue
				}

				partUploadUrl := initResp.UploadUrls[i-1]
				putReq, err := http.NewRequest("PUT", partUploadUrl, bytes.NewReader(chunk))
				if err != nil {
					return "", "", "", 0, err
				}
				putReq.Header.Set("Content-Type", mimeType)
				putReq.Header.Set("x-amz-checksum-crc64nvme", partChecksumBase64)
				putReq.ContentLength = int64(n)

				uploadClient := &http.Client{Timeout: 10 * time.Minute}
				putResp, err := uploadClient.Do(putReq)
				if err != nil {
					return "", "", "", 0, err
				}

				if putResp.StatusCode != 200 && putResp.StatusCode != 204 {
					bodyBytes, _ := io.ReadAll(putResp.Body)
					putResp.Body.Close()
					return "", "", "", 0, fmt.Errorf("part %d failed: %s", i, string(bodyBytes))
				}
				putResp.Body.Close()

				retEtag := putResp.Header.Get("ETag")
				if retEtag != "" {
					etag = retEtag
				}

				pInfo := PartInfo{
					PartNumber: i,
					ETag:       etag,
					Checksum:   partChecksumBase64,
				}
				confirmReq.Parts[i-1] = pInfo

				totalUploaded += int64(n)
				printer.Print(totalUploaded)

				if resumeState != nil {
					resumeState.Done = append(resumeState.Done, i)
					resumeState.Parts = append(resumeState.Parts, pInfo)
					_ = resumeState.Save(stateFilename)
				}
			}
			if readErr != nil && readErr != io.EOF {
				return "", "", "", 0, readErr
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
			return "", "", "", 0, err
		}

		putReq.Header.Set("Content-Type", mimeType)
		putReq.Header.Set("x-amz-checksum-sha256", hashBase64)
		putReq.ContentLength = fileInfo.Size()

		uploadClient := &http.Client{Timeout: 30 * time.Minute}
		putResp, err := uploadClient.Do(putReq)
		if err != nil {
			return "", "", "", 0, err
		}
		defer putResp.Body.Close()

		if putResp.StatusCode != 200 && putResp.StatusCode != 204 {
			bodyBytes, _ := io.ReadAll(putResp.Body)
			return "", "", "", 0, fmt.Errorf("upload status %d: %s", putResp.StatusCode, string(bodyBytes))
		}
		fmt.Println()
	}

	// Confirm Upload
	confirmUrl := fmt.Sprintf("%s/api/v1/share/%s/confirm", serverUrl, initResp.ShareId)
	var confirmBody io.Reader = nil
	if isMultipart {
		cBytes, _ := json.Marshal(confirmReq)
		confirmBody = bytes.NewBuffer(cBytes)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	confirmReqObj, err := http.NewRequest("POST", confirmUrl, confirmBody)
	if err != nil {
		return "", "", "", 0, err
	}
	confirmReqObj.Header.Set("Content-Type", "application/json")

	confirmResp, err := client.Do(confirmReqObj)
	if err != nil {
		return "", "", "", 0, err
	}
	defer confirmResp.Body.Close()

	if confirmResp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(confirmResp.Body)
		return "", "", "", 0, fmt.Errorf("confirm status %d: %s", confirmResp.StatusCode, string(bodyBytes))
	}

	var confirmData struct {
		DownloadCode string `json:"downloadCode"`
		ShareId      string `json:"shareId"`
	}
	_ = json.NewDecoder(confirmResp.Body).Decode(&confirmData)

	if isMultipart {
		_ = DeleteResumeState(stateFilename)
	}

	finalCode := confirmData.DownloadCode
	if isEncrypted {
		finalCode = finalCode + ":" + keyHex
	}

	shareLink := fmt.Sprintf("%s/share/%s", serverUrl, confirmData.ShareId)
	if isEncrypted {
		shareLink = shareLink + ":" + keyHex
	}

	if finalCode != "" {
		_ = clipboard.WriteAll(finalCode)
	}

	return finalCode, shareLink, originalName, fileInfo.Size(), nil
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
		fmt.Println("✗ Error: Share link, ID, or download code is required.")
		fmt.Println("Usage:\n  uplink receive <share-link-or-code> [dest]")
		os.Exit(1)
	}

	shareInput := recvCmd.Arg(0)
	destPath := ""
	if recvCmd.NArg() >= 2 {
		destPath = recvCmd.Arg(1)
	}

	// Client-side E2EE decryption key check
	keyHex := ""
	if idx := strings.Index(shareInput, ":"); idx != -1 {
		keyHex = shareInput[idx+1:]
		shareInput = shareInput[:idx]
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
		addr, filename, size, fingerprint, fileSHA256, passwordRequired, mdnsErr := lan.DiscoverService(ctx, shareId)
		cancel()

		if mdnsErr == nil {
			fmt.Printf("Peer found on LAN at %s!\n", addr)
			fmt.Printf("File: %s (%s)\n", filename, formatBytes(size))
			fmt.Printf("Fingerprint: %s\n", fingerprint)

			pwdToUse := *passwordFlag
			if passwordRequired && pwdToUse == "" {
				fmt.Print("This LAN share is password-protected. Enter password: ")
				pwdBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
				if err != nil {
					fmt.Printf("\nError reading password: %v\n", err)
					os.Exit(1)
				}
				fmt.Println()
				pwdToUse = strings.TrimSpace(string(pwdBytes))
			}

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
					finalExtractDir = destPath
				}
			}

			tempTarFile := outputFilepath
			if isArchive {
				tempTarFile = outputFilepath + ".download.tar.gz"
			}
			if keyHex != "" {
				tempTarFile = tempTarFile + ".enc"
			}

			fmt.Printf("Downloading directly from local peer over HTTPS...\n")
			printer := &ProgressPrinter{
				title:      "Downloading (LAN)...",
				total:      size,
				startTime:  time.Now(),
				firstPrint: true,
			}

			lanUrl := fmt.Sprintf("https://%s", addr)
			err = lan.DownloadFileLAN(lanUrl, tempTarFile, 0, fingerprint, shareId, pwdToUse, fileSHA256, func(written int64) {
				printer.Print(written)
			})

			if err == nil {
				// Decrypt if E2EE
				if keyHex != "" {
					decryptedFile := strings.TrimSuffix(tempTarFile, ".enc")
					fmt.Println("\nDecrypting file client-side (E2EE)...")
					err = DecryptFileStream(tempTarFile, decryptedFile, keyHex)
					os.Remove(tempTarFile)
					if err != nil {
						fmt.Printf("✗ Decryption error: %v\n", err)
						os.Exit(1)
					}
					tempTarFile = decryptedFile
				}

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
				notifyTransferComplete(filename)
				os.Exit(0)
			} else {
				fmt.Printf("\n✗ LAN Transfer failed: %v\n", err)
			}
		}
		fmt.Println("No peer found on LAN. Falling back to cloud download...")
	}

	// Cloud Download fallback
	client := &http.Client{Timeout: 15 * time.Second}
	metaUrl := fmt.Sprintf("%s/api/v1/share/%s", serverUrl, shareId)
	resp, err := client.Get(metaUrl)
	if err != nil {
		fmt.Printf("✗ Error: Fetching metadata failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		fmt.Println("✗ Error: Download code not found.")
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
			finalExtractDir = destPath
		}
	}

	absOut, err := filepath.Abs(outputFilepath)
	if err != nil {
		fmt.Printf("Error resolving output path: %v\n", err)
		os.Exit(1)
	}

	// Overwrite check
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
	if keyHex != "" {
		tempTarFile = tempTarFile + ".enc"
	}

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

		outFd, err := os.OpenFile(tempTarFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
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

	// Decrypt if encrypted
	if keyHex != "" {
		decryptedFile := strings.TrimSuffix(tempTarFile, ".enc")
		fmt.Println("\nDecrypting file client-side (E2EE)...")
		err = DecryptFileStream(tempTarFile, decryptedFile, keyHex)
		os.Remove(tempTarFile)
		if err != nil {
			fmt.Printf("✗ Decryption error: %v\n", err)
			os.Exit(1)
		}
		tempTarFile = decryptedFile
	}

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

	notifyTransferComplete(meta.Filename)
}

func handleWatch(args []string) {
	cfg := LoadConfig()
	watchCmd := flag.NewFlagSet("watch", flag.ExitOnError)
	passwordFlag := watchCmd.String("password", "", "Password to protect watch shares")
	expireFlag := watchCmd.String("expire", cfg.Expiry, "Expiration duration")
	serverFlag := watchCmd.String("server", cfg.Server, "Server base URL")
	lanFlag := watchCmd.Bool("lan", false, "Enable direct LAN P2P transfer")
	encryptFlag := watchCmd.Bool("encrypt", false, "Enable client-side encryption")

	_ = watchCmd.Parse(args)
	if watchCmd.NArg() < 1 {
		fmt.Println("Usage: uplink watch <directory>")
		os.Exit(1)
	}

	dir := watchCmd.Arg(0)
	flags := SendFlags{
		Password: *passwordFlag,
		Expire:   *expireFlag,
		Server:   *serverFlag,
		Lan:      *lanFlag,
		Encrypt:  *encryptFlag,
	}

	err := WatchDirectory(dir, flags)
	if err != nil {
		fmt.Printf("Watch error: %v\n", err)
		os.Exit(1)
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
