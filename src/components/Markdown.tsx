import { useMemo } from "react";
import { marked } from "marked";
import DOMPurify from "dompurify";
import { Kube } from "../mygo";

const escapeHtml = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

// Markdown only: HTML in it shows as text, never as markup. DOMPurify then
// sanitizes what Markdown produced, as a second guard.
marked.use({ gfm: true, breaks: true, renderer: { html: ({ text }) => escapeHtml(text) } });

// Runbooks link to app actions with kubedesk: URLs; other text does not.
const withActions = /^(?:(?:https?|mailto|kubedesk):|[^a-z]|[a-z+.-]+(?:[^a-z+.\-:]|$))/i;

/**
 * Markdown renders Markdown safely. Links open in the browser; with
 * onAction, kubedesk: links are actions of the app, handed to it.
 */
export function Markdown({ text, onAction, className = "md" }: { text: string; onAction?(href: string): void; className?: string }) {
  const html = useMemo(() => {
    const raw = marked.parse(text, { async: false }) as string;
    return DOMPurify.sanitize(raw, onAction ? { ALLOWED_URI_REGEXP: withActions } : {});
  }, [text, onAction]);
  return (
    <div
      className={className}
      dangerouslySetInnerHTML={{ __html: html }}
      onClick={(e) => {
        const a = (e.target as HTMLElement).closest("a");
        if (!a) return;
        const href = a.getAttribute("href") ?? "";
        e.preventDefault();
        if (href.startsWith("kubedesk:")) onAction?.(href);
        else if (/^https?:|^mailto:/i.test(href)) Kube.openURL(href).catch(() => {});
      }}
    />
  );
}
