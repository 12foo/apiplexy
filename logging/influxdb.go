package logging

import (
	"fmt"
	"github.com/12foo/apiplexy"
	"net/http"
	"strings"
	"time"

	"github.com/influxdb/influxdb/client/v2"
)

type InfluxDBLoggingPlugin struct {
	client        client.Client
	batchConfig   client.BatchPointsConfig
	measurement   string
	done          chan bool
	tags          map[string]bool
	points        chan *client.Point
	flushInterval time.Duration
}

func (ix *InfluxDBLoggingPlugin) Log(req *http.Request, res *http.Response, ctx *apiplexy.APIContext) error {
	tags := make(map[string]string, len(ix.tags)+2)
	fields := map[string]interface{}{}
	tags["api_path"] = ctx.APIPath
	if !ctx.Keyless && ctx.Key != nil {
		tags["key"] = ctx.Key.ID
	} else {
		tags["key"] = ""
	}

	for k, v := range ctx.Log {
		if _, ok := ix.tags[k]; ok {
			if _, ok := v.(string); ok {
				tags[k] = v.(string)
			}
		} else {
			fields[k] = v
		}
	}
	point, err := client.NewPoint(ix.measurement, tags, fields, time.Now())
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
		"tags":           "",
	}
}

func (ix *InfluxDBLoggingPlugin) Configure(config map[string]interface{}) error {
	ix.measurement = config["measurement"].(string)
	if len(ix.measurement) == 0 {
		return fmt.Errorf("Measurement name cannot be an empty string.")
	}
	u := config["server"].(string)
	rawtags := strings.Split(config["tags"].(string), ",")
	ix.tags = make(map[string]bool, len(rawtags))
	for _, tag := range rawtags {
		ix.tags[strings.TrimSpace(tag)] = true
	}

	cfg := client.HTTPConfig{
		Addr: u,
	}
	if _, ok := config["username"]; ok {
		cfg.Username = config["username"].(string)
		cfg.Password = config["password"].(string)
	}
	c, err := client.NewHTTPClient(cfg)
	if err != nil {
		return err
	}
	ix.client = c

	ix.batchConfig = client.BatchPointsConfig{
		Database:  config["database"].(string),
		Precision: "s",
	}

	ix.points = make(chan *client.Point, 100)
	ix.done = make(chan bool)
	ix.flushInterval = time.Duration(config["flush_interval"].(int)) * time.Second
	return nil
}

func (ix *InfluxDBLoggingPlugin) performFlush(batch *client.BatchPoints) error {
	err := ix.client.Write(*batch)
	if err != nil {
		return err
	}
	return nil
}

func (ix *InfluxDBLoggingPlugin) Start(report func(error)) error {
	flush := time.Tick(ix.flushInterval)
	go func() {
		batch, err := client.NewBatchPoints(ix.batchConfig)
		if err != nil {
			report(err)
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
				if err := ix.performFlush(&batch); err != nil {
					report(err)
				}
				batch, _ = client.NewBatchPoints(ix.batchConfig)
			}
			if ix.points == nil || flush == nil {
				if err := ix.performFlush(&batch); err != nil {
					report(err)
				}
				batch, _ = client.NewBatchPoints(ix.batchConfig)
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
	ix.client.Close()
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
