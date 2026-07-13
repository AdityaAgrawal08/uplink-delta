"use client";

import { useEffect, useState } from "react";

interface Props {
  code: string;
  language: string;
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

interface Hljs {
  highlight: (code: string, options: { language: string }) => { value: string };
  highlightAuto: (code: string) => { value: string };
}

export default function SyntaxHighlighter({ code, language }: Props) {
  const [html, setHtml] = useState<string>("<pre><code>" + escapeHtml(code) + "</code></pre>");

  useEffect(() => {
    if (!document.getElementById("hljs-theme")) {
      const link = document.createElement("link");
      link.id = "hljs-theme";
      link.rel = "stylesheet";
      link.href = "https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/github-dark.min.css";
      document.head.appendChild(link);
    }

    const scriptId = "hljs-script";
    let script = document.getElementById(scriptId) as HTMLScriptElement;
    
    const highlight = () => {
      const hljs = (window as unknown as { hljs?: Hljs }).hljs;
      if (hljs) {
        try {
          const result = hljs.highlight(code, { language });
          setHtml(`<pre class="hljs"><code class="language-${language}">${result.value}</code></pre>`);
        } catch {
          const result = hljs.highlightAuto(code);
          setHtml(`<pre class="hljs"><code>${result.value}</code></pre>`);
        }
      }
    };

    if (!script) {
      script = document.createElement("script");
      script.id = scriptId;
      script.src = "https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/highlight.min.js";
      script.onload = highlight;
      document.head.appendChild(script);
    } else if ((window as unknown as { hljs?: Hljs }).hljs) {
      highlight();
    } else {
      script.addEventListener("load", highlight);
      return () => script.removeEventListener("load", highlight);
    }
  }, [code, language]);

  return <div dangerouslySetInnerHTML={{ __html: html }} />;
}
