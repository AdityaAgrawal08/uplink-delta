"use client";

import React, { useState, useRef } from "react";
import {
  Upload,
  Shield,
  Clock,
  Download,
  Copy,
  Check,
  File,
  Lock,
  Loader2,
  AlertCircle,
  Link as LinkIcon,
} from "lucide-react";

export default function Home() {
  const [file, setFile] = useState<File | null>(null);
  const [hashing, setHashing] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [uploadProgress, setUploadProgress] = useState(0);
  const [shareLink, setShareLink] = useState("");
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);

  // Settings
  const [expiresIn, setExpiresIn] = useState("86400"); // 24 hours
  const [downloadLimit, setDownloadLimit] = useState("10");
  const [password, setPassword] = useState("");
  const [usePassword, setUsePassword] = useState(false);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const [isDragActive, setIsDragActive] = useState(false);

  const handleDrag = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.type === "dragenter" || e.type === "dragover") {
      setIsDragActive(true);
    } else if (e.type === "dragleave") {
      setIsDragActive(false);
    }
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragActive(false);

    if (e.dataTransfer.files && e.dataTransfer.files[0]) {
      const droppedFile = e.dataTransfer.files[0];
      if (droppedFile.size > 200 * 1024 * 1024) {
        setError("File exceeds 200MB limit.");
        return;
      }
      setFile(droppedFile);
      setError("");
      setShareLink("");
    }
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files[0]) {
      const selectedFile = e.target.files[0];
      if (selectedFile.size > 200 * 1024 * 1024) {
        setError("File exceeds 200MB limit.");
        return;
      }
      setFile(selectedFile);
      setError("");
      setShareLink("");
    }
  };

  const triggerFileInput = () => {
    fileInputRef.current?.click();
  };

  // Convert ArrayBuffer to Hex
  const bufferToHex = (buffer: ArrayBuffer): string => {
    return Array.from(new Uint8Array(buffer))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
  };

  // Convert ArrayBuffer to Base64
  const bufferToBase64 = (buffer: ArrayBuffer): string => {
    const bytes = new Uint8Array(buffer);
    let binary = "";
    for (let i = 0; i < bytes.byteLength; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    return window.btoa(binary);
  };

  const handleUpload = async () => {
    if (!file) return;

    try {
      setError("");
      setHashing(true);

      // 1. Calculate SHA-256 hash locally in browser
      const arrayBuffer = await file.arrayBuffer();
      const hashBuffer = await window.crypto.subtle.digest("SHA-256", arrayBuffer);
      const hashHex = bufferToHex(hashBuffer);
      const hashBase64 = bufferToBase64(hashBuffer);

      setHashing(false);
      setUploading(true);
      setUploadProgress(5);

      // 2. Call /api/v1/share/init
      const initResponse = await fetch("/api/v1/share/init", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          filename: file.name,
          size: file.size,
          mimeType: file.type || "application/octet-stream",
          hashValue: hashHex,
          password: usePassword ? password : null,
          expiresInSeconds: parseInt(expiresIn, 10),
          downloadLimit: parseInt(downloadLimit, 10),
        }),
      });

      if (!initResponse.ok) {
        const errData = await initResponse.json();
        throw new Error(errData.error || "Initialization failed");
      }

      const { shareId, uploadUrl } = await initResponse.json();
      setUploadProgress(20);

      // 3. Upload File to Cloudflare R2 via presigned PUT URL
      const xhr = new XMLHttpRequest();
      xhr.open("PUT", uploadUrl, true);
      xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
      xhr.setRequestHeader("x-amz-checksum-sha256", hashBase64);

      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable) {
          // Map progress from 20% to 90%
          const percentComplete = Math.round((event.loaded / event.total) * 70) + 20;
          setUploadProgress(percentComplete);
        }
      };

      const uploadPromise = new Promise<void>((resolve, reject) => {
        xhr.onload = () => {
          if (xhr.status === 200 || xhr.status === 204) {
            resolve();
          } else {
            reject(new Error(`Storage upload failed with status ${xhr.status}`));
          }
        };
        xhr.onerror = () => reject(new Error("Storage upload network error"));
      });

      xhr.send(file);
      await uploadPromise;

      setUploadProgress(95);

      // 4. Confirm Upload
      const confirmResponse = await fetch(`/api/v1/share/${shareId}/confirm`, {
        method: "POST",
      });

      if (!confirmResponse.ok) {
        const errData = await confirmResponse.json();
        throw new Error(errData.error || "Upload confirmation failed");
      }

      setUploadProgress(100);
      setUploading(false);
      
      const generatedLink = `${window.location.origin}/share/${shareId}`;
      setShareLink(generatedLink);
    } catch (err: any) {
      console.error(err);
      setError(err?.message || "An unexpected error occurred during upload.");
      setHashing(false);
      setUploading(false);
      setUploadProgress(0);
    }
  };

  const copyToClipboard = () => {
    navigator.clipboard.writeText(shareLink);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const formatBytes = (bytes: number): string => {
    if (bytes === 0) return "0 Bytes";
    const k = 1024;
    const sizes = ["Bytes", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
  };

  return (
    <div className="relative flex flex-col items-center justify-center min-h-[90vh] py-10 px-4 sm:px-6 z-10">
      <div className="mesh-gradient-1"></div>
      <div className="mesh-gradient-2"></div>

      <div className="w-full max-w-xl text-center mb-8">
        <h1 className="text-4xl sm:text-5xl font-extrabold tracking-tight bg-gradient-to-r from-indigo-400 via-purple-400 to-indigo-400 bg-clip-text text-transparent text-glow mb-3">
          R2-UPLINK
        </h1>
        <p className="text-zinc-400 text-sm sm:text-base max-w-md mx-auto">
          High-performance, secure, and ephemeral file-sharing platform powered by Cloudflare R2 & Next.js.
        </p>
      </div>

      <div className="w-full max-w-lg glass-panel rounded-2xl p-6 sm:p-8">
        {/* Error Alert */}
        {error && (
          <div className="flex items-center gap-3 bg-red-950/40 border border-red-800/50 rounded-xl p-4 text-red-300 text-sm mb-6 animate-pulse">
            <AlertCircle className="shrink-0 h-5 w-5" />
            <span>{error}</span>
          </div>
        )}

        {/* Success share view */}
        {shareLink ? (
          <div className="flex flex-col items-center py-4 text-center">
            <div className="h-16 w-16 bg-emerald-500/10 border border-emerald-500/30 rounded-full flex items-center justify-center text-emerald-400 mb-4 animate-bounce">
              <Check className="h-8 w-8" />
            </div>
            <h2 className="text-xl font-bold text-white mb-2">Upload Completed!</h2>
            <p className="text-zinc-400 text-sm mb-6">
              Your file is now live. Share the secure link below.
            </p>

            <div className="w-full flex items-center bg-zinc-950/50 border border-zinc-800 rounded-xl p-3 mb-6">
              <LinkIcon className="text-indigo-400 h-5 w-5 shrink-0 mr-3" />
              <input
                type="text"
                readOnly
                value={shareLink}
                className="bg-transparent text-white text-sm outline-none w-full mr-2"
              />
              <button
                onClick={copyToClipboard}
                className="flex items-center gap-1.5 bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-semibold px-3 py-1.5 rounded-lg transition-colors"
              >
                {copied ? (
                  <>
                    <Check className="h-3.5 w-3.5" /> Copied
                  </>
                ) : (
                  <>
                    <Copy className="h-3.5 w-3.5" /> Copy
                  </>
                )}
              </button>
            </div>

            <div className="w-full border-t border-zinc-800/80 pt-5 flex justify-between text-xs text-zinc-500">
              <span>Filename: {file?.name}</span>
              <span>Size: {file ? formatBytes(file.size) : ""}</span>
            </div>

            <button
              onClick={() => {
                setFile(null);
                setShareLink("");
              }}
              className="mt-6 text-indigo-400 hover:text-indigo-300 text-sm font-semibold transition-colors"
            >
              Upload another file
            </button>
          </div>
        ) : (
          /* Main Uploader Form */
          <div className="space-y-6">
            {/* Drag & Drop Area */}
            {!hashing && !uploading ? (
              <div
                onDragEnter={handleDrag}
                onDragOver={handleDrag}
                onDragLeave={handleDrag}
                onDrop={handleDrop}
                onClick={triggerFileInput}
                className={`group flex flex-col items-center justify-center border-2 border-dashed rounded-xl p-8 cursor-pointer transition-all duration-300 ${
                  isDragActive
                    ? "border-indigo-500 bg-indigo-500/5 shadow-[0_0_20px_rgba(99,102,241,0.15)]"
                    : "border-zinc-800 bg-zinc-950/20 hover:border-zinc-700 hover:bg-zinc-950/40"
                }`}
              >
                <input
                  type="file"
                  ref={fileInputRef}
                  onChange={handleFileChange}
                  className="hidden"
                />
                
                {file ? (
                  <div className="flex flex-col items-center text-center">
                    <div className="h-12 w-12 bg-indigo-500/10 rounded-xl flex items-center justify-center text-indigo-400 mb-3 group-hover:scale-110 transition-transform">
                      <File className="h-6 w-6" />
                    </div>
                    <span className="text-white text-sm font-medium max-w-xs truncate mb-1">
                      {file.name}
                    </span>
                    <span className="text-zinc-500 text-xs">{formatBytes(file.size)}</span>
                  </div>
                ) : (
                  <div className="flex flex-col items-center text-center">
                    <div className="h-12 w-12 bg-zinc-900 border border-zinc-800 rounded-xl flex items-center justify-center text-zinc-400 mb-3 group-hover:scale-110 transition-transform group-hover:border-indigo-500 group-hover:text-indigo-400">
                      <Upload className="h-5 w-5" />
                    </div>
                    <span className="text-zinc-200 text-sm font-medium mb-1">
                      Drag & drop your file here
                    </span>
                    <span className="text-zinc-500 text-xs">or click to browse from device</span>
                    <span className="text-zinc-600 text-[10px] mt-4">Max file size: 200 MB</span>
                  </div>
                )}
              </div>
            ) : (
              /* Loading and Hashing States */
              <div className="flex flex-col items-center py-10">
                <Loader2 className="h-10 w-10 text-indigo-500 animate-spin mb-4" />
                <h3 className="text-white font-semibold mb-1">
                  {hashing ? "Analyzing Integrity..." : "Streaming to Storage..."}
                </h3>
                <p className="text-zinc-500 text-xs mb-6">
                  {hashing
                    ? "Calculating local SHA-256 checksum..."
                    : `Uploading chunks (${uploadProgress}%)`}
                </p>

                {uploading && (
                  <div className="w-full bg-zinc-900 h-2 rounded-full overflow-hidden">
                    <div
                      className="bg-indigo-500 h-full transition-all duration-300"
                      style={{ width: `${uploadProgress}%` }}
                    ></div>
                  </div>
                )}
              </div>
            )}

            {file && !hashing && !uploading && (
              <>
                {/* Accordion Settings */}
                <div className="border-t border-zinc-800/80 pt-5 space-y-4">
                  <h4 className="text-zinc-300 text-xs font-bold uppercase tracking-wider">
                    Share Configurations
                  </h4>

                  <div className="grid grid-cols-2 gap-4">
                    {/* Expiry Selection */}
                    <div className="space-y-1.5">
                      <label className="text-zinc-400 text-xs flex items-center gap-1.5">
                        <Clock className="h-3.5 w-3.5" /> Ephemeral Expiry
                      </label>
                      <select
                        value={expiresIn}
                        onChange={(e) => setExpiresIn(e.target.value)}
                        className="w-full glass-input rounded-lg p-2.5 text-sm"
                      >
                        <option value="3600" className="bg-zinc-950">1 Hour</option>
                        <option value="14400" className="bg-zinc-950">4 Hours</option>
                        <option value="43200" className="bg-zinc-950">12 Hours</option>
                        <option value="86400" className="bg-zinc-950">24 Hours</option>
                      </select>
                    </div>

                    {/* Download limit selection */}
                    <div className="space-y-1.5">
                      <label className="text-zinc-400 text-xs flex items-center gap-1.5">
                        <Download className="h-3.5 w-3.5" /> Download Limit
                      </label>
                      <select
                        value={downloadLimit}
                        onChange={(e) => setDownloadLimit(e.target.value)}
                        className="w-full glass-input rounded-lg p-2.5 text-sm"
                      >
                        <option value="1" className="bg-zinc-950">1 Download</option>
                        <option value="5" className="bg-zinc-950">5 Downloads</option>
                        <option value="10" className="bg-zinc-950">10 Downloads</option>
                        <option value="50" className="bg-zinc-950">50 Downloads</option>
                        <option value="100" className="bg-zinc-950">100 Downloads</option>
                      </select>
                    </div>
                  </div>

                  {/* Password Protection */}
                  <div className="space-y-2">
                    <label className="flex items-center gap-2 cursor-pointer select-none">
                      <input
                        type="checkbox"
                        checked={usePassword}
                        onChange={(e) => setUsePassword(e.target.checked)}
                        className="rounded border-zinc-800 text-indigo-600 bg-zinc-950 focus:ring-indigo-600 focus:ring-offset-zinc-950"
                      />
                      <span className="text-zinc-300 text-xs flex items-center gap-1.5">
                        <Shield className="h-3.5 w-3.5" /> Enable Password Protection
                      </span>
                    </label>

                    {usePassword && (
                      <div className="relative flex items-center mt-2">
                        <Lock className="absolute left-3 text-zinc-500 h-4 w-4" />
                        <input
                          type="password"
                          value={password}
                          onChange={(e) => setPassword(e.target.value)}
                          placeholder="Enter access password"
                          className="w-full glass-input rounded-lg pl-9 pr-3 py-2 text-sm"
                        />
                      </div>
                    )}
                  </div>
                </div>

                {/* Submit button */}
                <button
                  onClick={handleUpload}
                  className="w-full flex items-center justify-center gap-2 bg-gradient-to-r from-indigo-600 to-purple-600 hover:from-indigo-500 hover:to-purple-500 text-white font-bold py-3 px-4 rounded-xl btn-glow focus:outline-none"
                >
                  <Upload className="h-5 w-5" />
                  Generate Link
                </button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
