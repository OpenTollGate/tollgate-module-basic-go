package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// Bridge is the seam the dispatcher talks to; SocketBridge is the production
// implementation and FakeBridge is the test double.
type Bridge interface {
	Call(msg CLIMessage) (CLIResponse, error)
}

// FakeBridge records/answers for tests.
type FakeBridge struct {
	fn func(CLIMessage) (CLIResponse, error)
}

func (f *FakeBridge) Call(msg CLIMessage) (CLIResponse, error) { return f.fn(msg) }

// Dispatcher turns one unwrapped JSON-RPC request from one caller into one
// JSON-RPC response. It owns the order of the gates: authorize FIRST, then
// touch the socket — a refused call must never reach the service.
type Dispatcher struct {
	authz     *Authorizer
	bridge    Bridge
	serverNpub string
}

func NewDispatcher(authz *Authorizer, bridge Bridge, serverNpub string) *Dispatcher {
	return &Dispatcher{authz: authz, bridge: bridge, serverNpub: serverNpub}
}

func errResp(id interface{}, code int, msg string) JSONRPCResponse {
	return JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
}

// Dispatch handles one request. callerNpub is the seal author from
// UnwrapRequest — the identity every authorization decision keys on.
func (d *Dispatcher) Dispatch(callerNpub string, req JSONRPCRequest) JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: mustMarshal(map[string]interface{}{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo": map[string]interface{}{
				"name":    "tollgate-control-plane",
				"version": "0.1.0",
			},
		})}
	case "tools/list":
		tools := make([]map[string]interface{}, 0, len(ToolSurface()))
		for _, tool := range ToolSurface() {
			tools = append(tools, map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
			})
		}
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: mustMarshal(map[string]interface{}{"tools": tools})}

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if len(req.Params) == 0 {
			return errResp(req.ID, ErrParams, "tools/call requires params")
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResp(req.ID, ErrParams, fmt.Sprintf("params: %v", err))
		}
		if params.Name == "" {
			return errResp(req.ID, ErrParams, "tools/call requires a tool name")
		}

		// Gate 1: authorization, BEFORE any socket traffic.
		if err := d.authz.Authorize(callerNpub, params.Name); err != nil {
			return errResp(req.ID, ErrAuthz, err.Error())
		}

		// Gate 2: only tools that are actually on the surface can be called
		// (Authorize already refuses unknown names; this maps name → CLI msg).
		surface := ToolSurface()
		var tool *Tool
		for i := range surface {
			if surface[i].Name == params.Name {
				tool = &surface[i]
				break
			}
		}
		if tool == nil {
			return errResp(req.ID, ErrMethodNF, fmt.Sprintf("tool %q not found", params.Name))
		}

		msg := CLIMessage{Command: tool.CLICommand, Args: append([]string{}, tool.CLIArgs...), Timestamp: time.Now()}

		// Per-tool argument adaptation. Only write tools take arguments, and
		// each shape is explicit — no generic "pass everything through".
		switch params.Name {
		case "config_set":
			key, _ := params.Arguments["key"].(string)
			value, _ := params.Arguments["value"].(string)
			if key == "" {
				return errResp(req.ID, ErrParams, "config_set requires key and value")
			}
			msg.Args = append(msg.Args, key, value)
		case "private_rename":
			ssid, _ := params.Arguments["ssid"].(string)
			if ssid == "" {
				return errResp(req.ID, ErrParams, "private_rename requires ssid")
			}
			msg.Args = append(msg.Args, ssid)
		}

		cliResp, err := d.bridge.Call(msg)
		if err != nil {
			return errResp(req.ID, ErrInternal, err.Error())
		}
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: mustMarshal(cliResp)}

	default:
		return errResp(req.ID, ErrMethodNF, fmt.Sprintf("unknown method %q", req.Method))
	}
}

func mustMarshal(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(b)
}
