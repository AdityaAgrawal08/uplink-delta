import { NextRequest, NextResponse } from "next/server";
import fs from "fs";
import path from "path";
import { crc64nvmeBase64 } from "@/lib/crc64";

export async function PUT(req: NextRequest) {
  try {
    const { searchParams } = new URL(req.url);
    const key = searchParams.get("key");
    const sha256 = searchParams.get("sha256") || "";
    const mimeType = searchParams.get("mimeType") || "";
    const uploadId = searchParams.get("uploadId");
    const partNumber = searchParams.get("partNumber");

    if (!key) {
      return NextResponse.json({ error: "Missing key" }, { status: 400 });
    }

    if (key.includes("..")) {
      return NextResponse.json({ error: "Invalid key: path traversal attempt detected" }, { status: 400 });
    }

    const baseDir = path.resolve(process.cwd(), "uploads_dev");
    const testPath = path.resolve(baseDir, key);
    if (!testPath.startsWith(baseDir + path.sep)) {
      return NextResponse.json({ error: "Access denied: invalid path key" }, { status: 400 });
    }

    const arrayBuffer = await req.arrayBuffer();
    const buffer = Buffer.from(arrayBuffer);

    const headers = new Headers();
    let localPath = "";

    if (uploadId && partNumber) {
      // Multipart upload: Save part file
      localPath = path.join(process.cwd(), "uploads_dev", `${key}.part_${partNumber}`);
      const dir = path.dirname(localPath);
      if (!fs.existsSync(dir)) {
        fs.mkdirSync(dir, { recursive: true });
      }
      fs.writeFileSync(localPath, buffer);

      // Compute and return part-level CRC64NVME and ETag
      const checksum = crc64nvmeBase64(buffer);
      headers.set("x-amz-checksum-crc64nvme", checksum);
      headers.set("ETag", `"${uploadId}-${partNumber}"`);
    } else {
      // Single-part upload
      localPath = path.join(process.cwd(), "uploads_dev", key);
      const dir = path.dirname(localPath);
      if (!fs.existsSync(dir)) {
        fs.mkdirSync(dir, { recursive: true });
      }
      fs.writeFileSync(localPath, buffer);
      
      // Save metadata
      fs.writeFileSync(
        localPath + ".meta",
        JSON.stringify({ sha256, mimeType, size: buffer.length })
      );
    }

    return new Response(null, { status: 200, headers });
  } catch (error: unknown) {
    console.error("Local mock R2 upload failed:", error);
    const errMsg = error instanceof Error ? error.message : "Internal Server Error";
    return NextResponse.json({ error: errMsg }, { status: 500 });
  }
}
