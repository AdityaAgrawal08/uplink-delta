import { NextRequest, NextResponse } from "next/server";
import fs from "fs";
import path from "path";

export async function PUT(req: NextRequest) {
  try {
    const { searchParams } = new URL(req.url);
    const key = searchParams.get("key");
    const sha256 = searchParams.get("sha256") || "";
    const mimeType = searchParams.get("mimeType") || "";

    if (!key) {
      return NextResponse.json({ error: "Missing key" }, { status: 400 });
    }

    const arrayBuffer = await req.arrayBuffer();
    const buffer = Buffer.from(arrayBuffer);

    const localPath = path.join(process.cwd(), "uploads_dev", key);
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

    return new Response(null, { status: 200 });
  } catch (error: any) {
    console.error("Local mock R2 upload failed:", error);
    return NextResponse.json({ error: error.message }, { status: 500 });
  }
}
