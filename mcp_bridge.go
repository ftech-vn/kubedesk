package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// runBridge connects an MCP client that starts its servers as processes,
// over standard input and output, to the MCP server of the running app:
//
//	Kube Desk mcp --config <user data>/mcp.json
//
// It opens the app when it is not running.
func runBridge(args []string) int {
	path := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			path = args[i+1]
			i++
		}
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "usage: kube-desk mcp --config <path of mcp.json>")
		return 2
	}
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	client := &http.Client{Timeout: approvalTimeout + time.Minute}
	session := ""
	launched := false
	for {
		line, err := in.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			reply := forward(client, path, line, &session, &launched)
			if len(reply) > 0 {
				out.Write(bytes.TrimSpace(reply))
				out.WriteByte('\n')
				out.Flush()
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
}

// forward posts one message to the app, opening the app once if needed.
func forward(client *http.Client, path string, msg []byte, session *string, launched *bool) []byte {
	var id json.RawMessage
	var probe struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(msg, &probe) == nil {
		id = probe.ID
	}
	fail := func(text string) []byte {
		if id == nil {
			return nil
		}
		return rpcError(id, -32000, text)
	}
	for attempt := 0; ; attempt++ {
		cfg, err := readBridgeConfig(path)
		if err == nil {
			req, _ := http.NewRequest(http.MethodPost, cfg.URL, bytes.NewReader(msg))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Authorization", "Bearer "+cfg.Token)
			if *session != "" {
				req.Header.Set("Mcp-Session-Id", *session)
			}
			res, err := client.Do(req)
			if err == nil {
				defer res.Body.Close()
				if s := res.Header.Get("Mcp-Session-Id"); s != "" {
					*session = s
				}
				body, _ := io.ReadAll(res.Body)
				if res.StatusCode == http.StatusAccepted {
					return nil
				}
				if res.StatusCode != http.StatusOK {
					return fail(fmt.Sprintf("Kube Desk answered %s: %s", res.Status, strings.TrimSpace(string(body))))
				}
				return body
			}
		}
		// The app is not running: open it, once, and wait for its server.
		if !*launched && err == nil || !*launched && os.IsNotExist(err) {
			*launched = true
			if cfg.App != "" {
				_ = exec.Command("open", "-g", cfg.App).Run()
			}
		}
		if attempt >= 20 {
			return fail("Kube Desk is not running, or its MCP server is off: open Kube Desk and turn on AI agents")
		}
		time.Sleep(time.Second)
	}
}

func readBridgeConfig(path string) (bridgeConfig, error) {
	var cfg bridgeConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	return cfg, json.Unmarshal(b, &cfg)
}
