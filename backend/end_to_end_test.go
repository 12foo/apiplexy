package backend

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/12foo/apiplexy"
	_ "github.com/12foo/apiplexy/auth/hmac"
	_ "github.com/12foo/apiplexy/backend/sql"
	"github.com/garyburd/redigo/redis"
	. "github.com/smartystreets/goconvey/convey"
	"gopkg.in/yaml.v2"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

const yaml_config = `redis:
  host: 127.0.0.1
  port: 6379
  db: 1
quotas:
  default:
    minutes: 5
    max_key: 10
  keyless:
    minutes: 5
    max_ip: 5
serve:
  port: 5000
  api: /
  upstreams:
  - http://your-actual-api:8000/
  portal_api: /portal-api/
  signing_key: test-signing-key
plugins:
  auth:
  - plugin: hmac
  backend:
  - plugin: sql-full
    config:
      connection_string: ":memory:"
      create_tables: true
      driver: sqlite3`

var ap *http.ServeMux
var rd redis.Conn

func toBody(n interface{}) io.Reader {
	b, _ := json.Marshal(n)
	return bytes.NewReader(b)
}

func shouldHaveStatus(actual interface{}, expected ...interface{}) string {
	res := actual.(*httptest.ResponseRecorder)
	exp := expected[0].(int)
	if res.Code == exp {
		return ""
	} else {
		return fmt.Sprintf("Expected status: %d %s\n  Actual status: %d %s\n  Response body: %s",
			exp, http.StatusText(exp), res.Code, http.StatusText(res.Code), res.Body.String())
	}
}

func TestMain(m *testing.M) {
	// set up a mock API that apiplexy proxies to
	mockAPI := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusOK)
		res.Write([]byte("API-OK"))
	}))
	defer mockAPI.Close()
	log.Printf("Launched mock API at %s.\n", mockAPI.URL)

	// set up apiplexy
	config := apiplexy.ApiplexConfig{}
	if err := yaml.Unmarshal([]byte(yaml_config), &config); err != nil {
		log.Fatalln(err)
	}
	config.Serve.Upstreams[0] = mockAPI.URL
	a, err := apiplexy.New(config)
	if err != nil {
		log.Fatalln(err)
	}
	ap = a

	r, _ := redis.Dial("tcp", config.Redis.Host+":"+strconv.Itoa(config.Redis.Port))
	r.Do("SELECT", 1)
	rd = r

	result := m.Run()

	// after testing, clean out database for future tests
	r.Do("FLUSHDB")

	os.Exit(result)
}

func TestKeyless(t *testing.T) {
	Convey("Keyless access should work within limits", t, func() {
		for i := 1; i <= 5; i++ {
			r := httptest.NewRecorder()
			keylessRequest, _ := http.NewRequest("GET", "/", nil)
			ap.ServeHTTP(r, keylessRequest)
			So(r, shouldHaveStatus, 200)
			So(r.Body.String(), ShouldEqual, "API-OK")
		}
	})

	Convey("Keyless access should deny if over limit", t, func() {
		r := httptest.NewRecorder()
		keylessRequest, _ := http.NewRequest("GET", "/", nil)
		ap.ServeHTTP(r, keylessRequest)
		So(r, shouldHaveStatus, 403)
		So(r.Body.String(), ShouldNotEqual, "API-OK")
	})
}

func TestPortalAPI(t *testing.T) {
	var token string

	Convey("User can't access protected paths without authentication", t, func() {
		req, _ := http.NewRequest("GET", "/portal-api/keys/types", nil)
		res := httptest.NewRecorder()
		ap.ServeHTTP(res, req)
		So(res, shouldHaveStatus, 403)
	})

	Convey("Creating user", t, func() {
		req, _ := http.NewRequest("POST", "/portal-api/account", toBody(map[string]interface{}{
			"email":    "test@user.com",
			"name":     "Test User",
			"password": "test-password",
			"link":     "http://example-redirect.com/CODE",
		}))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		ap.ServeHTTP(res, req)
		So(res, shouldHaveStatus, 200)
	})

	Convey("Un-activated user can't log in", t, func() {
		req, _ := http.NewRequest("POST", "/portal-api/account/token", toBody(map[string]interface{}{
			"email":    "test@user.com",
			"password": "test-password",
		}))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		ap.ServeHTTP(res, req)
		So(res, shouldHaveStatus, 403)
	})

	Convey("Activating user", t, func() {
		possibleKeys, _ := redis.Values(rd.Do("KEYS", "activation:*"))
		code, _ := redis.String(possibleKeys[0], nil)
		code = strings.TrimPrefix(code, "activation:")
		So(code, ShouldNotEqual, "")
		req, _ := http.NewRequest("GET", "/portal-api/account/activate/"+code, nil)
		res := httptest.NewRecorder()
		ap.ServeHTTP(res, req)
		So(res, shouldHaveStatus, 200)
	})

	Convey("Activated user can log in", t, func() {
		req, _ := http.NewRequest("POST", "/portal-api/account/token", toBody(map[string]interface{}{
			"email":    "test@user.com",
			"password": "test-password",
		}))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		ap.ServeHTTP(res, req)
		So(res, shouldHaveStatus, 200)
		ts := struct {
			Token string
		}{}
		json.Unmarshal(res.Body.Bytes(), &ts)
		So(ts.Token, ShouldNotEqual, "")
		token = ts.Token
	})

	var ktype string
	var key apiplexy.Key

	Convey("Valid user can access protected paths", t, func() {
		req, _ := http.NewRequest("GET", "/portal-api/keys/types", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		ap.ServeHTTP(res, req)
		So(res, shouldHaveStatus, 200)
		var types map[string]apiplexy.KeyType
		json.Unmarshal(res.Body.Bytes(), &types)
		So(types, ShouldContainKey, "HMAC")
		ktype = "HMAC"
	})

	Convey("User should have 0 keys", t, func() {
		req, _ := http.NewRequest("GET", "/portal-api/keys", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		ap.ServeHTTP(res, req)
		So(res, shouldHaveStatus, 200)
		var keys []apiplexy.Key
		json.Unmarshal(res.Body.Bytes(), &keys)
		So(keys, ShouldBeEmpty)
	})

	Convey("Create a key", t, func() {
		req, _ := http.NewRequest("POST", "/portal-api/keys", toBody(map[string]interface{}{
			"type":  ktype,
			"realm": "test-realm",
		}))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		ap.ServeHTTP(res, req)
		So(res, shouldHaveStatus, 200)
	})

	Convey("User should have 1 key", t, func() {
		req, _ := http.NewRequest("GET", "/portal-api/keys", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		ap.ServeHTTP(res, req)
		So(res, shouldHaveStatus, 200)
		var keys []struct {
			Key apiplexy.Key
		}
		json.Unmarshal(res.Body.Bytes(), &keys)
		So(len(keys), ShouldEqual, 1)
		key = keys[0].Key
	})

	Convey("Access with invalid signature shouldn't work", t, func() {
		for i := 1; i <= 10; i++ {
			r := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/", nil)
			mac := hmac.New(sha1.New, []byte(key.Data["secret"].(string)))
			mac.Write([]byte(req.Header.Get("Date")))
			sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
			req.Header.Set("Authorization", fmt.Sprintf("Signature keyId=\"%s\",algorithm=\"hmac-sha1\",signature=\"%s\"", key.ID, "dummy"+sig))
			ap.ServeHTTP(r, req)
			So(r, shouldHaveStatus, 403)
		}
	})

	Convey("Access with key should work within limits", t, func() {
		for i := 1; i <= 10; i++ {
			r := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/", nil)
			mac := hmac.New(sha1.New, []byte(key.Data["secret"].(string)))
			mac.Write([]byte(req.Header.Get("Date")))
			sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
			req.Header.Set("Authorization", fmt.Sprintf("Signature keyId=\"%s\",algorithm=\"hmac-sha1\",signature=\"%s\"", key.ID, sig))
			ap.ServeHTTP(r, req)
			So(r, shouldHaveStatus, 200)
			So(r.Body.String(), ShouldEqual, "API-OK")
		}
	})

	Convey("Access with key should deny if over per_key limit", t, func() {
		r := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/", nil)
		mac := hmac.New(sha1.New, []byte(key.Data["secret"].(string)))
		mac.Write([]byte(req.Header.Get("Date")))
		sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		req.Header.Set("Authorization", fmt.Sprintf("Signature keyId=\"%s\",algorithm=\"hmac-sha1\",signature=\"%s\"", key.ID, sig))
		ap.ServeHTTP(r, req)
		So(r, shouldHaveStatus, 403)
		So(r.Body.String(), ShouldNotEqual, "API-OK")
	})

}
