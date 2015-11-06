package sql

import (
	gosql "database/sql"
	"encoding/json"
	"fmt"
	"github.com/12foo/apiplexy"
	_ "github.com/denisenkom/go-mssqldb"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"regexp"
	"strconv"
	"strings"
)

type SQLKeyBackend struct {
	query    string
	stmt     *gosql.Stmt
	argmap   []bool
	db       *gosql.DB
	keytypes map[string]bool
}

func (sql *SQLKeyBackend) GetKey(keyId string, keyType string) (*apiplexy.Key, error) {
	_, ok := sql.keytypes[keyType]
	if !ok {
		return nil, nil
	}
	args := make([]interface{}, len(sql.argmap))
	for i, isId := range sql.argmap {
		if isId {
			args[i] = keyId
		} else {
			args[i] = keyType
		}
	}
	row := sql.stmt.QueryRow(args...)
	k := apiplexy.Key{
		Type: keyType,
	}
	var jsonData string
	err := row.Scan(&k.ID, &k.Realm, &k.Quota, &jsonData)
	if err != nil {
		if err == gosql.ErrNoRows {
			return nil, nil
		} else {
			return nil, err
		}
	}
	if err = json.Unmarshal([]byte(jsonData), &k.Data); err != nil {
		return nil, err
	}
	return &k, nil
}

func (sql *SQLKeyBackend) DefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"driver":            strings.Join(gosql.Drivers(), "/"),
		"keytypes":          []interface{}{"run-for-these", "keytypes"},
		"connection_string": "host=localhost port=5432 user=apiplexy password=apiplexy dbname=apiplexy",
		"query":             "SELECT key_id, realm, quota_name, json_data FROM table WHERE id = :key_id AND type = :key_type",
	}
}

func (sql *SQLKeyBackend) Configure(config map[string]interface{}) error {
	driver := config["driver"].(string)
	db, err := gosql.Open(driver, config["connection_string"].(string))
	if err != nil {
		return fmt.Errorf("Error connecting to database. %s", err.Error())
	}

	sql.db = db
	sql.query = config["query"].(string)
	kts := config["keytypes"].([]interface{})
	sql.keytypes = make(map[string]bool, len(kts))
	for _, kt := range kts {
		skt, ok := kt.(string)
		if !ok {
			return fmt.Errorf("Expected a string keytype. Found %v (%T).", kt, kt)
		}
		sql.keytypes[skt] = true
	}

	replacer := regexp.MustCompile("(:key_id|:key_type)")
	argmap := make([]bool, 0)
	count := 0
	stmt := replacer.ReplaceAllFunc([]byte(sql.query), func(found []byte) []byte {
		f := string(found)
		if f == ":key_id" {
			argmap = append(argmap, true)
		} else if f == ":key_type" {
			argmap = append(argmap, false)
		}
		if driver == "postgres" {
			count += 1
			return []byte("$" + strconv.Itoa(count))
		} else {
			return []byte("?")
		}
	})
	sql.stmt, err = db.Prepare(string(stmt))
	if err != nil {
		return fmt.Errorf("Error preparing SQL statement.\nStatement: %s\nError: %s", string(stmt), err.Error())
	}
	sql.argmap = argmap

	return nil
}

func init() {
	// _ = apiplexy.BackendPlugin(&SQLKeyBackend{})
	apiplexy.RegisterPlugin(
		"sql-query",
		"Perform simple key checks via query against a backend SQL database.",
		"https://github.com/12foo/apiplexy/tree/master/backend/sql",
		SQLKeyBackend{},
	)
}
