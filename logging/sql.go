package logging

import (
	"encoding/json"
	"github.com/12foo/apiplexy"
	"github.com/jinzhu/gorm"
	_ "github.com/lib/pq"
	"net/http"
	"time"
)

type PostgresLogItem struct {
	IP        string
	Timestamp time.Time
	Status    int
	Path      string
	Keyless   bool
	KeyID     string
	LogData   string `sql:"type:JSONB NOT NULL DEFAULT '{}'::JSONB"`
}

func (s *PostgresLogItem) TableName(db *gorm.DB) string {
	return "logs"
}

type PostgresLoggingPlugin struct {
	db gorm.DB
}

func (pq *PostgresLoggingPlugin) Log(req *http.Request, res *http.Response, ctx *apiplexy.APIContext) error {
	item := PostgresLogItem{
		IP:        ctx.ClientIP,
		Path:      ctx.Path,
		Keyless:   ctx.Keyless,
		Timestamp: time.Now(),
		Status:    ctx.Log["status"].(int),
	}
	if ctx.Key != nil {
		item.KeyID = ctx.Key.ID
	} else {
		item.KeyID = ""
	}
	byt, _ := json.Marshal(ctx.Log)
	item.LogData = string(byt)
	pq.db.Save(&item)
	return nil
}

func (pq *PostgresLoggingPlugin) DefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"connection_string": "host=localhost port=5432 user=apiplexy password=apiplexy dbname=apiplexy",
		"create_tables":     false,
	}
}

func (pq *PostgresLoggingPlugin) Configure(config map[string]interface{}) error {
	db, err := gorm.Open("postgres", config["connection_string"].(string))
	if err != nil {
		return err
	}
	pq.db = db
	if config["create_tables"].(bool) {
		db.AutoMigrate(&PostgresLogItem{})
	}
	return nil
}

func init() {
	apiplexy.RegisterPlugin(
		"log-postgres",
		"Log requests to Postgres (using native JSONB).",
		"https://github.com/12foo/apiplexy/tree/master/logging",
		PostgresLoggingPlugin{},
	)
}
