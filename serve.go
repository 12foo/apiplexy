package apiplexy

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"gopkg.in/gomail.v2"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"
)

// Hop-by-hop headers. These are removed when sent to the backend.
// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html
var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te", // canonicalized version of "TE"
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

type processingError struct {
	Error string `json:"error"`
}

func (ap *apiplex) sendEmail(to string, subject string, contentType string, body string) {
	m := gomail.NewMessage()
	m.SetHeader("From", ap.email.From)
	if to == "alerts" {
		m.SetHeader("To", ap.email.AlertsTo...)
	} else {
		m.SetHeader("To", to)
	}
	m.SetHeader("Subject", subject)
	m.SetBody(contentType, body)
	d := gomail.NewPlainDialer(ap.email.Server, ap.email.Port, ap.email.User, ap.email.Password)
	if ap.email.Server == "localhost" {
		d.TLSConfig = &tls.Config{InsecureSkipVerify: true}
	}
	d.DialAndSend(m)
}

func (ap *apiplex) reportError(err error) {
	if len(ap.email.AlertsTo) > 0 && (ap.lastAlert == nil || time.Since(*ap.lastAlert) > time.Duration(ap.email.AlertsCooldown)*time.Minute) {
		now := time.Now()
		ap.sendEmail("alerts", "[API Error] Error on API gateway", "text/plain; charset=UTF-8",
			err.Error())
		ap.lastAlert = &now
	}
}

// Shortcut function to end requests prematurely. If called with an AbortRequest, will end request
// nicely with an error message to the user. If called with any other error type, will throw a 500
// and report the error through reporting.
func (ap *apiplex) error(status int, err error, res http.ResponseWriter) {
	switch e := err.(type) {
	case AbortRequest:
		res.WriteHeader(e.Status)
		jsonError, _ := json.Marshal(&processingError{Error: e.Error()})
		res.Write(jsonError)
	default:
		ap.reportError(e)
		res.WriteHeader(status)
		jsonError, _ := json.Marshal(&processingError{Error: e.Error()})
		res.Write(jsonError)
	}
}

// Authenticate a request: first, tries all AuthPlugins in order. The first one that Detect()s
// an auth scheme in the request extracts the identifying ID and other bits of an auth key.
// These are then tried in the backends until one responds back with the corresponding full key
// e.g. from a database. The full key is then passed back once more to the original AuthPlugin
// for final cryptographic validation.
//
// Authenticated keys are cached for some time and only need to perform the validation step
// on subsequent requests.
//
// If no key is detected in the request and keyless mode is enabled in the config (i.e. a "keyless"
// quota is present), the request is marked as keyless and allowed to proceed against the
// "keyless" quota.
func (ap *apiplex) authenticateRequest(req *http.Request, rd redis.Conn, ctx *APIContext) error {
	found := false
	for _, auth := range ap.auth {
		maybeKey, keyType, bits, err := auth.Detect(req, ctx)
		if err != nil {
			return err
		}

		// we've found a key (probably)
		if maybeKey != "" {
			// quick auth: is key in redis?
			kjson, _ := redis.String(rd.Do("GET", "auth_cache:"+maybeKey))
			if kjson != "" {
				// yes-- proceed immediately
				key := Key{}
				json.Unmarshal([]byte(kjson), &key)
				ok, err := auth.Validate(&key, req, ctx, bits)
				if err != nil {
					return err
				}
				if ok {
					ctx.Key = &key
					found = true
					break
				} else {
					return Abort(403, fmt.Sprintf("Access denied. Found a key of type '%s', but it is invalid.", key.Type))
				}
			} else {
				// no-- try the backends
				for _, bend := range ap.backends {
					key, err := bend.GetKey(maybeKey, keyType)
					if err != nil {
						return err
					}
					if key == nil {
						continue
					}
					ok, err := auth.Validate(key, req, ctx, bits)
					if err != nil {
						return err
					}
					if ok {
						kjson, _ := json.Marshal(&key)
						// TODO error handling if things go wrong in redis?
						rd.Do("SETEX", "auth_cache:"+maybeKey, ap.authCacheMins*60, string(kjson))
						ctx.Key = key
						found = true
						break
					} else {
						return Abort(403, fmt.Sprintf("Access denied. Found a key of type '%s', but it is invalid.", key.Type))
					}
				}
			}
		}
	}
	if !found {
		if ap.allowKeyless {
			ctx.Keyless = true
			ctx.Key = nil
		} else {
			return Abort(403, "Access denied. You or your app must supply valid credentials to access this API.")
		}
	}
	return nil
}

// checks a single quota (e.g. per_ip or per_key). This is done inside redis
// using lua scripting (for atomicity). The script is at the top of build.go
// and loaded into redis at the end of the building phase.
func (ap *apiplex) overQuota(rd redis.Conn, key string, cost, max, minutes int) bool {
	over, err := redis.Int(ap.ewmaScript.Do(rd, key+":ts", key+":avg", time.Now().Unix(), max, minutes*60, cost))
	if err != nil {
		ap.reportError(err)
	}
	return over == 1
}

// checks a request's quota by its context.
func (ap *apiplex) checkQuota(rd redis.Conn, req *http.Request, ctx *APIContext) error {
	if ctx.Cost == 0 {
		return nil
	}
	var quotaName string
	var keyID string
	if ctx.Keyless {
		quotaName = "keyless"
		keyID = "keyless"
	} else {
		quotaName = ctx.Key.Quota
		keyID = ctx.Key.ID
	}
	quota, ok := ap.quotas[quotaName]
	if !ok {
		// TODO nonexistant quota requested-- this should be reported
		quota = ap.quotas["default"]
	}
	if quota.Minutes <= 0 {
		return nil
	}
	if quota.MaxIP > 0 {
		var clientIP string
		if req.Header.Get("X-Forwarded-For") != "" {
			clientIP = req.Header.Get("X-Forwarded-For")
		} else {
			clientIP, _, _ = net.SplitHostPort(req.RemoteAddr)
		}
		if ap.overQuota(rd, "quota:ip:"+keyID+":"+clientIP, ctx.Cost, quota.MaxIP, quota.Minutes) {
			return Abort(403, fmt.Sprintf("Request quota per IP exceeded (%d reqs / %d mins). Please wait before making new requests.", quota.MaxIP, quota.Minutes))
		}
	}
	if quota.MaxKey > 0 {
		if ap.overQuota(rd, "quota:key:"+keyID, ctx.Cost, quota.MaxKey, quota.Minutes) {
			if ctx.Key.Owner != "" {
				notified, err := redis.Bool(rd.Do("GET", "quota:key:"+keyID+":notified"))
				if err == nil && notified {
					ap.sendEmail(ctx.Key.Owner, "[WARNING] Quota exceeded", "text/plain; charset=UTF-8",
						fmt.Sprintf(`This is an automated warning message. One of your API keys has exceeded its quota.

Key ID: %s
Realm : %s
Quota : %d requests per %d minutes

This warning will repeat every hour as long as the key continues to exceed its quota.
`, ctx.Key.ID, ctx.Key.Realm, quota.MaxKey, quota.Minutes))
					rd.Do("SETEX", "quota:key:"+keyID+":notified", 60*60, true)
				}
			}
			return Abort(403, fmt.Sprintf("Request quota per key exceeded (%d reqs / %d mins). Please wait before making new requests.", quota.MaxKey, quota.Minutes))
		}
	}
	return nil
}

func prepLog(ctx *APIContext, req *http.Request) {
	var clientIP string
	if req.Header.Get("X-Forwarded-For") != "" {
		clientIP = req.Header.Get("X-Forwarded-For")
	} else {
		clientIP, _, _ = net.SplitHostPort(req.RemoteAddr)
	}
	ctx.Log["client_ip"] = clientIP
	ctx.Log["path"] = ctx.Path
	ctx.Log["keyless"] = ctx.Keyless
	if !ctx.Keyless {
		ctx.Log["key_id"] = ctx.Key.ID
		ctx.Log["key_realm"] = ctx.Key.Realm
		ctx.Log["key_type"] = ctx.Key.Type
	}
}

// HandleAPI is the main processing function. It receives a request, checks for authentication,
// calculates quota, runs plugins and then passes the request to an upstream backend. On the
// returned response, it again runs plugins, and then sends the (possibly modified) result
// back to the user. After the request is thus handled, logging plugins are run in a background
// goroutine.
func (ap *apiplex) HandleAPI(res http.ResponseWriter, req *http.Request) {
	ctx := APIContext{
		Keyless:  false,
		DoNotLog: false,
		Cost:     1,
		Log:      make(map[string]interface{}),
		Data:     make(map[string]interface{}),
	}
	var apipath string
	for path, backends := range ap.upstreams {
		if strings.HasPrefix(req.URL.Path, path) {
			apipath = path
			if len(backends) == 1 {
				ctx.Upstream = &backends[0]
			} else {
				ctx.Upstream = &backends[rand.Intn(len(backends))]
			}
		}
	}

	rd := ap.redis.Get()

	if err := ap.authenticateRequest(req, rd, &ctx); err != nil {
		ap.error(500, err, res)
		return
	}

	for _, postauth := range ap.postauth {
		if err := postauth.PostAuth(req, &ctx); err != nil {
			ap.error(500, err, res)
			return
		}
	}

	if err := ap.checkQuota(rd, req, &ctx); err != nil {
		ap.error(500, err, res)
		return
	}

	for _, preupstream := range ap.preupstream {
		if err := preupstream.PreUpstream(req, &ctx); err != nil {
			ap.error(500, err, res)
			return
		}
	}

	if ctx.Upstream == nil {
		ap.error(500, fmt.Errorf("Request to '%s' has no upstream backend set.", ctx.Path), res)
		return
	}

	// prepare request for backend
	outreq := new(http.Request)
	*outreq = *req

	outreq.URL.Scheme = ctx.Upstream.Address.Scheme
	outreq.URL.Host = ctx.Upstream.Address.Host
	outreq.URL.Path = strings.Replace(outreq.URL.Path, apipath, ctx.Upstream.Address.Path, 1)
	outreq.RequestURI = ""
	outreq.Close = false

	// TODO golang reverseproxy does something more elaborate here, find out why
	for _, h := range hopHeaders {
		outreq.Header.Del(h)
	}
	if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		if prior, ok := outreq.Header["X-Forwarded-For"]; ok {
			clientIP = strings.Join(prior, ", ") + ", " + clientIP
		}
		outreq.Header.Set("X-Forwarded-For", clientIP)
	}

	urs, err := ctx.Upstream.Client.Do(outreq)
	if err != nil {
		ap.error(500, err, res)
		return
	}
	b, err := ioutil.ReadAll(urs.Body)
	if err != nil {
		ap.error(500, err, res)
		return
	}
	urs.Body.Close()
	urs.Body = ioutil.NopCloser(bytes.NewReader(b))

	// clean up reqponse for processing
	for _, h := range hopHeaders {
		urs.Header.Del(h)
	}
	for k, vv := range urs.Header {
		for _, v := range vv {
			res.Header().Add(k, v)
		}
	}

	for _, postupstream := range ap.postupstream {
		if err := postupstream.PostUpstream(req, urs, &ctx); err != nil {
			ap.error(500, err, res)
			return
		}
	}

	// TODO client abort early, better response processing
	body, _ := ioutil.ReadAll(urs.Body)
	urs.Body.Close()

	// if something seriously went wrong on the backend, report
	if urs.StatusCode >= 500 {
		if len(ap.email.AlertsTo) > 0 && (ap.lastAlert == nil || time.Since(*ap.lastAlert) > time.Duration(ap.email.AlertsCooldown)*time.Minute) {
			now := time.Now()
			ap.lastAlert = &now
			subject := "[API Error] Upstream server error"

			type detail struct {
				Item  string
				Value string
			}
			details := []detail{
				{"Code", fmt.Sprintf("%d - %s", urs.StatusCode, urs.Status)},
				{"Backend Server", ctx.Upstream.Address.String()},
				{"Method", req.Method},
				{"Request URI", req.RequestURI},
			}
			if !ctx.Keyless {
				details = append(details, detail{"Key ID", ctx.Key.ID})
			}
			if req.Method == "POST" {
				b, _ := ioutil.ReadAll(req.Body)
				details = append(details, detail{"Request Body", string(b)})
			}

			ebody := ""
			ebody = ebody + fmt.Sprintf("<h2>%s</h2>", "Upstream server error")
			ebody = ebody + "<table>"
			for _, d := range details {
				ebody = ebody + fmt.Sprintf("<tr><th>%s</th><td>%s</td></tr>", d.Item, d.Value)
			}
			ebody = ebody + "</table><hr>"

			if strings.HasPrefix(urs.Header.Get("Content-Type"), "text/html") {
				ap.sendEmail("alerts", subject, "text/html", ebody+string(body))
			} else {
				ap.sendEmail("alerts", subject, "text/html", ebody+"<pre>"+string(body)+"</pre>")
			}
		}

		msg := map[string]interface{}{
			"error":   "Internal API error",
			"details": "Sorry, something went wrong on the API server. The error has been reported to technical staff.",
			"code":    urs.StatusCode,
		}
		res.WriteHeader(urs.StatusCode)
		r, _ := json.Marshal(msg)
		res.Write(r)
		return
	}

	res.WriteHeader(urs.StatusCode)
	res.Write(body)

	// do logging in a goroutine so the request can finish as fast as possible
	if !ctx.DoNotLog {
		prepLog(&ctx, req)
		for _, logging := range ap.logging {
			if err := logging.Log(req, urs, &ctx); err != nil {
				ap.error(500, err, res)
				return
			}
		}
	}

}
