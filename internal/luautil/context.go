package luautil

import (
	"net/http"

	lua "github.com/yuin/gopher-lua"

	"github.com/wudi/runway/internal/middleware/geo"
	"github.com/wudi/runway/variables"
)

// NewContextUserData creates a Lua userdata for runway context access.
func NewContextUserData(L *lua.LState, r *http.Request, varCtx *variables.Context) *lua.LUserData {
	ud := L.NewUserData()
	ud.Value = &ctxData{r: r, varCtx: varCtx}

	mt := L.NewTable()
	index := L.NewTable()

	L.SetField(index, "route_id", L.NewFunction(ctxRouteID))
	L.SetField(index, "request_id", L.NewFunction(ctxRequestID))
	L.SetField(index, "tenant_id", L.NewFunction(ctxTenantID))
	L.SetField(index, "client_id", L.NewFunction(ctxClientID))
	L.SetField(index, "auth_type", L.NewFunction(ctxAuthType))
	L.SetField(index, "claim", L.NewFunction(ctxClaim))
	L.SetField(index, "geo_country", L.NewFunction(ctxGeoCountry))
	L.SetField(index, "geo_city", L.NewFunction(ctxGeoCity))
	L.SetField(index, "path_param", L.NewFunction(ctxPathParam))
	L.SetField(index, "get_var", L.NewFunction(ctxGetVar))
	L.SetField(index, "set_var", L.NewFunction(ctxSetVar))

	L.SetField(mt, "__index", index)
	L.SetMetatable(ud, mt)
	return ud
}

type ctxData struct {
	r      *http.Request
	varCtx *variables.Context
}

func checkCtxData(L *lua.LState) *ctxData {
	ud := L.CheckUserData(1)
	if cd, ok := ud.Value.(*ctxData); ok {
		return cd
	}
	L.ArgError(1, "ctx expected")
	return nil
}

func ctxRouteID(L *lua.LState) int {
	cd := checkCtxData(L)
	if cd.varCtx != nil {
		L.Push(lua.LString(cd.varCtx.RouteID))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

func ctxRequestID(L *lua.LState) int {
	cd := checkCtxData(L)
	if cd.varCtx != nil {
		L.Push(lua.LString(cd.varCtx.RequestID))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

func ctxTenantID(L *lua.LState) int {
	cd := checkCtxData(L)
	if cd.varCtx != nil {
		L.Push(lua.LString(cd.varCtx.TenantID))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

func ctxClientID(L *lua.LState) int {
	cd := checkCtxData(L)
	if cd.varCtx != nil && cd.varCtx.Identity != nil {
		L.Push(lua.LString(cd.varCtx.Identity.ClientID))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

func ctxAuthType(L *lua.LState) int {
	cd := checkCtxData(L)
	if cd.varCtx != nil && cd.varCtx.Identity != nil {
		L.Push(lua.LString(cd.varCtx.Identity.AuthType))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

func ctxClaim(L *lua.LState) int {
	cd := checkCtxData(L)
	name := L.CheckString(2)
	if cd.varCtx != nil && cd.varCtx.Identity != nil && cd.varCtx.Identity.Claims != nil {
		if v, ok := cd.varCtx.Identity.Claims[name]; ok {
			switch val := v.(type) {
			case string:
				L.Push(lua.LString(val))
			case float64:
				L.Push(lua.LNumber(val))
			case bool:
				L.Push(lua.LBool(val))
			default:
				L.Push(lua.LString(""))
			}
			return 1
		}
	}
	L.Push(lua.LString(""))
	return 1
}

func ctxGeoCountry(L *lua.LState) int {
	cd := checkCtxData(L)
	if result := geo.GeoResultFromContext(cd.r.Context()); result != nil {
		L.Push(lua.LString(result.CountryCode))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

func ctxGeoCity(L *lua.LState) int {
	cd := checkCtxData(L)
	if result := geo.GeoResultFromContext(cd.r.Context()); result != nil {
		L.Push(lua.LString(result.City))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

func ctxPathParam(L *lua.LState) int {
	cd := checkCtxData(L)
	name := L.CheckString(2)
	if cd.varCtx != nil && cd.varCtx.PathParams != nil {
		L.Push(lua.LString(cd.varCtx.PathParams[name]))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

func ctxGetVar(L *lua.LState) int {
	cd := checkCtxData(L)
	name := L.CheckString(2)
	if cd.varCtx != nil && cd.varCtx.Custom != nil {
		L.Push(lua.LString(cd.varCtx.Custom[name]))
	} else {
		L.Push(lua.LString(""))
	}
	return 1
}

func ctxSetVar(L *lua.LState) int {
	cd := checkCtxData(L)
	name := L.CheckString(2)
	value := L.CheckString(3)
	if cd.varCtx != nil {
		if cd.varCtx.Custom == nil {
			cd.varCtx.Custom = make(map[string]string)
		}
		cd.varCtx.Custom[name] = value
	}
	return 0
}
