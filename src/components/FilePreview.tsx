"use client";

import { useState } from "react";
import SyntaxHighlighter from "./SyntaxHighlighter";

interface ShareMetadata {
  shareId: string;
  filename: string;
  size: number;
  mimeType: string;
  hashValue: string;
  passwordRequired: boolean;
  isEncrypted?: boolean;
}

interface Props {
  share: ShareMetadata;
}

type PreviewType = "text" | "image" | "video" | "audio" | "pdf" | "other";

function getPreviewType(mime: string, name: string): PreviewType {
  const m = mime.toLowerCase();
  if (m.startsWith("text/")) return "text";
  if (m.startsWith("image/")) return "image";
  if (m.startsWith("video/")) return "video";
  if (m.startsWith("audio/")) return "audio";
  if (m === "application/pdf") return "pdf";

  const ext = name.split(".").pop()?.toLowerCase() || "";
  if (["json", "js", "ts", "py", "go", "rs", "md", "html", "css", "yaml", "xml", "ini", "conf", "sh", "bat"].includes(ext)) {
    return "text";
  }
  return "other";
}

export default function FilePreview({ share }: Props) {
  const [password, setPassword] = useState("");
  const [authorized, setAuthorized] = useState(!share.passwordRequired);
  const [downloadUrl, setDownloadUrl] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const [textContent, setTextContent] = useState<string | null>(null);
  const [textLoading, setTextLoading] = useState(false);

  if (share.isEncrypted) {
    return (
      <div className="preview-container password-box">
        <h3>🔒 End-to-End Encrypted</h3>
        <p>This file is encrypted client-side and cannot be decrypted by the browser.</p>
        <p className="subtitle">Please use the Uplink CLI to download and decrypt this file.</p>
      </div>
    );
  }

  const previewType = getPreviewType(share.mimeType, share.filename);

  const handleAuthorize = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError("");

    try {
      const res = await fetch(`/api/v1/share/${share.shareId}/authorize-download`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password }),
      });

      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || "Failed to authorize");
      }

      setDownloadUrl(data.downloadUrl);
      setAuthorized(true);
    } catch (err: unknown) {
      const errMsg = err instanceof Error ? err.message : String(err);
      setError(errMsg || "Invalid password");
    } finally {
      setLoading(false);
    }
  };

  const fetchTextContent = async () => {
    setTextLoading(true);
    setError("");
    try {
      let url = downloadUrl;
      if (!url) {
        const res = await fetch(`/api/v1/share/${share.shareId}/authorize-download`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({}),
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || "Failed to get download URL");
        url = data.downloadUrl;
        setDownloadUrl(url);
      }

      const fileRes = await fetch(url);
      if (!fileRes.ok) throw new Error("Failed to fetch file content");
      const text = await fileRes.text();
      setTextContent(text.slice(0, 100000)); // Cap at 100 KB
    } catch (err: unknown) {
      const errMsg = err instanceof Error ? err.message : String(err);
      setError(errMsg || "Failed to load text preview");
    } finally {
      setTextLoading(false);
    }
  };

  const ensureDownloadUrl = async () => {
    if (downloadUrl) return downloadUrl;
    try {
      const res = await fetch(`/api/v1/share/${share.shareId}/authorize-download`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });
      const data = await res.json();
      if (res.ok) {
        setDownloadUrl(data.downloadUrl);
        return data.downloadUrl;
      }
    } catch {}
    return "";
  };

  if (!authorized) {
    return (
      <div className="preview-container password-box">
        <h3>🔑 Password Protected</h3>
        <p className="subtitle">Enter password to preview file</p>
        <form onSubmit={handleAuthorize} className="flex-row">
          <input
            type="password"
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            className="input-field"
          />
          <button type="submit" disabled={loading} className="btn-primary">
            {loading ? "Verifying..." : "Unlock"}
          </button>
        </form>
        {error && <p className="error-text">{error}</p>}
      </div>
    );
  }

  if (!downloadUrl && ["image", "video", "audio", "pdf"].includes(previewType)) {
    ensureDownloadUrl();
  }

  const fileExt = share.filename.split(".").pop()?.toLowerCase() || "txt";

  return (
    <div className="preview-container">
      <h3>📄 File Preview: {share.filename}</h3>
      <div className="preview-body">
        {previewType === "text" && (
          <div>
            {!textContent ? (
              <button onClick={fetchTextContent} disabled={textLoading} className="btn-secondary">
                {textLoading ? "Loading content..." : "Show Text Preview"}
              </button>
            ) : (
              <div className="text-preview-box">
                <SyntaxHighlighter code={textContent} language={fileExt} />
                {share.size > 100000 && (
                  <p className="preview-note">* Content truncated to first 100 KB</p>
                )}
              </div>
            )}
          </div>
        )}

        {previewType === "image" && downloadUrl && (
          <div className="media-preview-box">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={downloadUrl} alt={share.filename} loading="lazy" className="img-preview" />
          </div>
        )}

        {previewType === "video" && downloadUrl && (
          <div className="media-preview-box">
            <video src={downloadUrl} controls preload="metadata" className="video-preview" />
          </div>
        )}

        {previewType === "audio" && downloadUrl && (
          <div className="media-preview-box">
            <audio src={downloadUrl} controls className="audio-preview" />
          </div>
        )}

        {previewType === "pdf" && downloadUrl && (
          <div className="pdf-preview-box">
            <iframe src={downloadUrl} className="pdf-frame" title="PDF Preview" />
          </div>
        )}

        {previewType === "other" && (
          <div className="no-preview-box">
            <p>Preview is not supported for this file format.</p>
            <p className="subtitle">Please download using the CLI commands above.</p>
          </div>
        )}

        {error && <p className="error-text">{error}</p>}
      </div>
    </div>
  );
}
