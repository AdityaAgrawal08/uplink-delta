import { NextRequest, NextResponse } from "next/server";
import fs from "fs";
import path from "path";

export async function GET(req: NextRequest) {
  try {
    const { searchParams } = new URL(req.url);
    const key = searchParams.get("key");
    const preview = searchParams.get("preview") === "true";
    const filename = searchParams.get("filename") || "file";
    const mimeType = searchParams.get("mimeType") || "application/octet-stream";

    if (!key) {
      return NextResponse.json({ error: "Missing key" }, { status: 400 });
    }

    if (key.includes("..")) {
      return NextResponse.json({ error: "Invalid key: path traversal attempt detected" }, { status: 400 });
    }

    const baseDir = path.resolve(process.cwd(), "uploads_dev");
    const localPath = path.resolve(baseDir, key);
    if (!localPath.startsWith(baseDir + path.sep)) {
      return NextResponse.json({ error: "Access denied: invalid path key" }, { status: 400 });
    }

    if (!fs.existsSync(localPath)) {
      return new Response("File Not Found", { status: 404 });
    }

    const fileBuffer = fs.readFileSync(localPath);

    const headers = new Headers();
    if (preview) {
      headers.set("Content-Type", mimeType);
      headers.set("Content-Disposition", "inline");
    } else {
      headers.set("Content-Type", "application/octet-stream");
      headers.set("Content-Disposition", `attachment; filename="${filename}"`);
    }
    headers.set("Content-Length", String(fileBuffer.length));

    return new Response(fileBuffer, {
      status: 200,
      headers,
    });
  } catch (error: unknown) {
    console.error("Local mock R2 download failed:", error);
    const errMsg = error instanceof Error ? error.message : "Internal Server Error";
    return NextResponse.json({ error: errMsg }, { status: 500 });
  }
}
