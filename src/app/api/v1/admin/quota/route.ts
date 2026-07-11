import { NextRequest, NextResponse } from "next/server";
import {
  getQuotaState,
  MAX_STORAGE_LIMIT,
  MAX_CLASS_A_LIMIT,
  MAX_CLASS_B_LIMIT,
} from "@/lib/quota";

export async function GET(req: NextRequest) {
  try {
    // Optional admin secret validation
    const adminSecret = process.env.ADMIN_SECRET;
    if (adminSecret) {
      const authHeader = req.headers.get("authorization");
      const urlSecret = req.nextUrl.searchParams.get("secret");
      const providedSecret = authHeader?.replace("Bearer ", "") || urlSecret;
      
      if (providedSecret !== adminSecret) {
        return NextResponse.json({ error: "Unauthorized access" }, { status: 401 });
      }
    }

    const state = await getQuotaState();
    const now = new Date().getTime();
    
    const classAResetAt = new Date(state.classAResetAt).getTime();
    const classBResetAt = new Date(state.classBResetAt).getTime();

    const secondsUntilClassAReset = Math.max(0, Math.round((classAResetAt - now) / 1000));
    const secondsUntilClassBReset = Math.max(0, Math.round((classBResetAt - now) / 1000));

    const totalOccupied = state.storageBytes + state.reservedBytes;
    const uploadsEnabled = totalOccupied < MAX_STORAGE_LIMIT && state.classAOps < MAX_CLASS_A_LIMIT;

    return NextResponse.json({
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
    return NextResponse.json(
      { error: "Failed to determine system quota status (fail-closed)" },
      { status: 503 }
    );
  }
}
