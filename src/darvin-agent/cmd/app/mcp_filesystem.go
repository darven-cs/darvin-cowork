// spec 36 — bundled filesystem MCP server。
//
// 作为 darvin-agent 二进制的 subcommand 启动: `darvin-agent mcp-filesystem`。
// 走 stdio + JSON-RPC 2.0,暴露 3 个 tool(list_directory / read_file /
// write_file)。Root 目录由 `DARVIN_MCP_FS_ROOT` env 决定,缺省 cwd。
//
// 协议:严格遵循 spec 36 与 MCP 2024-11-05 Initialize 协议;request 与
// response 都走 LSP 风格 `Content-Length: N\r\n\r\n` 帧(与 registry 的
// StdioTransport 一致)。tool result 走 MCP `content` 数组(text 单元素)。
// 所有路径在执行前 realpath 校验,必须在 root 内,避免 path traversal。

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	rpcErrParse          = -32700
	rpcErrInvalidRequest = -32600
	rpcErrMethodNotFound = -32601
	rpcErrInvalidParams  = -32602
	rpcErrInternal       = -32603
)

func runFilesystemMCP() {
	root := strings.TrimSpace(os.Getenv("DARVIN_MCP_FS_ROOT"))
	runFilesystemMCPWithIO(os.Stdin, os.Stdout, root)
}

// runFilesystemMCPWithIO 把 IO 抽成参数便于测试。root 为空时用 cwd。
// 协议:LSP 风格 Content-Length 帧——每个 request 一帧,响应一帧;
// notifications(id 缺失) 不写响应。
func runFilesystemMCPWithIO(in io.Reader, out io.Writer, root string) {
	if strings.TrimSpace(root) == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			writeErrorTo(out, nil, rpcErrInternal, "cannot resolve cwd: "+err.Error())
			return
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		writeErrorTo(out, nil, rpcErrInternal, "cannot resolve root: "+err.Error())
		return
	}
	for {
		body, err := readFrame(in)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			writeErrorTo(out, nil, rpcErrParse, "frame error: "+err.Error())
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeErrorTo(out, nil, rpcErrParse, "parse error: "+err.Error())
			continue
		}
		if req.JSONRPC != "2.0" {
			writeErrorTo(out, req.ID, rpcErrInvalidRequest, "jsonrpc must be 2.0")
			continue
		}
		isNotification := req.ID == nil
		var resp rpcResponse
		resp.JSONRPC = "2.0"
		if req.ID != nil {
			resp.ID = *req.ID
		}
		switch req.Method {
		case "initialize":
			resp.Result = handleInitialize()
		case "notifications/initialized":
			if isNotification {
				continue
			}
			resp.Result = map[string]any{}
		case "tools/list":
			resp.Result = handleToolsList()
		case "tools/call":
			result, rpcErr := handleToolsCall(req.Params, absRoot)
			if rpcErr != nil {
				resp.Error = rpcErr
			} else {
				resp.Result = result
			}
		case "ping":
			resp.Result = map[string]any{}
		default:
			if isNotification {
				continue
			}
			resp.Error = &rpcError{Code: rpcErrMethodNotFound, Message: "method not found: " + req.Method}
		}
		if isNotification {
			continue
		}
		writeResponseTo(out, resp)
	}
}

// readFrame reads one LSP-style frame: `Content-Length: N\r\n\r\n` headers
// followed by exactly N bytes of body. EOF before a frame returns io.EOF.
func readFrame(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	contentLength := 0
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		const prefix = "Content-Length: "
		if strings.HasPrefix(line, prefix) {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
			if err != nil {
				return nil, fmt.Errorf("bad Content-Length %q: %w", line, err)
			}
			contentLength = n
		}
	}
	if contentLength <= 0 {
		return nil, errors.New("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, err
	}
	return body, nil
}

// writeFrame writes one LSP-style frame around body.
func writeFrame(w io.Writer, body []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

func writeResponse(resp rpcResponse) {
	writeResponseTo(os.Stdout, resp)
}

func writeResponseTo(out io.Writer, resp rpcResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		writeErrorTo(out, nil, rpcErrInternal, "marshal: "+err.Error())
		return
	}
	_ = writeFrame(out, b)
}

func writeError(id *json.RawMessage, code int, msg string) {
	writeErrorTo(os.Stdout, id, code, msg)
}

func writeErrorTo(out io.Writer, id *json.RawMessage, code int, msg string) {
	var rid json.RawMessage
	if id != nil {
		rid = *id
	}
	writeResponseTo(out, rpcResponse{
		JSONRPC: "2.0",
		ID:      rid,
		Error:   &rpcError{Code: code, Message: msg},
	})
}

func handleInitialize() map[string]any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo": map[string]any{
			"name":    "darvin-filesystem",
			"version": "0.1.0",
		},
	}
}

func handleToolsList() map[string]any {
	return map[string]any{
		"tools": []map[string]any{
			{
				"name":        "list_directory",
				"description": "列出目录下的直接子项(文件 + 子目录,不含递归内容)",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "相对 root 的目录路径;缺省列 root 本身",
						},
					},
				},
			},
			{
				"name":        "read_file",
				"description": "读取文本文件内容(UTF-8,最大 4 MiB)",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "相对 root 的文件路径",
						},
					},
					"required": []string{"path"},
				},
			},
			{
				"name":        "write_file",
				"description": "写入文本文件(覆盖已存在文件;最大 4 MiB)",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string"},
						"content": map[string]any{"type": "string"},
					},
					"required": []string{"path", "content"},
				},
			},
		},
	}
}

type toolCallParams struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"arguments"`
}

func handleToolsCall(params json.RawMessage, root string) (map[string]any, *rpcError) {
	var p toolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: rpcErrInvalidParams, Message: "params: " + err.Error()}
	}
	var args map[string]any
	if len(p.Args) > 0 {
		_ = json.Unmarshal(p.Args, &args)
	}
	switch p.Name {
	case "list_directory":
		return toolListDirectory(args, root)
	case "read_file":
		return toolReadFile(args, root)
	case "write_file":
		return toolWriteFile(args, root)
	default:
		return nil, &rpcError{Code: rpcErrMethodNotFound, Message: "tool not found: " + p.Name}
	}
}

// resolveWithin 把 path 解析成 root 内的绝对路径;不合法返 error message
// 走 tool result 文本(不是 RPC error),这样 caller 能拿到原因重试。
func resolveWithin(root, p string) (string, error) {
	if p == "" {
		return root, nil
	}
	abs, err := filepath.Abs(filepath.Join(root, p))
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		// root 可能不存在(fresh install):用 abs 形式而非 realpath 校验。
		realRoot = root
	}
	realAbs, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// 目标可能尚未存在(write_file);直接用 abs 校验前缀。
		realAbs = abs
	}
	if realAbs != realRoot && !strings.HasPrefix(realAbs, realRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root")
	}
	return realAbs, nil
}

func toolListDirectory(args map[string]any, root string) (map[string]any, *rpcError) {
	path, _ := args["path"].(string)
	abs, err := resolveWithin(root, path)
	if err != nil {
		return toolErrorResult(err.Error()), nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return toolErrorResult(err.Error()), nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": strings.Join(names, "\n")},
		},
	}, nil
}

const maxFileBytes = 4 * 1024 * 1024

func toolReadFile(args map[string]any, root string) (map[string]any, *rpcError) {
	path, _ := args["path"].(string)
	if path == "" {
		return toolErrorResult("path required"), nil
	}
	abs, err := resolveWithin(root, path)
	if err != nil {
		return toolErrorResult(err.Error()), nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return toolErrorResult(err.Error()), nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return toolErrorResult(err.Error()), nil
	}
	if st.IsDir() {
		return toolErrorResult("is a directory"), nil
	}
	if st.Size() > maxFileBytes {
		return toolErrorResult(fmt.Sprintf("file too large: %d bytes (max %d)", st.Size(), maxFileBytes)), nil
	}
	buf := make([]byte, st.Size())
	if _, err := io.ReadFull(f, buf); err != nil {
		return toolErrorResult(err.Error()), nil
	}
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(buf)},
		},
	}, nil
}

func toolWriteFile(args map[string]any, root string) (map[string]any, *rpcError) {
	path, _ := args["path"].(string)
	if path == "" {
		return toolErrorResult("path required"), nil
	}
	content, ok := args["content"].(string)
	if !ok {
		return toolErrorResult("content (string) required"), nil
	}
	if len(content) > maxFileBytes {
		return toolErrorResult(fmt.Sprintf("content too large: %d bytes (max %d)", len(content), maxFileBytes)), nil
	}
	abs, err := resolveWithin(root, path)
	if err != nil {
		return toolErrorResult(err.Error()), nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return toolErrorResult(err.Error()), nil
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return toolErrorResult(err.Error()), nil
	}
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": fmt.Sprintf("wrote %d bytes to %s", len(content), path)},
		},
	}, nil
}

func toolErrorResult(msg string) map[string]any {
	return map[string]any{
		"isError": true,
		"content": []map[string]any{
			{"type": "text", "text": msg},
		},
	}
}
