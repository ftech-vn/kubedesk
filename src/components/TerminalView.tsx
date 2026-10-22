import { useEffect, useRef, useState } from "react";
import { Channel } from "mygo-runtime";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { RotateCcw } from "lucide-react";
import { Kube } from "../mygo";
import { base64ToBytes, errorMessage, type KObj } from "../lib/k8s";
import { Button } from "./ui";

function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

/** TerminalView is a shell in a container of a pod. */
export function TerminalView({ cluster, pod, initialContainer }: { cluster: string; pod: KObj; initialContainer?: string }) {
  const containers: string[] = (pod.spec?.containers ?? []).map((c: any) => c.name);
  const [container, setContainer] = useState(initialContainer ?? containers[0] ?? "");
  const [shell, setShell] = useState("auto");
  const [generation, setGeneration] = useState(0);
  const [ended, setEnded] = useState(false);
  const host = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setEnded(false);
    const term = new Terminal({
      fontFamily: cssVar("--mono") || "Menlo, monospace",
      fontSize: 12.5,
      cursorBlink: true,
      allowProposedApi: true,
      theme: {
        background: cssVar("--term-bg"),
        foreground: cssVar("--term-fg"),
        cursor: cssVar("--accent"),
        selectionBackground: cssVar("--selection"),
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host.current!);
    fit.fit();
    term.focus();

    const session = crypto.randomUUID();
    const out = new Channel<string>((chunk) => term.write(base64ToBytes(chunk)));
    const command = shell === "auto" ? [] : [shell];
    term.write(`\x1b[2mConnecting to ${pod.metadata.name}/${container}…\x1b[0m\r\n`);
    let started = false;
    const sendSize = () => Kube.execResize(session, term.cols, term.rows).catch(() => {});
    Kube.exec(cluster, pod.metadata.namespace ?? "", pod.metadata.name, container, command, session, out)
      .then(() => term.write("\r\n\x1b[2m[session ended]\x1b[0m\r\n"))
      .catch((e) => term.write(`\r\n\x1b[31m${errorMessage(e)}\x1b[0m\r\n`))
      .finally(() => setEnded(true));
    // The session exists once Go registered it; send the size when it does.
    const sizeTimer = setInterval(() => {
      if (started) return;
      Kube.execResize(session, term.cols, term.rows)
        .then(() => {
          started = true;
        })
        .catch(() => {});
    }, 300);
    setTimeout(() => clearInterval(sizeTimer), 5000);

    const input = term.onData((d) => Kube.execInput(session, d).catch(() => {}));
    const resize = term.onResize(() => sendSize());
    const ro = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        /* hidden */
      }
    });
    ro.observe(host.current!);
    return () => {
      clearInterval(sizeTimer);
      ro.disconnect();
      input.dispose();
      resize.dispose();
      out.close();
      term.dispose();
    };
  }, [cluster, pod.metadata.namespace, pod.metadata.name, container, shell, generation]);

  return (
    <div className="terminal">
      <div className="logs-toolbar">
        {containers.length > 1 && (
          <select value={container} onChange={(e) => setContainer(e.target.value)} className="select-sm">
            {containers.map((c) => (
              <option key={c}>{c}</option>
            ))}
          </select>
        )}
        <select value={shell} onChange={(e) => setShell(e.target.value)} className="select-sm" title="Shell">
          <option value="auto">bash, else sh</option>
          <option value="/bin/sh">/bin/sh</option>
          <option value="/bin/bash">/bin/bash</option>
          <option value="/bin/ash">/bin/ash</option>
          <option value="/busybox/sh">/busybox/sh</option>
        </select>
        <div className="spacer" />
        {ended && (
          <Button size="sm" icon={<RotateCcw size={14} />} onClick={() => setGeneration((g) => g + 1)}>
            Reconnect
          </Button>
        )}
      </div>
      <div className="terminal-host" ref={host} />
    </div>
  );
}
