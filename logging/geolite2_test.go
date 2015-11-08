package logging

import (
	"os"
	"testing"
)

var g2Available bool = false
var g2Config map[string]interface{} = map[string]interface{}{
	"mmdb_path":      "GeoLite2-City.mmdb",
	"localize_names": "en",
}
var g2Plugin *GeoLite2Plugin

var g2TestData = []struct {
	IP        string
	Country   string
	Latitude  float64
	Longitude float64
	City      string
}{
	{"92.236.102.213", "GB", 51.7974, -2.4248, "Gloucester"},
}

func TestGeoLite2Setup(t *testing.T) {
	if _, err := os.Stat("GeoLite2-City.mmdb"); err != nil {
		t.Log("GeoIP2 database file not available for testing. Skipping the other tests.")
		t.Log("Download file, ungzip and place alongside geolite2_test.go:")
		t.Log("http://geolite.maxmind.com/download/geoip/database/GeoLite2-City.mmdb.gz")
	} else {
		g2Plugin = &GeoLite2Plugin{}
		assertNil(t, g2Plugin.Configure(g2Config))
		g2Available = true
	}
}

func TestGeoLite2Log(t *testing.T) {
	if !g2Available {
		t.SkipNow()
	}
	for _, test := range g2TestData {
		req, res, ctx := generateLog()
		req.RemoteAddr = test.IP
		ctx.ClientIP = test.IP
		g2Plugin.Log(req, res, ctx)
		assertEqual(t, ctx.Log["geo_country"], test.Country)
		assertEqual(t, ctx.Log["geo_city"], test.City)
		assertEqual(t, ctx.Log["geo_latitude"], test.Latitude)
		assertEqual(t, ctx.Log["geo_longitude"], test.Longitude)
	}
}

func TestGeoLite2Cache(t *testing.T) {
	if !g2Available {
		t.SkipNow()
	}
	for _, test := range g2TestData {
		gc, ok := g2Plugin.cache.Get(test.IP)
		if !ok {
			t.Fatalf("IP %s not found in cache, but should have been there.", test.IP)
		}
		gi := gc.(*geoinfo)
		gi.City.Names["en"] = "cached"
	}
	for _, test := range g2TestData {
		req, res, ctx := generateLog()
		req.RemoteAddr = test.IP
		ctx.ClientIP = test.IP
		g2Plugin.Log(req, res, ctx)
		assertEqual(t, ctx.Log["geo_city"], "cached")
	}
}
