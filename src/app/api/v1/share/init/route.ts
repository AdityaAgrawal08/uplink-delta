import { NextRequest, NextResponse, after } from "next/server";
import { getDb, initIndexes } from "@/lib/mongodb";
import { performCleanup } from "../../cleanup/route";
import { redis } from "@/lib/redis";
import { getPresignedUploadUrl, getPresignedMultipartUrls } from "@/lib/r2";
import {
  generateShareId,
  sanitizeFilename,
  hashPassword,
} from "@/lib/crypto";
import { reserveUploadQuota, releaseUploadQuota } from "@/lib/quota";

export async function POST(req: NextRequest) {
  let size = 0;
  let partsCount = 0;
  let isMultipart = false;
  let quotaReserved = false;

  try {
    const body = await req.json().catch(() => ({}));
    const {
      filename,
      mimeType,
      hashValue,
      password,
      expiresInSeconds,
      downloadLimit,
      checksumCrc64nvme,
    } = body;
    size = Number(body.size) || 0;
    partsCount = Number(body.partsCount) || 0;

    // 1. Basic Validations
    if (!filename || typeof filename !== "string") {
      return NextResponse.json({ error: "Filename is required" }, { status: 400 });
    }
    if (size === undefined || typeof size !== "number" || size <= 0) {
      return NextResponse.json({ error: "File size must be greater than 0" }, { status: 400 });
    }
    if (!hashValue || typeof hashValue !== "string" || hashValue.length !== 64) {
      return NextResponse.json(
        { error: "Valid SHA-256 hashValue (64 chars hex) is required" },
        { status: 400 }
      );
    }

    isMultipart = partsCount > 1;

    // Enforce limits: Max 500 MB for multipart directories, 200 MB for guest single-part files
    const MAX_SIZE = isMultipart ? 500 * 1024 * 1024 : 200 * 1024 * 1024;
    if (size > MAX_SIZE) {
      return NextResponse.json(
        { error: `File size exceeds ${isMultipart ? "500 MB directory" : "200 MB file"} limit` },
        { status: 400 }
      );
    }

    // Expiry verification
    const DEFAULT_EXPIRY = 86400; // 24 hours
    const maxExpiry = 86400;
    let expirySec = expiresInSeconds !== undefined ? Number(expiresInSeconds) : DEFAULT_EXPIRY;
    if (isNaN(expirySec) || expirySec <= 0 || expirySec > maxExpiry) {
      expirySec = DEFAULT_EXPIRY;
    }

    // Download limit verification
    const defaultDownloadLimit = 10;
    let dlLimit = downloadLimit !== undefined ? Number(downloadLimit) : defaultDownloadLimit;
    if (isNaN(dlLimit) || dlLimit <= 0) {
      dlLimit = defaultDownloadLimit;
    }

    // 2. Idempotency Check
    const idempotencyKey = req.headers.get("idempotency-key");
    const redisIdempotencyKey = idempotencyKey ? `idempotency:${idempotencyKey}` : null;

    if (redisIdempotencyKey) {
      const lockAcquired = await redis.set(redisIdempotencyKey, "PROCESSING", {
        nx: true,
        ex: 30,
      });

      if (lockAcquired === null) {
        const status = await redis.get(redisIdempotencyKey);
        if (status === "PROCESSING") {
          return NextResponse.json(
            { error: "A request with this Idempotency-Key is currently processing" },
            { status: 409 }
          );
        }
        if (status) {
          return NextResponse.json(status);
        }
      }
    }

    // Initialize MongoDB index checks in the background (non-blocking)
    initIndexes().catch(err => console.error("Background index initialization failed:", err));

    const db = await getDb();

    // 3. Quota Enforcement check (Milestone 5)
    // Estimate Class A operations:
    // If multipart: partsCount + 2 (Initiate + UploadParts + Complete)
    // If singlepart: 1 (PutObject)
    const estimatedClassAOps = isMultipart ? Number(partsCount) + 2 : 1;

    try {
      const quotaApproved = await reserveUploadQuota(size, estimatedClassAOps);
      if (!quotaApproved) {
        if (redisIdempotencyKey) {
          await redis.del(redisIdempotencyKey);
        }
        return NextResponse.json(
          {
            error: "Storage capacity has been reached. New uploads are temporarily unavailable. Please wait while older files are removed automatically to free space.",
          },
          { status: 503 }
        );
      }
      quotaReserved = true;
    } catch (quotaErr) {
      console.error("Fail-closed: Quota check error:", quotaErr);
      if (redisIdempotencyKey) {
        await redis.del(redisIdempotencyKey);
      }
      return NextResponse.json(
        {
          error: "Uploads are temporarily unavailable due to system quota validation failure.",
        },
        { status: 503 }
      );
    }

    // 4. Share ID and Key construction
    const shareId = generateShareId();
    const storageFilename = sanitizeFilename(filename);
    const date = new Date();
    const year = date.getUTCFullYear();
    const month = String(date.getUTCMonth() + 1).padStart(2, "0");
    const objectKey = `uploads/${year}/${month}/${shareId}/${storageFilename}`;

    // Hash password if supplied
    let passwordHash = null;
    if (password && typeof password === "string") {
      passwordHash = await hashPassword(password);
    }

    const expiresAt = new Date(Date.now() + expirySec * 1000);
    const uploadExpiresAt = new Date(Date.now() + 2 * 3600 * 1000); // Upload session valid for 2h
    const uploadUrlExpiry = 900; // Presigned URL valid for 15m

    let uploadId = "";
    let uploadUrl = null;
    let uploadUrls = null;

    // 4. Generate Presigned R2 Upload URLs (Single-part vs Multipart)
    if (isMultipart) {
      const parts = Number(partsCount);
      const mpDetails = await getPresignedMultipartUrls(objectKey, parts);
      uploadId = mpDetails.uploadId;
      uploadUrls = mpDetails.urls;
    } else {
      uploadId = `upload_${crypto.randomUUID().replace(/-/g, "")}`;
      uploadUrl = await getPresignedUploadUrl(
        objectKey,
        uploadUrlExpiry,
        mimeType || "application/octet-stream",
        hashValue
      );
    }

    // Generate a unique 10-digit numeric code
    let downloadCode = "";
    let isCodeUnique = false;
    let codeAttempts = 0;
    while (!isCodeUnique && codeAttempts < 10) {
      downloadCode = "";
      for (let i = 0; i < 10; i++) {
        downloadCode += Math.floor(Math.random() * 10).toString();
      }
      const existingCode = await db.collection("shares").findOne({ downloadCode });
      if (!existingCode) {
        isCodeUnique = true;
      }
      codeAttempts++;
    }

    if (!isCodeUnique) {
      if (redisIdempotencyKey) {
        await redis.del(redisIdempotencyKey);
      }
      return NextResponse.json(
        { error: "Unique download code generation failed due to collision limits" },
        { status: 500 }
      );
    }

    // 5. Database Insert (Share & Upload Session)
    const shareDoc = {
      shareId,
      downloadCode,
      filename,
      storageFilename,
      size,
      mimeType: mimeType || "application/octet-stream",
      observedMimeType: null,
      etag: null,
      objectKey,
      hashAlgorithm: "SHA-256",
      hashValue,
      checksumCrc64nvme: checksumCrc64nvme || null,
      passwordHash,
      status: "CREATED",
      createdAt: date,
      expiresAt,
      downloadLimit: dlLimit,
      downloadsCount: 0,
      firstDownloadedAt: null,
      lastDownloadedAt: null,
      schemaVersion: 1,
      cleanupLockedUntil: null,
      cleanupWorkerId: null,
      retryCount: 0,
      lastRetryAt: null,
      nextRetryAt: null,
      lastErrorCode: null,
      lastErrorMessage: null,
    };

    const uploadSessionDoc = {
      uploadId,
      shareId,
      uploadExpiresAt,
      uploadUrlExpiresAt: new Date(Date.now() + uploadUrlExpiry * 1000),
      status: "PENDING",
      createdAt: date,
      isMultipart,
      partsCount: isMultipart ? Number(partsCount) : 1,
    };

    await db.collection("shares").insertOne(shareDoc);
    await db.collection("upload_sessions").insertOne(uploadSessionDoc);

    const responseData = {
      shareId,
      uploadId,
      uploadUrl,
      uploadUrls,
      objectKey,
      filename,
      storageFilename,
      expiresAt: expiresAt.toISOString(),
      uploadExpiresAt: uploadExpiresAt.toISOString(),
    };

    // Cache final response in Redis with a 24h TTL if idempotency key is used
    if (redisIdempotencyKey) {
      await redis.set(redisIdempotencyKey, responseData, { ex: 86400 });
    }

    // Trigger background cleanup asynchronously to purge any expired uploads
    after(async () => {
      await performCleanup().catch(err => console.error("Background cleanup failed:", err));
    });

    return NextResponse.json(responseData, { status: 201 });
  } catch (error: unknown) {
    console.error("Error in POST /api/v1/share/init:", error);
    if (quotaReserved) {
      try {
        const estimatedClassAOps = isMultipart ? partsCount + 2 : 1;
        await releaseUploadQuota(size, estimatedClassAOps);
      } catch (refundErr) {
        console.error("Failed to refund quota on init error:", refundErr);
      }
    }
    const errMsg = error instanceof Error ? error.message : "Internal Server Error";
    return NextResponse.json({ error: errMsg }, { status: 500 });
  }
}
