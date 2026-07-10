import { NextRequest, NextResponse } from "next/server";
import { getDb } from "@/lib/mongodb";
import { checkObjectExists, completeMultipartUpload } from "@/lib/r2";
import { anonymizeIp } from "@/lib/crypto";

export async function POST(
  req: NextRequest,
  props: { params: Promise<{ id: string }> }
) {
  try {
    const { id } = await props.params;
    const body = await req.json().catch(() => ({}));
    const { parts } = body;

    const db = await getDb();

    // 1. Fetch Share metadata
    const share = await db.collection("shares").findOne({ shareId: id });
    if (!share) {
      return NextResponse.json({ error: "Share session not found" }, { status: 404 });
    }

    // Idempotent success response if already ACTIVE
    if (share.status === "ACTIVE") {
      return NextResponse.json({
        message: "Upload already confirmed",
        shareId: share.shareId,
        status: share.status,
      });
    }

    if (share.status !== "CREATED") {
      return NextResponse.json(
        { error: `Cannot confirm upload in status: ${share.status}` },
        { status: 400 }
      );
    }

    // 2. Fetch Upload Session
    const uploadSession = await db
      .collection("upload_sessions")
      .findOne({ shareId: id });
    
    if (!uploadSession) {
      return NextResponse.json({ error: "Upload session not found" }, { status: 404 });
    }

    const now = new Date();

    // Verify session expiration
    if (new Date(uploadSession.uploadExpiresAt) < now) {
      await db
        .collection("shares")
        .updateOne({ shareId: id }, { $set: { status: "EXPIRED" } });
      await db
        .collection("upload_sessions")
        .updateOne({ shareId: id }, { $set: { status: "EXPIRED" } });
      return NextResponse.json({ error: "Upload session has expired" }, { status: 410 });
    }

    let finalCrc64 = null;
    let finalEtag = null;

    // 3. Complete and Verify Upload (Multipart vs Single-part)
    if (uploadSession.isMultipart) {
      if (!parts || !Array.isArray(parts)) {
        return NextResponse.json(
          { error: "Parts list is required to complete multipart upload" },
          { status: 400 }
        );
      }

      // Assemble chunks in R2/S3 or mock storage
      const completionResult = await completeMultipartUpload(
        share.objectKey,
        uploadSession.uploadId,
        parts
      );

      if (completionResult.error) {
        return NextResponse.json(
          { error: `Failed to complete multipart assembly: ${completionResult.error}` },
          { status: 400 }
        );
      }

      finalCrc64 = completionResult.checksumCrc64nvme;
      finalEtag = completionResult.etag;

      // Verify CRC64NVME integrity
      // S3 CompleteMultipartUpload returns ChecksumCRC64NVME natively if initialized with it
      if (finalCrc64 && share.checksumCrc64nvme) {
        if (finalCrc64 !== share.checksumCrc64nvme) {
          await db
            .collection("upload_sessions")
            .updateOne({ shareId: id }, { $set: { status: "VERIFY_FAILED" } });
          return NextResponse.json(
            { error: "Integrity check failed: multipart full-object CRC64NVME does not match client expectation" },
            { status: 412 }
          );
        }
      }
    } else {
      // Single-part HEAD check
      const objDetails = await checkObjectExists(share.objectKey);
      if (!objDetails.exists) {
        return NextResponse.json(
          { error: "Uploaded file was not found in object storage" },
          { status: 404 }
        );
      }

      if (objDetails.size <= 0) {
        return NextResponse.json(
          { error: "Uploaded file cannot be empty (0 bytes)" },
          { status: 400 }
        );
      }

      // Verify SHA-256 integrity
      const expectedHex = share.hashValue.toLowerCase();
      let verified = false;

      if (objDetails.checksumSha256) {
        let r2Hex = objDetails.checksumSha256.toLowerCase();
        if (!/^[a-f0-9]{64}$/.test(r2Hex)) {
          try {
            r2Hex = Buffer.from(objDetails.checksumSha256, "base64").toString("hex").toLowerCase();
          } catch {}
        }
        if (r2Hex === expectedHex) {
          verified = true;
        }
      } else {
        // Fallback for mock mode or unpopulated checksum header
        verified = true;
      }

      if (!verified) {
        await db
          .collection("upload_sessions")
          .updateOne({ shareId: id }, { $set: { status: "VERIFY_FAILED" } });
        return NextResponse.json(
          { error: "Integrity check failed: uploaded file SHA-256 does not match client expectation" },
          { status: 412 }
        );
      }
    }

    // 4. Update Statuses to ACTIVE / COMPLETED
    const shareStatusBefore = share.status;
    const shareStatusAfter = "ACTIVE";

    const updateShareResult = await db.collection("shares").findOneAndUpdate(
      { shareId: id, status: "CREATED" },
      {
        $set: {
          status: shareStatusAfter,
          etag: finalEtag || null,
          // Store observed checksum if verified
          checksumCrc64nvme: finalCrc64 || share.checksumCrc64nvme,
        },
      },
      { returnDocument: "after" }
    );

    if (!updateShareResult) {
      return NextResponse.json({ error: "Conflict updating share status" }, { status: 409 });
    }

    await db
      .collection("upload_sessions")
      .updateOne({ shareId: id }, { $set: { status: "COMPLETED" } });

    // 5. Structured Diagnostics Logging
    const clientIp = req.headers.get("x-forwarded-for") || "127.0.0.1";
    const ipHash = anonymizeIp(clientIp);
    const userAgent = req.headers.get("user-agent") || "Unknown";

    const logEvent = {
      timestamp: now.toISOString(),
      requestId: `req_${crypto.randomUUID().replace(/-/g, "").substring(0, 16)}`,
      event: "UploadConfirmed",
      shareId: id,
      shareStatusBefore,
      shareStatusAfter,
      isMultipart: !!uploadSession.isMultipart,
      latencyMs: Date.now() - now.getTime(),
      ipHash,
      userAgentParsed: {
        raw: userAgent,
      },
      error: null,
    };

    console.log(JSON.stringify(logEvent));

    return NextResponse.json({
      message: "Upload confirmed successfully",
      shareId: id,
      status: shareStatusAfter,
      filename: share.filename,
      size: share.size,
    });
  } catch (error: unknown) {
    console.error("Error in POST /api/v1/share/[id]/confirm:", error);
    const errMsg = error instanceof Error ? error.message : "Internal Server Error";
    return NextResponse.json({ error: errMsg }, { status: 500 });
  }
}
