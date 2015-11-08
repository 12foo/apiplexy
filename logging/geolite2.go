package logging

import (
	"fmt"
	"github.com/12foo/apiplexy"
	"github.com/oschwald/maxminddb-golang"
	"github.com/pmylund/go-cache"
	"net"
	"net/http"
	"time"
)

type GeoLite2Plugin struct {
	db           *maxminddb.Reader
	cache        *cache.Cache
	locale       string
	alertTripped bool
}

type geoinfo struct {
	Country struct {
		ISO string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Location struct {
		Latitude  float64 `maxminddb:"latitude"`
		Longitude float64 `maxminddb:"longitude"`
	} `maxminddb:"location"`
}

func (geo *GeoLite2Plugin) Log(req *http.Request, res *http.Response, ctx *apiplexy.APIContext) error {
	var gi *geoinfo
	cached, ok := geo.cache.Get(ctx.ClientIP)
	if ok {
		gi = cached.(*geoinfo)
	} else {
		ip := net.ParseIP(ctx.ClientIP)
		var lookup geoinfo
		if err := geo.db.Lookup(ip, &lookup); err != nil {
			if !geo.alertTripped {
				geo.alertTripped = true
				return fmt.Errorf("There was an error during GeoIP lookup. Possibly the database file is outdated or corrupt.\nThis error alert will not repeat.\nTried to look up IP: %s\nOriginal error: %s", ctx.ClientIP, err.Error())
			}
		}
		gi = &lookup
		geo.cache.Set(ctx.ClientIP, gi, cache.DefaultExpiration)
	}

	ctx.Log["geo_country"] = gi.Country.ISO
	if cityname, ok := gi.City.Names[geo.locale]; ok {
		ctx.Log["geo_city"] = cityname
	}
	ctx.Log["geo_latitude"] = gi.Location.Latitude
	ctx.Log["geo_longitude"] = gi.Location.Longitude
	return nil
}

func (geo *GeoLite2Plugin) DefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"mmdb_path":      "/path/to/geolite2.mmdb",
		"localize_names": "en",
	}
}

func (geo *GeoLite2Plugin) Configure(config map[string]interface{}) error {
	path := config["mmdb_path"].(string)
	db, err := maxminddb.Open(path)
	if err != nil {
		return err
	}
	geo.db = db
	geo.locale = config["localize_names"].(string)
	geo.cache = cache.New(5*time.Minute, 30*time.Second)
	return nil
}

func init() {
	// _ = apiplexy.LoggingPlugin(&GeoLite2Plugin{})
	apiplexy.RegisterPlugin(
		"geolite2",
		"Resolve IPs to their geographical location (using MaxMind GeoLite2).",
		"http://github.com/12foo/apiplexy/tree/master/logging",
		GeoLite2Plugin{},
	)
}
