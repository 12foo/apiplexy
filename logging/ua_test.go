package logging

import (
	"testing"
)

var uaTestData = []struct {
	UA      string
	Browser string
	Version string
	OS      string
	Bot     bool
	Mobile  bool
}{
	{"", "", "", "", false, false},
	{"Mozilla/5.0 (X11; Linux x86_64; rv:41.0) Gecko/20100101 Firefox/41.0", "Firefox", "41.0", "Linux x86_64", false, false},
}
var uaPlugin *UADetectionPlugin

func TestUASetup(t *testing.T) {
	uaPlugin = &UADetectionPlugin{}
	assertNil(t, uaPlugin.Configure(map[string]interface{}{}))
}

func TestUALog(t *testing.T) {
	for _, test := range uaTestData {
		req, res, ctx := generateLog()
		req.Header.Set("User-Agent", test.UA)
		uaPlugin.Log(req, res, ctx)
		assertEqual(t, ctx.Log["ua_browser"], test.Browser)
		assertEqual(t, ctx.Log["ua_version"], test.Version)
		assertEqual(t, ctx.Log["ua_os"], test.OS)
		assertEqual(t, ctx.Log["ua_bot"], test.Bot)
		assertEqual(t, ctx.Log["ua_mobile"], test.Mobile)
	}
}
