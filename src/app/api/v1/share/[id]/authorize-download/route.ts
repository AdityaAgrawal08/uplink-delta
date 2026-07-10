import { NextRequest, NextResponse } from "next/server";
import { getDb } from "@/lib/mongodb";
import { redis } from "@/lib/redis";
import { getPresignedDownloadUrl } from "@/lib/r2";
import { verifyPassword, anonymizeIp } from "@/lib/crypto";

// Safe preview mime-types allowlist
const SAFE_PREVIEW_TYPES = [
  "application/pdf",
  "image/jpeg",
  "image/png",
  "image/gif",
  "image/webp",
];

export async function POST(
  req: NextRequest,
  props: { params: Promise<{ id: string }> }
) {
  try {
    const { id } = await props.params;
    const body = await req.json().catch(() => ({}));
    const { password, preview } = body;

    const clientIp = req.headers.get("x-forwarded-for") || "127.0.0.1";
    const ipHash = anonymizeIp(clientIp);

    // 1. Rate Limiting check
    const rateLimitKey = `rate:download:${ipHash}:${id}`;
    const attempts = await redis.incr(rateLimitKey);
    if (attempts === 1) {
      // 5-minute window
      await redis.expire(rateLimitKey, 300);
    }
    if (attempts > 5) {
      return NextResponse.json(
        { error: "Too many failed attempts. Locked out for 5 minutes." },
        { status: 429 }
      );
    }

    const db = await getDb();

    // 2. Fetch share document
    const share = await db.collection("shares").findOne({ shareId: id });
    if (!share) {
      return NextResponse.json({ error: "Share not found" }, { status: 404 });
    }

    const now = new Date();

    // Check expiration
    if (new Date(share.expiresAt) < now || share.status === "EXPIRED") {
      if (share.status !== "EXPIRED" && share.status !== "DELETED" && share.status !== "PENDING_DELETE") {
        await db.collection("shares").updateOne({ shareId: id }, { $set: { status: "EXPIRED" } });
      }
      return NextResponse.json({ error: "This share link has expired" }, { status: 410 });
    }

    if (share.status !== "ACTIVE") {
      return NextResponse.json(
        { error: `This share link is not active (${share.status})` },
        { status: 400 }
      );
    }

    // 3. Password Verification
    if (share.passwordHash) {
      if (!password) {
        return NextResponse.json(
          { error: "Password required for this share", passwordRequired: true },
          { status: 401 }
        );
      }
      const isPasswordValid = await verifyPassword(password, share.passwordHash);
      if (!isPasswordValid) {
        return NextResponse.json(
          { error: "Incorrect password", passwordRequired: true },
          { status: 401 }
        );
      }
    }

    // Reset rate-limit key on successful auth/verification
    await redis.set(rateLimitKey, 0, { ex: 1 });

    // 4. Atomic Download Counter and Limit Check
    const result = await db.collection("shares").findOneAndUpdate(
      {
        shareId: id,
        status: "ACTIVE",
        $expr: { $lt: ["$downloadsCount", "$downloadLimit"] },
      },
      [
        {
          $set: {
            downloadsCount: { $add: ["$downloadsCount", 1] },
            lastDownloadedAt: now,
            firstDownloadedAt: { $ifNull: ["$firstDownloadedAt", now] },
          },
        },
      ],
      { returnDocument: "after" }
    );

    if (!result) {
      // Limit exceeded or transition conflict. Check state.
      const refreshedShare = await db.collection("shares").findOne({ shareId: id });
      if (refreshedShare && refreshedShare.downloadsCount >= refreshedShare.downloadLimit) {
        await db.collection("shares").updateOne({ shareId: id }, { $set: { status: "EXPIRED" } });
        return NextResponse.json({ error: "Download limit exceeded for this file" }, { status: 410 });
      }
      return NextResponse.json({ error: "Download authorization failed" }, { status: 400 });
    }

    // 5. Generate Presigned GET URL
    const isSafePreview = SAFE_PREVIEW_TYPES.includes(share.mimeType);
    const wantPreview = preview === true && isSafePreview;

    const downloadUrlExpiry = 60; // 1m expiry for download link
    const downloadUrl = await getPresignedDownloadUrl(
      share.objectKey,
      downloadUrlExpiry,
      share.storageFilename,
      share.mimeType,
      wantPreview
    );

    // 6. Structured Log Event
    const userAgent = req.headers.get("user-agent") || "Unknown";
    const logEvent = {
      timestamp: now.toISOString(),
      requestId: `req_${crypto.randomUUID().replace(/-/g, "").substring(0, 16)}`,
      event: "DownloadAuthorized",
      shareId: id,
      preview: wantPreview,
      downloadsCount: result.downloadsCount,
      downloadLimit: result.downloadLimit,
      latencyMs: Date.now() - now.getTime(),
      ipHash,
      userAgentParsed: {
        raw: userAgent,
      },
      error: null,
    };
    console.log(JSON.stringify(logEvent));

    return NextResponse.json({
      downloadUrl,
      filename: share.filename,
      size: share.size,
      mimeType: share.mimeType,
      hashValue: share.hashValue,
      expiresAt: share.expiresAt,
    });
  } catch (error: any) {
    console.error("Error in POST /api/v1/share/[id]/authorize-download:", error);
    return NextResponse.json({ error: error?.message || "Internal Server Error" }, { status: 500 });
  }
}
