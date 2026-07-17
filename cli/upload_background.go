package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AdityaAgrawal08/uplink-delta/cli/pkg/crc64"
)

type BackgroundUploadState struct {
	FileId     string
	Filename   string
	Size       int64
	Uploaded   int64
	Status     string // "ANNOUNCED", "UPLOADING", "UPLOADED", "FAILED"
	Err        error
}

var (
	UploadsMutex   sync.Mutex
	ActiveUploads  = make(map[string]*BackgroundUploadState)
)

func AddActiveUpload(fileId, filename string, size int64) {
	UploadsMutex.Lock()
	defer UploadsMutex.Unlock()
	ActiveUploads[fileId] = &BackgroundUploadState{
		FileId:   fileId,
		Filename: filename,
		Size:     size,
		Status:   "ANNOUNCED",
	}
}

func UpdateUploadProgress(fileId string, uploaded int64, status string, err error) {
	UploadsMutex.Lock()
	defer UploadsMutex.Unlock()
	if state, ok := ActiveUploads[fileId]; ok {
		state.Uploaded = uploaded
		state.Status = status
		state.Err = err
	}
}

func StartBackgroundUpload(sessionId, username, serverUrl, filePath, sessionPassword string, encrypt bool) {
	go func() {
		err := performBackgroundUpload(sessionId, username, serverUrl, filePath, sessionPassword, encrypt)
		if err != nil {
			fmt.Printf("\n[Background Upload Error] %s: %v\n", filepath.Base(filePath), err)
		}
	}()
}

func performBackgroundUpload(sessionId, username, serverUrl, filePath, sessionPassword string, encrypt bool) error {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	originalName := fileInfo.Name()

	// 1. Prepare E2EE if enabled
	var keyHex string
	var uploadFilePath = filePath
	if encrypt {
		tempEncFile, err := os.CreateTemp("", "uplink_background_enc_*.enc")
		if err != nil {
			return fmt.Errorf("temp file creation: %w", err)
		}
		tempEncFile.Close()
		defer os.Remove(tempEncFile.Name())

		keyHex, err = EncryptFileStream(filePath, tempEncFile.Name())
		if err != nil {
			return fmt.Errorf("encryption failed: %w", err)
		}
		uploadFilePath = tempEncFile.Name()
		fileInfo, _ = os.Stat(uploadFilePath)
	}

	file, err := os.Open(uploadFilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Compute hashes
	shaHasher := sha256.New()
	var crcHasher hash.Hash64
	var multiWriter io.Writer

	// 4 MB chunks for multipart
	chunkSize := int64(4 * 1024 * 1024)
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
		return err
	}

	hashBytes := shaHasher.Sum(nil)
	hashHex := hex.EncodeToString(hashBytes)

	var crcBase64 string
	if isMultipart {
		crcBytes := crcHasher.Sum(nil)
		crcBase64 = base64.StdEncoding.EncodeToString(crcBytes)
	}

	_, _ = file.Seek(0, 0)

	// 2. Announce file to session
	announceUrl := fmt.Sprintf("%s/api/v1/session/%s/announce", serverUrl, sessionId)
	announceReq := SessionAnnounceRequest{
		Filename:      originalName,
		Size:          fileInfo.Size(),
		SHA256:        hashHex,
		EncryptionKey: keyHex,
	}
	annBytes, err := json.Marshal(announceReq)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", announceUrl, bytes.NewBuffer(annBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Uplink-Username", username)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("announcement failed with status %d", resp.StatusCode)
	}

	var annRes SessionAnnounceResponse
	err = json.NewDecoder(resp.Body).Decode(&annRes)
	if err != nil {
		return err
	}

	// Add to background tracker
	AddActiveUpload(annRes.FileId, originalName, fileInfo.Size())

	// Register locally for P2P WAN sharing
	LocalFilesMutex.Lock()
	SessionLocalFiles[annRes.ShareId] = filePath
	LocalFilesMutex.Unlock()

	// 3. Initialize Cloud Share Upload
	UpdateUploadProgress(annRes.FileId, 0, "UPLOADING", nil)

	mimeType := mime.TypeByExtension(filepath.Ext(originalName))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	initReq := InitRequest{
		Filename:         originalName,
		Size:             fileInfo.Size(),
		MimeType:         mimeType,
		HashValue:        hashHex,
		Password:         sessionPassword, // Reuse session password for the file share
		ExpiresInSeconds: 3600,            // Expires in 1 hour aligned with session max
		PartsCount:       partsCount,
		IsEncrypted:      encrypt,
	}
	if isMultipart {
		initReq.ChecksumCrc64nvme = crcBase64
	}

	// We pass the pre-allocated shareId to link them
	type InitRequestWithShare struct {
		InitRequest
		ShareId string `json:"shareId"`
	}
	initReqWithShare := InitRequestWithShare{
		InitRequest: initReq,
		ShareId:     annRes.ShareId,
	}

	initBytes, err := json.Marshal(initReqWithShare)
	if err != nil {
		UpdateUploadProgress(annRes.FileId, 0, "FAILED", err)
		return err
	}

	initUrl := fmt.Sprintf("%s/api/v1/share/init", serverUrl)
	req, err = http.NewRequest("POST", initUrl, bytes.NewBuffer(initBytes))
	if err != nil {
		UpdateUploadProgress(annRes.FileId, 0, "FAILED", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		UpdateUploadProgress(annRes.FileId, 0, "FAILED", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		err = fmt.Errorf("share init status %d: %s", resp.StatusCode, string(bodyBytes))
		UpdateUploadProgress(annRes.FileId, 0, "FAILED", err)
		return err
	}

	var initResp InitResponse
	err = json.NewDecoder(resp.Body).Decode(&initResp)
	if err != nil {
		UpdateUploadProgress(annRes.FileId, 0, "FAILED", err)
		return err
	}

	// 4. Perform Upload with 3 retries
	var confirmReq ConfirmRequest
	if isMultipart {
		confirmReq.Parts = make([]PartInfo, partsCount)
		buffer := make([]byte, chunkSize)
		totalUploaded := int64(0)

		for i := 1; i <= partsCount; i++ {
			n, readErr := file.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]
				partHasher := crc64.New()
				partHasher.Write(chunk)
				partChecksumBase64 := base64.StdEncoding.EncodeToString(partHasher.Sum(nil))
				etag := fmt.Sprintf("\"%s-%d\"", initResp.UploadId, i)

				var putResp *http.Response
				var putErr error

				// Retry loop for part upload
				for retry := 0; retry < 3; retry++ {
					if retry > 0 {
						time.Sleep(time.Duration(retry*retry) * time.Second)
					}
					partUploadUrl := initResp.UploadUrls[i-1]
					putReq, err := http.NewRequest("PUT", partUploadUrl, bytes.NewReader(chunk))
					if err != nil {
						putErr = err
						continue
					}
					putReq.Header.Set("Content-Type", mimeType)
					putReq.ContentLength = int64(n)

					uploadClient := &http.Client{Timeout: 10 * time.Minute}
					putResp, putErr = uploadClient.Do(putReq)
					if putErr == nil {
						if putResp.StatusCode == 200 || putResp.StatusCode == 204 {
							break
						}
						bodyBytes, _ := io.ReadAll(putResp.Body)
						putResp.Body.Close()
						putErr = fmt.Errorf("part %d failed: status %d %s", i, putResp.StatusCode, string(bodyBytes))
					}
				}

				if putErr != nil {
					UpdateUploadProgress(annRes.FileId, totalUploaded, "FAILED", putErr)
					return putErr
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
				UpdateUploadProgress(annRes.FileId, totalUploaded, "UPLOADING", nil)
			}
			if readErr != nil && readErr != io.EOF {
				UpdateUploadProgress(annRes.FileId, totalUploaded, "FAILED", readErr)
				return readErr
			}
		}
	} else {
		// Single part upload
		var putResp *http.Response
		var putErr error

		for retry := 0; retry < 3; retry++ {
			if retry > 0 {
				time.Sleep(time.Duration(retry*retry) * time.Second)
			}
			// We track progress through custom Reader wrapper
			_, _ = file.Seek(0, 0)
			progressReader := &ProgressReaderWrapper{
				reader: file,
				onProgress: func(written int64) {
					UpdateUploadProgress(annRes.FileId, written, "UPLOADING", nil)
				},
			}

			putReq, err := http.NewRequest("PUT", initResp.UploadUrl, progressReader)
			if err != nil {
				putErr = err
				continue
			}
			putReq.Header.Set("Content-Type", mimeType)
			putReq.ContentLength = fileInfo.Size()

			uploadClient := &http.Client{Timeout: 30 * time.Minute}
			putResp, putErr = uploadClient.Do(putReq)
			if putErr == nil {
				if putResp.StatusCode == 200 || putResp.StatusCode == 204 {
					break
				}
				bodyBytes, _ := io.ReadAll(putResp.Body)
				putResp.Body.Close()
				putErr = fmt.Errorf("upload failed: status %d %s", putResp.StatusCode, string(bodyBytes))
			}
		}

		if putErr != nil {
			UpdateUploadProgress(annRes.FileId, 0, "FAILED", putErr)
			return putErr
		}
		putResp.Body.Close()
	}

	// 5. Confirm Upload on Cloud Share
	confirmUrl := fmt.Sprintf("%s/api/v1/share/%s/confirm", serverUrl, initResp.ShareId)
	var confirmBody io.Reader = nil
	if isMultipart {
		cBytes, _ := json.Marshal(confirmReq)
		confirmBody = bytes.NewBuffer(cBytes)
	}

	confirmReqObj, err := http.NewRequest("POST", confirmUrl, confirmBody)
	if err != nil {
		UpdateUploadProgress(annRes.FileId, fileInfo.Size(), "FAILED", err)
		return err
	}
	confirmReqObj.Header.Set("Content-Type", "application/json")

	confirmResp, err := client.Do(confirmReqObj)
	if err != nil {
		UpdateUploadProgress(annRes.FileId, fileInfo.Size(), "FAILED", err)
		return err
	}
	defer confirmResp.Body.Close()

	if confirmResp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(confirmResp.Body)
		err = fmt.Errorf("confirm status %d: %s", confirmResp.StatusCode, string(bodyBytes))
		UpdateUploadProgress(annRes.FileId, fileInfo.Size(), "FAILED", err)
		return err
	}

	// 6. Complete Session Upload Status
	completeUrl := fmt.Sprintf("%s/api/v1/session/%s/upload-complete", serverUrl, sessionId)
	completeReq := SessionUploadCompleteRequest{
		FileId:  annRes.FileId,
		ShareId: annRes.ShareId,
	}
	compBytes, _ := json.Marshal(completeReq)
	
	compReq, err := http.NewRequest("POST", completeUrl, bytes.NewBuffer(compBytes))
	if err != nil {
		UpdateUploadProgress(annRes.FileId, fileInfo.Size(), "FAILED", err)
		return err
	}
	compReq.Header.Set("Content-Type", "application/json")
	compReq.Header.Set("X-Uplink-Username", username)

	compResp, err := client.Do(compReq)
	if err != nil {
		UpdateUploadProgress(annRes.FileId, fileInfo.Size(), "FAILED", err)
		return err
	}
	defer compResp.Body.Close()

	if compResp.StatusCode != 200 {
		err = fmt.Errorf("session completion failed status %d", compResp.StatusCode)
		UpdateUploadProgress(annRes.FileId, fileInfo.Size(), "FAILED", err)
		return err
	}

	UpdateUploadProgress(annRes.FileId, fileInfo.Size(), "UPLOADED", nil)
	return nil
}

type ProgressReaderWrapper struct {
	reader     io.Reader
	onProgress func(int64)
	written    int64
}

func (pr *ProgressReaderWrapper) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.written += int64(n)
		if pr.onProgress != nil {
			pr.onProgress(pr.written)
		}
	}
	return n, err
}

func performSessionUpload(sess *ActiveSession, filePath string, encrypt bool) error {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	originalName := fileInfo.Name()

	var keyHex string
	var uploadFilePath = filePath
	if encrypt {
		tempEncFile, err := os.CreateTemp("", "uplink_session_enc_*.enc")
		if err != nil {
			return fmt.Errorf("temp file creation: %w", err)
		}
		tempEncFile.Close()
		defer os.Remove(tempEncFile.Name())

		keyHex, err = EncryptFileStream(filePath, tempEncFile.Name())
		if err != nil {
			return fmt.Errorf("encryption failed: %w", err)
		}
		uploadFilePath = tempEncFile.Name()
		fileInfo, _ = os.Stat(uploadFilePath)
	}

	file, err := os.Open(uploadFilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Compute hashes
	shaHasher := sha256.New()
	var crcHasher hash.Hash64
	var multiWriter io.Writer

	chunkSize := int64(4 * 1024 * 1024)
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
		return err
	}

	hashBytes := shaHasher.Sum(nil)
	hashHex := hex.EncodeToString(hashBytes)

	var crcBase64 string
	if isMultipart {
		crcBytes := crcHasher.Sum(nil)
		crcBase64 = base64.StdEncoding.EncodeToString(crcBytes)
	}

	_, _ = file.Seek(0, 0)

	// Announce file
	announceUrl := fmt.Sprintf("%s/api/v1/session/%s/announce", sess.Server, sess.SessionId)
	announceReq := SessionAnnounceRequest{
		Filename:      originalName,
		Size:          fileInfo.Size(),
		SHA256:        hashHex,
		EncryptionKey: keyHex,
	}
	annBytes, err := json.Marshal(announceReq)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", announceUrl, bytes.NewBuffer(annBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Uplink-Username", sess.Username)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("announcement failed: status %d", resp.StatusCode)
	}

	var annRes SessionAnnounceResponse
	err = json.NewDecoder(resp.Body).Decode(&annRes)
	if err != nil {
		return err
	}

	fmt.Printf("File announced successfully. Initializing cloud share...\n")

	// Register locally for P2P serving
	LocalFilesMutex.Lock()
	SessionLocalFiles[annRes.ShareId] = filePath
	LocalFilesMutex.Unlock()

	mimeType := mime.TypeByExtension(filepath.Ext(originalName))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	initReq := InitRequest{
		Filename:         originalName,
		Size:             fileInfo.Size(),
		MimeType:         mimeType,
		HashValue:        hashHex,
		Password:         sess.Password,
		ExpiresInSeconds: 3600,
		PartsCount:       partsCount,
		IsEncrypted:      encrypt,
	}
	if isMultipart {
		initReq.ChecksumCrc64nvme = crcBase64
	}

	type InitRequestWithShare struct {
		InitRequest
		ShareId string `json:"shareId"`
	}
	initReqWithShare := InitRequestWithShare{
		InitRequest: initReq,
		ShareId:     annRes.ShareId,
	}

	initBytes, err := json.Marshal(initReqWithShare)
	if err != nil {
		return err
	}

	initUrl := fmt.Sprintf("%s/api/v1/share/init", sess.Server)
	req, err = http.NewRequest("POST", initUrl, bytes.NewBuffer(initBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("share init status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var initResp InitResponse
	err = json.NewDecoder(resp.Body).Decode(&initResp)
	if err != nil {
		return err
	}

	printer := &ProgressPrinter{
		title:      "Uploading...",
		total:      fileInfo.Size(),
		startTime:  time.Now(),
		firstPrint: true,
	}

	var confirmReq ConfirmRequest
	if isMultipart {
		confirmReq.Parts = make([]PartInfo, partsCount)
		buffer := make([]byte, chunkSize)
		totalUploaded := int64(0)

		for i := 1; i <= partsCount; i++ {
			n, readErr := file.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]
				partHasher := crc64.New()
				partHasher.Write(chunk)
				partChecksumBase64 := base64.StdEncoding.EncodeToString(partHasher.Sum(nil))
				etag := fmt.Sprintf("\"%s-%d\"", initResp.UploadId, i)

				var putResp *http.Response
				var putErr error

				for retry := 0; retry < 3; retry++ {
					if retry > 0 {
						time.Sleep(time.Duration(retry*retry) * time.Second)
					}
					partUploadUrl := initResp.UploadUrls[i-1]
					putReq, err := http.NewRequest("PUT", partUploadUrl, bytes.NewReader(chunk))
					if err != nil {
						putErr = err
						continue
					}
					putReq.Header.Set("Content-Type", mimeType)
					putReq.ContentLength = int64(n)

					uploadClient := &http.Client{Timeout: 10 * time.Minute}
					putResp, putErr = uploadClient.Do(putReq)
					if putErr == nil {
						if putResp.StatusCode == 200 || putResp.StatusCode == 204 {
							break
						}
						bodyBytes, _ := io.ReadAll(putResp.Body)
						putResp.Body.Close()
						putErr = fmt.Errorf("part %d failed: status %d %s", i, putResp.StatusCode, string(bodyBytes))
					}
				}

				if putErr != nil {
					return putErr
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
			}
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
		}
	} else {
		progressReader := &ProgressReader{
			reader:  file,
			printer: printer,
		}

		putReq, err := http.NewRequest("PUT", initResp.UploadUrl, progressReader)
		if err != nil {
			return err
		}
		putReq.Header.Set("Content-Type", mimeType)
		putReq.ContentLength = fileInfo.Size()

		uploadClient := &http.Client{Timeout: 30 * time.Minute}
		putResp, err := uploadClient.Do(putReq)
		if err != nil {
			return err
		}
		defer putResp.Body.Close()

		if putResp.StatusCode != 200 && putResp.StatusCode != 204 {
			bodyBytes, _ := io.ReadAll(putResp.Body)
			return fmt.Errorf("upload status %d: %s", putResp.StatusCode, string(bodyBytes))
		}
		fmt.Println()
	}

	// Confirm
	confirmUrl := fmt.Sprintf("%s/api/v1/share/%s/confirm", sess.Server, initResp.ShareId)
	var confirmBody io.Reader = nil
	if isMultipart {
		cBytes, _ := json.Marshal(confirmReq)
		confirmBody = bytes.NewBuffer(cBytes)
	}

	confirmReqObj, err := http.NewRequest("POST", confirmUrl, confirmBody)
	if err != nil {
		return err
	}
	confirmReqObj.Header.Set("Content-Type", "application/json")

	confirmResp, err := client.Do(confirmReqObj)
	if err != nil {
		return err
	}
	defer confirmResp.Body.Close()

	if confirmResp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(confirmResp.Body)
		return fmt.Errorf("confirm status %d: %s", confirmResp.StatusCode, string(bodyBytes))
	}

	// Complete upload status
	completeUrl := fmt.Sprintf("%s/api/v1/session/%s/upload-complete", sess.Server, sess.SessionId)
	completeReq := SessionUploadCompleteRequest{
		FileId:  annRes.FileId,
		ShareId: annRes.ShareId,
	}
	compBytes, _ := json.Marshal(completeReq)
	
	compReq, err := http.NewRequest("POST", completeUrl, bytes.NewBuffer(compBytes))
	if err != nil {
		return err
	}
	compReq.Header.Set("Content-Type", "application/json")
	compReq.Header.Set("X-Uplink-Username", sess.Username)

	compResp, err := client.Do(compReq)
	if err != nil {
		return err
	}
	defer compResp.Body.Close()

	if compResp.StatusCode != 200 {
		return fmt.Errorf("session completion failed status %d", compResp.StatusCode)
	}

	return nil
}
