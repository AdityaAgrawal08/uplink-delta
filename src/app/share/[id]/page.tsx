"use client";

import React, { useState, useEffect, use, useCallback } from "react";
import Link from "next/link";
import {
  FileDown,
  ShieldAlert,
  Calendar,
  Lock,
  Loader2,
  FileText,
  AlertTriangle,
  Eye,
  ArrowLeft,
  CheckCircle,
} from "lucide-react";

interface ShareMeta {
  shareId: string;
  filename: string;
  size: number;
  mimeType: string;
  hashValue: string;
  expiresAt: string;
  passwordRequired: boolean;
  downloadsCount: number;
  downloadLimit: number;
}

export default function ShareView({ params }: { params: Promise<{ id: string }> }) {
  // Resolve params Promise
  const resolvedParams = use(params);
  const id = resolvedParams.id;

  const [loading, setLoading] = useState(true);
  const [meta, setMeta] = useState<ShareMeta | null>(null);
  const [error, setError] = useState("");
  const [password, setPassword] = useState("");
  const [passwordError, setPasswordError] = useState("");
  const [authorizing, setAuthorizing] = useState(false);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);

  // Time left state
  const [timeLeft, setTimeLeft] = useState("");

  const fetchMeta = useCallback(async () => {
    try {
      setLoading(true);
      setError("");
      const response = await fetch(`/api/v1/share/${id}`);
      if (!response.ok) {
        const errData = await response.json();
        throw new Error(errData.error || "Failed to fetch file metadata");
      }
      const data = await response.json();
      setMeta(data);
    } catch (err: unknown) {
      const errMsg = err instanceof Error ? err.message : "Link is invalid or has expired.";
      setError(errMsg);
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    fetchMeta();
  }, [fetchMeta]);

  useEffect(() => {
    if (!meta) return;

    const calculateTimeLeft = () => {
      const difference = +new Date(meta.expiresAt) - +new Date();
      if (difference <= 0) {
        setTimeLeft("Expired");
        setError("This share link has expired.");
        return;
      }

      const hours = Math.floor(difference / (1000 * 60 * 60));
      const minutes = Math.floor((difference / 1000 / 60) % 60);
      const seconds = Math.floor((difference / 1000) % 60);

      setTimeLeft(
        `${hours.toString().padStart(2, "0")}:${minutes
          .toString()
          .padStart(2, "0")}:${seconds.toString().padStart(2, "0")}`
      );
    };

    calculateTimeLeft();
    const interval = setInterval(calculateTimeLeft, 1000);
    return () => clearInterval(interval);
  }, [meta]);

  const handleAuthorize = async (wantPreview = false) => {
    try {
      setAuthorizing(true);
      setPasswordError("");

      const response = await fetch(`/api/v1/share/${id}/authorize-download`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          password: meta?.passwordRequired ? password : null,
          preview: wantPreview,
        }),
      });

      if (!response.ok) {
        const errData = await response.json();
        if (response.status === 401) {
          setPasswordError(errData.error || "Incorrect password");
        } else {
          setError(errData.error || "Download authorization failed");
        }
        return;
      }

      const { downloadUrl } = await response.json();

      if (wantPreview) {
        setPreviewUrl(downloadUrl);
      } else {
        // Trigger download
        const a = document.createElement("a");
        a.href = downloadUrl;
        a.download = meta?.filename || "download";
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        
        // Refresh metadata to reflect download count changes
        fetchMeta();
      }
    } catch (err: unknown) {
      console.error(err);
      const errMsg = err instanceof Error ? err.message : "Failed to authorize transfer";
      setError(errMsg);
    } finally {
      setAuthorizing(false);
    }
  };

  const formatBytes = (bytes: number): string => {
    if (bytes === 0) return "0 Bytes";
    const k = 1024;
    const sizes = ["Bytes", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
  };

  // Safe types for preview
  const isPreviewable = meta && [
    "application/pdf",
    "image/jpeg",
    "image/png",
    "image/gif",
    "image/webp",
  ].includes(meta.mimeType);

  if (loading) {
    return (
      <div className="flex flex-col items-center justify-center min-h-[90vh] text-center z-10">
        <Loader2 className="h-10 w-10 text-indigo-500 animate-spin mb-4" />
        <p className="text-zinc-400 text-sm">Retrieving shared file metadata...</p>
      </div>
    );
  }

  return (
    <div className="relative flex flex-col items-center justify-center min-h-[90vh] py-10 px-4 sm:px-6 z-10">
      <div className="mesh-gradient-1"></div>
      <div className="mesh-gradient-2"></div>

      <div className="w-full max-w-xl text-center mb-8">
        <h1 className="text-3xl font-extrabold tracking-tight bg-gradient-to-r from-indigo-400 to-purple-400 bg-clip-text text-transparent text-glow mb-1">
          Secure Download Gate
        </h1>
        <p className="text-zinc-500 text-xs sm:text-sm">R2-Uplink Ephemeral Share Portal</p>
      </div>

      <div className="w-full max-w-lg glass-panel rounded-2xl p-6 sm:p-8">
        {/* Error State */}
        {error ? (
          <div className="text-center py-6">
            <div className="h-14 w-14 bg-red-500/10 border border-red-500/30 rounded-full flex items-center justify-center text-red-400 mx-auto mb-4">
              <ShieldAlert className="h-7 w-7" />
            </div>
            <h2 className="text-lg font-bold text-white mb-2">Access Denied</h2>
            <p className="text-zinc-400 text-sm max-w-xs mx-auto mb-6">{error}</p>
            <Link
              href="/"
              className="inline-flex items-center gap-2 text-indigo-400 hover:text-indigo-300 text-sm font-semibold transition-colors"
            >
              <ArrowLeft className="h-4 w-4" /> Go back to Uploader
            </Link>
          </div>
        ) : meta?.passwordRequired && !previewUrl && !meta.filename ? (
          // Wait, this shouldn't happen unless we are not authenticated yet.
          // If passwordRequired is true, let's render the Password Prompter.
          null
        ) : meta && (!meta.passwordRequired || meta.filename) ? (
          /* File download layout */
          <div className="space-y-6">
            {previewUrl ? (
              /* Inline Preview Area */
              <div className="space-y-4">
                <button
                  onClick={() => setPreviewUrl(null)}
                  className="flex items-center gap-1.5 text-zinc-400 hover:text-zinc-200 text-xs font-semibold transition-colors"
                >
                  <ArrowLeft className="h-3.5 w-3.5" /> Back to details
                </button>
                <div className="glass-panel border-zinc-800 rounded-xl overflow-hidden min-h-[300px] flex items-center justify-center bg-zinc-950/40">
                  {meta.mimeType === "application/pdf" ? (
                    <iframe src={previewUrl} className="w-full h-[450px] border-none" />
                  ) : (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img
                      src={previewUrl}
                      alt={meta.filename}
                      className="max-w-full max-h-[450px] object-contain rounded"
                    />
                  )}
                </div>
              </div>
            ) : (
              /* File Details & Download Buttons */
              <>
                <div className="flex items-start gap-4 bg-zinc-950/30 border border-zinc-800/60 rounded-xl p-4">
                  <div className="h-12 w-12 bg-indigo-500/10 rounded-xl flex items-center justify-center text-indigo-400 shrink-0">
                    <FileText className="h-6 w-6" />
                  </div>
                  <div className="overflow-hidden">
                    <h3 className="text-white text-base font-semibold truncate mb-1" title={meta.filename}>
                      {meta.filename}
                    </h3>
                    <div className="flex flex-wrap gap-x-4 gap-y-1 text-zinc-500 text-xs">
                      <span>Size: {formatBytes(meta.size)}</span>
                      <span>Type: {meta.mimeType.split("/")[1] || "Unknown"}</span>
                    </div>
                  </div>
                </div>

                {/* Meta details */}
                <div className="grid grid-cols-2 gap-4 bg-zinc-950/10 rounded-xl border border-zinc-900 p-4 text-xs text-zinc-400">
                  <div className="space-y-1">
                    <span className="text-zinc-500 block">Link Expiration:</span>
                    <span className="text-amber-400 font-mono flex items-center gap-1">
                      <Calendar className="h-3.5 w-3.5" /> {timeLeft || "00:00:00"}
                    </span>
                  </div>
                  <div className="space-y-1">
                    <span className="text-zinc-500 block">Downloads Remaining:</span>
                    <span className="text-indigo-400 font-mono">
                      {meta.downloadLimit - meta.downloadsCount} of {meta.downloadLimit}
                    </span>
                  </div>
                </div>

                <div className="flex flex-col sm:flex-row gap-3">
                  {/* Download Trigger */}
                  <button
                    onClick={() => handleAuthorize(false)}
                    disabled={authorizing}
                    className="flex-1 flex items-center justify-center gap-2 bg-gradient-to-r from-indigo-600 to-purple-600 hover:from-indigo-500 hover:to-purple-500 text-white font-bold py-3.5 px-4 rounded-xl btn-glow disabled:opacity-50"
                  >
                    {authorizing ? (
                      <Loader2 className="h-5 w-5 animate-spin" />
                    ) : (
                      <FileDown className="h-5 w-5" />
                    )}
                    Download File
                  </button>

                  {/* Preview Trigger (only safe types) */}
                  {isPreviewable && (
                    <button
                      onClick={() => handleAuthorize(true)}
                      disabled={authorizing}
                      className="flex-1 flex items-center justify-center gap-2 bg-zinc-900 hover:bg-zinc-800 border border-zinc-800 text-zinc-200 font-semibold py-3.5 px-4 rounded-xl transition-colors disabled:opacity-50"
                    >
                      <Eye className="h-5 w-5" />
                      Preview File
                    </button>
                  )}
                </div>

                <div className="border-t border-zinc-900 pt-4 flex flex-col items-center">
                  <span className="text-zinc-600 text-[10px] uppercase tracking-wider font-bold">
                    File Checksum (SHA-256)
                  </span>
                  <code className="text-zinc-500 text-[10px] break-all select-all font-mono mt-1 text-center bg-zinc-950/20 border border-zinc-900 px-2 py-1 rounded w-full">
                    {meta.hashValue}
                  </code>
                </div>
              </>
            )}
          </div>
        ) : (
          /* Password Form Prompter */
          <div className="space-y-5">
            <div className="text-center py-2">
              <div className="h-12 w-12 bg-purple-500/10 border border-purple-500/20 rounded-full flex items-center justify-center text-purple-400 mx-auto mb-3">
                <Lock className="h-6 w-6" />
              </div>
              <h2 className="text-lg font-bold text-white mb-1">Password Protected</h2>
              <p className="text-zinc-400 text-xs max-w-xs mx-auto">
                This share requires a decryption key to verify authorization.
              </p>
            </div>

            {passwordError && (
              <div className="flex items-center gap-2 bg-red-950/40 border border-red-800/50 rounded-xl p-3 text-red-300 text-xs animate-pulse">
                <AlertTriangle className="shrink-0 h-4 w-4" />
                <span>{passwordError}</span>
              </div>
            )}

            <div className="space-y-1">
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Enter password to access"
                className="w-full glass-input rounded-xl p-3 text-sm text-center"
                onKeyDown={(e) => {
                  if (e.key === "Enter") handleAuthorize(false);
                }}
              />
            </div>

            <button
              onClick={() => handleAuthorize(false)}
              disabled={authorizing || !password}
              className="w-full flex items-center justify-center gap-2 bg-gradient-to-r from-indigo-600 to-purple-600 hover:from-indigo-500 hover:to-purple-500 text-white font-bold py-3 px-4 rounded-xl btn-glow disabled:opacity-50"
            >
              {authorizing ? (
                <Loader2 className="h-5 w-5 animate-spin" />
              ) : (
                <CheckCircle className="h-5 w-5" />
              )}
              Unlock & Download
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
