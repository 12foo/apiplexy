package misc

import (
	"fmt"
	"github.com/12foo/apiplexy"
	"github.com/aarzilli/golua/lua"
	"github.com/fatih/structs"
	"net"
	"net/http"
	"strings"
)

type LuaPlugin struct {
	script     string
	pathFilter bool
	debug      bool
	paths      []string
}

func pushMap(L *lua.State, m map[string]interface{}, lower bool) {
	L.CreateTable(0, len(m))
	for k, v := range m {
		if lower {
			L.PushString(strings.ToLower(k))
		} else {
			L.PushString(k)
		}
		switch t := v.(type) {
		case string:
			L.PushString(t)
		case int64:
			L.PushInteger(t)
		case int:
			L.PushInteger(int64(t))
		case float64:
			L.PushNumber(t)
		case bool:
			L.PushBoolean(t)
		case map[string]interface{}:
			pushMap(L, t, false)
		default:
			L.PushNil()
		}
		L.SetTable(-3)
	}
}

func popMap(L *lua.State) map[string]interface{} {
	m := make(map[string]interface{})
	L.PushNil()
	for L.Next(-2) != 0 {
		if L.IsString(-2) {
			var v interface{}
			if L.IsBoolean(-1) {
				v = L.ToBoolean(-1)
			} else if L.IsNumber(-1) {
				var t float64
				t = L.ToNumber(-1)
				if t == float64(int64(t)) {
					v = int(t)
				} else {
					v = t
				}
			} else if L.IsString(-1) {
				v = L.ToString(-1)
			} else if L.IsNil(-1) {
				v = nil
			} else if L.IsTable(-1) {
				v = popMap(L)
			}
			m[L.ToString(-2)] = v
		}
		L.Pop(1)
	}
	return m
}

func (p *LuaPlugin) prepContext(L *lua.State, req *http.Request, ctx *apiplexy.APIContext) {
	var clientIP string
	if req.Header.Get("X-Forwarded-For") != "" {
		clientIP = req.Header.Get("X-Forwarded-For")
	} else {
		clientIP, _, _ = net.SplitHostPort(req.RemoteAddr)
	}

	headers := make(map[string]interface{}, len(req.Header))
	for k, vs := range req.Header {
		headers[k] = strings.Join(vs, " ")
	}

	request := map[string]interface{}{
		"path":     req.URL.Path,
		"method":   req.Method,
		"ip":       clientIP,
		"referrer": req.Referer(),
		"browser":  req.UserAgent(),
		"headers":  headers,
	}
	pushMap(L, request, false)
	L.SetGlobal("request")

	pushMap(L, structs.Map(ctx), true)
	L.SetGlobal("context")
}

func (p *LuaPlugin) enableDebug(L *lua.State) {
	L.DoString(inspectLua)
	L.SetGlobal("inspect")
}

func (p *LuaPlugin) runScript(req *http.Request, ctx *apiplexy.APIContext) error {
	if p.pathFilter {
		run := false
		for _, path := range p.paths {
			if strings.HasPrefix(req.URL.Path, path) {
				run = true
			}
		}
		if !run {
			return nil
		}
	}

	L := lua.NewState()
	L.OpenBase()
	L.OpenString()
	L.OpenTable()
	L.OpenMath()
	defer L.Close()

	p.prepContext(L, req, ctx)

	if p.debug {
		p.enableDebug(L)
	}

	if load := L.LoadString(p.script); load != 0 {
		return fmt.Errorf("Lua script error (%d): %s", load, L.ToString(-1))
	}
	if err := L.Call(0, 2); err != nil {
		return err
	}

	if !L.IsNoneOrNil(1) {
		var msg string
		var status int
		if !L.IsString(1) {
			return fmt.Errorf("Return errors from Lua like so: 'return \"my error message\", 400'")
		}
		msg = L.ToString(1)
		if L.IsNumber(2) {
			status = L.ToInteger(2)
		} else {
			status = 500
		}
		L.Pop(2)
		return apiplexy.Abort(status, msg)
	}

	L.GetGlobal("context")
	newctx := popMap(L)

	ctx.Cost = newctx["cost"].(int)
	ctx.Data = newctx["data"].(map[string]interface{})
	ctx.Log = newctx["log"].(map[string]interface{})

	return nil
}

func (p *LuaPlugin) PostAuth(req *http.Request, ctx *apiplexy.APIContext) error {
	return p.runScript(req, ctx)
}

func (p *LuaPlugin) PreUpstream(req *http.Request, ctx *apiplexy.APIContext) error {
	return p.runScript(req, ctx)
}

func (p *LuaPlugin) PostUpstream(req *http.Request, res *http.Response, ctx *apiplexy.APIContext) error {
	return p.runScript(req, ctx)
}

func (p *LuaPlugin) Log(req *http.Request, res *http.Response, ctx *apiplexy.APIContext) error {
	return p.runScript(req, ctx)
}

func (p *LuaPlugin) DefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"script": "-- your script here",
		"debug":  false,
		"paths":  []string{},
	}
}

func (p *LuaPlugin) Configure(config map[string]interface{}) error {
	p.script = config["script"].(string)
	p.debug = config["debug"].(bool)
	p.paths = config["paths"].([]string)
	if len(p.paths) > 0 {
		p.pathFilter = true
	} else {
		p.pathFilter = false
	}
	return nil
}

func init() {
	apiplexy.RegisterPlugin(
		"lua",
		"Run Lua scripts on incoming requests/responses.",
		"https://github.com/12foo/apiplexy/tree/master/misc/lua.md",
		LuaPlugin{},
	)
}
