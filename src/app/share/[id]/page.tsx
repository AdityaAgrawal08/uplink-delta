import { use } from "react";

export default function SharePage(props: { params: Promise<{ id: string }> }) {
  const { id } = use(props.params);

  return (
    <div style={{ fontFamily: "monospace", padding: "2rem", color: "#ccc", backgroundColor: "#111", minHeight: "100vh" }}>
      <h1>R2-Uplink File Download</h1>
      <p>This resource must be downloaded via the command line interface.</p>
      <h2>Download Command</h2>
      <pre style={{ backgroundColor: "#222", padding: "1rem", borderRadius: "4px", overflowX: "auto" }}>
        uplink receive http://localhost:3000/share/{id}
      </pre>
      <h2>First Time? Install the CLI</h2>
      <pre style={{ backgroundColor: "#222", padding: "1rem", borderRadius: "4px", overflowX: "auto" }}>
        curl -sSf https://raw.githubusercontent.com/AdityaAgrawal08/uplink-delta/main/install.sh | sh
      </pre>
    </div>
  );
}
