"use client";

import hljs from "highlight.js";
import "highlight.js/styles/github-dark.css";
import DOMPurify from "isomorphic-dompurify";

interface Props {
  code: string;
  language: string;
}

export default function SyntaxHighlighter({ code, language }: Props) {
  let highlightedValue = "";
  try {
    const result = hljs.highlight(code, { language });
    highlightedValue = `<pre class="hljs"><code class="language-${language}">${result.value}</code></pre>`;
  } catch {
    const result = hljs.highlightAuto(code);
    highlightedValue = `<pre class="hljs"><code>${result.value}</code></pre>`;
  }

  // Sanitize the output to prevent any potential XSS vulnerabilities
  const sanitizedHtml = DOMPurify.sanitize(highlightedValue);

  return <div dangerouslySetInnerHTML={{ __html: sanitizedHtml }} />;
}
