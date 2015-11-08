package logging

import (
	"github.com/12foo/apiplexy"
	"github.com/mssola/user_agent"
	"net/http"
)

type UADetectionPlugin struct{}

func (uap *UADetectionPlugin) Log(req *http.Request, res *http.Response, ctx *apiplexy.APIContext) error {
	ua := user_agent.New(req.UserAgent())
	ctx.Log["ua_mobile"] = ua.Mobile()
	ctx.Log["ua_bot"] = ua.Bot()
	browser, version := ua.Browser()
	ctx.Log["ua_browser"] = browser
	ctx.Log["ua_version"] = version
	ctx.Log["ua_os"] = ua.OS()
	return nil
}

func (uap *UADetectionPlugin) DefaultConfig() map[string]interface{} {
	return map[string]interface{}{}
}

func (uap *UADetectionPlugin) Configure(config map[string]interface{}) error {
	return nil
}

func init() {
	// _ = apiplexy.LoggingPlugin(&UADetectionPlugin{})
	apiplexy.RegisterPlugin(
		"ua-detect",
		"Detect user-agents, i.e. the user's browser.",
		"http://github.com/12foo/apiplexy/tree/master/logging",
		UADetectionPlugin{},
	)
}
