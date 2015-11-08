package logging

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var influxConfig map[string]interface{} = map[string]interface{}{
	"database":       "testdb",
	"measurement":    "testmeasure",
	"flush_interval": 3,
	"username":       "",
	"password":       "",
	"tags":           "",
}

var mockInflux *httptest.Server
var written []string
var errors []string
var influxPlugin *InfluxDBLoggingPlugin

func TestInfluxInit(t *testing.T) {
	written = []string{}
	errors = []string{}

	mockInflux = httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		b, _ := ioutil.ReadAll(req.Body)
		req.Body.Close()
		if len(b) > 0 {
			lines := strings.Split(string(b), "\n")
			for _, l := range lines {
				if len(l) > 0 {
					written = append(written, l)
				}
			}
			res.WriteHeader(204)
			res.Write(nil)
		}
	}))

	influxPlugin = &InfluxDBLoggingPlugin{}
	t.Log("Mock influxDB at ", mockInflux.URL)
	influxConfig["server"] = mockInflux.URL
	assertNil(t, influxPlugin.Configure(influxConfig))
	assertNil(t, influxPlugin.Start(func(err error) {
		errors = append(errors, err.Error())
	}))
}

func TestInfluxLog(t *testing.T) {
	runLogs(t, influxPlugin.Log, 5)
}

func TestInfluxFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping flush test in short mode.")
	}
	time.Sleep(4 * time.Second) // wait for flush
	assertLength(t, written, 5)
}

func TestInfluxShutdown(t *testing.T) {
	runLogs(t, influxPlugin.Log, 5)
	assertNil(t, influxPlugin.Stop())
}

func TestInfluxAfterShutdown(t *testing.T) {
	assertLength(t, errors, 0)
	assertLength(t, written, 10)
	mockInflux.Close()
}
