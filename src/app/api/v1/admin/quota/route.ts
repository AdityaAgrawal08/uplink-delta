import { NextRequest, NextResponse } from "next/server";
import { getDb } from "@/lib/mongodb";
import {
  getQuotaState,
  MAX_STORAGE_LIMIT,
  MAX_CLASS_A_LIMIT,
  MAX_CLASS_B_LIMIT,
} from "@/lib/quota";
import { validateAdminAuth } from "@/lib/auth";
import { apiError } from "@/lib/api-utils";

export async function GET(req: NextRequest) {
  try {
    if (!validateAdminAuth(req)) {
      return apiError("Unauthorized access", 401);
    }

    const state = await getQuotaState();
    const now = new Date().getTime();
    
    const classAResetAt = new Date(state.classAResetAt).getTime();
    const classBResetAt = new Date(state.classBResetAt).getTime();

    const secondsUntilClassAReset = Math.max(0, Math.round((classAResetAt - now) / 1000));
    const secondsUntilClassBReset = Math.max(0, Math.round((classBResetAt - now) / 1000));

    const db = await getDb();
    const totalUploads = await db.collection("shares").countDocuments({ status: "ACTIVE" });

    const totalOccupied = state.storageBytes + state.reservedBytes;
    const uploadsEnabled = totalOccupied < MAX_STORAGE_LIMIT && state.classAOps < MAX_CLASS_A_LIMIT;

    return NextResponse.json({
      totalUploads,
      storageUsageBytes: state.storageBytes,
      storageReservedBytes: state.reservedBytes,
      storageThresholdBytes: MAX_STORAGE_LIMIT,
      storageRemainingBytes: Math.max(0, MAX_STORAGE_LIMIT - totalOccupied),
      classAUsage: state.classAOps,
      classAThreshold: MAX_CLASS_A_LIMIT,
      classBUsage: state.classBOps,
      classBThreshold: MAX_CLASS_B_LIMIT,
      classAResetAt: state.classAResetAt,
      classBResetAt: state.classBResetAt,
      secondsUntilClassAReset,
      secondsUntilClassBReset,
      uploadsEnabled,
      quotaProtectionActive: true,
      recentEvents: state.quotaEvents || [],
    });
  } catch (err: unknown) {
    console.error("Failed to query admin quotas status:", err);
    return apiError("Failed to determine system quota status (fail-closed)", 503);
  }
}
