package logging

import (
	"github.com/12foo/apiplexy"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func assertNil(t *testing.T, val interface{}) {
	if val != nil {
		t.Error("Expected nil, got %v", val)
	}
}

func assertLength(t *testing.T, vals []string, length int) {
	if len(vals) != length {
		if len(vals) == 0 {
			t.Errorf("Expected %d entries, got none.", length)
		} else {
			t.Errorf("Expected %d entries, got the following:\n- %s\n", length, strings.Join(written, "\n- "))
		}
	}
}

var config map[string]interface{} = map[string]interface{}{
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

func TestInit(t *testing.T) {
	influxPlugin = &InfluxDBLoggingPlugin{}
	t.Log("Mock influxDB at ", mockInflux.URL)
	config["server"] = mockInflux.URL
	assertNil(t, influxPlugin.Configure(config))
	assertNil(t, influxPlugin.Start(func(err error) {
		errors = append(errors, err.Error())
	}))
}

func generateLog(t *testing.T, count int) {
	for i := 0; i < count; i++ {
		req, _ := http.NewRequest("GET", "/test", nil)
		res := http.Response{}
		ctx := apiplexy.APIContext{
			Log: map[string]interface{}{
				"test": i,
			},
		}
		assertNil(t, influxPlugin.Log(req, &res, &ctx))
	}
}

func TestLog(t *testing.T) {
	generateLog(t, 5)
}

func TestFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping flush test in short mode.")
	}
	time.Sleep(4 * time.Second) // wait for flush
	assertLength(t, written, 5)
}

func TestShutdown(t *testing.T) {
	generateLog(t, 5)
	assertNil(t, influxPlugin.Stop())
}

func TestAfterShutdown(t *testing.T) {
	assertLength(t, errors, 0)
	assertLength(t, written, 10)
}

func TestMain(m *testing.M) {
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
	defer mockInflux.Close()

	os.Exit(m.Run())
}
