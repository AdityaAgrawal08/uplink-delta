import { NextResponse } from "next/server";
import { getDb } from "@/lib/mongodb";
import { deleteObject } from "@/lib/r2";
import { recordDeleteQuota, releaseUploadQuota } from "@/lib/quota";

export async function POST() {
  return performCleanup();
}

export async function GET() {
  return performCleanup();
}

async function performCleanup() {
  try {
    const db = await getDb();
    const now = new Date();
    const workerId = `worker_${crypto.randomUUID().substring(0, 8)}`;
    const lockDurationMs = 5 * 60 * 1000; // 5 minutes
    const cleanupLockedUntil = new Date(Date.now() + lockDurationMs);

    // 1. Identify and lock expired shares
    // Candidates are:
    // - status in [ACTIVE, EXPIRED, CREATED, DELETE_FAILED] and expired
    // - OR status in PENDING_DELETE and lock has expired (crashed worker)
    const lockQuery = {
      $or: [
        {
          status: { $in: ["ACTIVE", "EXPIRED", "CREATED", "DELETE_FAILED"] },
          expiresAt: { $lt: now },
        },
        {
          status: "PENDING_DELETE",
          cleanupLockedUntil: { $lt: now },
        },
      ],
    };

    const lockUpdate = {
      $set: {
        status: "PENDING_DELETE",
        cleanupLockedUntil,
        cleanupWorkerId: workerId,
        lastRetryAt: now,
      },
      $inc: { retryCount: 1 },
    };

    // 1. Identify expired shares first to preserve their original status
    const candidates = await db.collection("shares").find(lockQuery).toArray();
    
    if (candidates.length === 0) {
      // 4. Also clean up old upload sessions metadata that are completed or expired
      const uploadSessionsCleanupResult = await db.collection("upload_sessions").deleteMany({
        $or: [
          { status: "COMPLETED", createdAt: { $lt: new Date(Date.now() - 24 * 3600 * 1000) } }, // Keep logs for 24h
          { status: { $in: ["PENDING", "UPLOADING", "VERIFY_FAILED", "EXPIRED"] }, uploadExpiresAt: { $lt: now } }
        ]
      });
      return NextResponse.json({
        message: "No expired shares found for cleanup",
        uploadSessionsCleaned: uploadSessionsCleanupResult.deletedCount
      });
    }

    const candidateIds = candidates.map(c => c._id);
    const lockResult = await db.collection("shares").updateMany(
      { _id: { $in: candidateIds } },
      lockUpdate
    );

    // Create a lookup map for candidates by shareId to retrieve their original status
    const candidateMap = new Map(candidates.map(c => [c.shareId, c]));

    // 2. Fetch locked shares for this worker
    const lockedShares = await db
      .collection("shares")
      .find({
        status: "PENDING_DELETE",
        cleanupWorkerId: workerId,
      })
      .toArray();

    const results = [];

    // 3. Process deletions
    for (const share of lockedShares) {
      const originalShare = candidateMap.get(share.shareId);
      const originalStatus = originalShare ? originalShare.status : "ACTIVE";

      const deleteSuccess = await deleteObject(share.objectKey);
      
      if (deleteSuccess) {
        // Deletion succeeded: Update status to DELETED
        await db.collection("shares").updateOne(
          { shareId: share.shareId },
          {
            $set: {
              status: "DELETED",
              cleanupLockedUntil: null,
              cleanupWorkerId: null,
            },
          }
        );

        // Adjust quota system metrics based on original status
        if (originalStatus === "CREATED") {
          // If it was unconfirmed, release the reservation
          const uploadSession = await db.collection("upload_sessions").findOne({ shareId: share.shareId });
          const estimatedOps = uploadSession?.isMultipart ? uploadSession.partsCount + 2 : 1;
          await releaseUploadQuota(share.size, estimatedOps);
        } else {
          // If it was committed, decrement active storage bytes and record delete op
          await recordDeleteQuota(share.size);
        }

        results.push({ shareId: share.shareId, status: "DELETED" });
      } else {
        // Deletion failed: Transition to DELETE_FAILED and release lock
        await db.collection("shares").updateOne(
          { shareId: share.shareId },
          {
            $set: {
              status: "DELETE_FAILED",
              cleanupLockedUntil: null,
              cleanupWorkerId: null,
              lastErrorCode: "R2_DELETE_FAILED",
              lastErrorMessage: "Failed to delete file from R2 object storage",
            },
          }
        );

        // If the unconfirmed upload expired and R2 deletion failed, we still release the reservation
        // so storage is not leaked.
        if (originalStatus === "CREATED") {
          const uploadSession = await db.collection("upload_sessions").findOne({ shareId: share.shareId });
          const estimatedOps = uploadSession?.isMultipart ? uploadSession.partsCount + 2 : 1;
          await releaseUploadQuota(share.size, estimatedOps);
        }

        results.push({ shareId: share.shareId, status: "DELETE_FAILED" });
      }
    }

    // 4. Also clean up old upload sessions metadata that are completed or expired
    const uploadSessionsCleanupResult = await db.collection("upload_sessions").deleteMany({
      $or: [
        { status: "COMPLETED", createdAt: { $lt: new Date(Date.now() - 24 * 3600 * 1000) } }, // Keep logs for 24h
        { status: { $in: ["PENDING", "UPLOADING", "VERIFY_FAILED", "EXPIRED"] }, uploadExpiresAt: { $lt: now } }
      ]
    });

    return NextResponse.json({
      message: "Cleanup task completed",
      workerId,
      sharesMatched: lockResult.matchedCount,
      sharesLocked: lockedShares.length,
      actions: results,
      uploadSessionsCleaned: uploadSessionsCleanupResult.deletedCount,
    });
  } catch (error: unknown) {
    console.error("Cleanup job error:", error);
    const errMsg = error instanceof Error ? error.message : "Internal Server Error";
    return NextResponse.json({ error: errMsg }, { status: 500 });
  }
}
