package logging

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/12foo/apiplexy"
	g "github.com/oschwald/geoip2-golang"
)

type concurrentMap struct {
	sync.RWMutex
	m map[string]interface{}
}

//IPLocatorPlugin ..
type IPLocatorPlugin struct {
	pathToMmdbFile string         //Path to the GeoIP2 File-Database
	ipCache        *concurrentMap //Caching of Locations to avoid slow fs access
}

//Log ..
func (l *IPLocatorPlugin) Log(req *http.Request, res *http.Response, ctx *apiplexy.APIContext) error {

	ip, _, _ := net.SplitHostPort(req.RemoteAddr)
	if l.ipCache != nil { //Try to use Ip Cache

		l.ipCache.RLock()
		ipLocation, inCache := l.ipCache.m[ip]
		l.ipCache.RUnlock()

		if inCache {
			//Retrive Ip-Location from ipCache
			ctx.Log["location"] = ipLocation
		} else {
			//Retrive Location from File-Database...
			err := l.retriveLocationFromFsDB(ip, ctx)
			if err != nil {
				return err
			}

			//..and put Loction into ipCache
			l.ipCache.Lock()
			l.ipCache.m[ip] = ctx.Log["Location"]
			l.ipCache.Unlock()
		}
	} else { //No IpCache; Always use file system database to retrive the ips location
		return l.retriveLocationFromFsDB(ip, ctx)
	}

	return nil
}

func (l *IPLocatorPlugin) retriveLocationFromFsDB(ip string, ctx *apiplexy.APIContext) error {

	db, err := g.Open(l.pathToMmdbFile)
	if err != nil {
		return fmt.Errorf("GeoLite database not found")
	}
	defer db.Close()

	netIP := net.ParseIP(ip)
	city, err := db.City(netIP)
	if err != nil {
		return fmt.Errorf("No record for IP:" + ip)
	}
	ctx.Log["Location"] = city.Location

	return nil
}

//DefaultConfig ...
func (l *IPLocatorPlugin) DefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"mmdb_path":  "/path/to/geolite2.mmdb",
		"ip_caching": true,
	}
}

//Configure ...
func (l *IPLocatorPlugin) Configure(config map[string]interface{}) error {
	path := config["mmdb_path"].(string)
	if strings.HasSuffix(path, ".mmdb") {
		return fmt.Errorf("'%s' is not a valid geo database", path)
	}
	l.pathToMmdbFile = path
	if config["ip_caching"].(bool) {
		l.ipCache = &concurrentMap{m: make(map[string]interface{})}
	}
	return nil
}

func init() {
	// _ = apiplexy.LoggingPlugin(&IPLocatorPlugin{})
	apiplexy.RegisterPlugin(
		"geolocation",
		"Resolve IPs to their geographical location (using GeoLite2).",
		"http://github.com/12foo/apiplexy/tree/master/logging",
		IPLocatorPlugin{},
	)
}
