package logging

import (
	"github.com/12foo/apiplexy"
	"net/http"
	"net/url"
	"time"

	"github.com/influxdb/influxdb/client/v2"
)

type InfluxDBLoggingPlugin struct {
	client        client.Client
	batchConfig   client.BatchPointsConfig
	measurement   string
	done          chan bool
	points        chan *client.Point
	flushInterval time.Duration
}

func (ix *InfluxDBLoggingPlugin) Log(req *http.Request, res *http.Response, ctx *apiplexy.APIContext) error {
	point, err := client.NewPoint(ix.measurement, map[string]string{}, ctx.Log, time.Now())
	if err != nil {
		return err
	}
	ix.points <- point
	return nil
}

func (ix *InfluxDBLoggingPlugin) DefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"database":       "your-db-name",
		"measurement":    "api_hits",
		"server":         "http://localhost:8086",
		"flush_interval": 30,
		"username":       "",
		"password":       "",
	}
}

func (ix *InfluxDBLoggingPlugin) Configure(config map[string]interface{}) error {
	u, _ := url.Parse(config["server"].(string))
	cfg := client.Config{
		URL: u,
	}
	if _, ok := config["username"]; ok {
		cfg.Username = config["username"].(string)
		cfg.Password = config["password"].(string)
	}
	ix.client = client.NewClient(cfg)

	ix.batchConfig = client.BatchPointsConfig{
		Database:  config["database"].(string),
		Precision: "s",
	}

	ix.points = make(chan *client.Point, 100)
	ix.done = make(chan bool)
	ix.flushInterval = time.Duration(config["flush_interval"].(int)) * time.Second
	return nil
}

func (ix *InfluxDBLoggingPlugin) Start(report func(error)) error {
	flush := time.Tick(ix.flushInterval)
	go func() {
		batch, err := client.NewBatchPoints(ix.batchConfig)
		if err != nil {
			report(err)
		}

		performFlush := func() {
			ix.client.Write(batch)
			batch, err = client.NewBatchPoints(ix.batchConfig)
			if err != nil {
				report(err)
			}
		}

		for {
			select {
			case point, ok := <-ix.points:
				if ok {
					batch.AddPoint(point)
				} else {
					ix.points = nil
				}
			case _ = <-flush:
				performFlush()
			}
			if ix.points == nil || flush == nil {
				performFlush()
				break
			}
		}
		ix.done <- true
	}()
	return nil
}

func (ix *InfluxDBLoggingPlugin) Stop() error {
	close(ix.points)
	<-ix.done
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
