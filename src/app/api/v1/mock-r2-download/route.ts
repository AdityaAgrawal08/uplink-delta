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

    const localPath = path.join(process.cwd(), "uploads_dev", key);
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
  } catch (error: any) {
    console.error("Local mock R2 download failed:", error);
    return NextResponse.json({ error: error.message }, { status: 500 });
  }
}
