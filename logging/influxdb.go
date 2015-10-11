package logging

import (
	"fmt"
	"github.com/12foo/apiplexy"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type InfluxDBLoggingPlugin struct {
	url           string
	measurement   string
	lines         chan string
	flushInterval time.Duration
}

func (ix *InfluxDBLoggingPlugin) Log(req *http.Request, res *http.Response, ctx *apiplexy.APIContext) error {
	// TODO log more types
	// TODO map log key names to storeable keys / types in config?
	fields := []string{}
	for k, v := range ctx.Log {
		log := ""
		switch t := v.(type) {
		case string:
			log = "\"" + strings.Replace(t, "\"", "\\\"", -1) + "\""
		case int:
			log = strconv.Itoa(t) + "i"
		case float64:
			log = strconv.FormatFloat(t, 'e', -1, 64)
		}
		if log != "" {
			fields = append(fields, k+"="+log)
		}
	}
	line := fmt.Sprintf("%s %s %d", ix.measurement, strings.Join(fields, ","), time.Now().UnixNano())
	ix.lines <- line
	return nil
}

func (ix *InfluxDBLoggingPlugin) DefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"database":       "your-db-name",
		"measurement":    "api_hits",
		"server":         "localhost:8086",
		"flush_interval": 30,
	}
}

func (ix *InfluxDBLoggingPlugin) Configure(config map[string]interface{}) error {
	ix.url = "http://" + config["server"].(string) + "/write?db=" + config["database"].(string)
	ix.measurement = config["measurement"].(string)
	ix.lines = make(chan string, 100)
	ix.flushInterval = time.Duration(config["flush_interval"].(int)) * time.Second

	return nil
}

func (ix *InfluxDBLoggingPlugin) Start(report func(error)) error {
	flush := time.Tick(ix.flushInterval)
	go func() {
		lines := []string{}

		select {
		case line := <-ix.lines:
			lines = append(lines, line)
		case _ = <-flush:
			res, err := http.Post(ix.url, "text/plain", strings.NewReader(strings.Join(lines, "\n")))
			if err != nil {
				report(err)
			}
			if res.StatusCode != 204 {
				report(fmt.Errorf("Error logging to InfluxDB. HTTP status code: %d", res.StatusCode))
			}
			lines = nil
		}
	}()
	return nil
}

func (ix *InfluxDBLoggingPlugin) Stop() error {
	return nil
}

func init() {
	apiplexy.RegisterPlugin(
		"influxdb",
		"Log requests to InfluxDB.",
		"https://github.com/12foo/apiplexy/tree/master/logging",
		InfluxDBLoggingPlugin{},
	)
}