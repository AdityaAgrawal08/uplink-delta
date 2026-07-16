import { getPresignedDownloadUrl } from "@/lib/r2";
import { getDb } from "@/lib/mongodb";
import FilePreview from "@/components/FilePreview";
import { notFound } from "next/navigation";

export default async function SharePage(props: { params: Promise<{ id: string }> }) {
  const { id } = await props.params;
  const db = await getDb();
  
  const share = await db.collection("shares").findOne({
    $or: [{ shareId: id }, { downloadCode: id }],
    status: "ACTIVE",
  });

  if (!share) {
    notFound();
  }

  const serverDownloadUrl = "";

  const shareMeta = {
    shareId: share.shareId,
    filename: share.filename,
    size: share.size,
    mimeType: share.mimeType,
    hashValue: share.hashValue,
    passwordRequired: !!share.passwordHash,
    isEncrypted: !!share.isEncrypted,
    createdAt: share.createdAt ? new Date(share.createdAt).toISOString() : null,
    expiresAt: share.expiresAt ? new Date(share.expiresAt).toISOString() : null,
    downloadUrl: serverDownloadUrl,
  };

  return (
    <div className="container">
      <h1>R2-Uplink File Share</h1>
      <p>This resource is available for download and inline web preview.</p>
      
      <h2>Download Command</h2>
      <pre className="code-block">
        uplink receive {share.shareId}
      </pre>

      <h2>First Time? Install the CLI</h2>
      <pre className="code-block">
        curl -sSf https://raw.githubusercontent.com/AdityaAgrawal08/uplink-delta/main/install.sh | sh
      </pre>

      <FilePreview share={shareMeta} />
    </div>
  );
}
