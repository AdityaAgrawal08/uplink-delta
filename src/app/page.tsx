export default function Home() {
  return (
    <div style={{ fontFamily: "monospace", padding: "2rem", color: "#ccc", backgroundColor: "#111", minHeight: "100vh" }}>
      <h1>R2-Uplink File-Sharing Platform</h1>
      <p>This is a CLI-only file-sharing service. Browser-based uploads are not supported.</p>
      <h2>Installation</h2>
      <pre style={{ backgroundColor: "#222", padding: "1rem", borderRadius: "4px", overflowX: "auto" }}>
        curl -sSf https://raw.githubusercontent.com/AdityaAgrawal08/uplink-delta/main/install.sh | sh
      </pre>
      <h2>Usage</h2>
      <p>To share a file or directory:</p>
      <pre style={{ backgroundColor: "#222", padding: "1rem", borderRadius: "4px", overflowX: "auto" }}>
        uplink send &lt;filepath-or-directory&gt;
      </pre>
    </div>
  );
}
